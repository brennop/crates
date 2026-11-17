// Command crates is the music library manager.
//
// It reads the config, runs the startup checks, builds and starts the app,
// and runs the UI. The Bubble Tea model is the developer's: assign newUI
// (see ui.go). Until then, or with -headless, it runs without a UI and logs
// every event to stderr until interrupted.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
)

func main() {
	os.Exit(run())
}

func run() int {
	checkOnly := flag.Bool("check", false, "run the startup checks, print what they found and exit")
	headless := flag.Bool("headless", false, "run without the UI and log events to stderr")
	flag.Parse()

	path, err := config.Path()
	if err != nil {
		return fail(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return fail(err)
	}
	c, err := runChecks()
	if err != nil {
		return fail(err)
	}
	if *checkOnly {
		printChecks(path, cfg, c)
		return 0
	}
	opts, err := defaultOptions()
	if err != nil {
		return fail(err)
	}

	a, bus, err := build(cfg, opts)
	if err != nil {
		return fail(err)
	}
	if newUI == nil || *headless {
		err = runHeadless(bus)
	} else {
		err = runUI(a, bus)
	}
	if cerr := a.Close(); cerr != nil {
		err = errors.Join(err, cerr)
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

// runUI runs the developer's model. The ctx it gets is cancelled when the
// program ends, so commands in flight stop before Close.
func runUI(a *app.App, bus *events.Bus) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	p := tea.NewProgram(newUI(ctx, a, bus), tea.WithContext(ctx))
	_, err := p.Run()
	cancel()
	if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // Ctrl-C or a signal ended it
	}
	return err
}

// runHeadless logs every event until SIGINT, SIGTERM or SIGHUP.
func runHeadless(bus *events.Bus) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	fmt.Fprintln(os.Stderr, "crates: running without a UI; Ctrl-C quits")
	for {
		e, err := bus.Next(ctx)
		if err != nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "%T %+v\n", e, e)
	}
}

func printChecks(path string, cfg config.Config, c checks) {
	kid3 := c.Kid3
	if kid3 == "" {
		kid3 = "not found (tag editing is off)"
	}
	fmt.Printf("config       %s\n", path)
	fmt.Printf("master_dir   %s\n", cfg.MasterDir)
	for _, d := range cfg.InboxDirs {
		fmt.Printf("inbox        %s\n", d)
	}
	fmt.Printf("mpv          %s\n", c.MPV)
	fmt.Printf("kid3-cli     %s\n", kid3)
	fmt.Printf("delete_mode  %s\n", cfg.DeleteMode)
	if cfg.DeleteMode == config.DeleteTrash {
		fmt.Printf("trash        %s\n", c.Trash)
	}
	fmt.Printf("music        enabled=%t autolaunch=%t\n", cfg.Music.Enabled, cfg.Music.Autolaunch)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "crates:", err)
	return 1
}
