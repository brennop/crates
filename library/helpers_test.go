package library

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

// recordSink keeps every published event in order. waitFor blocks until an
// event matches, so tests need no sleeps.
type recordSink struct {
	mu   sync.Mutex
	evs  []events.Event
	wake chan struct{}
}

func newSink() *recordSink { return &recordSink{wake: make(chan struct{})} }

func (s *recordSink) Publish(e events.Event) {
	s.mu.Lock()
	s.evs = append(s.evs, e)
	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
}

func (s *recordSink) all() []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]events.Event(nil), s.evs...)
}

func (s *recordSink) count(pred func(events.Event) bool) int {
	n := 0
	for _, e := range s.all() {
		if pred(e) {
			n++
		}
	}
	return n
}

func (s *recordSink) waitFor(t *testing.T, pred func(events.Event) bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		s.mu.Lock()
		for _, e := range s.evs {
			if pred(e) {
				s.mu.Unlock()
				return
			}
		}
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-wake:
		case <-deadline:
			t.Fatal("timed out waiting for an event")
		}
	}
}

func isScanDone(e events.Event) bool {
	p, ok := e.(events.ScanProgress)
	return ok && p.Done
}

// fakeReader returns a summary built from the file name. It can fail or
// shorten named files, and it blocks every read until gate is closed when gate
// is set.
type fakeReader struct {
	mu        sync.Mutex
	calls     int
	fail      map[string]error
	durations map[string]time.Duration // overrides the 1 s default
	gate      chan struct{}
}

func (r *fakeReader) Summary(path string) (model.TagSummary, time.Duration, error) {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.calls++
	err := r.fail[path]
	d, ok := r.durations[path]
	r.mu.Unlock()
	if err != nil {
		return model.TagSummary{}, 0, err
	}
	if !ok {
		d = time.Second
	}
	return model.TagSummary{Title: filepath.Base(path), Artist: "Artist"}, d, nil
}

func (r *fakeReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// env is a master dir, an inbox and a cache, all under one temp dir.
type env struct {
	t      *testing.T
	master string
	inbox  string
	cache  string
	sink   *recordSink
	reader *fakeReader
	// noInbox leaves the inbox out of the config, for tests that count passes.
	noInbox bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	// The library stores normalized paths, so the test dirs must be resolved
	// too: t.TempDir sits under /var, which is a symlink.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t:      t,
		master: filepath.Join(dir, "Master"),
		inbox:  filepath.Join(dir, "Inbox"),
		cache:  filepath.Join(dir, "state", "library.db"),
		sink:   newSink(),
		reader: &fakeReader{},
	}
	for _, d := range []string{e.master, e.inbox} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *env) config() Config {
	cfg := Config{
		Master:    e.master,
		Inboxes:   []string{e.inbox},
		CachePath: e.cache,
		Reader:    e.reader,
		Sink:      e.sink,
	}
	if e.noInbox {
		cfg.Inboxes = nil
	}
	return cfg
}

// open opens the library and closes it when the test ends.
func (e *env) open() *Library {
	e.t.Helper()
	l, err := Open(e.config())
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { l.Close() })
	return l
}

// put writes an n-byte file at path, creating its directory.
func put(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, l *Library, r *root) {
	t.Helper()
	if err := l.scanRoot(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func (l *Library) masterRoot() *root { return l.roots[0] }
func (l *Library) inboxRoot() *root  { return l.roots[1] }

// all returns copies of every indexed track.
func all(l *Library) []model.Track {
	l.idx.mu.RLock()
	defer l.idx.mu.RUnlock()
	out := make([]model.Track, 0, len(l.idx.tracks))
	for _, t := range l.idx.tracks {
		out = append(out, t)
	}
	return out
}

// byTitle indexes tracks by title, which the fake reader sets to the file name.
func byTitle(ts []model.Track) map[string]model.Track {
	out := make(map[string]model.Track, len(ts))
	for _, t := range ts {
		out[t.Summary.Title] = t
	}
	return out
}

// close shuts l down and forgets it, so the cache file can be reopened.
func (e *env) close(l *Library) {
	e.t.Helper()
	if err := l.Close(); err != nil {
		e.t.Fatal(err)
	}
}

// reopen opens the same cache with the same config and returns the library
// without scanning it, so it shows what the cache holds.
func reopen(t *testing.T, e *env) *Library {
	t.Helper()
	l, err := Open(e.config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
