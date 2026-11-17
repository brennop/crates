package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// ErrNotInbox: promote or overwrite got a track that is already in master.
var ErrNotInbox = errors.New("app: track is not in an inbox")

// Collision is an inbox track whose name is taken in master.
type Collision struct {
	Inbox    model.TrackID
	Existing model.TrackID // 0 if the master file is not indexed yet
	Name     string
}

// Failure is a track an operation could not handle.
type Failure struct {
	ID  model.TrackID
	Err error
}

// PromoteSummary is what a batch promote did with each track.
type PromoteSummary struct {
	Promoted   []model.TrackID
	Collisions []Collision
	Failures   []Failure
}

// DeleteSummary is what a batch delete did with each track.
type DeleteSummary struct {
	Deleted  []model.TrackID
	Failures []Failure
}

// Promote moves inbox tracks into master, one at a time, in order. A track
// that fails does not stop the rest. Each promoted track keeps its ID, and
// so its place in the queue.
func (a *App) Promote(ctx context.Context, ids []model.TrackID) PromoteSummary {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	var sum PromoteSummary
	for _, id := range ids {
		err := ctx.Err()
		if err == nil {
			err = a.promote(id)
		}
		var c model.ErrCollision
		switch {
		case err == nil:
			sum.Promoted = append(sum.Promoted, id)
		case errors.As(err, &c):
			sum.Collisions = append(sum.Collisions, Collision{Inbox: id, Existing: c.Existing, Name: c.Name})
		default:
			sum.Failures = append(sum.Failures, Failure{ID: id, Err: err})
		}
	}
	return sum
}

// promote runs the promote steps for one track. Callers hold opMu.
func (a *App) promote(id model.TrackID) error {
	t, err := a.promotable(id)
	if err != nil {
		return err
	}
	var missed bool
	err = a.lib.Exclusive(func() error {
		var err error
		missed, err = a.promoteLocked(t)
		return err
	})
	if missed {
		go a.sweepMaster()
	}
	return err
}

// promotable returns track id if it is an inbox track with every required
// tag.
func (a *App) promotable(id model.TrackID) (model.Track, error) {
	t, err := a.track(id)
	if err != nil {
		return t, err
	}
	if t.Role != model.Inbox {
		return t, fmt.Errorf("%w: %d", ErrNotInbox, id)
	}
	if missing := t.Summary.Missing(a.required); len(missing) > 0 {
		return t, model.ErrMissingTags{Missing: missing}
	}
	return t, nil
}

// promoteLocked moves t into master under its own name and updates the
// index under the same ID. missed is true when the move found a master file
// the index does not know yet. Callers hold opMu and the library's Exclusive.
func (a *App) promoteLocked(t model.Track) (missed bool, err error) {
	name := filepath.Base(t.Path)
	if m, ok := a.lib.FindMaster(name); ok {
		return false, model.ErrCollision{Name: name, Existing: m.ID}
	}
	dst := pathnorm.Child(a.lib.MasterDir(), name)
	if err := a.fs.Move(t.Path, dst); err != nil {
		var c model.ErrCollision
		if errors.As(err, &c) {
			return true, model.ErrCollision{Name: name}
		}
		return false, err
	}
	if err := a.lib.Move(t.ID, dst); err != nil {
		return false, fmt.Errorf("moved %s into master, but the index was not updated: %w", name, err)
	}
	a.handToReconciler(t.ID)
	return false, nil
}

// handToReconciler queues a newly promoted master track for adding to
// Music. The reconciler arrives in Phase 7 (task 7.8); until then the
// master sweep that follows any snapshot picks the track up.
func (a *App) handToReconciler(model.TrackID) {}

// sweepMaster runs a master sweep in the background and reports failures.
func (a *App) sweepMaster() {
	if err := a.lib.SweepMaster(context.Background()); err != nil && !errors.Is(err, library.ErrNotStarted) && !errors.Is(err, library.ErrClosed) {
		a.report("sweep master", err)
	}
}

// Overwrite replaces the master file that inbox track inboxID collides
// with. The master track keeps its ID, its name and its Music entry, and
// takes the inbox file's content and tags; the inbox track is removed. If
// the collision is gone, it is a plain promote. An ErrBackupKept means the
// overwrite is done but the old file was kept.
func (a *App) Overwrite(ctx context.Context, inboxID model.TrackID) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	t, err := a.promotable(inboxID)
	if err != nil {
		return err
	}
	var missed bool
	var refresh string
	err = a.lib.Exclusive(func() error {
		m, ok := a.lib.FindMaster(filepath.Base(t.Path))
		if !ok {
			var err error
			missed, err = a.promoteLocked(t)
			return err
		}
		a.pmu.Lock()
		stopErr := a.stopIfPlaying(ctx, t.ID, m.ID)
		a.dropFromQueue(t.ID)
		a.pmu.Unlock()
		if stopErr != nil {
			return fmt.Errorf("stop playback: %w", stopErr)
		}

		err := a.fs.Replace(t.Path, m.Path, a.mode)
		var kept model.ErrBackupKept
		if err != nil && !errors.As(err, &kept) {
			return err
		}
		m.Size, m.ModTime, m.Summary, m.Duration = t.Size, t.ModTime, t.Summary, t.Duration
		if _, uerr := a.lib.Upsert(m); uerr != nil {
			return errors.Join(err, uerr)
		}
		if rerr := a.lib.Remove(t.ID); rerr != nil {
			return errors.Join(err, rerr)
		}
		refresh = m.MusicID
		return err
	})
	if missed {
		go a.sweepMaster()
	}
	if refresh != "" {
		if rerr := a.music.Refresh(ctx, refresh); rerr != nil {
			a.report("refresh in Music", rerr)
		}
	}
	return err
}

// dropFromQueue removes every queue entry for id. Callers hold pmu and
// have stopped playback of id.
func (a *App) dropFromQueue(id model.TrackID) {
	for i := len(a.queue) - 1; i >= 0; i-- {
		if a.queue[i] == id {
			a.removeAt(i)
		}
	}
}

// Delete deletes tracks one at a time, in order. A track that fails does
// not stop the rest.
//
// Per track: drop it from the queue (stopping it first if it plays); for a
// master track in Music, delete the Music entry, and on failure stop there;
// trash or remove the file; remove it from the index. If the file cannot be
// deleted after its Music entry was, the failure is ErrPlaylistsLost.
func (a *App) Delete(ctx context.Context, ids []model.TrackID) DeleteSummary {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	var sum DeleteSummary
	for _, id := range ids {
		err := ctx.Err()
		if err == nil {
			err = a.delete(ctx, id)
		}
		if err != nil {
			sum.Failures = append(sum.Failures, Failure{ID: id, Err: err})
		} else {
			sum.Deleted = append(sum.Deleted, id)
		}
	}
	return sum
}

func (a *App) delete(ctx context.Context, id model.TrackID) error {
	t, err := a.track(id)
	if err != nil {
		return err
	}
	a.pmu.Lock()
	stopErr := a.stopIfPlaying(ctx, id)
	a.dropFromQueue(id)
	a.pmu.Unlock()
	if stopErr != nil {
		return fmt.Errorf("stop playback: %w", stopErr)
	}

	inMusic := t.Role == model.Master && t.MusicID != ""
	if inMusic {
		if err := a.music.DeleteTrack(ctx, t.MusicID); err != nil {
			a.refreshAfterFailure(ctx)
			return err
		}
		a.mmu.Lock()
		a.snap.RemoveTrack(t.MusicID)
		a.mmu.Unlock()
		a.bus.Publish(events.PlaylistsChanged{})
		if err := a.lib.SetMusicID(id, ""); err != nil {
			a.report("clear Music ID", err)
		}
	}

	if err := a.fs.Delete(t.Path, a.mode); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if inMusic {
			return model.ErrPlaylistsLost{Path: t.Path, Err: err}
		}
		return err
	}
	return a.lib.Remove(id)
}
