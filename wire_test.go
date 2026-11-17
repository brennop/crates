package main

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/pathnorm"
	"github.com/brennop/crates/tags"
)

// waitFor reads the bus until an event matches.
func waitFor(t *testing.T, bus *events.Bus, pred func(events.Event) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		e, err := bus.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for an event: %v", err)
		}
		if pred(e) {
			return
		}
	}
}

func writeMP3(t *testing.T, path, title string) {
	t.Helper()
	b := fixture.MP3(fixture.Options{Tag: fixture.ID3v24, Summary: model.TagSummary{Title: title, Artist: "A", Album: "B"}, Xing: true})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBuildAndClose wires the real pieces (mpv, kid3-cli when present, the
// library and its cache; Music is the fake, so the real library is never
// touched), then checks that Close leaves nothing behind: no mpv, socket
// dir, pidfile or kid3 config, and a cache that holds the last batch.
func TestBuildAndClose(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not installed")
	}
	root, err := pathnorm.Normalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The socket dir and the kid3 config go under TMPDIR. A fresh, short
	// one shows what is left after Close and keeps the socket path short.
	tmp, err := os.MkdirTemp("", "c")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)

	master, inbox := filepath.Join(root, "master"), filepath.Join(root, "inbox")
	for _, d := range []string{master, inbox} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeMP3(t, filepath.Join(master, "a.mp3"), "a")
	// A crashed copy's temp file is removed before the first sweep.
	stale := filepath.Join(master, ".crates-copy-123.mp3")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.MasterDir, cfg.InboxDirs, cfg.DeleteMode = master, []string{inbox}, config.DeleteRemove
	fake := music.NewFake()
	opts := options{
		CachePath: filepath.Join(root, "cache", "library.db"),
		PidFile:   filepath.Join(root, "mpv.pid"),
		MPVArgs:   []string{"--ao=null"},
		Music:     fake,
	}
	a, bus, err := build(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			a.Close()
		}
	})

	waitFor(t, bus, func(e events.Event) bool { _, ok := e.(events.LibraryLoaded); return ok })
	waitFor(t, bus, func(e events.Event) bool {
		tc, ok := e.(events.TracksChanged)
		return ok && len(tc.Tracks) == 1 && tc.Tracks[0].Summary.Title == "a"
	})
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale copy temp survived startup: %v", err)
	}
	// The reconciler reads the snapshot and adds a.mp3 to Music.
	deadline := time.Now().Add(10 * time.Second)
	for len(fake.CallsOf(music.OpAdd)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the reconciler never added the master track")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The last batch before Close: a new master file found by a sweep.
	writeMP3(t, filepath.Join(master, "b.mp3"), "b")
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	pid, sock := readPidfile(t, opts.PidFile)
	if syscall.Kill(pid, 0) != nil {
		t.Fatalf("mpv %d is not running", pid)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket: %v", err)
	}
	_, kid3 := exec.LookPath("kid3-cli")
	if kid3 == nil && !hasEntry(t, tmp, "crates-kid3-") {
		t.Error("no private kid3 config under TMPDIR")
	}

	closed = true
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}

	if syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Errorf("mpv %d still running after Close", pid)
	}
	if _, err := os.Stat(opts.PidFile); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("pidfile remains: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(sock)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("socket dir remains: %v", err)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		names := make([]string, len(left))
		for i, e := range left {
			names[i] = e.Name()
		}
		t.Errorf("left under TMPDIR: %v", names)
	}
	if !fake.Closed() {
		t.Error("the Music service was not closed")
	}

	// The cache holds both tracks.
	lib, err := library.Open(library.Config{
		Master: master, Inboxes: []string{inbox}, CachePath: opts.CachePath,
		Reader: tags.NewFake(), Sink: events.NewBus(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	titles := map[string]bool{}
	for _, tr := range lib.MasterTracks() {
		titles[tr.Summary.Title] = true
	}
	if !titles["a"] || !titles["b"] || len(titles) != 2 {
		t.Errorf("reopened cache holds %v, want a and b", titles)
	}
}

func readPidfile(t *testing.T, path string) (int, string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("pidfile = %q", lines)
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	return pid, lines[1]
}

func hasEntry(t *testing.T, dir, prefix string) bool {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), prefix) {
			return true
		}
	}
	return false
}

func TestRunChecks(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := runChecks(); err == nil || !strings.Contains(err.Error(), "mpv") {
		t.Errorf("no mpv on PATH: err = %v", err)
	}
}
