package app

import (
	"context"
	"sync"
	"time"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/model"
)

// analysisKey names a cached result: the job, the track, and what the job
// read from it. A changed file (size, mtime), summary or duration misses,
// so a stale result is never shown; a promote or rename keeps the hit.
type analysisKey struct {
	job      analysis.Job
	id       model.TrackID
	size     int64
	mtime    time.Time
	artist   string
	title    string
	duration time.Duration
}

// analysisCache keeps the results that succeeded until the app quits. It
// lives in memory only: nothing about it is written to disk.
type analysisCache struct {
	mu sync.Mutex
	m  map[analysisKey]analysis.Result
}

func keyFor(job analysis.Job, t model.Track) analysisKey {
	return analysisKey{job, t.ID, t.Size, t.ModTime, t.Summary.Artist, t.Summary.Title, t.Duration}
}

// carry keeps before's results for after, the same track once the app has
// written its tags: kid3 changes the size and mtime but not the audio. The
// Beatport result is carried only while the artist and title it searched
// for are the same; the audio jobs read neither.
func (c *analysisCache) carry(before, after model.Track) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, job := range analysis.Jobs {
		r, ok := c.m[keyFor(job, before)]
		if !ok {
			continue
		}
		if job == analysis.Beatport && (before.Summary.Artist != after.Summary.Artist || before.Summary.Title != after.Summary.Title) {
			continue
		}
		delete(c.m, keyFor(job, before))
		c.m[keyFor(job, after)] = r
	}
}

// Analyze runs one analysis job on track id. Jobs are independent: run
// them in parallel, one call each. It changes nothing; the results are for
// the user to read. Close, or cancelling ctx, stops a job in flight.
//
// A job that succeeded is not run again on the same track for the rest of
// the session, unless the file, its summary or its duration changed.
// Failures are not kept, so the next call tries again.
func (a *App) Analyze(ctx context.Context, id model.TrackID, job analysis.Job) analysis.Result {
	t, err := a.track(id)
	if err != nil {
		return analysis.Result{Job: job, Err: err}
	}
	k := keyFor(job, t)
	a.analyses.mu.Lock()
	r, ok := a.analyses.m[k]
	a.analyses.mu.Unlock()
	if ok {
		return r
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	r = a.runJob(ctx, job, analysis.Track{Path: t.Path, Artist: t.Summary.Artist, Title: t.Summary.Title, Duration: t.Duration})
	if r.Err == nil {
		a.analyses.mu.Lock()
		if a.analyses.m == nil {
			a.analyses.m = map[analysisKey]analysis.Result{}
		}
		a.analyses.m[k] = r
		a.analyses.mu.Unlock()
	}
	return r
}

// Spek opens track id in Spek and returns once its window is closed.
// Close kills it. A track over analysis.MaxDuration is refused.
func (a *App) Spek(ctx context.Context, id model.TrackID) error {
	t, err := a.track(id)
	if err != nil {
		return err
	}
	a.speksMu.Lock()
	if a.ctx.Err() != nil { // closing: Close may already be waiting
		a.speksMu.Unlock()
		return a.ctx.Err()
	}
	a.speks.Add(1)
	a.speksMu.Unlock()
	defer a.speks.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	return analysis.Spek(ctx, analysis.Track{Path: t.Path, Duration: t.Duration})
}

// HasBeatportToken reports whether a Beatport token is saved, whether or
// not Beatport will still accept it.
func (a *App) HasBeatportToken() bool { return a.analyzer.HasBeatportToken() }

// SaveBeatportToken saves s, a Beatport token response as JSON or a bare
// access token, for the Beatport job.
func (a *App) SaveBeatportToken(s string) error { return a.analyzer.SaveBeatportToken(s) }
