package library

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/model"
)

// testIndex builds an index over a master and an inbox at fixed paths. The
// paths need not exist: the index never touches disk.
func testIndex(caseSensitive bool) *index {
	master := &root{dir: "/lib/master", role: model.Master, caseSensitive: caseSensitive}
	inbox := &root{dir: "/lib/inbox", role: model.Inbox, recursive: true, caseSensitive: caseSensitive}
	return newIndex([]*root{master, inbox})
}

func track(path string, title string) model.Track {
	return model.Track{Path: path, Size: 10, ModTime: time.Unix(100, 0), Summary: model.TagSummary{Title: title}}
}

func TestUpsertIsIdempotent(t *testing.T) {
	x := testIndex(true)
	first, changed, err := x.upsert(track("/lib/master/a.mp3", "A"))
	if err != nil || !changed || first.ID == 0 {
		t.Fatalf("first upsert = %+v, %v, %v", first, changed, err)
	}
	second, changed, err := x.upsert(track("/lib/master/a.mp3", "A"))
	if err != nil || changed || second.ID != first.ID {
		t.Errorf("second upsert = %+v, changed %v, err %v; want same ID, no change", second, changed, err)
	}
	third, changed, _ := x.upsert(track("/lib/master/a.mp3", "Renamed"))
	if !changed || third.ID != first.ID {
		t.Errorf("tag change: changed %v, ID %d; want changed, ID %d", changed, third.ID, first.ID)
	}
}

func TestRoleComesFromRoot(t *testing.T) {
	x := testIndex(true)
	m, _, _ := x.upsert(track("/lib/master/a.mp3", "A"))
	i, _, _ := x.upsert(track("/lib/inbox/deep/b.mp3", "B"))
	if m.Role != model.Master || i.Role != model.Inbox {
		t.Errorf("roles = %v, %v; want master, inbox", m.Role, i.Role)
	}
	if _, _, err := x.upsert(track("/elsewhere/c.mp3", "C")); err == nil {
		t.Error("a path outside every root was accepted")
	}
	if _, _, err := x.upsert(track("/lib/masterx/d.mp3", "D")); err == nil {
		t.Error("a sibling that shares a name prefix was accepted")
	}
}

func TestUpsertKeepsMusicID(t *testing.T) {
	x := testIndex(true)
	a, _, _ := x.upsert(track("/lib/master/a.mp3", "A"))
	x.mu.Lock()
	tr := x.tracks[a.ID]
	tr.MusicID = "ABC"
	x.tracks[a.ID] = tr
	x.mu.Unlock()

	updated := track("/lib/master/a.mp3", "A2") // the scan never knows the Music ID
	got, changed, _ := x.upsert(updated)
	if !changed || got.MusicID != "ABC" {
		t.Errorf("after upsert: changed %v, MusicID %q; want changed and kept", changed, got.MusicID)
	}
}

func TestCaseVariantsShareAKeyOnlyOnInsensitiveVolumes(t *testing.T) {
	insensitive := testIndex(false)
	a, _, _ := insensitive.upsert(track("/lib/master/Song.mp3", "A"))
	b, changed, _ := insensitive.upsert(track("/lib/master/song.mp3", "A"))
	if b.ID != a.ID || !changed {
		t.Errorf("insensitive: ID %d vs %d, changed %v; want the same track with its path updated", b.ID, a.ID, changed)
	}
	if b.Path != "/lib/master/song.mp3" {
		t.Errorf("path = %q, want the new case", b.Path)
	}

	sensitive := testIndex(true)
	c, _, _ := sensitive.upsert(track("/lib/master/Song.mp3", "A"))
	d, _, _ := sensitive.upsert(track("/lib/master/song.mp3", "A"))
	if c.ID == d.ID {
		t.Error("sensitive: two names differing in case shared one track")
	}
}

func TestMoveKeepsIDAndDisplaces(t *testing.T) {
	x := testIndex(true)
	a, _, _ := x.upsert(track("/lib/inbox/a.mp3", "A"))
	b, _, _ := x.upsert(track("/lib/master/b.mp3", "B"))

	moved, displaced, err := x.move(a.ID, "/lib/master/b.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != a.ID || moved.Role != model.Master {
		t.Errorf("moved = %+v; want ID %d in master", moved, a.ID)
	}
	if displaced != b.ID {
		t.Errorf("displaced = %d, want %d", displaced, b.ID)
	}
	if _, ok := x.byKey("/lib/master/b.mp3"); !ok {
		t.Error("the moved track is not found under its new key")
	}
	if n := x.count(); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

func TestGetReturnsCopies(t *testing.T) {
	x := testIndex(true)
	a, _, _ := x.upsert(track("/lib/master/a.mp3", "A"))
	got := x.get([]model.TrackID{a.ID, 999})
	if len(got) != 1 {
		t.Fatalf("get returned %d tracks, want 1 (unknown IDs skipped)", len(got))
	}
	got[0].Summary.Title = "changed by caller"
	if again := x.get([]model.TrackID{a.ID}); again[0].Summary.Title != "A" {
		t.Error("a caller's copy changed the index")
	}
}

func TestIndexConcurrentReadersAndWriter(t *testing.T) {
	x := testIndex(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			x.upsert(track(filepath.Join("/lib/master", itoaName(i)), "T"))
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				x.count()
				x.get([]model.TrackID{1, 2, 3})
				x.byKey("/lib/master/x.mp3")
			}
		}()
	}
	wg.Wait()
	if n := x.count(); n != 500 {
		t.Errorf("count = %d, want 500", n)
	}
}

func itoaName(i int) string {
	return "t" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + ".mp3"
}
