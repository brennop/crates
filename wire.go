package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/app"
	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/fsops"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/player"
	"github.com/brennop/crates/tags"
)

// checks is what the startup checks found. Only a missing mpv is fatal;
// the rest shows up in app.Capabilities.
type checks struct {
	MPV   string            // path of the mpv binary
	Kid3  string            // path of kid3-cli; "" = not found, tag editing off
	Trash fsops.TrashMethod // how delete_mode = "trash" reaches the Trash
}

// runChecks looks for the external programs. The config was validated by
// config.Load already.
func runChecks() (checks, error) {
	var c checks
	var err error
	if c.MPV, err = exec.LookPath("mpv"); err != nil {
		return c, errors.New("mpv not found on PATH: install it (brew install mpv)")
	}
	c.Kid3, _ = exec.LookPath("kid3-cli")
	c.Trash = fsops.Method()
	return c, nil
}

// options are the paths and pieces build takes from outside the config, so
// tests can point them elsewhere.
type options struct {
	CachePath string        // the library cache
	PidFile   string        // mpv's pidfile
	MPVArgs   []string      // extra mpv arguments (tests pass --ao=null)
	Music     music.Service // nil: built from the config
	// BeatportTokenFile is the Beatport token the analysis panel refreshes
	// and saves; "" leaves only $BEATPORT_TOKEN.
	BeatportTokenFile string
}

// defaultOptions puts the cache and the pidfile in the user cache dir:
// ~/Library/Caches/crates on macOS.
func defaultOptions() (options, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return options{}, err
	}
	pid, err := player.DefaultPidFile()
	if err != nil {
		return options{}, err
	}
	bp, err := config.BeatportTokenPath()
	if err != nil {
		return options{}, err
	}
	return options{CachePath: filepath.Join(dir, "crates", "library.db"), PidFile: pid, BeatportTokenFile: bp}, nil
}

// build tidies master, builds every piece, wires them to one bus and starts
// them: the library loads its cache (LibraryLoaded) and starts its sweeps,
// mpv starts (killing an orphan named by the pidfile), and the reconciler
// waits for the first master sweep, then reads the Music snapshot. On error
// whatever was built is closed again. app.Close shuts it all down.
func build(cfg config.Config, o options) (*app.App, *events.Bus, error) {
	bus := events.NewBus()

	// Before any sweep or move: drop a crashed copy's temp file, and report
	// old files an overwrite could not trash.
	st, err := fsops.CleanStale(cfg.MasterDir)
	if err != nil {
		bus.Publish(events.Error{Op: "clean master", Err: err})
	}
	for _, p := range st.Kept {
		bus.Publish(events.Error{Op: "clean master", Err: fmt.Errorf("an earlier overwrite kept the replaced file at %s; delete it if you do not need it", p)})
	}

	kid3, err := tags.New()
	if err != nil {
		return nil, nil, fmt.Errorf("kid3-cli: %w", err)
	}
	lib, err := library.Open(library.Config{
		Master:    cfg.MasterDir,
		Inboxes:   cfg.InboxDirs,
		CachePath: o.CachePath,
		Reader:    kid3,
		Sink:      bus,
	})
	if err != nil {
		kid3.Close()
		return nil, nil, fmt.Errorf("library: %w", err)
	}
	svc := o.Music
	if svc == nil {
		if cfg.Music.Enabled {
			svc = music.New(music.Options{Autolaunch: cfg.Music.Autolaunch})
		} else {
			svc = music.Disabled{}
		}
	}
	a, err := app.New(app.Config{
		Library: lib,
		Tags:    kid3,
		Music:   svc,
		NewPlayer: func(s events.Sink) (player.Player, error) {
			m, err := player.Start(player.Config{Sink: s, PidFile: o.PidFile, Args: o.MPVArgs})
			if err != nil {
				return nil, fmt.Errorf("mpv: %w", err)
			}
			return m, nil
		},
		Bus:      bus,
		Settings: cfg,
		Analyzer: analysis.New(analysis.Options{BeatportToken: os.Getenv("BEATPORT_TOKEN"), BeatportTokenFile: o.BeatportTokenFile}),
	})
	if err != nil {
		return nil, nil, err // New closed the pieces
	}
	if err := lib.Start(context.Background()); err != nil {
		a.Close()
		return nil, nil, fmt.Errorf("library: %w", err)
	}
	if err := a.Start(); err != nil {
		a.Close()
		return nil, nil, err
	}
	return a, bus, nil
}
