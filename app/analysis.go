package app

import (
	"context"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/model"
)

// Analyze runs one analysis job on track id. Jobs are independent: run
// them in parallel, one call each. It changes nothing; the results are for
// the user to read. Close, or cancelling ctx, stops a job in flight.
func (a *App) Analyze(ctx context.Context, id model.TrackID, job analysis.Job) analysis.Result {
	t, err := a.track(id)
	if err != nil {
		return analysis.Result{Job: job, Err: err}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	return a.analyzer.Run(ctx, job, analysis.Track{Path: t.Path, Artist: t.Summary.Artist, Title: t.Summary.Title})
}

// HasBeatportToken reports whether a Beatport token is saved, whether or
// not Beatport will still accept it.
func (a *App) HasBeatportToken() bool { return a.analyzer.HasBeatportToken() }

// SaveBeatportToken saves s, a Beatport token response as JSON or a bare
// access token, for the Beatport job.
func (a *App) SaveBeatportToken(s string) error { return a.analyzer.SaveBeatportToken(s) }
