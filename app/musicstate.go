package app

import (
	"context"
	"errors"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
)

// MusicStatus returns Music's availability as the last call found it. Before
// the first call it is Unavailable.
func (a *App) MusicStatus() model.MusicState {
	a.recMu.Lock()
	defer a.recMu.Unlock()
	return a.state
}

// LaunchMusic starts Music, then reads a new snapshot, which lets the
// reconciler resume adds that were waiting.
func (a *App) LaunchMusic(ctx context.Context) error {
	if !a.musicOn {
		return music.ErrDisabled
	}
	if err := a.music.Launch(ctx); err != nil {
		return err
	}
	return a.refreshSnapshot(ctx)
}

// setMusicState records a new state and publishes MusicStatus when it
// changed. Disabled never changes, and CopySettingOn holds until restart:
// the user has to fix the setting and clean up Music's copies first.
func (a *App) setMusicState(s model.MusicState) {
	a.recMu.Lock()
	switch {
	case !a.musicOn:
		s = model.MusicDisabled
	case a.latched:
		s = model.MusicCopySettingOn
	case s == model.MusicCopySettingOn:
		a.latched = true
	}
	changed := !a.stateKnown || a.state != s
	a.state, a.stateKnown = s, true
	a.recMu.Unlock()
	if changed {
		a.bus.Publish(events.MusicStatus{State: s})
	}
}

// noteMusic updates the state from the outcome of a Music call. Errors that
// say nothing about availability leave it alone.
func (a *App) noteMusic(err error) {
	switch {
	case err == nil:
		a.setMusicState(model.MusicAvailable)
	case errors.As(err, new(model.ErrAutomationDenied)):
		a.setMusicState(model.MusicDenied)
	case errors.As(err, new(model.ErrCopySettingOn)):
		a.setMusicState(model.MusicCopySettingOn)
	case errors.As(err, new(model.ErrMusicUnavailable)):
		a.setMusicState(model.MusicUnavailable)
	}
}

// trackedClient passes every call through and notes its outcome, so
// MusicStatus follows whatever part of the app talked to Music last.
type trackedClient struct {
	music.Client
	a *App
}

func (c trackedClient) Status(ctx context.Context) (model.MusicState, error) {
	s, err := c.Client.Status(ctx)
	if ctx.Err() == nil {
		c.a.setMusicState(s)
	}
	return s, err
}

func (c trackedClient) Launch(ctx context.Context) error {
	err := c.Client.Launch(ctx)
	c.a.noteMusic(err)
	return err
}

func (c trackedClient) Snapshot(ctx context.Context) (music.Snapshot, error) {
	s, err := c.Client.Snapshot(ctx)
	c.a.noteMusic(err)
	return s, err
}

func (c trackedClient) Add(ctx context.Context, path string) (string, error) {
	id, err := c.Client.Add(ctx, path)
	c.a.noteMusic(err)
	return id, err
}

func (c trackedClient) AddToPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error) {
	got, err := c.Client.AddToPlaylist(ctx, playlistID, musicIDs)
	c.a.noteMusic(err)
	return got, err
}

func (c trackedClient) RemoveFromPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error) {
	got, err := c.Client.RemoveFromPlaylist(ctx, playlistID, musicIDs)
	c.a.noteMusic(err)
	return got, err
}

func (c trackedClient) DeleteTrack(ctx context.Context, musicID string) error {
	err := c.Client.DeleteTrack(ctx, musicID)
	c.a.noteMusic(err)
	return err
}

func (c trackedClient) Refresh(ctx context.Context, musicID string) error {
	err := c.Client.Refresh(ctx, musicID)
	c.a.noteMusic(err)
	return err
}
