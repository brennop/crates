package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

func isLibraryLoaded(ev events.Event) bool { _, ok := ev.(events.LibraryLoaded); return ok }

func TestCacheRoundTripKeepsIDs(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		put(t, filepath.Join(e.master, name), 10)
	}
	l := e.open()
	scan(t, l, l.masterRoot())
	before := byTitle(all(l))
	e.close(l)

	l2 := reopen(t, e)
	after := byTitle(all(l2))
	if len(after) != 3 {
		t.Fatalf("restart loaded %d tracks, want 3", len(after))
	}
	for title, want := range before {
		got := after[title]
		if got.ID != want.ID || got.Path != want.Path || got.Summary != want.Summary || got.Duration != want.Duration || !got.ModTime.Equal(want.ModTime) {
			t.Errorf("%s after restart = %+v, want %+v", title, got, want)
		}
	}
	// A new file takes the next ID after the highest one loaded.
	put(t, filepath.Join(e.master, "d.mp3"), 10)
	scan(t, l2, l2.masterRoot())
	if id := byTitle(all(l2))["d.mp3"].ID; id != 4 {
		t.Errorf("new track ID = %d, want 4", id)
	}
}

func TestCacheSchemaMismatchRebuilds(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	e.close(l)

	db, err := sql.Open("sqlite", e.cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_version SET version = 99`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	l2 := reopen(t, e)
	if n := len(all(l2)); n != 0 {
		t.Fatalf("a cache of another version loaded %d tracks, want 0", n)
	}
	scan(t, l2, l2.masterRoot())
	if n := len(all(l2)); n != 1 {
		t.Errorf("rebuild indexed %d tracks, want 1", n)
	}
}

func TestUnreadableCacheRebuilds(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	if err := os.MkdirAll(filepath.Dir(e.cache), 0o755); err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, 4096)
	for i := range junk {
		junk[i] = byte(i*7 + 3)
	}
	if err := os.WriteFile(e.cache, junk, 0o644); err != nil {
		t.Fatal(err)
	}
	l := e.open()
	if n := len(all(l)); n != 0 {
		t.Fatalf("junk cache loaded %d tracks, want 0", n)
	}
	scan(t, l, l.masterRoot())
	if n := len(all(l)); n != 1 {
		t.Errorf("rebuilt cache indexed %d tracks, want 1", n)
	}
}

func TestCacheTracksOutsideRootsAreDropped(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	e.close(l)

	e.master = filepath.Join(t.TempDir(), "Elsewhere") // the config moved
	e.sink = newSink()
	l2 := reopen(t, e)
	if n := len(all(l2)); n != 0 {
		t.Errorf("tracks outside every root survived the load: %d", n)
	}
	if n := e.sink.count(func(ev events.Event) bool { c, ok := ev.(events.LibraryLoaded); return ok && c.Count == 0 }); n != 1 {
		t.Errorf("LibraryLoaded with count 0 published %d times", n)
	}
}

func TestStartShowsCacheBeforeScanFinishes(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "old.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	e.close(l)

	put(t, filepath.Join(e.master, "new.mp3"), 10)
	e.reader.gate = make(chan struct{})
	e.sink = newSink()
	l2 := reopen(t, e)
	if err := l2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The scan is blocked in the reader, so whatever is shown now came from the cache.
	e.sink.waitFor(t, isLibraryLoaded)
	shown := byTitle(all(l2))
	if _, ok := shown["old.mp3"]; !ok || len(shown) != 1 {
		t.Fatalf("while the scan runs, index holds %v, want only the cached old.mp3", shown)
	}
	close(e.reader.gate)
	e.sink.waitFor(t, isScanDone)
	if n := len(all(l2)); n != 2 {
		t.Errorf("after the scan the index holds %d tracks, want 2", n)
	}
	evs := e.sink.all()
	var loadedAt, doneAt = -1, -1
	for i, ev := range evs {
		if isLibraryLoaded(ev) && loadedAt < 0 {
			loadedAt = i
		}
		if isScanDone(ev) && doneAt < 0 {
			doneAt = i
		}
	}
	if loadedAt < 0 || doneAt < 0 || loadedAt > doneAt {
		t.Errorf("LibraryLoaded at %d, scan done at %d", loadedAt, doneAt)
	}
}

func TestSweepMasterCoalescesRequests(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		put(t, filepath.Join(e.master, name), 10)
	}
	e.reader.gate = make(chan struct{})
	e.noInbox = true // the test counts master passes only
	l := e.open()
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The startup pass is blocked in the reader. Queue ten requests behind it.
	const requests = 10
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- l.SweepMaster(context.Background())
		}()
	}
	waitQueued(t, l, requests)
	close(e.reader.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("SweepMaster: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// One startup pass and one shared sweep: two master passes in all.
	if n := e.sink.count(isScanDone); n != 2 {
		t.Errorf("master passes = %d, want 2 (startup plus one shared sweep)", n)
	}
}

func waitQueued(t *testing.T, l *Library, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		queued := len(l.waiters)
		l.mu.Unlock()
		if queued == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("requests did not queue")
}

func TestSweepMasterSeesNewFile(t *testing.T) {
	e := newEnv(t)
	l := e.open()
	if err := l.SweepMaster(context.Background()); err != ErrNotStarted {
		t.Fatalf("SweepMaster before Start = %v, want ErrNotStarted", err)
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.sink.waitFor(t, isScanDone)

	put(t, filepath.Join(e.master, "late.mp3"), 10)
	if err := l.SweepMaster(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := byTitle(all(l))
	if _, ok := got["late.mp3"]; !ok {
		t.Errorf("after SweepMaster the index holds %v, want late.mp3", got)
	}
}

func TestMoveKeepsIDAndChangesRole(t *testing.T) {
	e := newEnv(t)
	src := filepath.Join(e.inbox, "song.mp3")
	put(t, src, 10)
	l := e.open()
	scan(t, l, l.inboxRoot())
	scan(t, l, l.inboxRoot())
	id := byTitle(all(l))["song.mp3"].ID

	dst := filepath.Join(e.master, "song.mp3")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := l.Move(id, dst); err != nil {
		t.Fatal(err)
	}

	got := l.Get(id)
	if len(got) != 1 || got[0].Path != dst || got[0].Role != model.Master {
		t.Fatalf("after Move: %+v", got)
	}
	// A sweep of the new location finds the track unchanged: no re-read.
	reads := e.reader.count()
	scan(t, l, l.masterRoot())
	if e.reader.count() != reads {
		t.Error("sweep after Move re-read the file")
	}
	e.close(l)
	if got := reopen(t, e).Get(id); len(got) != 1 || got[0].Path != dst {
		t.Errorf("cache after Move: %+v", got)
	}
}

func TestMoveDisplacesTrackAtTarget(t *testing.T) {
	e := newEnv(t)
	masterFile := filepath.Join(e.master, "dup.mp3")
	put(t, masterFile, 10)
	put(t, filepath.Join(e.inbox, "dup.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	scan(t, l, l.inboxRoot())
	scan(t, l, l.inboxRoot())
	masterID, inboxID := roleID(t, l, model.Master), roleID(t, l, model.Inbox)

	// The inbox track takes the master file's path; the master track is displaced.
	if err := l.Move(inboxID, masterFile); err != nil {
		t.Fatal(err)
	}

	got := all(l)
	if len(got) != 1 || got[0].ID != inboxID || got[0].Role != model.Master || got[0].Path != masterFile {
		t.Fatalf("after displacing Move: %+v", got)
	}
	removed := e.sink.count(func(ev events.Event) bool {
		r, ok := ev.(events.TrackRemoved)
		return ok && len(r.IDs) == 1 && r.IDs[0] == masterID
	})
	if removed != 1 {
		t.Errorf("TrackRemoved for the displaced track: %d events, want 1", removed)
	}
}

// roleID returns the ID of the one indexed track with role r.
func roleID(t *testing.T, l *Library, r model.Role) model.TrackID {
	t.Helper()
	for _, tr := range all(l) {
		if tr.Role == r {
			return tr.ID
		}
	}
	t.Fatalf("no %v track", r)
	return 0
}

func TestMoveOutsideRootsAndUnknownTrack(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	id := byTitle(all(l))["a.mp3"].ID

	if err := l.Move(id, filepath.Join(t.TempDir(), "x.mp3")); err == nil {
		t.Error("Move outside every root: want an error")
	}
	if err := l.Move(999, filepath.Join(e.master, "y.mp3")); err != ErrUnknownTrack {
		t.Errorf("Move of an unknown track = %v, want ErrUnknownTrack", err)
	}
}

func TestSetDurationOnlyWhenUnset(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	blank := filepath.Join(e.master, "b.mp3")
	put(t, blank, 10)
	e.reader.durations = map[string]time.Duration{blank: 0} // no Xing header: no duration
	l := e.open()
	scan(t, l, l.masterRoot())
	byName := byTitle(all(l))
	idA, idB := byName["a.mp3"].ID, byName["b.mp3"].ID

	ok, err := l.SetDuration(idA, 5*time.Second)
	if err != nil || ok {
		t.Fatalf("SetDuration over a stored duration = %v, %v; want false, nil", ok, err)
	}
	if got := l.Get(idA)[0].Duration; got != time.Second {
		t.Errorf("duration = %v, want the stored 1s", got)
	}

	ok, err = l.SetDuration(idB, 7*time.Second)
	if err != nil || !ok {
		t.Fatalf("SetDuration on an unset duration = %v, %v; want true, nil", ok, err)
	}
	if got := l.Get(idB)[0].Duration; got != 7*time.Second {
		t.Errorf("duration = %v, want 7s", got)
	}
	e.close(l)
	if got := reopen(t, e).Get(idB); got[0].Duration != 7*time.Second {
		t.Errorf("cache duration after restart = %v, want 7s", got[0].Duration)
	}
}

func TestRemoveDropsFromIndexAndCache(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	put(t, filepath.Join(e.master, "b.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	byName := byTitle(all(l))

	if err := l.Remove(byName["a.mp3"].ID, 999); err != nil {
		t.Fatal(err)
	}
	if got := all(l); len(got) != 1 || got[0].ID != byName["b.mp3"].ID {
		t.Fatalf("after Remove: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(e.master, "a.mp3")); err != nil {
		t.Error("Remove touched the file on disk")
	}
	e.close(l)
	if got := all(reopen(t, e)); len(got) != 1 {
		t.Errorf("cache holds %d tracks after Remove and restart, want 1", len(got))
	}
}

func TestDeletedCacheFileTriggersFullScan(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	l := e.open()
	scan(t, l, l.masterRoot())
	e.close(l)

	if err := os.Remove(e.cache); err != nil {
		t.Fatal(err)
	}
	l2 := reopen(t, e)
	if n := len(all(l2)); n != 0 {
		t.Fatalf("deleted cache loaded %d tracks, want 0", n)
	}
	scan(t, l2, l2.masterRoot())
	if n := len(all(l2)); n != 1 {
		t.Errorf("full scan indexed %d tracks, want 1", n)
	}
}

func TestMasterSweptAndTracks(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.master, "a.mp3"), 10)
	put(t, filepath.Join(e.inbox, "b.mp3"), 10)
	l := e.open()
	defer e.close(l)

	at, ch := l.MasterSwept()
	if !at.IsZero() {
		t.Fatalf("swept at %v before any sweep", at)
	}
	before := time.Now()
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("startup pass did not mark the master swept")
	}
	first, ch := l.MasterSwept()
	if first.Before(before) {
		t.Errorf("swept at %v, before Start at %v", first, before)
	}
	ms := l.MasterTracks()
	if len(ms) != 1 || filepath.Base(ms[0].Path) != "a.mp3" || ms[0].Role != model.Master {
		t.Errorf("master tracks = %+v", ms)
	}

	if err := l.SweepMaster(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("SweepMaster returned before marking the sweep")
	}
	if second, _ := l.MasterSwept(); !second.After(first) {
		t.Errorf("second sweep at %v, not after %v", second, first)
	}
}
