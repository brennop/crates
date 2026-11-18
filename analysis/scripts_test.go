package analysis

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// totalBudget caps the memory of all audio jobs running together. Use a
// full-length track: memory can grow with the track, and a short clip hides
// that (a 30 s test tone once hid S-KEY going past 3.3 GB).
const totalBudget = 2 << 30

// TestScripts runs the real audio jobs, in parallel, on the file named by
// CRATES_ANALYSIS_AUDIO, and fails if together they pass totalBudget. It
// needs uv, ffmpeg and, on a first run, the network.
func TestScripts(t *testing.T) {
	path := os.Getenv("CRATES_ANALYSIS_AUDIO")
	if path == "" {
		t.Skip("set CRATES_ANALYSIS_AUDIO to an audio file to run the scripts")
	}
	dir := t.TempDir()
	a := New(Options{ScriptDir: dir})
	stop := make(chan struct{})
	peak := make(chan int64)
	go func() { peak <- samplePeak(dir, stop) }()
	var wg sync.WaitGroup
	for _, job := range []Job{BPM, SKey, EssentiaKey} {
		wg.Go(func() {
			r := a.Run(context.Background(), job, Track{Path: path})
			if r.Err != nil {
				t.Errorf("%s: %v", job, r.Err)
				return
			}
			t.Logf("%s: %+v", job, r)
			if job == BPM && r.BPM <= 0 || job != BPM && len(r.Keys) == 0 {
				t.Errorf("%s found nothing: %+v", job, r)
			}
		})
	}
	wg.Wait()
	close(stop)
	p := <-peak
	t.Logf("peak memory of all jobs together: %d MB", p>>20)
	if p > totalBudget {
		t.Errorf("jobs together peaked at %d MB, over the %d MB budget", p>>20, totalBudget>>20)
	}
	if r := a.Run(context.Background(), BPM, Track{Path: path + ".missing"}); r.Err == nil {
		t.Error("a missing file gave no error")
	} else {
		t.Logf("missing file: %v", r.Err)
	}
}

// TestScriptsCancel cancels the real jobs mid-run and checks that no
// process from their script dir is left.
func TestScriptsCancel(t *testing.T) {
	path := os.Getenv("CRATES_ANALYSIS_AUDIO")
	if path == "" {
		t.Skip("set CRATES_ANALYSIS_AUDIO to an audio file to run the scripts")
	}
	dir := t.TempDir()
	a := New(Options{ScriptDir: dir})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, job := range []Job{BPM, SKey, EssentiaKey} {
		wg.Go(func() { a.Run(ctx, job, Track{Path: path}) })
	}
	time.Sleep(3 * time.Second)
	cancel()
	wg.Wait()
	time.Sleep(200 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", dir).Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("processes left after cancel: %s", out)
	}
}

// samplePeak returns the highest total resident memory, until stop closes,
// of every process group that has a process running a script from dir.
// Groups count their ffmpeg children too.
func samplePeak(dir string, stop <-chan struct{}) int64 {
	var peak int64
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return peak
		case <-tick.C:
		}
		out, err := exec.Command("ps", "-A", "-o", "pgid=,rss=,command=").Output()
		if err != nil {
			continue
		}
		groups := map[string]bool{}
		type proc struct {
			pgid string
			kb   int64
		}
		var procs []proc
		for line := range strings.SplitSeq(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			procs = append(procs, proc{f[0], kb})
			if strings.Contains(line, dir) {
				groups[f[0]] = true
			}
		}
		var total int64
		for _, p := range procs {
			if groups[p.pgid] {
				total += p.kb << 10
			}
		}
		peak = max(peak, total)
	}
}

// TestSpekCancel opens the real Spek on CRATES_ANALYSIS_AUDIO, cancels it
// while its window is open, and checks that it is gone.
func TestSpekCancel(t *testing.T) {
	path := os.Getenv("CRATES_ANALYSIS_AUDIO")
	if path == "" {
		t.Skip("set CRATES_ANALYSIS_AUDIO to an audio file to open Spek")
	}
	if _, err := exec.LookPath("spek"); err != nil {
		t.Skip("spek not on PATH")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Spek(ctx, Track{Path: path}) }()
	time.Sleep(3 * time.Second)
	if out, _ := exec.Command("pgrep", "-f", "spek "+path).Output(); len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("spek is not running")
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("a cancelled Spek returned no error")
	}
	time.Sleep(200 * time.Millisecond)
	if out, _ := exec.Command("pgrep", "-f", path).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("processes left after cancel: %s", out)
	}
}
