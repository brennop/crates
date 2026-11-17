package main

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/ui"
)

// newUI builds the Bubble Tea model. While it is nil, crates runs headless.
//
// ctx is cancelled when the program ends; pass it to the ui commands so
// calls in flight stop before app.Close. bus is what ui.WaitForEvent
// reads. main closes the app after the program returns, so the model need
// not send ui.Close.
var newUI func(ctx context.Context, a *app.App, bus *events.Bus) tea.Model = ui.New
