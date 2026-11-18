package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/model"
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

func TestAnalyzeCacheSurvivesTagWrites(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3", tagged)
	runs, _ := countRuns(e)
	e.app.Analyze(e.ctx, a.ID, analysis.BPM)
	e.app.Analyze(e.ctx, a.ID, analysis.Beatport)

	// write changes the file's mtime, as kid3 does, then writes through
	// the app.
	write := func(name, value string, h time.Duration) {
		t.Helper()
		later := time.Now().Add(h)
		if err := os.Chtimes(a.Path, later, later); err != nil {
			t.Fatal(err)
		}
		if err := e.app.WriteTags(e.ctx, []model.TrackID{a.ID}, []model.FrameChange{{Name: name, Value: value}}); err != nil {
			t.Fatal(err)
		}
	}

	write("Initial Key", "8A", time.Hour)
	e.app.Analyze(e.ctx, a.ID, analysis.BPM)
	e.app.Analyze(e.ctx, a.ID, analysis.Beatport)
	if runs(analysis.BPM) != 1 || runs(analysis.Beatport) != 1 {
		t.Fatalf("after a key write: BPM ran %d, Beatport %d, want 1 each", runs(analysis.BPM), runs(analysis.Beatport))
	}

	// A new title is a new Beatport search; the audio jobs stay.
	write("Title", "Other", 2*time.Hour)
	e.app.Analyze(e.ctx, a.ID, analysis.BPM)
	e.app.Analyze(e.ctx, a.ID, analysis.Beatport)
	if runs(analysis.BPM) != 1 || runs(analysis.Beatport) != 2 {
		t.Fatalf("after a title write: BPM ran %d, Beatport %d, want 1 and 2", runs(analysis.BPM), runs(analysis.Beatport))
	}
}
