package library

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/brennop/crates/model"
)

// Watcher keeps the index in line with disk. Polling is the only
// implementation: fsnotify holds one descriptor per file on macOS.
type Watcher interface {
	Start(ctx context.Context) error
	SweepMaster(ctx context.Context) error
	Close() error
}

var _ Watcher = (*Library)(nil)

// SweepMaster sweeps the master dir and returns when the sweep is done. Calls
// that arrive while a sweep runs share one sweep that starts after them. It
// needs Start first.
func (l *Library) SweepMaster(ctx context.Context) error {
	reply := make(chan error, 1)
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return ErrNotStarted
	}
	l.waiters = append(l.waiters, reply)
	done := l.done
	l.mu.Unlock()

	select {
	case l.wake <- struct{}{}:
	default:
	}
	select {
	case err := <-reply:
		return err
	case <-done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the only goroutine that scans. It does a full pass first, then
// alternates inbox ticks and master requests.
func (l *Library) run(ctx context.Context) {
	defer close(l.done)
	l.pass(ctx)
	tick := time.NewTicker(inboxInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, r := range l.roots[1:] {
				if err := l.scanRoot(ctx, r); err != nil && !errors.Is(err, fs.ErrNotExist) {
					l.report("sweep "+r.dir, err)
				}
			}
		case <-l.wake:
			l.serveSweeps(ctx)
		}
	}
}

// pass scans the master and then every inbox. A missing inbox is expected
// and is not reported.
func (l *Library) pass(ctx context.Context) {
	for _, r := range l.roots {
		if err := l.scanRoot(ctx, r); err != nil {
			if r.role == model.Inbox && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			l.report("scan "+r.dir, err)
		}
	}
}

// serveSweeps runs one master sweep for every request queued so far, and
// wakes each requester with the result.
func (l *Library) serveSweeps(ctx context.Context) {
	l.mu.Lock()
	batch := l.waiters
	l.waiters = nil
	l.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	err := l.scanRoot(ctx, l.roots[0])
	for _, reply := range batch {
		reply <- err
	}
}
