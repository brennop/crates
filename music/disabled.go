package music

import (
	"context"
	"time"

	"github.com/brennop/crates/model"
)

// Disabled is the client used when music.enabled is false, and on systems
// without Music. Status reports MusicDisabled, Snapshot is empty, and every
// change fails with ErrDisabled.
type Disabled struct{}

var _ Client = Disabled{}

func (Disabled) Status(context.Context) (model.MusicState, error) { return model.MusicDisabled, nil }
func (Disabled) Launch(context.Context) error                     { return ErrDisabled }

func (Disabled) Snapshot(context.Context) (Snapshot, error) {
	return Snapshot{Taken: time.Now(), Tracks: map[string]string{}, Members: map[string][]string{}}, nil
}

func (Disabled) Add(context.Context, string) (string, error) { return "", ErrDisabled }

func (Disabled) AddToPlaylist(context.Context, string, []string) ([]string, error) {
	return nil, ErrDisabled
}

func (Disabled) RemoveFromPlaylist(context.Context, string, []string) ([]string, error) {
	return nil, ErrDisabled
}

func (Disabled) DeleteTrack(context.Context, string) error { return ErrDisabled }
func (Disabled) Refresh(context.Context, string) error     { return ErrDisabled }
func (Disabled) Close() error                              { return nil }
