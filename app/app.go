package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/fsops"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/player"
	"github.com/brennop/crates/tags"
)

// Config holds the pieces App is built from. The player is built by
// NewPlayer because App is its event sink. App owns the pieces from New on:
// Close closes them all, and so does a failed New (unless a piece is nil).
type Config struct {
	Library   *library.Library
	Tags      tags.Store
	Music     music.Service
	NewPlayer func(events.Sink) (player.Player, error)
	Bus       events.Sink // the UI's bus
	Settings  config.Config
	Analyzer  *analysis.Analyzer // nil: one that cannot save a Beatport token
}

// ErrUnknownTrack: an ID is not in the index (deleted, or never there).
var ErrUnknownTrack = errors.New("app: unknown track")

// reloadWindow is how long after WriteTags on the playing track a load
// failure reloads the track instead of stopping.
const reloadWindow = 10 * time.Second

// App is the facade over library, tags, player, music and fsops. Its
// methods are safe from any goroutine, return values or model typed errors,
// and report background work on the bus. None waits on the UI.
//
// Lock order: passMu, then opMu, then the library's Exclusive, then pmu,
// then smu, then mmu, then recMu.
type App struct {
	lib      *library.Library
	tags     tags.Store
	analyzer *analysis.Analyzer
	music    music.Client  // wrapped to track Music's state
	musicSvc music.Service // the unwrapped one, to close
	player   player.Player
	bus      events.Sink
	required []string
	mode     fsops.Mode
	fs       fileOps

	// opMu serializes operations that change files, the index or Music:
	// promote, overwrite, delete, tag writes, playlist writes.
	opMu sync.Mutex

	// pmu guards the queue and serializes player commands.
	pmu   sync.Mutex
	queue []model.TrackID
	pos   int // index into queue of the current track; -1 = none

	// smu guards what the player last told us.
	smu    sync.Mutex
	loaded loadedTrack // the track the player was last told to load
	last   events.PlayerState
	reload reloadArm

	// mmu guards the Music snapshot.
	mmu  sync.Mutex
	snap music.Snapshot

	// The reconciler. musicOn is fixed at New.
	musicOn bool
	passMu  sync.Mutex    // one reconcile pass at a time
	kick    chan struct{} // buffered 1: run a pass
	recDone chan struct{} // closed when the reconciler stops

	// recMu guards the fields below.
	recMu      sync.Mutex
	recStarted bool
	state      model.MusicState
	stateKnown bool // state was set at least once
	latched    bool // an add found the copy setting on: no adds until restart
	promoted   []model.TrackID
	pending    int  // tracks in the ImportPending awaiting an answer; 0 = none
	confirmed  bool // the user confirmed an import; cleared when one completes
	dead       []events.DeadEntry

	closeOnce sync.Once

	inbox  *events.Bus // player events, handled by run
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	now    func() time.Time
}

type loadedTrack struct {
	id   model.TrackID // 0 = nothing loaded
	path string
}

// reloadArm marks the playing track as just rewritten by WriteTags.
type reloadArm struct {
	id    model.TrackID // 0 = not armed
	pos   time.Duration
	until time.Time
}

// fileOps is fsops, swappable in tests.
type fileOps interface {
	Move(src, dst string) error
	Replace(src, dst string, mode fsops.Mode) error
	Delete(path string, mode fsops.Mode) error
}

type realFS struct{}

func (realFS) Move(src, dst string) error                     { return fsops.Move(src, dst) }
func (realFS) Replace(src, dst string, mode fsops.Mode) error { return fsops.Replace(src, dst, mode) }
func (realFS) Delete(path string, mode fsops.Mode) error      { return fsops.Delete(path, mode) }

// New builds the app and its player and starts handling player events. The
// library should already be open; starting its sweeps is the caller's job.
// Start starts the reconciler. With Settings.Music.Enabled off, Music should
// be music.Disabled; the reconciler stays off.
func New(cfg Config) (*App, error) {
	if cfg.Library == nil || cfg.Tags == nil || cfg.Music == nil || cfg.NewPlayer == nil || cfg.Bus == nil {
		return nil, errors.New("app: library, tags, music, player and bus are required")
	}
	mode := fsops.Mode(cfg.Settings.DeleteMode)
	if mode != fsops.ModeTrash && mode != fsops.ModeRemove {
		return nil, errors.Join(fmt.Errorf("app: unknown delete mode %q", cfg.Settings.DeleteMode), closePieces(cfg))
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		lib:      cfg.Library,
		tags:     cfg.Tags,
		analyzer: cfg.Analyzer,
		musicSvc: cfg.Music,
		bus:      cfg.Bus,
		required: append([]string(nil), cfg.Settings.RequiredTags...),
		mode:     mode,
		fs:       realFS{},
		pos:      -1,
		musicOn:  cfg.Settings.Music.Enabled,
		kick:     make(chan struct{}, 1),
		recDone:  make(chan struct{}),
		state:    model.MusicUnavailable,
		inbox:    events.NewBus(),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		now:      time.Now,
	}
	if a.analyzer == nil {
		a.analyzer = analysis.New(analysis.Options{})
	}
	p, err := cfg.NewPlayer(a)
	if err != nil {
		cancel()
		return nil, errors.Join(err, closePieces(cfg))
	}
	if !a.musicOn {
		a.state = model.MusicDisabled
	}
	a.music = trackedClient{Client: cfg.Music, a: a}
	a.player = p
	go a.run(ctx)
	return a, nil
}

// closePieces closes what a failed New was given.
func closePieces(cfg Config) error {
	return errors.Join(cfg.Music.Close(), cfg.Tags.Close(), cfg.Library.Close())
}

// Close is the one shutdown path. In order: it stops handling player events
// and the reconciler; closes the player (mpv quits, its socket dir and
// pidfile are removed); closes Music (an osascript in flight is killed);
// closes tags (a kid3-cli call in flight is killed, the private kid3 config
// removed); then closes the library, which stops its sweeps and flushes
// and closes the cache. Calls into App after Close fail. A second Close
// does nothing and returns nil.
func (a *App) Close() error {
	var first bool
	a.closeOnce.Do(func() { first = true })
	if !first {
		return nil
	}
	a.cancel()
	<-a.done
	a.recMu.Lock()
	started := a.recStarted
	a.recMu.Unlock()
	if started {
		<-a.recDone
	}
	var errs []error
	if err := a.player.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close player: %w", err))
	}
	if err := a.musicSvc.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close music: %w", err))
	}
	if err := a.tags.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close tags: %w", err))
	}
	if err := a.lib.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close library: %w", err))
	}
	return errors.Join(errs...)
}

// Publish is the player's sink. It never blocks: events queue up for run,
// which acts on them and forwards them to the bus.
func (a *App) Publish(e events.Event) { a.inbox.Publish(e) }

func (a *App) run(ctx context.Context) {
	defer close(a.done)
	for {
		e, err := a.inbox.Next(ctx)
		if err != nil {
			return
		}
		a.handle(ctx, e)
	}
}

// track returns the indexed track with id.
func (a *App) track(id model.TrackID) (model.Track, error) {
	ts := a.lib.Get(id)
	if len(ts) == 0 {
		return model.Track{}, fmt.Errorf("%w: %d", ErrUnknownTrack, id)
	}
	return ts[0], nil
}

// report publishes a background failure.
func (a *App) report(op string, err error) {
	a.bus.Publish(events.Error{Op: op, Err: err})
}
