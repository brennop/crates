package music

import (
	"time"

	"github.com/brennop/crates/model"
)

// Timeouts for one osascript run.
const (
	SnapshotTimeout = 60 * time.Second
	CallTimeout     = 15 * time.Second
)

// Options configures the osascript client.
type Options struct {
	// Autolaunch starts Music when a call finds it not running. Off, such a
	// call returns ErrMusicUnavailable and Music stays closed.
	Autolaunch bool
	// Bin is the osascript binary; "" means "osascript" on PATH. Tests
	// point it at a stub.
	Bin string
	// SnapshotTimeout and CallTimeout override the defaults when non-zero.
	SnapshotTimeout time.Duration
	CallTimeout     time.Duration
}

// Service is a Client that holds a worker and must be closed.
type Service interface {
	Client
	// Close stops the worker and kills a script in flight.
	Close() error
}

var _ Service = Disabled{}

// editable reports whether a playlist can be written: not smart, and not a
// folder or a special playlist (spike finding 5).
func editable(smart bool, special string) bool { return !smart && special == "none" }

// playlistOf builds the model value from snapshot fields.
func playlistOf(id, name string, smart bool, special string) model.Playlist {
	return model.Playlist{ID: id, Name: name, Editable: editable(smart, special)}
}
