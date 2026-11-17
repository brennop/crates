package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

func isTracksChanged(ev events.Event) bool { _, ok := ev.(events.TracksChanged); return ok }
func isError(ev events.Event) bool         { _, ok := ev.(events.Error); return ok }

func TestScanMasterFiltersNames(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	put(t, filepath.Join(e.master, "B.MP3"), 10)
	put(t, filepath.Join(e.master, ".hidden.mp3"), 10)
	put(t, filepath.Join(e.master, "c.txt"), 10)
	put(t, filepath.Join(e.master, "d.mp3.part"), 10)
	put(t, filepath.Join(e.master, "sub", "e.mp3"), 10) // master is flat
	l := e.open()

	scan(t, l, l.masterRoot())

	got := byTitle(all(l))
	if len(got) != 2 || got["a.mp3"].ID == 0 || got["B.MP3"].ID == 0 {
		t.Fatalf("indexed %v, want a.mp3 and B.MP3 only", got)
	}
	if got["a.mp3"].Role != model.Master {
		t.Errorf("role = %v, want master", got["a.mp3"].Role)
	}
	if n := e.sink.count(isTracksChanged); n != 1 {
		t.Errorf("TracksChanged events = %d, want 1", n)
	}
}

func TestInboxFilesSettleAcrossTwoSweeps(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(e.inbox, "deep", "song.mp3")
	put(t, path, 100)
	put(t, filepath.Join(e.inbox, "song.mp3.crdownload"), 100)
	put(t, filepath.Join(e.inbox, ".tmp", "skip.mp3"), 100)
	l := e.open()

	scan(t, l, l.inboxRoot())
	if n := len(all(l)); n != 0 {
		t.Fatalf("after first sighting %d tracks indexed, want 0", n)
	}
	scan(t, l, l.inboxRoot())
	got := all(l)
	if len(got) != 1 || got[0].Path != path || got[0].Role != model.Inbox {
		t.Fatalf("after second sighting got %+v", got)
	}

	// A file still growing is not re-indexed until its size holds.
	put(t, path, 200)
	scan(t, l, l.inboxRoot())
	if got := all(l); got[0].Size != 100 {
		t.Errorf("growing file re-indexed at size %d, want the settled 100", got[0].Size)
	}
	scan(t, l, l.inboxRoot())
	if got := all(l); got[0].Size != 200 {
		t.Errorf("after the size settled, size = %d, want 200", got[0].Size)
	}
}

func TestUnchangedFilesAreNotReread(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	reads, changed := e.reader.count(), e.sink.count(isTracksChanged)

	scan(t, l, l.masterRoot())

	if e.reader.count() != reads {
		t.Errorf("reads grew from %d to %d on an unchanged sweep", reads, e.reader.count())
	}
	if e.sink.count(isTracksChanged) != changed {
		t.Error("an unchanged sweep published TracksChanged")
	}
}

func TestChangedAndRemovedFiles(t *testing.T) {
	e := newEnv(t)
	a := filepath.Join(e.master, "a.mp3")
	b := filepath.Join(e.master, "b.mp3")
	put(t, a, 10)
	put(t, b, 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	idA := byTitle(all(l))["a.mp3"].ID

	put(t, a, 99) // size changed: re-read, same ID
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	scan(t, l, l.masterRoot())

	got := byTitle(all(l))
	if len(got) != 1 || got["a.mp3"].ID != idA || got["a.mp3"].Size != 99 {
		t.Fatalf("after change and removal: %+v", got)
	}
	removed := e.sink.count(func(ev events.Event) bool {
		r, ok := ev.(events.TrackRemoved)
		return ok && len(r.IDs) == 1
	})
	if removed != 1 {
		t.Errorf("TrackRemoved events = %d, want 1", removed)
	}
	// The removal must have reached the cache.
	e.close(l)
	if n := len(all(reopen(t, e))); n != 1 {
		t.Errorf("cache holds %d tracks after restart, want 1", n)
	}
}

func TestMissingMasterKeepsIndex(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())

	if err := os.RemoveAll(e.master); err != nil {
		t.Fatal(err)
	}
	err := l.scanRoot(context.Background(), l.masterRoot())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("scan of a missing master: err = %v, want not-exist", err)
	}
	if n := len(all(l)); n != 1 {
		t.Errorf("index lost tracks (%d left) when the master disappeared", n)
	}
}

func TestUnreadableSubdirKeepsItsTracks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	e := newEnv(t)
	locked := filepath.Join(e.inbox, "locked")
	put(t, filepath.Join(locked, "x.mp3"), 10)
	l := e.open()
	scan(t, l, l.inboxRoot())
	scan(t, l, l.inboxRoot())
	if n := len(all(l)); n != 1 {
		t.Fatalf("setup: %d tracks, want 1", n)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	scan(t, l, l.inboxRoot())

	if n := len(all(l)); n != 1 {
		t.Errorf("an incomplete walk removed tracks: %d left, want 1", n)
	}
}

func TestReadErrorIndexesWithEmptySummary(t *testing.T) {
	e := newEnv(t)
	bad := filepath.Join(e.master, "bad.mp3")
	put(t, bad, 10)
	e.reader.fail = map[string]error{bad: errors.New("corrupt")}
	l := e.open()

	scan(t, l, l.masterRoot())

	got := all(l)
	if len(got) != 1 || got[0].Summary.Title != "" {
		t.Fatalf("got %+v, want one track with an empty summary", got)
	}
	if n := e.sink.count(isError); n != 1 {
		t.Errorf("Error events = %d, want 1", n)
	}
}

func TestScanFlushesInBatches(t *testing.T) {
	e := newEnv(t)
	const n = 1200
	for i := 0; i < n; i++ {
		put(t, filepath.Join(e.master, fmt.Sprintf("track-%04d.mp3", i)), 1)
	}
	l := e.open()

	scan(t, l, l.masterRoot())

	var batches, total int
	for _, ev := range e.sink.all() {
		if c, ok := ev.(events.TracksChanged); ok {
			batches++
			total += len(c.Tracks)
			if len(c.Tracks) > batchMax {
				t.Errorf("batch of %d exceeds %d", len(c.Tracks), batchMax)
			}
		}
	}
	if total != n || batches < 3 {
		t.Errorf("published %d tracks in %d batches, want %d in at least 3", total, batches, n)
	}
}

// The watch loop's inbox tick must settle a new inbox file on its own, with
// no test driving scanRoot.
func TestInboxTickIndexesSettledFile(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 3 s inbox tick")
	}
	e := newEnv(t)
	put(t, filepath.Join(e.inbox, "tick.mp3"), 10)
	l := e.open()
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.sink.waitFor(t, func(ev events.Event) bool {
		c, ok := ev.(events.TracksChanged)
		if !ok {
			return false
		}
		for _, tr := range c.Tracks {
			if tr.Role == model.Inbox {
				return true
			}
		}
		return false
	})
}
