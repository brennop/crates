package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/analysis"
)

// countRuns swaps the app's job runner for one that counts calls per job
// and fails while fail is set.
func countRuns(e *env) (runs func(analysis.Job) int, setFail func(bool)) {
	var mu sync.Mutex
	n := map[analysis.Job]int{}
	fail := false
	e.app.runJob = func(_ context.Context, job analysis.Job, t analysis.Track) analysis.Result {
		mu.Lock()
		defer mu.Unlock()
		n[job]++
		if fail {
			return analysis.Result{Job: job, Err: errors.New("boom")}
		}
		return analysis.Result{Job: job, BPM: 120, Match: t.Title}
	}
	return func(j analysis.Job) int { mu.Lock(); defer mu.Unlock(); return n[j] },
		func(f bool) { mu.Lock(); fail = f; mu.Unlock() }
}

func TestAnalyzeCachesForSession(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	b := e.file(e.master, "b.mp3", tagged)
	runs, setFail := countRuns(e)

	for range 2 {
		if r := e.app.Analyze(e.ctx, a.ID, analysis.BPM); r.Err != nil || r.BPM != 120 {
			t.Fatalf("result = %+v", r)
		}
	}
	if runs(analysis.BPM) != 1 {
		t.Fatalf("BPM ran %d times, want 1", runs(analysis.BPM))
	}

	// Another job and another track are their own entries.
	e.app.Analyze(e.ctx, a.ID, analysis.SKey)
	e.app.Analyze(e.ctx, b.ID, analysis.BPM)
	if runs(analysis.SKey) != 1 || runs(analysis.BPM) != 2 {
		t.Fatalf("SKey ran %d, BPM %d", runs(analysis.SKey), runs(analysis.BPM))
	}

	// Failures are not kept.
	setFail(true)
	for range 2 {
		if r := e.app.Analyze(e.ctx, a.ID, analysis.Beatport); r.Err == nil {
			t.Fatal("want the failure")
		}
	}
	if runs(analysis.Beatport) != 2 {
		t.Fatalf("Beatport ran %d times, want 2", runs(analysis.Beatport))
	}
	setFail(false)

	// A changed file runs again.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(a.Path, later, later); err != nil {
		t.Fatal(err)
	}
	a.ModTime = later
	if _, err := e.lib.Upsert(a); err != nil {
		t.Fatal(err)
	}
	e.app.Analyze(e.ctx, a.ID, analysis.BPM)
	if runs(analysis.BPM) != 3 {
		t.Fatalf("BPM ran %d times after the file changed, want 3", runs(analysis.BPM))
	}
}
