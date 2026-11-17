package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
)

// QueueState is a copy of the play queue.
type QueueState struct {
	IDs     []model.TrackID
	Pos     int           // index of the current track in IDs; -1 = none
	Playing model.TrackID // the track loaded in the player; 0 = none
}

// ErrQueueIndex: RemoveFromQueue got an index outside the queue.
var ErrQueueIndex = errors.New("app: no such queue entry")

// Queue returns a copy of the play queue.
func (a *App) Queue() QueueState {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	return QueueState{IDs: slices.Clone(a.queue), Pos: a.pos, Playing: a.loadedID()}
}

// Play plays track id. If it is in the queue, the queue jumps to it;
// otherwise it is inserted after the current track.
func (a *App) Play(ctx context.Context, id model.TrackID) error {
	if _, err := a.track(id); err != nil {
		return err
	}
	a.pmu.Lock()
	defer a.pmu.Unlock()
	i := slices.Index(a.queue, id)
	if i < 0 {
		i = a.pos + 1
		a.queue = slices.Insert(a.queue, i, id)
	}
	return a.loadAt(ctx, i, 0)
}

// Enqueue appends tracks to the queue. It does not start playback.
func (a *App) Enqueue(ids ...model.TrackID) {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	a.queue = append(a.queue, ids...)
}

// Next plays the next track still in the index. Past the end, playback
// stops.
func (a *App) Next(ctx context.Context) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	return a.advance(ctx, a.pos+1, +1)
}

// Previous plays the previous track still in the index. At the start it
// restarts the current track.
func (a *App) Previous(ctx context.Context) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	for j := a.pos - 1; j >= 0; j-- {
		if len(a.lib.Get(a.queue[j])) == 1 {
			return a.loadAt(ctx, j, 0)
		}
	}
	if a.pos >= 0 && a.pos < len(a.queue) {
		return a.loadAt(ctx, a.pos, 0)
	}
	return nil
}

// RemoveFromQueue removes entry i. Removing the playing entry stops
// playback; the position stays on the entry that follows.
func (a *App) RemoveFromQueue(ctx context.Context, i int) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	if i < 0 || i >= len(a.queue) {
		return fmt.Errorf("%w: %d", ErrQueueIndex, i)
	}
	var err error
	if i == a.pos && a.loadedID() != 0 {
		err = a.stop(ctx)
	}
	a.removeAt(i)
	return err
}

// ClearQueue stops playback and empties the queue.
func (a *App) ClearQueue(ctx context.Context) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	var err error
	if a.loadedID() != 0 {
		err = a.stop(ctx)
	}
	a.queue, a.pos = nil, -1
	return err
}

// TogglePause pauses or resumes. With nothing loaded it plays the current
// queue entry from the start.
func (a *App) TogglePause(ctx context.Context) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	a.smu.Lock()
	loaded, paused := a.loaded.id, a.last.Paused
	a.smu.Unlock()
	switch {
	case loaded == 0 && a.pos >= 0 && a.pos < len(a.queue):
		return a.loadAt(ctx, a.pos, 0)
	case loaded == 0:
		return nil
	case paused:
		return a.player.Resume(ctx)
	}
	return a.player.Pause(ctx)
}

// Seek moves to d, or by d when relative. Positions below zero become zero.
func (a *App) Seek(ctx context.Context, d time.Duration, relative bool) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	a.smu.Lock()
	loaded, pos := a.loaded.id, a.last.Position
	a.smu.Unlock()
	if loaded == 0 {
		return nil
	}
	if relative {
		d += pos
	}
	return a.player.Seek(ctx, max(d, 0))
}

// SetVolume sets the volume, 0–100.
func (a *App) SetVolume(ctx context.Context, v float64) error {
	a.pmu.Lock()
	defer a.pmu.Unlock()
	return a.player.SetVolume(ctx, v)
}

// ---- helpers; callers hold pmu ----

func (a *App) loadedID() model.TrackID {
	a.smu.Lock()
	defer a.smu.Unlock()
	return a.loaded.id
}

// loadAt makes entry i current and loads it, resolving its ID to the path it
// has now. On failure the position stays on i and nothing is loaded.
func (a *App) loadAt(ctx context.Context, i int, start time.Duration) error {
	a.pos = i
	id := a.queue[i]
	t, err := a.track(id)
	if err == nil {
		err = a.player.Load(ctx, t.Path, start)
	}
	a.smu.Lock()
	defer a.smu.Unlock()
	if err != nil {
		a.loaded = loadedTrack{}
		return err
	}
	a.loaded = loadedTrack{id: id, path: t.Path}
	if a.reload.id != id {
		a.reload = reloadArm{}
	}
	return nil
}

// advance plays the first entry from i on, stepping by dir, whose track is
// still indexed. When there is none, playback stops and the position stays.
func (a *App) advance(ctx context.Context, i, dir int) error {
	for j := i; j >= 0 && j < len(a.queue); j += dir {
		if len(a.lib.Get(a.queue[j])) == 1 {
			return a.loadAt(ctx, j, 0)
		}
	}
	if a.loadedID() == 0 {
		return nil
	}
	return a.stop(ctx)
}

func (a *App) stop(ctx context.Context) error {
	a.smu.Lock()
	a.loaded = loadedTrack{}
	a.reload = reloadArm{}
	a.smu.Unlock()
	return a.player.Stop(ctx)
}

// removeAt drops entry i and keeps the position on the same track, or on
// the entry after a removed current one.
func (a *App) removeAt(i int) {
	a.queue = slices.Delete(a.queue, i, i+1)
	switch {
	case i < a.pos:
		a.pos--
	case a.pos >= len(a.queue):
		a.pos = len(a.queue) - 1
	}
}

// stopIfPlaying stops playback if any of ids is loaded.
func (a *App) stopIfPlaying(ctx context.Context, ids ...model.TrackID) error {
	if id := a.loadedID(); id != 0 && slices.Contains(ids, id) {
		return a.stop(ctx)
	}
	return nil
}

// ---- player events ----

// handle acts on one player event and forwards it to the bus.
func (a *App) handle(ctx context.Context, e events.Event) {
	switch e := e.(type) {
	case events.PlayerState:
		a.onState(e)
	case events.TrackEnded:
		a.onEnded(ctx, e)
	case events.LoadFailed:
		a.onFailed(ctx, e)
	case events.PlayerDown:
		a.smu.Lock()
		a.loaded, a.last, a.reload = loadedTrack{}, events.PlayerState{}, reloadArm{}
		a.smu.Unlock()
		a.bus.Publish(e)
	default:
		a.bus.Publish(e)
	}
}

func (a *App) onState(s events.PlayerState) {
	a.smu.Lock()
	if !s.Idle && a.loaded.id != 0 {
		s.TrackID = a.loaded.id
	}
	a.last = s
	a.smu.Unlock()
	if s.TrackID != 0 && s.Duration > 0 && a.needsDuration(s.TrackID) {
		if _, err := a.lib.SetDuration(s.TrackID, s.Duration); err != nil && !errors.Is(err, library.ErrUnknownTrack) {
			a.report("store duration", err)
		}
	}
	a.bus.Publish(s)
}

// needsDuration reports whether track id has no duration yet. It is a
// read-locked check, so the write lock is taken only when there is work.
func (a *App) needsDuration(id model.TrackID) bool {
	ts := a.lib.Get(id)
	return len(ts) == 1 && ts[0].Duration == 0
}

func (a *App) onEnded(ctx context.Context, e events.TrackEnded) {
	a.pmu.Lock()
	a.smu.Lock()
	current := a.loaded.id != 0 && a.loaded.path == e.Path
	if current {
		e.TrackID = a.loaded.id
		a.loaded = loadedTrack{}
	}
	a.smu.Unlock()
	if current {
		if err := a.advance(ctx, a.pos+1, +1); err != nil {
			a.report("play next", err)
		}
	}
	a.pmu.Unlock()
	a.bus.Publish(e)
}

func (a *App) onFailed(ctx context.Context, e events.LoadFailed) {
	a.pmu.Lock()
	defer a.pmu.Unlock() // runs after the Publish deferred below
	a.smu.Lock()
	current := a.loaded.id != 0 && a.loaded.path == e.Path
	var arm reloadArm
	if current {
		e.TrackID = a.loaded.id
		if a.reload.id == a.loaded.id && a.now().Before(a.reload.until) {
			arm = a.reload
		}
		a.reload = reloadArm{}
	}
	a.smu.Unlock()
	defer a.bus.Publish(e)
	if !current {
		return
	}
	if arm.id != 0 {
		// The file was just rewritten by a tag edit; load it again where it was.
		if err := a.loadAt(ctx, a.pos, arm.pos); err == nil {
			return
		}
	}
	a.report("play", fmt.Errorf("could not play %s: %s", e.Path, e.Reason))
	if err := a.stop(ctx); err != nil {
		a.report("stop", err)
	}
}
