//go:build darwin && musicint

// Integration tests against the real Music app. Run by hand:
//
//	go test -tags musicint -run Integration -v ./music/
//
// TestIntegrationReadOnly reads the status and a snapshot and changes
// nothing. TestIntegrationWrites also needs:
//
//	CRATES_MUSIC_MASTER    a scratch dir outside Music's Media folder
//	CRATES_MUSIC_PLAYLIST  the persistent ID of a plain test playlist
//
// It writes a generated MP3 into the scratch dir, adds it to the library and
// the playlist, removes it from both, and leaves the file on disk. Music
// must be running, with the copy setting off.
package music

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

func TestIntegrationReadOnly(t *testing.T) {
	c := New(Options{})
	defer c.Close()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != model.MusicAvailable {
		t.Skipf("Music is %v", st)
	}
	start := time.Now()
	s, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot: %d tracks, %d playlists in %v", len(s.Tracks), len(s.Playlists), time.Since(start))
	for p := range s.Tracks {
		if n, err := pathnorm.Normalize(p); err != nil || n != p {
			t.Errorf("location %q is not normalized", p)
		}
	}
	for _, p := range s.Playlists {
		t.Logf("playlist %s %q editable=%v members=%d", p.ID, p.Name, p.Editable, len(s.Members[p.ID]))
	}
}

func TestIntegrationWrites(t *testing.T) {
	dir, pid := os.Getenv("CRATES_MUSIC_MASTER"), os.Getenv("CRATES_MUSIC_PLAYLIST")
	if dir == "" || pid == "" {
		t.Skip("set CRATES_MUSIC_MASTER and CRATES_MUSIC_PLAYLIST")
	}
	c := New(Options{})
	defer c.Close()

	name := fmt.Sprintf(`crates it's "%d" ção.mp3`, time.Now().UnixNano())
	raw := filepath.Join(dir, name)
	if err := os.WriteFile(raw, fixture.MP3(fixture.Options{Tag: fixture.ID3v24,
		Summary: model.TagSummary{Title: "crates integration", Artist: "crates", Album: "test"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := pathnorm.Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}

	id, err := c.Add(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Add(ctx, path)
	if err != nil || again != id {
		t.Errorf("second add: %q, %v; want %q", again, err, id)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Tracks[path] != id {
		t.Errorf("snapshot maps %q to %q, want %q", path, snap.Tracks[path], id)
	}
	if p, ok := snap.Playlist(pid); !ok || !p.Editable {
		t.Fatalf("test playlist %s: %+v, found %v", pid, p, ok)
	}
	before := snap.Members[pid]

	got, err := c.AddToPlaylist(ctx, pid, []string{id})
	if err != nil || !slices.Contains(got, id) {
		t.Fatalf("playlist add: %q, %v", got, err)
	}
	got, err = c.AddToPlaylist(ctx, pid, []string{id})
	if err != nil || len(got) != len(before)+1 {
		t.Errorf("repeat add: %d members, %v; want %d (no second copy)", len(got), err, len(before)+1)
	}
	if err := c.Refresh(ctx, id); err != nil {
		t.Error(err)
	}
	got, err = c.RemoveFromPlaylist(ctx, pid, []string{id})
	if err != nil || slices.Contains(got, id) {
		t.Errorf("playlist remove: %q, %v", got, err)
	}
	if err := c.DeleteTrack(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(ctx, id); err == nil {
		t.Error("refresh of a deleted entry succeeded")
	}
	if _, err := os.Stat(raw); err != nil {
		t.Errorf("delete removed the file: %v", err)
	}
	t.Logf("left %s on disk", raw)
}
