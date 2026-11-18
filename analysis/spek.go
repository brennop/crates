package analysis

import (
	"context"
	"errors"
	"os/exec"
)

// ErrNoSpek: spek is not on PATH, so the spectrogram cannot be shown.
var ErrNoSpek = errors.New("spek not found on PATH: install it (brew install spek)")

// Spek opens t in Spek, the spectrum viewer, and returns once its window
// is closed. It runs like the scripts (runGroup): its own process group at
// lower priority, killed on cancel or past MemoryLimit. Spek peaks at
// 0.12 GB on a 12:27 track. Tracks over MaxDuration are refused with
// ErrTooLong.
func Spek(ctx context.Context, t Track) error {
	if t.Duration > MaxDuration {
		return ErrTooLong
	}
	spek, err := exec.LookPath("spek")
	if err != nil {
		return ErrNoSpek
	}
	_, stderr, err := runGroup(ctx, spek, t.Path)
	if err != nil && ctx.Err() == nil && !errors.Is(err, ErrTooMuchMemory) {
		if msg := lastLine(string(stderr)); msg != "" {
			return errors.New(msg)
		}
	}
	return err
}
