package library

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/model"
)

// tableReader answers from the summaries the fixture generator wrote, so the
// scan measures the library and not tag parsing.
type tableReader struct {
	summaries map[string]fixture.File
	calls     atomic.Int64 // read by many workers at once
}

func (r *tableReader) Summary(path string) (model.TagSummary, time.Duration, error) {
	r.calls.Add(1)
	f := r.summaries[path]
	return f.Summary, f.Duration(), nil
}

// TestScale measures a cold scan, a warm start from the cache, a no-change
// sweep and the index's memory, on 5000 generated files. It is skipped unless
// CRATES_SCALE is set, since generating and scanning take a while.
func TestScale(t *testing.T) {
	if testing.Short() || !envSet("CRATES_SCALE") {
		t.Skip("set CRATES_SCALE=1 to run the scale test")
	}
	const n = 5000
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(dir, "Master")
	files, err := fixture.GenerateLibrary(master, n, 1)
	if err != nil {
		t.Fatal(err)
	}
	reader := &tableReader{summaries: make(map[string]fixture.File, n)}
	for _, f := range files {
		reader.summaries[f.Path] = f
	}
	cfg := func(sink *recordSink) Config {
		return Config{Master: master, CachePath: filepath.Join(dir, "state", "library.db"), Reader: reader, Sink: sink}
	}

	// Cold: no cache, so every file is read.
	cold := newSink()
	start := time.Now()
	l, err := Open(cfg(cold))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.waitFor(t, isScanDone)
	coldScan := time.Since(start)
	if got := len(all(l)); got != n {
		t.Fatalf("cold scan indexed %d tracks, want %d", got, n)
	}
	coldReads := int(reader.calls.Load())
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Warm: the cache loads first, then a scan finds nothing changed.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	warm := newSink()
	start = time.Now()
	l, err = Open(cfg(warm))
	if err != nil {
		t.Fatal(err)
	}
	warm.waitFor(t, isLibraryLoaded)
	loaded := time.Since(start)
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	warm.waitFor(t, isScanDone)
	warmScan := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Cleanup(func() { l.Close() })
	if got := len(all(l)); got != n {
		t.Fatalf("warm start holds %d tracks, want %d", got, n)
	}
	warmReads := int(reader.calls.Load()) - coldReads

	// No-change sweep: the master is listed and stat'ed, nothing is read.
	readsBefore := int(reader.calls.Load())
	start = time.Now()
	if err := l.SweepMaster(context.Background()); err != nil {
		t.Fatal(err)
	}
	sweep := time.Since(start)

	t.Logf("files: %d", n)
	t.Logf("cold scan (no cache, %d summary reads): %v", coldReads, coldScan.Round(time.Millisecond))
	t.Logf("warm start: LibraryLoaded after %v; scan finished after %v with %d summary reads", loaded.Round(time.Millisecond), warmScan.Round(time.Millisecond), warmReads)
	t.Logf("no-change master sweep: %v with %d summary reads", sweep.Round(time.Millisecond), int(reader.calls.Load())-readsBefore)
	t.Logf("index memory with %d tracks: about %.1f MiB (heap after GC)", n, float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
	if int(reader.calls.Load())-readsBefore != 0 {
		t.Errorf("no-change sweep read %d summaries, want 0", int(reader.calls.Load())-readsBefore)
	}
}

func envSet(name string) bool { return os.Getenv(name) != "" }
