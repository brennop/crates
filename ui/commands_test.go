package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/pathnorm"
	"github.com/brennop/crates/player"
	"github.com/brennop/crates/tags"
)

type env struct {
	t      *testing.T
	ctx    context.Context
	app    *app.App
	lib    *library.Library
	tags   *tags.Fake
	music  *music.Fake
	player *player.Fake
	bus    *events.Bus
	master string
	inbox  string
}

var tagged = model.TagSummary{Title: "Title", Artist: "Artist", Album: "Album"}

// newEnv builds a real App over fakes, with the library started and its
// first scan done. Library events go to their own bus; the app's is e.bus.
func newEnv(t *testing.T) *env {
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
		bus:    events.NewBus(),
		master: filepath.Join(root, "master"),
		inbox:  filepath.Join(root, "inbox"),
	}
	for _, d := range []string{e.master, e.inbox} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	libBus := events.NewBus()
	e.lib, err = library.Open(library.Config{
		Master:    e.master,
		Inboxes:   []string{e.inbox},
		CachePath: filepath.Join(root, "cache.db"),
		Reader:    e.tags,
		Sink:      libBus,
	})
	if err != nil {
		t.Fatal(err)
	}
	libCtx, stopLib := context.WithCancel(context.Background())
	if err := e.lib.Start(libCtx); err != nil {
		t.Fatal(err)
	}
	waitScan(t, libBus)
	settings := config.Defaults()
	settings.DeleteMode = config.DeleteRemove
	e.app, err = app.New(app.Config{
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
	t.Cleanup(func() {
		e.app.Close()
		stopLib()
		e.lib.Close()
	})
	return e
}

// waitScan drains the library's bus until its first scan is done.
func waitScan(t *testing.T, bus *events.Bus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		ev, err := bus.Next(ctx)
		if err != nil {
			t.Fatal("library scan did not finish")
		}
		if p, ok := ev.(events.ScanProgress); ok && p.Done {
			return
		}
	}
}

// file writes a track under dir and indexes it.
func (e *env) file(dir, name string) model.Track {
	e.t.Helper()
	p := pathnorm.Child(dir, name)
	if err := os.WriteFile(p, []byte("audio of "+name), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.tags.PutSummary(p, time.Second, tagged)
	fi, err := os.Stat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	tr, err := e.lib.Upsert(model.Track{Path: p, Size: fi.Size(), ModTime: fi.ModTime(), Summary: tagged, Duration: time.Second})
	if err != nil {
		e.t.Fatal(err)
	}
	return tr
}

// inMusic adds a master track to the fake Music and records its MusicID.
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

// run executes cmd and returns its message as T, failing if it is not one.
func run[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	if cmd == nil {
		t.Fatal("nil command")
	}
	msg := cmd()
	got, ok := msg.(T)
	if !ok {
		var want T
		t.Fatalf("message = %T, want %T", msg, want)
	}
	return got
}

func noErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func TestLibraryCommands(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	b := e.file(e.inbox, "b.mp3")

	q := run[QueryResult](t, Query(e.ctx, e.app, 7, app.Filter{}, app.Sort{Field: "path"}))
	noErr(t, "query", q.Err)
	if q.Gen != 7 || !slices.Equal(q.IDs, []model.TrackID{b.ID, a.ID}) {
		t.Errorf("query = %+v, want gen 7, [%d %d]", q, b.ID, a.ID)
	}
	q = run[QueryResult](t, Query(e.ctx, e.app, 8, app.Filter{}, app.Sort{Field: "nope"}))
	if q.Gen != 8 || q.Err == nil {
		t.Errorf("bad sort: %+v, want gen 8 and an error", q)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	if q := run[QueryResult](t, Query(ctx, e.app, 9, app.Filter{}, app.Sort{})); !errors.Is(q.Err, context.Canceled) {
		t.Errorf("cancelled query err = %v", q.Err)
	}

	tr := run[TracksResult](t, Tracks(e.app, []model.TrackID{a.ID, 999, b.ID}))
	if len(tr.Tracks) != 2 || tr.Tracks[0].ID != a.ID || tr.Tracks[1].ID != b.ID {
		t.Errorf("tracks = %+v", tr.Tracks)
	}

	noErr(t, "refresh", run[RefreshResult](t, Refresh(e.ctx, e.app)).Err)
	noErr(t, "focus", run[FocusGainedResult](t, FocusGained(e.ctx, e.app)).Err)
}

func TestFileCommands(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "taken.mp3")
	fresh := e.file(e.inbox, "fresh.mp3")
	clash := e.file(e.inbox, "taken.mp3")
	gone := e.file(e.inbox, "gone.mp3")

	p := run[PromoteResult](t, Promote(e.ctx, e.app, []model.TrackID{fresh.ID, clash.ID})).Summary
	if !slices.Equal(p.Promoted, []model.TrackID{fresh.ID}) || len(p.Collisions) != 1 || p.Collisions[0].Inbox != clash.ID {
		t.Errorf("promote = %+v", p)
	}

	o := run[OverwriteResult](t, Overwrite(e.ctx, e.app, clash.ID))
	if o.Inbox != clash.ID || o.Err != nil {
		t.Errorf("overwrite = %+v", o)
	}
	o = run[OverwriteResult](t, Overwrite(e.ctx, e.app, fresh.ID)) // now in master
	var ni model.ErrNotInbox
	if !errors.As(o.Err, &ni) || ni.ID != fresh.ID {
		t.Errorf("overwrite of a master track: err = %v, want ErrNotInbox", o.Err)
	}

	d := run[DeleteResult](t, Delete(e.ctx, e.app, []model.TrackID{gone.ID})).Summary
	if !slices.Equal(d.Deleted, []model.TrackID{gone.ID}) || len(d.Failures) != 0 {
		t.Errorf("delete = %+v", d)
	}
}

func TestTagCommands(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")

	f := run[FullTagsResult](t, FullTags(e.ctx, e.app, a.ID))
	noErr(t, "full tags", f.Err)
	if f.ID != a.ID || len(f.Frames) == 0 {
		t.Errorf("full tags = %+v", f)
	}

	ch := []model.FrameChange{{Name: "Title", Value: "New"}}
	w := run[WriteTagsResult](t, WriteTags(e.ctx, e.app, []model.TrackID{a.ID}, ch))
	noErr(t, "write tags", w.Err)
	if !slices.Equal(w.IDs, []model.TrackID{a.ID}) {
		t.Errorf("write ids = %v", w.IDs)
	}

	e.tags.SetMissing(true)
	f = run[FullTagsResult](t, FullTags(e.ctx, e.app, a.ID))
	if !errors.As(f.Err, new(model.ErrKid3Missing)) {
		t.Errorf("without kid3: err = %v, want ErrKid3Missing", f.Err)
	}
}

func TestPlaylistCommands(t *testing.T) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3"))
	e.music.AddPlaylist(model.Playlist{ID: "p1", Name: "Mix", Editable: true})
	noErr(t, "refresh", run[RefreshResult](t, Refresh(e.ctx, e.app)).Err)

	pl := run[PlaylistsResult](t, Playlists(e.app))
	if len(pl.Playlists) != 1 || pl.Playlists[0].ID != "p1" {
		t.Fatalf("playlists = %+v", pl.Playlists)
	}

	ids := []model.TrackID{a.ID}
	add := run[AddToPlaylistResult](t, AddToPlaylist(e.ctx, e.app, ids, "p1"))
	noErr(t, "add", add.Err)
	if add.PlaylistID != "p1" || !slices.Equal(add.IDs, ids) {
		t.Errorf("add = %+v", add)
	}
	m := run[MembershipsResult](t, Memberships(e.app, a.ID))
	noErr(t, "memberships", m.Err)
	if m.ID != a.ID || len(m.Playlists) != 1 || m.Playlists[0].ID != "p1" {
		t.Errorf("memberships after add = %+v", m)
	}

	rm := run[RemoveFromPlaylistResult](t, RemoveFromPlaylist(e.ctx, e.app, ids, "p1"))
	noErr(t, "remove", rm.Err)
	if m := run[MembershipsResult](t, Memberships(e.app, a.ID)); len(m.Playlists) != 0 {
		t.Errorf("memberships after remove = %+v", m.Playlists)
	}

	bad := run[AddToPlaylistResult](t, AddToPlaylist(e.ctx, e.app, ids, "nope"))
	if !errors.Is(bad.Err, app.ErrUnknownPlaylist) {
		t.Errorf("unknown playlist: err = %v", bad.Err)
	}
}

func TestPlaybackCommands(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	b := e.file(e.master, "b.mp3")

	p := run[PlayResult](t, Play(e.ctx, e.app, a.ID))
	if p.ID != a.ID || p.Err != nil {
		t.Errorf("play = %+v", p)
	}
	if en := run[EnqueueResult](t, Enqueue(e.app, b.ID)); !slices.Equal(en.IDs, []model.TrackID{b.ID}) {
		t.Errorf("enqueue = %+v", en)
	}
	q := run[QueueResult](t, Queue(e.app)).Queue
	if !slices.Equal(q.IDs, []model.TrackID{a.ID, b.ID}) || q.Pos != 0 || q.Playing != a.ID {
		t.Errorf("queue = %+v", q)
	}

	noErr(t, "next", run[NextResult](t, Next(e.ctx, e.app)).Err)
	noErr(t, "previous", run[PreviousResult](t, Previous(e.ctx, e.app)).Err)
	noErr(t, "pause", run[TogglePauseResult](t, TogglePause(e.ctx, e.app)).Err)
	noErr(t, "seek", run[SeekResult](t, Seek(e.ctx, e.app, 5*time.Second, true)).Err)
	noErr(t, "volume", run[SetVolumeResult](t, SetVolume(e.ctx, e.app, 40)).Err)

	r := run[RemoveFromQueueResult](t, RemoveFromQueue(e.ctx, e.app, 1))
	if r.Index != 1 || r.Err != nil {
		t.Errorf("remove from queue = %+v", r)
	}
	r = run[RemoveFromQueueResult](t, RemoveFromQueue(e.ctx, e.app, 5))
	if !errors.Is(r.Err, app.ErrQueueIndex) {
		t.Errorf("bad index: err = %v", r.Err)
	}
	noErr(t, "clear", run[ClearQueueResult](t, ClearQueue(e.ctx, e.app)).Err)
	if q := run[QueueResult](t, Queue(e.app)).Queue; len(q.IDs) != 0 {
		t.Errorf("queue after clear = %+v", q)
	}

	if p := run[PlayResult](t, Play(e.ctx, e.app, 999)); !errors.Is(p.Err, app.ErrUnknownTrack) {
		t.Errorf("play unknown: err = %v", p.Err)
	}
}

func TestMusicCommands(t *testing.T) {
	e := newEnv(t)
	if s := run[MusicStatusResult](t, MusicStatus(e.app)); s.State != model.MusicUnavailable {
		t.Errorf("status before any call = %v", s.State)
	}
	e.music.SetRunning(false)
	noErr(t, "launch", run[LaunchMusicResult](t, LaunchMusic(e.ctx, e.app)).Err)
	if e.music.Launches() != 1 {
		t.Errorf("launches = %d", e.music.Launches())
	}
	if s := run[MusicStatusResult](t, MusicStatus(e.app)); s.State != model.MusicAvailable {
		t.Errorf("status after launch = %v", s.State)
	}
	if p := run[PendingImportResult](t, PendingImport(e.app)); p.Count != 0 {
		t.Errorf("pending import with none waiting = %d", p.Count)
	}
	run[ConfirmImportResult](t, ConfirmImport(e.app))
	run[CancelImportResult](t, CancelImport(e.app))
	if d := run[DeadEntriesResult](t, DeadEntries(e.app)); len(d.Entries) != 0 {
		t.Errorf("dead = %+v", d.Entries)
	}
}

func TestOtherCommands(t *testing.T) {
	e := newEnv(t)
	c := run[CapabilitiesResult](t, Capabilities(e.app)).Capabilities
	if !c.Kid3 || c.DeleteMode != "remove" || !c.MusicEnabled || c.TrashMethod == "" {
		t.Errorf("capabilities = %+v", c)
	}
	e.tags.SetMissing(true)
	if c := run[CapabilitiesResult](t, Capabilities(e.app)).Capabilities; c.Kid3 {
		t.Error("Kid3 still reported after SetMissing")
	}
	noErr(t, "close", run[CloseResult](t, Close(e.app)).Err)
	if ops := e.player.Calls(); len(ops) == 0 || ops[len(ops)-1].Op != "close" {
		t.Errorf("player calls = %+v, want a final close", ops)
	}
}

func TestWaitForEventInBusOrder(t *testing.T) {
	bus := events.NewBus()
	want := []events.Event{
		events.LibraryLoaded{Count: 3},
		events.TrackRemoved{IDs: []model.TrackID{1}},
		events.ImportPending{Count: 2},
		events.PlaylistsChanged{},
	}
	for _, ev := range want {
		bus.Publish(ev)
	}
	bus.Publish(events.PlayerState{Position: time.Second})
	bus.Publish(events.PlayerState{Position: 2 * time.Second}) // latest wins

	for i, w := range want {
		got := WaitForEvent(bus)()
		if !eventEqual(got, w) {
			t.Fatalf("event %d = %#v, want %#v", i, got, w)
		}
	}
	got := WaitForEvent(bus)()
	if s, ok := got.(events.PlayerState); !ok || s.Position != 2*time.Second {
		t.Fatalf("state = %#v, want the latest PlayerState", got)
	}
}

func TestWaitForEventBlocksUntilPublish(t *testing.T) {
	bus := events.NewBus()
	got := make(chan tea.Msg, 1)
	go func() { got <- WaitForEvent(bus)() }()
	select {
	case m := <-got:
		t.Fatalf("returned %#v from an empty bus", m)
	case <-time.After(20 * time.Millisecond):
	}
	bus.Publish(events.DeadEntry{MusicID: "x", Path: "/m/x.mp3"})
	select {
	case m := <-got:
		if d, ok := m.(events.DeadEntry); !ok || d.MusicID != "x" {
			t.Fatalf("got %#v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForEvent did not wake on publish")
	}
}

// TestPumpCarriesAppEvents runs the pump the way Update would, re-arming it
// after each event, and checks that app's own events arrive.
func TestPumpCarriesAppEvents(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	run[PlayResult](t, Play(e.ctx, e.app, a.ID))
	e.player.Emit(events.TrackEnded{Path: a.Path})

	cmd := WaitForEvent(e.bus)
	deadline := time.After(5 * time.Second)
	for {
		msgs := make(chan tea.Msg, 1)
		go func() { msgs <- cmd() }()
		select {
		case m := <-msgs:
			if end, ok := m.(events.TrackEnded); ok {
				if end.TrackID != a.ID {
					t.Errorf("TrackEnded.TrackID = %d, want %d", end.TrackID, a.ID)
				}
				return
			}
			cmd = WaitForEvent(e.bus) // re-arm
		case <-deadline:
			t.Fatal("TrackEnded never arrived")
		}
	}
}

func eventEqual(a, b tea.Msg) bool {
	switch x := a.(type) {
	case events.TrackRemoved:
		y, ok := b.(events.TrackRemoved)
		return ok && slices.Equal(x.IDs, y.IDs)
	}
	return a == b
}
