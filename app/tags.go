package app

import (
	"context"
	"errors"
	"os"

	"github.com/brennop/crates/model"
)

// FullTags reads every frame of a track's ID3v2 tag through kid3-cli. An
// ID3v1-only file has none, though its summary shows ID3v1 fields.
func (a *App) FullTags(ctx context.Context, id model.TrackID) ([]model.Frame, error) {
	t, err := a.track(id)
	if err != nil {
		return nil, err
	}
	return a.tags.Full(ctx, t.Path)
}

// WriteTags applies the same changes to every track, then re-reads each
// summary into the index and asks Music to refresh tracks it holds. Music
// refresh failures are reported on the bus; the write itself stands.
//
// If the playing track is among ids and the player then fails to read it,
// it is reloaded at the position it had, instead of stopping.
func (a *App) WriteTags(ctx context.Context, ids []model.TrackID, changes []model.FrameChange) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	ts := a.lib.Get(ids...)
	if len(ts) != len(ids) {
		return ErrUnknownTrack
	}
	paths := make([]string, len(ts))
	for i, t := range ts {
		paths[i] = t.Path
	}
	a.armReload(ids)
	werr := a.tags.Write(ctx, paths, changes)

	// Re-read even after a failed write: kid3 may have changed some files.
	var errs []error
	for _, t := range ts {
		s, d, err := a.tags.Summary(t.Path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if fi, err := os.Stat(t.Path); err == nil {
			t.Size, t.ModTime = fi.Size(), fi.ModTime()
		}
		t.Summary = s
		if d != 0 {
			t.Duration = d
		}
		if _, err := a.lib.Upsert(t); err != nil {
			errs = append(errs, err)
		}
	}
	if werr != nil {
		return errors.Join(append([]error{werr}, errs...)...)
	}
	for _, t := range ts {
		if t.MusicID == "" {
			continue
		}
		if err := a.music.Refresh(ctx, t.MusicID); err != nil {
			a.report("refresh in Music", err)
		}
	}
	return errors.Join(errs...)
}

// armReload marks the playing track, if it is among ids, for one reload on
// a load failure.
func (a *App) armReload(ids []model.TrackID) {
	a.smu.Lock()
	defer a.smu.Unlock()
	for _, id := range ids {
		if id != 0 && id == a.loaded.id {
			a.reload = reloadArm{id: id, pos: a.last.Position, until: a.now().Add(reloadWindow)}
			return
		}
	}
}
