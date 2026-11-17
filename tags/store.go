package tags

import (
	"context"
	"time"

	"github.com/brennop/crates/model"
)

// Store reads summaries natively and full frames through kid3-cli. *Kid3 is
// the real one, *Fake the one for tests. It also satisfies
// library.SummaryReader.
type Store interface {
	Summary(path string) (model.TagSummary, time.Duration, error)
	Full(ctx context.Context, path string) ([]model.Frame, error)
	Write(ctx context.Context, paths []string, ch []model.FrameChange) error
	// Available reports whether kid3-cli was found. Without it Full and
	// Write return model.ErrKid3Missing.
	Available() bool
	// Close kills a kid3-cli call in flight and removes the private kid3
	// config. Full and Write fail afterwards; Summary still works.
	Close() error
}

var (
	_ Store = (*Kid3)(nil)
	_ Store = (*Fake)(nil)
)
