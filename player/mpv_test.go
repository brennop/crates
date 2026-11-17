package player

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/internal/fixture"
)

func needMPV(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("mpv")
	if err != nil {
		t.Skip("mpv not installed")
	}
	return bin
}

// writeFixture writes a silent MP3 of about frames/40 seconds.
func writeFixture(t *testing.T, name string, frames int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, fixture.MP3(fixture.Options{Frames: frames, Xing: true}), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestRealMPV(t *testing.T) {
	needMPV(t)
	ctx := context.Background()
	sk := newSink()
	pidfile := filepath.Join(t.TempDir(), "mpv.pid")
	m, err := Start(Config{Sink: sk, PidFile: pidfile, Args: []string{"--ao=null"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	m.mu.Lock()
	pid := m.p.pid()
	m.mu.Unlock()
	if got, _, err := readPidfile(pidfile); err != nil || got != pid {
		t.Fatalf("pidfile = %d, %v; want %d", got, err, pid)
	}

	// A short file plays to its end.
	short := writeFixture(t, "short.mp3", 20) // ~0.5 s
	if err := m.Load(ctx, short, 0); err != nil {
		t.Fatal(err)
	}
	ended := sk.wait(t, 5*time.Second, func(e events.Event) bool { _, ok := e.(events.TrackEnded); return ok })
	if ended.(events.TrackEnded).Path != short {
		t.Errorf("TrackEnded %#v", ended)
	}

	// A broken file is reported, once.
	bad := filepath.Join(t.TempDir(), "bad.mp3")
	os.WriteFile(bad, []byte("not audio at all"), 0o644)
	m.Load(ctx, bad, 0)
	lf := sk.wait(t, 5*time.Second, func(e events.Event) bool { _, ok := e.(events.LoadFailed); return ok })
	if lf.(events.LoadFailed).Path != bad || lf.(events.LoadFailed).Reason == "" {
		t.Errorf("LoadFailed %#v", lf)
	}

	// A long file starts at the requested position; volume and pause fold.
	long := writeFixture(t, "long.mp3", 2400) // ~63 s
	longDur := fixture.Options{Frames: 2400}.Duration()
	if err := m.Load(ctx, long, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := m.SetVolume(ctx, 37); err != nil {
		t.Fatal(err)
	}
	sk.wait(t, 5*time.Second, func(e events.Event) bool {
		s, ok := e.(events.PlayerState)
		return ok && s.Path == long && s.Position >= 20*time.Second && s.Volume == 37 && !s.Idle &&
			(s.Duration-longDur).Abs() < 100*time.Millisecond
	})
	if err := m.Seek(ctx, 40*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	sk.wait(t, 5*time.Second, func(e events.Event) bool {
		s, ok := e.(events.PlayerState)
		return ok && s.Paused && s.Position >= 40*time.Second
	})
	m.Resume(ctx)

	// Kill mpv mid-play: PlayerDown, a new mpv, idle, not resumed.
	syscall.Kill(pid, syscall.SIGKILL)
	sk.wait(t, 5*time.Second, func(e events.Event) bool { _, ok := e.(events.PlayerDown); return ok })
	deadline := time.Now().Add(StartTimeout + time.Second)
	var pid2 int
	for time.Now().Before(deadline) {
		m.mu.Lock()
		if m.p != nil && m.c != nil {
			pid2 = m.p.pid()
		}
		m.mu.Unlock()
		if pid2 != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid2 == 0 || pid2 == pid {
		t.Fatalf("mpv not restarted (pid %d → %d)", pid, pid2)
	}
	if got, _, _ := readPidfile(pidfile); got != pid2 {
		t.Errorf("pidfile %d after restart, want %d", got, pid2)
	}
	if n := count[events.TrackEnded](sk); n != 1 {
		t.Errorf("%d TrackEnded, want 1 (the crash is not an end)", n)
	}
	// It works after the restart.
	if err := m.Load(ctx, short, 0); err != nil {
		t.Fatal(err)
	}
	sk.wait(t, 5*time.Second, func(e events.Event) bool {
		e2, ok := e.(events.TrackEnded)
		return ok && count[events.TrackEnded](sk) == 2 && e2.Path == short
	})

	// Close stops the process and removes the pidfile and socket dir.
	m.mu.Lock()
	sock := m.c.nc.RemoteAddr().String()
	m.mu.Unlock()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if alive(pid2) {
		t.Errorf("mpv %d still running after Close", pid2)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Errorf("pidfile left behind: %v", err)
	}
	if sock != "" {
		if _, err := os.Stat(filepath.Dir(sock)); !os.IsNotExist(err) {
			t.Errorf("socket dir left behind: %v", err)
		}
	}
}

func TestStartWithoutMPV(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Start(Config{Sink: newSink()}); err != ErrNotInstalled {
		t.Fatalf("got %v, want ErrNotInstalled", err)
	}
}

func TestCleanOrphanKillsOrphanMPV(t *testing.T) {
	bin := needMPV(t)
	dir, err := os.MkdirTemp("", socketDirPrefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "mpv.sock")
	// The shell exits at once, so mpv is reparented to launchd: an orphan.
	out, err := exec.Command("/bin/sh", "-c",
		`"$0" --idle --no-video --no-terminal --ao=null --input-ipc-server="$1" </dev/null >/dev/null 2>&1 & echo $!`,
		bin, sock).Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if alive(pid) && isOrphanMPV(pid, sock) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	// Wait until ps shows it reparented.
	deadline := time.Now().Add(2 * time.Second)
	for !isOrphanMPV(pid, sock) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	pidfile := filepath.Join(t.TempDir(), "mpv.pid")
	// A pidfile naming the same PID with another socket is not trusted.
	writePidfile(pidfile, pid, sock+"-other")
	if killed, err := CleanOrphan(pidfile); killed || err != nil {
		t.Fatalf("killed=%v err=%v for a socket mismatch", killed, err)
	}
	if !alive(pid) {
		t.Fatal("mpv killed on a socket mismatch")
	}

	writePidfile(pidfile, pid, sock)
	killed, err := CleanOrphan(pidfile)
	if err != nil || !killed {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
	if alive(pid) && isOrphanMPV(pid, sock) {
		t.Error("orphan still alive")
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Error("pidfile not removed")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("orphan's socket dir not removed")
	}
}

func TestCleanOrphanLeavesOthersAlone(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "mpv.pid")

	// No pidfile: nothing to do.
	if killed, err := CleanOrphan(pidfile); killed || err != nil {
		t.Fatalf("no pidfile: %v %v", killed, err)
	}

	// A live process that is not an orphan mpv: this test binary.
	writePidfile(pidfile, os.Getpid(), "/tmp/crates-mpv-x/mpv.sock")
	if killed, err := CleanOrphan(pidfile); killed || err != nil {
		t.Fatalf("live non-mpv: %v %v", killed, err)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Error("stale pidfile not removed")
	}

	// A dead PID.
	cmd := exec.Command("/usr/bin/true")
	cmd.Run()
	writePidfile(pidfile, cmd.Process.Pid, "/tmp/crates-mpv-x/mpv.sock")
	if killed, err := CleanOrphan(pidfile); killed || err != nil {
		t.Fatalf("dead pid: %v %v", killed, err)
	}

	// Garbage.
	os.WriteFile(pidfile, []byte("hello"), 0o600)
	if killed, err := CleanOrphan(pidfile); killed || err != nil {
		t.Fatalf("garbage: %v %v", killed, err)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Error("garbage pidfile not removed")
	}
}
