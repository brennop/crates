package library

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// SummaryReader reads the summary and duration of one file. The tags package
// provides it; library may not import tags.
type SummaryReader interface {
	Summary(path string) (model.TagSummary, time.Duration, error)
}

// Config says where the library lives and what reads its files.
type Config struct {
	Master    string   // the flat master dir; required
	Inboxes   []string // scanned recursively; may not exist yet
	CachePath string   // SQLite file; created with its directory if missing
	Reader    SummaryReader
	Sink      events.Sink
}

var (
	ErrNotStarted   = errors.New("library: not started")
	ErrClosed       = errors.New("library: closed")
	ErrUnknownTrack = errors.New("library: unknown track")
)

// Library is the track index, its cache and the sweeps that keep both in line
// with disk.
type Library struct {
	sink    events.Sink
	reader  SummaryReader
	roots   []*root // roots[0] is the master
	idx     *index
	cache   *cache
	workers int

	wake chan struct{} // buffered 1: a master sweep was requested

	mu      sync.Mutex // guards the fields below
	started bool
	closed  bool
	waiters []chan error // master sweep requests not yet served
	cancel  context.CancelFunc
	done    chan struct{} // closed when the watch goroutine exits
}

// Open opens the cache and loads its tracks into the index, then publishes
// LibraryLoaded. Nothing is scanned until Start. Tracks whose paths no longer
// sit under any configured root are dropped.
func Open(cfg Config) (*Library, error) {
	if cfg.Master == "" {
		return nil, errors.New("library: master dir is required")
	}
	if cfg.CachePath == "" || cfg.Reader == nil || cfg.Sink == nil {
		return nil, errors.New("library: cache path, reader and sink are required")
	}
	master, err := newRoot(cfg.Master, model.Master, false)
	if err != nil {
		return nil, fmt.Errorf("master: %w", err)
	}
	roots := []*root{master}
	for _, dir := range cfg.Inboxes {
		r, err := newRoot(dir, model.Inbox, true)
		if err != nil {
			return nil, fmt.Errorf("inbox %s: %w", dir, err)
		}
		roots = append(roots, r)
	}
	c, tracks, err := openCache(cfg.CachePath)
	if err != nil {
		return nil, err
	}
	l := &Library{
		sink:    cfg.Sink,
		reader:  cfg.Reader,
		roots:   roots,
		idx:     newIndex(roots),
		cache:   c,
		workers: runtime.NumCPU(),
		wake:    make(chan struct{}, 1),
	}
	var stale []model.TrackID
	for _, t := range tracks {
		if _, _, err := l.idx.upsert(t); err != nil {
			stale = append(stale, t.ID)
		}
	}
	if err := c.write(nil, stale); err != nil {
		c.close()
		return nil, err
	}
	l.sink.Publish(events.LibraryLoaded{Count: l.idx.count()})
	return l, nil
}

// Start scans every root in the background and then sweeps: the inboxes every
// 3 s, and the master when SweepMaster asks. It returns at once. ctx or Close
// stops it.
func (l *Library) Start(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	if l.started {
		return errors.New("library: already started")
	}
	l.started = true
	ctx, l.cancel = context.WithCancel(ctx)
	l.done = make(chan struct{})
	go l.run(ctx)
	return nil
}

// Close stops the watch goroutine, waits for it, and closes the cache. Calls
// after Close fail.
func (l *Library) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	started, cancel, done := l.started, l.cancel, l.done
	l.mu.Unlock()
	if started {
		cancel()
		<-done
	}
	return l.cache.close()
}

// Get returns copies of the tracks with the given IDs, in the order given.
// Unknown IDs are skipped.
func (l *Library) Get(ids ...model.TrackID) []model.Track {
	return l.idx.get(ids)
}

// Move gives track id a new path and keeps its ID, for promote and rename.
// The path is normalized, and its role comes from the root it sits under. A
// track already at the new path is displaced and removed.
func (l *Library) Move(id model.TrackID, path string) error {
	p, err := pathnorm.Normalize(path)
	if err != nil {
		return err
	}
	moved, displaced, err := l.idx.move(id, p)
	if err != nil {
		return err
	}
	var removed []model.TrackID
	if displaced != 0 {
		removed = []model.TrackID{displaced}
	}
	if err := l.cache.write([]model.Track{moved}, removed); err != nil {
		return err
	}
	if displaced != 0 {
		l.sink.Publish(events.TrackRemoved{IDs: removed})
	}
	l.sink.Publish(events.TracksChanged{Tracks: []model.Track{moved}})
	return nil
}

// SetDuration stores d for track id when the track has no duration yet. It
// reports whether it stored one. A stored duration is never replaced.
func (l *Library) SetDuration(id model.TrackID, d time.Duration) (bool, error) {
	t, changed, err := l.idx.setDuration(id, d)
	if err != nil || !changed {
		return false, err
	}
	if err := l.cache.write([]model.Track{t}, nil); err != nil {
		return true, err
	}
	l.sink.Publish(events.TracksChanged{Tracks: []model.Track{t}})
	return true, nil
}

// Remove drops tracks from the index and the cache, and publishes
// TrackRemoved for those that were present. The files are not touched.
func (l *Library) Remove(ids ...model.TrackID) error {
	removed := l.idx.remove(ids)
	if len(removed) == 0 {
		return nil
	}
	if err := l.cache.write(nil, removed); err != nil {
		return err
	}
	l.sink.Publish(events.TrackRemoved{IDs: removed})
	return nil
}
