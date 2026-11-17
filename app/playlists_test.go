package app

import (
	"errors"
	"slices"
	"testing"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
)

func playlistSetup(t *testing.T) (*env, model.Track, model.Track) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3", tagged))
	b := e.inMusic(e.file(e.master, "b.mp3", tagged))
	e.music.AddPlaylist(model.Playlist{ID: "P1", Name: "Set", Editable: true}, a.MusicID)
	e.music.AddPlaylist(model.Playlist{ID: "S1", Name: "Smart", Editable: false})
	e.snapshot()
	return e, a, b
}

func TestPlaylistsAndMemberships(t *testing.T) {
	e, a, b := playlistSetup(t)
	if ps := e.app.Playlists(); len(ps) != 2 || ps[0].ID != "P1" {
		t.Fatalf("playlists = %+v", ps)
	}
	if ms, err := e.app.Memberships(a.ID); err != nil || len(ms) != 1 || ms[0].ID != "P1" {
		t.Fatalf("memberships = %+v, %v", ms, err)
	}
	if ms, err := e.app.Memberships(b.ID); err != nil || len(ms) != 0 {
		t.Fatalf("memberships = %+v, %v", ms, err)
	}
}

func TestAddToPlaylist(t *testing.T) {
	e, a, b := playlistSetup(t)
	from := e.bus.mark()
	if err := e.app.AddToPlaylist(e.ctx, []model.TrackID{a.ID, b.ID, b.ID}, "P1"); err != nil {
		t.Fatal(err)
	}
	// a is already a member, and b is sent once.
	calls := e.music.CallsOf(music.OpPlAdd)
	if len(calls) != 1 || !slices.Equal(calls[0].MusicIDs, []string{b.MusicID}) {
		t.Fatalf("calls = %+v", calls)
	}
	if got := e.music.Members("P1"); !slices.Equal(got, []string{a.MusicID, b.MusicID}) {
		t.Fatalf("members = %v", got)
	}
	e.bus.waitFor(t, from, is[events.PlaylistsChanged])
	// The snapshot is patched, not re-read.
	if ms, _ := e.app.Memberships(b.ID); len(ms) != 1 {
		t.Fatalf("memberships = %+v", ms)
	}
	if n := len(e.music.CallsOf(music.OpSnapshot)); n != 1 {
		t.Fatalf("%d snapshots", n)
	}
	// Adding members only is a no-op.
	if err := e.app.AddToPlaylist(e.ctx, []model.TrackID{a.ID}, "P1"); err != nil {
		t.Fatal(err)
	}
	if n := len(e.music.CallsOf(music.OpPlAdd)); n != 1 {
		t.Fatalf("%d add calls", n)
	}
}

func TestRemoveFromPlaylist(t *testing.T) {
	e, a, _ := playlistSetup(t)
	if err := e.app.RemoveFromPlaylist(e.ctx, []model.TrackID{a.ID}, "P1"); err != nil {
		t.Fatal(err)
	}
	if got := e.music.Members("P1"); len(got) != 0 {
		t.Fatalf("members = %v", got)
	}
	if ms, _ := e.app.Memberships(a.ID); len(ms) != 0 {
		t.Fatalf("memberships = %+v", ms)
	}
}

func TestPlaylistChecks(t *testing.T) {
	e, a, _ := playlistSetup(t)
	in := e.file(e.inbox, "i.mp3", tagged)
	var nim model.ErrNotInMusic
	if err := e.app.AddToPlaylist(e.ctx, []model.TrackID{a.ID, in.ID}, "P1"); !errors.As(err, &nim) || nim.ID != in.ID {
		t.Fatalf("err = %v", err)
	}
	var ne model.ErrNotEditable
	if err := e.app.AddToPlaylist(e.ctx, []model.TrackID{a.ID}, "S1"); !errors.As(err, &ne) {
		t.Fatalf("err = %v", err)
	}
	if err := e.app.RemoveFromPlaylist(e.ctx, []model.TrackID{a.ID}, "nope"); !errors.Is(err, ErrUnknownPlaylist) {
		t.Fatalf("err = %v", err)
	}
	if n := len(e.music.CallsOf(music.OpPlAdd)) + len(e.music.CallsOf(music.OpPlRemove)); n != 0 {
		t.Fatalf("%d writes reached Music", n)
	}
}

func TestPlaylistVerifyFailureRefreshesSnapshot(t *testing.T) {
	e, a, b := playlistSetup(t)
	e.music.TruncateNext("P1")
	err := e.app.AddToPlaylist(e.ctx, []model.TrackID{b.ID}, "P1")
	var vf model.ErrVerifyFailed
	if !errors.As(err, &vf) {
		t.Fatalf("err = %v", err)
	}
	if n := len(e.music.CallsOf(music.OpSnapshot)); n != 2 {
		t.Fatalf("%d snapshots, want a refresh", n)
	}
	// The snapshot shows what Music really holds: a was cut.
	if ms, _ := e.app.Memberships(a.ID); len(ms) != 0 {
		t.Fatalf("memberships of a = %+v", ms)
	}
	if ms, _ := e.app.Memberships(b.ID); len(ms) != 1 {
		t.Fatalf("memberships of b = %+v", ms)
	}
}

func TestPlaylistMusicUnavailable(t *testing.T) {
	e, _, b := playlistSetup(t)
	e.music.SetRunning(false)
	var u model.ErrMusicUnavailable
	if err := e.app.AddToPlaylist(e.ctx, []model.TrackID{b.ID}, "P1"); !errors.As(err, &u) {
		t.Fatalf("err = %v", err)
	}
	// The old snapshot is kept when the refresh cannot run.
	if ps := e.app.Playlists(); len(ps) != 2 {
		t.Fatalf("playlists = %+v", ps)
	}
}

func TestFocusGained(t *testing.T) {
	e := newEnv(t)
	e.music.AddPlaylist(model.Playlist{ID: "P1", Name: "Set", Editable: true})
	if err := e.app.FocusGained(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.app.Playlists()) != 1 {
		t.Fatal("snapshot not refreshed")
	}
	e.music.SetDenied(true)
	if err := e.app.Refresh(e.ctx); !errors.As(err, new(model.ErrAutomationDenied)) {
		t.Fatalf("err = %v", err)
	}
}
