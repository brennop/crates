package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
// NewPlayer because App is its event sink.
type Config struct {
	Library   *library.Library
	Tags      tags.Store
	Music     music.Client
	NewPlayer func(events.Sink) (player.Player, error)
	Bus       events.Sink // the UI's bus
	Settings  config.Config
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
// Lock order: opMu, then the library's Exclusive, then pmu, then smu, then
// mmu.
type App struct {
	lib      *library.Library
	tags     tags.Store
	music    music.Client
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

	inbox  *events.Bus // player events, handled by run
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
func New(cfg Config) (*App, error) {
	if cfg.Library == nil || cfg.Tags == nil || cfg.Music == nil || cfg.NewPlayer == nil || cfg.Bus == nil {
		return nil, errors.New("app: library, tags, music, player and bus are required")
	}
	mode := fsops.Mode(cfg.Settings.DeleteMode)
	if mode != fsops.ModeTrash && mode != fsops.ModeRemove {
		return nil, fmt.Errorf("app: unknown delete mode %q", cfg.Settings.DeleteMode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		lib:      cfg.Library,
		tags:     cfg.Tags,
		music:    cfg.Music,
		bus:      cfg.Bus,
		required: append([]string(nil), cfg.Settings.RequiredTags...),
		mode:     mode,
		fs:       realFS{},
		pos:      -1,
		inbox:    events.NewBus(),
		cancel:   cancel,
		done:     make(chan struct{}),
		now:      time.Now,
	}
	p, err := cfg.NewPlayer(a)
	if err != nil {
		cancel()
		return nil, err
	}
	a.player = p
	go a.run(ctx)
	return a, nil
}

// Close stops handling player events and closes the player.
func (a *App) Close() error {
	a.cancel()
	<-a.done
	return a.player.Close()
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
