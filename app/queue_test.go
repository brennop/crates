package app

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

func TestQueueBasics(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	c := e.file(e.master, "c.mp3", tagged)

	e.app.Enqueue(a.ID, b.ID)
	if q := e.app.Queue(); q.Pos != -1 || q.Playing != 0 {
		t.Fatalf("enqueue started playback: %+v", q)
	}
	if err := e.app.TogglePause(e.ctx); err != nil { // nothing current yet
		t.Fatal(err)
	}
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	if q := e.app.Queue(); q.Pos != 0 || q.Playing != a.ID {
		t.Fatalf("queue = %+v", q)
	}
	// Play of a track not queued inserts it after the current one.
	if err := e.app.Play(e.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	q := e.app.Queue()
	if !slices.Equal(q.IDs, []model.TrackID{a.ID, c.ID, b.ID}) || q.Pos != 1 || q.Playing != c.ID {
		t.Fatalf("queue = %+v", q)
	}
	// Play of a queued track jumps to it.
	if err := e.app.Play(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if q := e.app.Queue(); q.Pos != 0 || len(q.IDs) != 3 {
		t.Fatalf("queue = %+v", q)
	}
	if err := e.app.Previous(e.ctx); err != nil { // at the start: restart
		t.Fatal(err)
	}
	if l := e.lastLoad(); l.Path != a.Path || l.Pos != 0 {
		t.Fatalf("load = %+v", l)
	}

	// Remove an entry before the current one: position follows the track.
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.app.RemoveFromQueue(e.ctx, 0); err != nil {
		t.Fatal(err)
	}
	if q := e.app.Queue(); q.Pos != 0 || q.IDs[0] != c.ID || q.Playing != c.ID {
		t.Fatalf("queue = %+v", q)
	}
	// Remove the playing entry: stop, stay on the next entry.
	if err := e.app.RemoveFromQueue(e.ctx, 0); err != nil {
		t.Fatal(err)
	}
	if q := e.app.Queue(); q.Pos != 0 || q.Playing != 0 || !slices.Equal(q.IDs, []model.TrackID{b.ID}) {
		t.Fatalf("queue = %+v", q)
	}
	if err := e.app.RemoveFromQueue(e.ctx, 5); !errors.Is(err, ErrQueueIndex) {
		t.Fatalf("err = %v", err)
	}
	if err := e.app.ClearQueue(e.ctx); err != nil {
		t.Fatal(err)
	}
	if q := e.app.Queue(); len(q.IDs) != 0 || q.Pos != -1 {
		t.Fatalf("queue = %+v", q)
	}
	if err := e.app.Play(e.ctx, 4242); !errors.Is(err, ErrUnknownTrack) {
		t.Fatalf("err = %v", err)
	}
}

func TestTransport(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	if err := e.app.Play(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	from := e.bus.mark()
	e.player.Emit(events.PlayerState{Path: a.Path, Position: 30 * time.Second})
	e.bus.waitFor(t, from, is[events.PlayerState])

	if err := e.app.TogglePause(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Seek(e.ctx, -10*time.Second, true); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Seek(e.ctx, -time.Hour, true); err != nil {
		t.Fatal(err)
	}
	if err := e.app.SetVolume(e.ctx, 150); err != nil {
		t.Fatal(err)
	}
	calls := e.player.Calls()[1:]
	if calls[0].Op != "pause" || calls[1].Pos != 20*time.Second || calls[2].Pos != 0 || calls[3].Volume != 100 {
		t.Fatalf("calls = %+v", calls)
	}
	from = e.bus.mark()
	e.player.Emit(events.PlayerState{Path: a.Path, Paused: true, Position: 30 * time.Second})
	e.bus.waitFor(t, from, is[events.PlayerState])
	if err := e.app.TogglePause(e.ctx); err != nil {
		t.Fatal(err)
	}
	if ops := e.playerOps(); ops[len(ops)-1] != "resume" {
		t.Fatalf("ops = %v", ops)
	}
}

func TestTrackEndedPlaysNextSkippingGone(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	c := e.file(e.master, "c.mp3", tagged)
	e.app.Enqueue(a.ID, b.ID, c.ID)
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	// b leaves the index behind the app's back (a sweep, say).
	if err := e.lib.Remove(b.ID); err != nil {
		t.Fatal(err)
	}
	from := e.bus.mark()
	e.player.Emit(events.TrackEnded{Path: a.Path})
	ev := e.bus.waitFor(t, from, is[events.TrackEnded]).(events.TrackEnded)
	if ev.TrackID != a.ID {
		t.Fatalf("forwarded TrackID = %d", ev.TrackID)
	}
	if q := e.app.Queue(); q.Pos != 2 || q.Playing != c.ID {
		t.Fatalf("queue = %+v", q)
	}

	// The end of the queue stops playback and keeps the position.
	from = e.bus.mark()
	e.player.Emit(events.TrackEnded{Path: c.Path})
	e.bus.waitFor(t, from, is[events.TrackEnded])
	if q := e.app.Queue(); q.Pos != 2 || q.Playing != 0 {
		t.Fatalf("queue = %+v", q)
	}
	if ops := e.playerOps(); !slices.Equal(ops, []string{"load", "load"}) {
		t.Fatalf("ops = %v", ops) // mpv is idle after eof; nothing more to send
	}
	// Next past the end while playing stops the player.
	if err := e.app.Previous(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	if ops := e.playerOps(); ops[len(ops)-1] != "stop" {
		t.Fatalf("ops = %v", ops)
	}
}

func TestStaleTrackEndedIgnored(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	e.app.Enqueue(a.ID, b.ID)
	if err := e.app.Play(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	n := len(e.player.Calls())
	from := e.bus.mark()
	e.player.Emit(events.TrackEnded{Path: a.Path}) // a was never loaded
	e.bus.waitFor(t, from, is[events.TrackEnded])
	if len(e.player.Calls()) != n {
		t.Fatalf("calls = %+v", e.player.Calls()[n:])
	}
}

func TestLoadFailedStopsAndKeepsPosition(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	e.app.Enqueue(a.ID, b.ID)
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	from := e.bus.mark()
	e.player.Emit(events.LoadFailed{Path: a.Path, Reason: "unrecognized file format"})
	ev := e.bus.waitFor(t, from, is[events.LoadFailed]).(events.LoadFailed)
	if ev.TrackID != a.ID {
		t.Fatalf("TrackID = %d", ev.TrackID)
	}
	e.bus.waitFor(t, from, is[events.Error])
	if q := e.app.Queue(); q.Pos != 0 || q.Playing != 0 {
		t.Fatalf("queue = %+v", q)
	}
	if ops := e.playerOps(); ops[len(ops)-1] != "stop" {
		t.Fatalf("ops = %v", ops)
	}
}

func TestDurationBackfill(t *testing.T) {
	e := newEnv(t)
	p := e.file(e.master, "a.mp3", tagged)
	p.Duration = 0
	a, err := e.lib.Upsert(p)
	if err != nil || a.Duration != 0 {
		t.Fatalf("setup: %v %+v", err, a)
	}
	if err := e.app.Play(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	from := e.bus.mark()
	e.player.Emit(events.PlayerState{Path: a.Path, Duration: 3 * time.Minute})
	ev := e.bus.waitFor(t, from, is[events.PlayerState]).(events.PlayerState)
	if ev.TrackID != a.ID {
		t.Fatalf("TrackID = %d", ev.TrackID)
	}
	if d := e.get(a.ID).Duration; d != 3*time.Minute {
		t.Fatalf("duration = %v", d)
	}
	// A known duration is never replaced.
	from = e.bus.mark()
	e.player.Emit(events.PlayerState{Path: a.Path, Duration: time.Minute})
	e.bus.waitFor(t, from, func(ev events.Event) bool {
		s, ok := ev.(events.PlayerState)
		return ok && s.Duration == time.Minute
	})
	if d := e.get(a.ID).Duration; d != 3*time.Minute {
		t.Fatalf("duration = %v", d)
	}
}

func TestPlayerDownForwarded(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	if err := e.app.Play(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	from := e.bus.mark()
	e.player.Emit(events.PlayerDown{Err: errors.New("socket closed")})
	e.bus.waitFor(t, from, is[events.PlayerDown])
	if q := e.app.Queue(); q.Playing != 0 || q.Pos != 0 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestQueueSurvivesDelete(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	c := e.file(e.master, "c.mp3", tagged)
	e.app.Enqueue(a.ID, b.ID, c.ID)
	if err := e.app.Play(e.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if sum := e.app.Delete(e.ctx, []model.TrackID{a.ID}); len(sum.Deleted) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	q := e.app.Queue()
	if !slices.Equal(q.IDs, []model.TrackID{b.ID, c.ID}) || q.Pos != 1 || q.Playing != c.ID {
		t.Fatalf("queue = %+v", q)
	}
}
