package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

// ErrUnknownPlaylist: no playlist with that ID in the snapshot.
var ErrUnknownPlaylist = errors.New("app: unknown playlist")

// Playlists returns Music's user playlists as of the last snapshot.
func (a *App) Playlists() []model.Playlist {
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return slices.Clone(a.snap.Playlists)
}

// Memberships returns the playlists that hold track id. A track not in
// Music is in none.
func (a *App) Memberships(id model.TrackID) ([]model.Playlist, error) {
	t, err := a.track(id)
	if err != nil || t.MusicID == "" {
		return nil, err
	}
	a.mmu.Lock()
	defer a.mmu.Unlock()
	return a.snap.Memberships(t.MusicID), nil
}

// AddToPlaylist adds tracks to a playlist, skipping ones already in it.
func (a *App) AddToPlaylist(ctx context.Context, ids []model.TrackID, playlistID string) error {
	return a.editPlaylist(ctx, ids, playlistID, true)
}

// RemoveFromPlaylist removes tracks from a playlist.
func (a *App) RemoveFromPlaylist(ctx context.Context, ids []model.TrackID, playlistID string) error {
	return a.editPlaylist(ctx, ids, playlistID, false)
}

// editPlaylist checks that every track is in Music and the playlist is
// editable, writes, then patches the snapshot with the membership Music
// read back. A failed write refreshes the snapshot instead.
func (a *App) editPlaylist(ctx context.Context, ids []model.TrackID, playlistID string, add bool) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()

	a.mmu.Lock()
	p, ok := a.snap.Playlist(playlistID)
	members := slices.Clone(a.snap.Members[playlistID])
	a.mmu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownPlaylist, playlistID)
	}
	if !p.Editable {
		return model.ErrNotEditable{PlaylistID: playlistID}
	}

	var musicIDs []string
	for _, id := range ids {
		t, err := a.track(id)
		if err != nil {
			return err
		}
		if t.MusicID == "" {
			return model.ErrNotInMusic{ID: id}
		}
		if slices.Contains(musicIDs, t.MusicID) {
			continue
		}
		// Music adds a second copy of a track already in the playlist.
		if add && slices.Contains(members, t.MusicID) {
			continue
		}
		musicIDs = append(musicIDs, t.MusicID)
	}
	if len(musicIDs) == 0 {
		return nil
	}

	var got []string
	var err error
	if add {
		got, err = a.music.AddToPlaylist(ctx, playlistID, musicIDs)
	} else {
		got, err = a.music.RemoveFromPlaylist(ctx, playlistID, musicIDs)
	}
	if err != nil {
		a.refreshAfterFailure(ctx)
		return err
	}
	a.mmu.Lock()
	a.snap.SetMembers(playlistID, got)
	a.mmu.Unlock()
	a.bus.Publish(events.PlaylistsChanged{})
	return nil
}

// refreshSnapshot reads a new Music snapshot, publishes PlaylistsChanged
// and asks the reconciler for a pass.
func (a *App) refreshSnapshot(ctx context.Context) error {
	s, err := a.music.Snapshot(ctx)
	if err != nil {
		return err
	}
	a.mmu.Lock()
	a.snap = s
	a.mmu.Unlock()
	a.bus.Publish(events.PlaylistsChanged{})
	a.kickReconciler()
	return nil
}

// refreshAfterFailure re-reads the snapshot after a failed Music write, so
// it shows what Music really holds. A failure here is reported, since the
// caller is already returning the write's error.
func (a *App) refreshAfterFailure(ctx context.Context) {
	if err := a.refreshSnapshot(ctx); err != nil {
		a.report("read Music library", err)
	}
}

// FocusGained sweeps the master dir and refreshes the Music snapshot. The UI
// calls it when the terminal regains focus.
func (a *App) FocusGained(ctx context.Context) error { return a.Refresh(ctx) }

// Refresh sweeps the master dir, then refreshes the Music snapshot, and
// returns when both are done. The snapshot comes second so that it is newer
// than the sweep, which the reconciler needs; the reconciler then runs in
// the background.
func (a *App) Refresh(ctx context.Context) error {
	serr := a.lib.SweepMaster(ctx)
	if !a.musicOn {
		return serr
	}
	return errors.Join(serr, a.refreshSnapshot(ctx))
}
