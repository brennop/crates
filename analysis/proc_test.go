package analysis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// grandchild runs a shell that starts a background sleep (as uv starts
// Python), writes the sleep's pid to a file and waits. It returns the pid
// file and a function that reports whether that sleep is still alive.
func grandchild(t *testing.T) (string, []string, func() bool) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	args := []string{"-c", `sleep 60 & echo $! > "$1"; wait`, "sh", pidFile}
	alive := func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal("grandchild never started")
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return syscall.Kill(pid, 0) == nil
	}
	return pidFile, args, alive
}

// waitFile waits for path to exist.
func waitFile(t *testing.T, path string) {
	t.Helper()
	for range 100 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + path)
}

func TestRunGroupCancelKillsGrandchildren(t *testing.T) {
	pidFile, args, alive := grandchild(t)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, _, err := runGroup(ctx, "/bin/sh", args...)
		errc <- err
	}()
	waitFile(t, pidFile)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if alive() {
		t.Fatal("the grandchild outlived cancel")
	}
}

func TestRunGroupMemoryLimit(t *testing.T) {
	old := groupRSS
	defer func() { groupRSS = old }()
	pidFile, args, alive := grandchild(t)
	var asked int
	groupRSS = func(pgid int) (int64, error) {
		if _, err := os.Stat(pidFile); err != nil {
			return 0, nil
		}
		asked = pgid
		return MemoryLimit + 1, nil
	}
	_, _, err := runGroup(context.Background(), "/bin/sh", args...)
	if !errors.Is(err, ErrTooMuchMemory) {
		t.Fatalf("err = %v", err)
	}
	if asked == 0 {
		t.Fatal("the cap was never checked")
	}
	time.Sleep(100 * time.Millisecond)
	if alive() {
		t.Fatal("the grandchild outlived the cap")
	}
}

func TestGroupRSSCountsThisProcess(t *testing.T) {
	rss, err := groupRSS(syscall.Getpgrp())
	if err != nil || rss <= 0 {
		t.Fatalf("rss %d, err %v", rss, err)
	}
}
