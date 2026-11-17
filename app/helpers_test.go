package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/fsops"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/pathnorm"
	"github.com/brennop/crates/player"
	"github.com/brennop/crates/tags"
)

// recorder is the UI's bus in tests: it keeps every event, and waitFor
// blocks until one matches, so tests need no sleeps.
type recorder struct {
	mu   sync.Mutex
	evs  []events.Event
	wake chan struct{}
}

func newRecorder() *recorder { return &recorder{wake: make(chan struct{})} }

func (r *recorder) Publish(e events.Event) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	close(r.wake)
	r.wake = make(chan struct{})
	r.mu.Unlock()
}

func (r *recorder) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Event(nil), r.evs...)
}

// mark returns the number of events so far, for waitFor's from.
func (r *recorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.evs)
}

// waitFor returns the first event at or after index from that matches.
func (r *recorder) waitFor(t *testing.T, from int, pred func(events.Event) bool) events.Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		r.mu.Lock()
		for _, e := range r.evs[from:] {
			if pred(e) {
				r.mu.Unlock()
				return e
			}
		}
		wake := r.wake
		r.mu.Unlock()
		select {
		case <-wake:
		case <-deadline:
			t.Fatal("timed out waiting for an event")
		}
	}
}

func is[T events.Event](e events.Event) bool {
	_, ok := e.(T)
	return ok
}

// stubFS is fsops with failures on demand.
type stubFS struct {
	mu      sync.Mutex
	move    error
	replace error // returned instead of replacing
	kept    bool  // replace, then return ErrBackupKept
	del     error
}

func (s *stubFS) Move(src, dst string) error {
	s.mu.Lock()
	err := s.move
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return fsops.Move(src, dst)
}

func (s *stubFS) Replace(src, dst string, mode fsops.Mode) error {
	s.mu.Lock()
	err, kept := s.replace, s.kept
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if kept {
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		return model.ErrBackupKept{Path: "/held/" + filepath.Base(dst), Err: errors.New("trash failed")}
	}
	return fsops.Replace(src, dst, mode)
}

func (s *stubFS) Delete(path string, mode fsops.Mode) error {
	s.mu.Lock()
	err := s.del
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return fsops.Delete(path, mode)
}

type env struct {
	t      *testing.T
	ctx    context.Context
	app    *App
	lib    *library.Library
	tags   *tags.Fake
	music  *music.Fake
	player *player.Fake
	bus    *recorder
	fs     *stubFS
	master string
	inbox  string
}

var tagged = model.TagSummary{Title: "Title", Artist: "Artist", Album: "Album"}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, nil)
}

// newEnvWith is newEnv with settings changed by edit before App is built.
func newEnvWith(t *testing.T, edit func(*config.Config)) *env {
	t.Helper()
	root, err := pathnorm.Normalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t:      t,
		ctx:    context.Background(),
		tags:   tags.NewFake(),
		music:  music.NewFake(),
		bus:    newRecorder(),
		fs:     &stubFS{},
		master: filepath.Join(root, "master"),
		inbox:  filepath.Join(root, "inbox"),
	}
	for _, d := range []string{e.master, e.inbox} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.lib, err = library.Open(library.Config{
		Master:    e.master,
		Inboxes:   []string{e.inbox},
		CachePath: filepath.Join(root, "cache.db"),
		Reader:    e.tags,
		Sink:      e.bus,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.lib.Start(ctx); err != nil {
		t.Fatal(err)
	}
	e.bus.waitFor(t, 0, func(ev events.Event) bool {
		p, ok := ev.(events.ScanProgress)
		return ok && p.Done
	})
	settings := config.Defaults()
	settings.DeleteMode = config.DeleteRemove
	if edit != nil {
		edit(&settings)
	}
	e.app, err = New(Config{
		Library: e.lib,
		Tags:    e.tags,
		Music:   e.music,
		NewPlayer: func(s events.Sink) (player.Player, error) {
			e.player = player.NewFake(s)
			return e.player, nil
		},
		Bus:      e.bus,
		Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.app.fs = e.fs
	t.Cleanup(func() {
		e.app.Close()
		cancel()
		e.lib.Close()
	})
	return e
}

// file writes a track file under dir, gives it summary s in the tag fake and
// indexes it.
func (e *env) file(dir, name string, s model.TagSummary) model.Track {
	e.t.Helper()
	p := pathnorm.Child(dir, name)
	if err := os.WriteFile(p, []byte("audio of "+name), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.tags.PutSummary(p, time.Second, s)
	fi, err := os.Stat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	tr, err := e.lib.Upsert(model.Track{Path: p, Size: fi.Size(), ModTime: fi.ModTime(), Summary: s, Duration: time.Second})
	if err != nil {
		e.t.Fatal(err)
	}
	return tr
}

// inMusic adds a master track to the fake Music and stores its MusicID.
func (e *env) inMusic(tr model.Track) model.Track {
	e.t.Helper()
	id, err := e.music.Add(e.ctx, tr.Path)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.lib.SetMusicID(tr.ID, id); err != nil {
		e.t.Fatal(err)
	}
	tr.MusicID = id
	return tr
}

// get returns the indexed track, failing the test if it is gone.
func (e *env) get(id model.TrackID) model.Track {
	e.t.Helper()
	ts := e.lib.Get(id)
	if len(ts) != 1 {
		e.t.Fatalf("track %d is not indexed", id)
	}
	return ts[0]
}

func (e *env) indexed(id model.TrackID) bool { return len(e.lib.Get(id)) == 1 }

// snapshot loads the fake Music's state into the app.
func (e *env) snapshot() {
	e.t.Helper()
	if err := e.app.refreshSnapshot(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) playerOps() []string {
	var ops []string
	for _, c := range e.player.Calls() {
		ops = append(ops, c.Op)
	}
	return ops
}

func (e *env) lastLoad() player.Call {
	e.t.Helper()
	calls := e.player.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Op == "load" {
			return calls[i]
		}
	}
	e.t.Fatal("no load call")
	return player.Call{}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
