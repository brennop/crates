package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/pathnorm"
)

// importThreshold is the most adds one reconcile pass makes without asking.
// More trips the safety valve: ImportPending, then ConfirmImport or
// CancelImport.
const importThreshold = 100

// Start starts the reconciler. It waits for the library's first master
// sweep, reads the first Music snapshot and reconciles; after that it runs
// after every snapshot, every master sweep and every promote. With Music
// disabled it only publishes MusicStatus Disabled. Close stops it.
func (a *App) Start() error {
	a.recMu.Lock()
	if a.recStarted {
		a.recMu.Unlock()
		return errors.New("app: already started")
	}
	a.recStarted = true
	a.recMu.Unlock()
	if !a.musicOn {
		a.setMusicState(model.MusicDisabled)
		close(a.recDone)
		return nil
	}
	go a.reconcileLoop(a.ctx)
	return nil
}

func (a *App) reconcileLoop(ctx context.Context) {
	defer close(a.recDone)
	at, swept := a.lib.MasterSwept()
	if at.IsZero() {
		select {
		case <-swept:
		case <-ctx.Done():
			return
		}
	}
	if err := a.refreshSnapshot(ctx); err != nil && !isHold(err) {
		a.report("read Music library", err)
	}
	a.reconcile(ctx)
	for {
		_, swept = a.lib.MasterSwept()
		select {
		case <-ctx.Done():
			return
		case <-a.kick:
		case <-swept:
		}
		a.reconcile(ctx)
	}
}

// kickReconciler asks for a pass. It never blocks; kicks while a pass is
// queued fold into it.
func (a *App) kickReconciler() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// handToReconciler queues a master track for adding to Music on the next
// pass, without waiting for a fresh snapshot.
func (a *App) handToReconciler(id model.TrackID) {
	if !a.musicOn {
		return
	}
	a.recMu.Lock()
	a.promoted = append(a.promoted, id)
	a.recMu.Unlock()
	a.kickReconciler()
}

// ConfirmImport lets the reconciler make the adds the safety valve held,
// and runs a pass. It does nothing when no import is pending.
func (a *App) ConfirmImport() {
	a.recMu.Lock()
	if a.pending == 0 {
		a.recMu.Unlock()
		return
	}
	a.pending, a.confirmed = 0, true
	a.recMu.Unlock()
	a.kickReconciler()
}

// CancelImport drops the pending import. The next pass on a fresh snapshot
// (focus, refresh) finds the tracks still missing and asks again.
func (a *App) CancelImport() {
	a.recMu.Lock()
	a.pending = 0
	a.recMu.Unlock()
}

// PendingImport returns the count of the import waiting for an answer, or
// 0.
func (a *App) PendingImport() int {
	a.recMu.Lock()
	defer a.recMu.Unlock()
	return a.pending
}

// DeadEntries returns the Music entries inside master whose file is
// missing, as of the last pass. They are never deleted automatically.
func (a *App) DeadEntries() []events.DeadEntry {
	a.recMu.Lock()
	defer a.recMu.Unlock()
	return slices.Clone(a.dead)
}

// isHold reports an error that means "try again later" for every add, not
// just this one.
func isHold(err error) bool {
	return errors.As(err, new(model.ErrMusicUnavailable)) ||
		errors.As(err, new(model.ErrAutomationDenied)) ||
		errors.As(err, new(model.ErrCopySettingOn)) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, music.ErrClosed) || errors.Is(err, music.ErrDisabled)
}

// reconcile runs one pass. Promoted tracks are added first. Then, if the
// snapshot is newer than the last master sweep, master tracks take their
// MusicIDs from it, tracks it lacks are added (through the safety valve),
// and entries inside master with no file are reported as dead. Adds stop at
// the first error that holds them all (Music unavailable, denied, the copy
// setting on), and wait for the next pass.
func (a *App) reconcile(ctx context.Context) {
	if !a.musicOn {
		return
	}
	a.passMu.Lock()
	defer a.passMu.Unlock()

	a.recMu.Lock()
	promoted := a.promoted
	a.promoted = nil
	a.recMu.Unlock()
	for i, id := range promoted {
		if a.addOne(ctx, id) {
			a.recMu.Lock()
			a.promoted = append(promoted[i:], a.promoted...)
			a.recMu.Unlock()
			return
		}
	}

	toAdd, ok := a.match()
	if !ok {
		return
	}
	a.recMu.Lock()
	if len(toAdd) > importThreshold && !a.confirmed {
		a.pending = len(toAdd)
		a.recMu.Unlock()
		a.bus.Publish(events.ImportPending{Count: len(toAdd)})
		return
	}
	a.pending = 0
	a.recMu.Unlock()
	for _, id := range toAdd {
		if a.addOne(ctx, id) {
			return
		}
	}
	a.recMu.Lock()
	a.confirmed = false
	a.recMu.Unlock()
}

// match compares master with the snapshot. It attaches or clears MusicIDs,
// records dead entries, and returns the master tracks with no entry. ok is
// false when the snapshot is not newer than the last master sweep.
func (a *App) match() (toAdd []model.TrackID, ok bool) {
	a.opMu.Lock()
	defer a.opMu.Unlock()

	swept, _ := a.lib.MasterSwept()
	master := a.lib.MasterDir()
	cs := a.lib.MasterCaseSensitive()
	masterKey := pathnorm.Key(master, cs)

	type entry struct{ id, path string }
	entries := map[string]entry{}
	a.mmu.Lock()
	taken := a.snap.Taken
	if taken.IsZero() || !taken.After(swept) {
		a.mmu.Unlock()
		return nil, false
	}
	for p, id := range a.snap.Tracks {
		if pathnorm.Key(filepath.Dir(p), cs) == masterKey {
			entries[pathnorm.Key(p, cs)] = entry{id, p}
		}
	}
	a.mmu.Unlock()

	for _, t := range a.lib.MasterTracks() {
		k := pathnorm.Key(t.Path, cs)
		e, found := entries[k]
		delete(entries, k)
		if found {
			if t.MusicID != e.id {
				if err := a.lib.SetMusicID(t.ID, e.id); err != nil {
					a.report("set Music ID", err)
				}
			}
			continue
		}
		if t.MusicID != "" {
			// The entry is gone from Music (deleted there); re-add the file.
			if err := a.lib.SetMusicID(t.ID, ""); err != nil {
				a.report("clear Music ID", err)
			}
		}
		toAdd = append(toAdd, t.ID)
	}
	sort.Slice(toAdd, func(i, j int) bool { return toAdd[i] < toAdd[j] })

	var dead []events.DeadEntry
	for _, e := range entries {
		if _, err := os.Lstat(e.path); errors.Is(err, fs.ErrNotExist) {
			dead = append(dead, events.DeadEntry{MusicID: e.id, Path: e.path})
		}
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i].Path < dead[j].Path })
	a.recMu.Lock()
	old := a.dead
	a.dead = dead
	a.recMu.Unlock()
	for _, d := range dead {
		if !slices.Contains(old, d) {
			a.bus.Publish(d)
		}
	}
	return toAdd, true
}

// addOne adds master track id to Music and stores its MusicID. It skips a
// track that is gone, no longer in master or already in Music. hold is true
// when the error holds every add, so the pass must stop.
func (a *App) addOne(ctx context.Context, id model.TrackID) (hold bool) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.recMu.Lock()
	latched := a.latched
	a.recMu.Unlock()
	if latched {
		return true
	}
	if ctx.Err() != nil {
		return true
	}
	ts := a.lib.Get(id)
	if len(ts) == 0 || ts[0].Role != model.Master || ts[0].MusicID != "" {
		return false
	}
	t := ts[0]
	mid, err := a.music.Add(ctx, t.Path)
	if err != nil {
		var cs model.ErrCopySettingOn
		if errors.As(err, &cs) {
			// Reported once: the latch stops every later add.
			a.report("add to Music", err)
		} else if !isHold(err) {
			a.report("add to Music", err)
		}
		return isHold(err)
	}
	if err := a.lib.SetMusicID(id, mid); err != nil {
		a.report("set Music ID", err)
	}
	a.mmu.Lock()
	if a.snap.Tracks == nil {
		a.snap.Tracks = map[string]string{}
	}
	a.snap.Tracks[t.Path] = mid
	a.mmu.Unlock()
	return false
}
