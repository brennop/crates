package player

import (
	"context"
	"errors"
	"time"
)

// Tuning values (see TASKS.md, Decisions).
const (
	StartTimeout   = 5 * time.Second        // wait for mpv's socket to accept
	RequestTimeout = 5 * time.Second        // one IPC request
	StateInterval  = 250 * time.Millisecond // PlayerState at most this often
)

// Player plays one file at a time. The queue lives in app; the player only
// knows the file it was last told to load.
//
// Events go to the Sink given at construction: PlayerState (throttled),
// TrackEnded and LoadFailed (at once, with Path set and TrackID zero, for app
// to fill in) and PlayerDown.
type Player interface {
	// Load replaces the current file with path, which must be absolute, and
	// starts playing at start.
	Load(ctx context.Context, path string, start time.Duration) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	// Stop unloads the current file. It does not produce TrackEnded.
	Stop(ctx context.Context) error
	// Seek moves to an absolute position in the current file.
	Seek(ctx context.Context, pos time.Duration) error
	// SetVolume sets the volume, 0–100; values outside are clamped.
	SetVolume(ctx context.Context, v float64) error
	Close() error
}

var (
	// ErrNotInstalled: mpv is not on PATH.
	ErrNotInstalled = errors.New("player: mpv not found on PATH")
	// ErrClosed: the player was closed, or mpv is down and restarting.
	ErrClosed = errors.New("player: not connected to mpv")
	// ErrTimeout: mpv did not answer a request in time.
	ErrTimeout = errors.New("player: mpv did not answer")
)

// CommandError is an error mpv answered a command with.
type CommandError struct {
	Command string
	Msg     string // mpv's text, e.g. "error running command"
}

func (e *CommandError) Error() string { return "mpv " + e.Command + ": " + e.Msg }

func clampVolume(v float64) float64 {
	return min(max(v, 0), 100)
}
