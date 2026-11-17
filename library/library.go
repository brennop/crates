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

	// fsMu is held by a sweep while it lists and classifies files, while it
	// commits, and while it removes vanished tracks; and by Exclusive.
	fsMu sync.Mutex

	mu      sync.Mutex    // guards the fields below
	sweptAt time.Time     // start of the last complete master sweep; zero = none yet
	swept   chan struct{} // closed when the next master sweep completes
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
		swept:   make(chan struct{}),
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

// Upsert stores t, which must sit under a root. A zero ID takes the ID of the
// track already at t's path, or a new one. The role comes from the root and
// the MusicID is kept from the stored track. It publishes TracksChanged when
// anything changed, and returns the stored track.
func (l *Library) Upsert(t model.Track) (model.Track, error) {
	p, err := pathnorm.Normalize(t.Path)
	if err != nil {
		return model.Track{}, err
	}
	t.Path = p
	stored, changed, err := l.idx.upsert(t)
	if err != nil || !changed {
		return stored, err
	}
	if err := l.cache.write([]model.Track{stored}, nil); err != nil {
		return stored, err
	}
	l.sink.Publish(events.TracksChanged{Tracks: []model.Track{stored}})
	return stored, nil
}

// SetMusicID sets or clears ("") the Music persistent ID of track id and
// publishes TracksChanged when it changed. The cache does not store it.
func (l *Library) SetMusicID(id model.TrackID, musicID string) error {
	t, changed, err := l.idx.setMusicID(id, musicID)
	if err != nil || !changed {
		return err
	}
	l.sink.Publish(events.TracksChanged{Tracks: []model.Track{t}})
	return nil
}

// MasterTracks returns copies of every master track.
func (l *Library) MasterTracks() []model.Track { return l.idx.under(l.roots[0]) }

// MasterCaseSensitive reports whether the master volume tells names apart
// by case. Keys built with pathnorm.Key must use it.
func (l *Library) MasterCaseSensitive() bool { return l.roots[0].caseSensitive }

// MasterSwept returns when the last complete master sweep started (zero if
// none has completed yet), and a channel that is closed when the next one
// completes. The startup pass counts as a sweep.
func (l *Library) MasterSwept() (time.Time, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sweptAt, l.swept
}

// markSwept records a complete master sweep that started at start.
func (l *Library) markSwept(start time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweptAt = start
	close(l.swept)
	l.swept = make(chan struct{})
}

// MasterDir returns the normalized master dir.
func (l *Library) MasterDir() string { return l.roots[0].dir }

// FindMaster returns the master track whose file name equals name, ignoring
// case, whatever the master volume's case sensitivity.
func (l *Library) FindMaster(name string) (model.Track, bool) {
	return l.idx.findName(l.roots[0], name)
}

// Exclusive runs fn while no sweep is reading the disk or changing the index
// from what it read. Callers that move or replace a file and then update the
// index (promote, overwrite) do both inside fn, so a sweep never sees the
// file moved but the index not yet updated. fn must not call SweepMaster.
func (l *Library) Exclusive(fn func() error) error {
	l.fsMu.Lock()
	defer l.fsMu.Unlock()
	return fn()
}
