package analysis

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A script runs as uv, which starts Python, which may start ffmpeg. Killing
// uv alone leaves Python running, re-parented to launchd, so a job that was
// cancelled (panel closed, crates quit) kept its gigabytes. So each script
// gets its own process group, and everything here acts on the group: cancel
// and the memory cap kill all of it, and the cap counts all of it.

// MemoryLimit caps one job's process group. A job over the cap is killed
// and reports ErrTooMuchMemory. Four audio jobs run at once. Measured
// peaks, 6:43 track / 63-minute mix: Essentia BPM 0.34 / 0.39 GB and
// S-KEY 0.77 / 0.80 GB (both stream in windows); Essentia key
// 0.4 / 1.1 GB (loads the whole track); Cutoff (6:34 track / 58-minute
// mix) 0.31 / 0.54 GB (keeps 140 band levels per frame), so together
// about 2.8 GB on a long mix.
const MemoryLimit = 1200 << 20

// memoryPoll is how often the cap is checked.
const memoryPoll = 250 * time.Millisecond

// niceness lowers the scripts' CPU priority, so playback and the rest of
// the machine come first.
const niceness = "10"

// ErrTooMuchMemory: a job passed MemoryLimit and was stopped.
var ErrTooMuchMemory = fmt.Errorf("stopped: used more than %d MB", MemoryLimit>>20)

// groupRSS returns the resident memory of every process in group pgid, in
// bytes. A variable so tests can stand in for ps.
var groupRSS = func(pgid int) (int64, error) {
	out, err := exec.Command("ps", "-A", "-o", "pgid=,rss=").Output()
	if err != nil {
		return 0, err
	}
	var total int64
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != strconv.Itoa(pgid) {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		total += kb << 10
	}
	return total, nil
}

// runGroup runs name with args in a new process group at lower priority,
// and returns its stdout and stderr. Cancelling ctx, or the group passing
// MemoryLimit, kills the whole group.
func runGroup(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	if nice, lerr := exec.LookPath("nice"); lerr == nil {
		name, args = nice, append([]string{"-n", niceness, name}, args...)
	}
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	pgid := cmd.Process.Pid // Setpgid: the leader's pid is the group's id
	kill := func() { syscall.Kill(-pgid, syscall.SIGKILL) }
	defer kill() // whatever the leader left behind

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(memoryPoll)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return out.Bytes(), errb.Bytes(), err
		case <-ctx.Done():
			kill()
			<-done
			return nil, errb.Bytes(), ctx.Err()
		case <-tick.C:
			if rss, err := groupRSS(pgid); err == nil && rss > MemoryLimit {
				kill()
				<-done
				return nil, errb.Bytes(), ErrTooMuchMemory
			}
		}
	}
}
