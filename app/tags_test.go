package app

import (
	"errors"
	"testing"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/tags"
)

func TestFullTags(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	frames, err := e.app.FullTags(e.ctx, a.ID)
	if err != nil || len(frames) != 3 {
		t.Fatalf("frames = %+v, err = %v", frames, err)
	}
	e.tags.SetMissing(true)
	if _, err := e.app.FullTags(e.ctx, a.ID); !errors.As(err, new(model.ErrKid3Missing)) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.app.FullTags(e.ctx, 999); !errors.Is(err, ErrUnknownTrack) {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteTags(t *testing.T) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3", tagged))
	b := e.file(e.inbox, "b.mp3", tagged)
	from := e.bus.mark()

	err := e.app.WriteTags(e.ctx, []model.TrackID{a.ID, b.ID}, []model.FrameChange{{Name: "Genre", Value: "Dub"}})
	if err != nil {
		t.Fatal(err)
	}
	w := e.tags.Writes()
	if len(w) != 1 || len(w[0].Paths) != 2 {
		t.Fatalf("writes = %+v", w)
	}
	for _, id := range []model.TrackID{a.ID, b.ID} {
		if g := e.get(id).Summary.Genre; g != "Dub" {
			t.Fatalf("track %d genre = %q", id, g)
		}
	}
	e.bus.waitFor(t, from, is[events.TracksChanged])
	r := e.music.CallsOf(music.OpRefresh)
	if len(r) != 1 || r[0].MusicIDs[0] != a.MusicID {
		t.Fatalf("refresh calls = %+v", r)
	}
}

func TestWriteTagsFailure(t *testing.T) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3", tagged))
	e.tags.Fail(tags.OpWrite, "", errors.New("kid3 crashed"))
	if err := e.app.WriteTags(e.ctx, []model.TrackID{a.ID}, []model.FrameChange{{Name: "Genre", Value: "x"}}); err == nil {
		t.Fatal("no error")
	}
	if len(e.music.CallsOf(music.OpRefresh)) != 0 {
		t.Fatal("refreshed after a failed write")
	}
}

func TestWriteTagsMusicRefreshFailureReported(t *testing.T) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3", tagged))
	e.music.Fail(music.OpRefresh, model.ErrMusicUnavailable{})
	from := e.bus.mark()
	if err := e.app.WriteTags(e.ctx, []model.TrackID{a.ID}, []model.FrameChange{{Name: "Genre", Value: "x"}}); err != nil {
		t.Fatal(err)
	}
	e.bus.waitFor(t, from, is[events.Error])
}

func TestWriteTagsOnPlayingTrackReloadsOnce(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	e.app.Enqueue(a.ID, b.ID)
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	from := e.bus.mark()
	e.player.Emit(events.PlayerState{Path: a.Path, Position: 42 * time.Second})
	e.bus.waitFor(t, from, is[events.PlayerState])

	if err := e.app.WriteTags(e.ctx, []model.TrackID{a.ID}, []model.FrameChange{{Name: "Title", Value: "T2"}}); err != nil {
		t.Fatal(err)
	}
	// First failure: reload at the saved position, no Error, no stop.
	from = e.bus.mark()
	e.player.Emit(events.LoadFailed{Path: a.Path, Reason: "read error"})
	e.bus.waitFor(t, from, is[events.LoadFailed])
	if l := e.lastLoad(); l.Path != a.Path || l.Pos != 42*time.Second {
		t.Fatalf("load = %+v", l)
	}
	if q := e.app.Queue(); q.Playing != a.ID || q.Pos != 0 {
		t.Fatalf("queue = %+v", q)
	}
	for _, ev := range e.bus.all()[from:] {
		if is[events.Error](ev) {
			t.Fatalf("error published: %+v", ev)
		}
	}
	// Second failure: report and stop.
	from = e.bus.mark()
	e.player.Emit(events.LoadFailed{Path: a.Path, Reason: "read error"})
	e.bus.waitFor(t, from, is[events.LoadFailed])
	e.bus.waitFor(t, from, is[events.Error])
	if q := e.app.Queue(); q.Playing != 0 || q.Pos != 0 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestReloadArmExpires(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	if err := e.app.Play(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.app.WriteTags(e.ctx, []model.TrackID{a.ID}, []model.FrameChange{{Name: "Title", Value: "T2"}}); err != nil {
		t.Fatal(err)
	}
	e.app.smu.Lock()
	e.app.reload.until = time.Now().Add(-time.Second)
	e.app.smu.Unlock()
	from := e.bus.mark()
	e.player.Emit(events.LoadFailed{Path: a.Path, Reason: "x"})
	e.bus.waitFor(t, from, is[events.LoadFailed])
	e.bus.waitFor(t, from, is[events.Error])
	if q := e.app.Queue(); q.Playing != 0 {
		t.Fatalf("queue = %+v", q)
	}
}
