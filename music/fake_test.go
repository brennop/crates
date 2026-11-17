package music

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/brennop/crates/model"
)

func TestFakeAddIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	a, err := f.Add(ctx, "/m/a.mp3")
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.Add(ctx, "/m/a.mp3")
	if err != nil || again != a {
		t.Fatalf("second add = %q, %v", again, err)
	}
	s, err := f.Snapshot(ctx)
	if err != nil || len(s.Tracks) != 1 || s.Tracks["/m/a.mp3"] != a {
		t.Fatalf("snapshot = %+v, %v", s, err)
	}
}

func TestFakeCopySettingLatches(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	f.SetCopySetting(true)
	_, err := f.Add(ctx, "/m/a.mp3")
	var c model.ErrCopySettingOn
	if !errors.As(err, &c) || c.Location != MediaDir+"/a.mp3" {
		t.Fatalf("err = %v", err)
	}
	f.SetCopySetting(false)
	if _, err := f.Add(ctx, "/m/b.mp3"); !errors.As(err, &c) {
		t.Fatalf("add after latch: %v", err)
	}
	if st, _ := f.Status(ctx); st != model.MusicCopySettingOn {
		t.Fatalf("status = %v", st)
	}
}

func TestFakeAvailability(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	f.SetRunning(false)
	if _, err := f.Snapshot(ctx); !errors.As(err, new(model.ErrMusicUnavailable)) {
		t.Fatalf("err = %v", err)
	}
	if st, err := f.Status(ctx); err != nil || st != model.MusicUnavailable {
		t.Fatalf("status = %v, %v", st, err)
	}
	if err := f.Launch(ctx); err != nil || f.Launches() != 1 {
		t.Fatal("launch failed")
	}
	f.SetDenied(true)
	if st, err := f.Status(ctx); !errors.As(err, new(model.ErrAutomationDenied)) || st != model.MusicDenied {
		t.Fatalf("status = %v, %v", st, err)
	}
}

func TestFakePlaylists(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	a, _ := f.Add(ctx, "/m/a.mp3")
	b, _ := f.Add(ctx, "/m/b.mp3")
	f.AddPlaylist(model.Playlist{ID: "P", Editable: true}, a)
	f.AddPlaylist(model.Playlist{ID: "S"})

	got, err := f.AddToPlaylist(ctx, "P", []string{a, b})
	if err != nil || !slices.Equal(got, []string{a, b}) {
		t.Fatalf("add = %v, %v", got, err)
	}
	if _, err := f.AddToPlaylist(ctx, "S", []string{a}); !errors.As(err, new(model.ErrNotEditable)) {
		t.Fatalf("smart: %v", err)
	}
	f.TruncateNext("P")
	got, err = f.RemoveFromPlaylist(ctx, "P", []string{b})
	if !errors.As(err, new(model.ErrVerifyFailed)) || len(got) != 0 {
		t.Fatalf("truncated remove = %v, %v", got, err)
	}
	f.Fail(OpRefresh, errors.New("boom"))
	if err := f.Refresh(ctx, a); err == nil {
		t.Fatal("scripted failure not returned")
	}
	if err := f.Refresh(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteTrack(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Track(a); ok {
		t.Fatal("entry left")
	}
}

func TestSnapshotPatches(t *testing.T) {
	s := Snapshot{
		Tracks:    map[string]string{"/a": "A", "/b": "B"},
		Playlists: []model.Playlist{{ID: "P"}, {ID: "Q"}},
		Members:   map[string][]string{"P": {"A", "B"}, "Q": {"A"}},
	}
	c := s.Clone()
	c.RemoveTrack("A")
	if len(s.Members["P"]) != 2 || s.Tracks["/a"] != "A" {
		t.Fatal("Clone shares state")
	}
	if ms := c.Memberships("A"); len(ms) != 0 || len(c.Tracks) != 1 {
		t.Fatalf("after remove: %+v", c)
	}
	c.SetMembers("Q", []string{"B"})
	if ms := c.Memberships("B"); len(ms) != 2 {
		t.Fatalf("memberships = %+v", ms)
	}
	if _, ok := c.Playlist("Q"); !ok {
		t.Fatal("playlist lookup")
	}
}
