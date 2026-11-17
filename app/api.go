package app

import (
	"context"

	"github.com/brennop/crates/fsops"
	"github.com/brennop/crates/library"
	"github.com/brennop/crates/model"
)

// The app surface the UI calls, frozen in Phase 8:
//
//	Library:   Query, Tracks, Refresh, FocusGained
//	Files:     Promote, Overwrite, Delete
//	Tags:      FullTags, WriteTags
//	Playlists: Playlists, Memberships, AddToPlaylist, RemoveFromPlaylist
//	Playback:  Play, Enqueue, Next, Previous, RemoveFromQueue, ClearQueue,
//	           Queue, TogglePause, Seek, SetVolume
//	Music:     MusicStatus, LaunchMusic, PendingImport, ConfirmImport,
//	           CancelImport, DeadEntries
//	Other:     Capabilities, Close
//
// ui wraps each one in a tea.Cmd. docs/api.md describes them.

// Filter and Sort are the library's query types, so the UI needs only app.
type (
	Filter = library.Filter
	Sort   = library.Sort
)

// Query returns the IDs of the tracks matching f, ordered by s. It reads
// the in-memory index; ctx only stops it from starting late.
func (a *App) Query(ctx context.Context, f Filter, s Sort) ([]model.TrackID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.lib.Query(f, s)
}

// Tracks returns copies of the tracks with ids, in the order given, for
// the rows on screen. IDs no longer indexed are left out.
func (a *App) Tracks(ids []model.TrackID) []model.Track {
	return a.lib.Get(ids...)
}

// Capabilities is what this machine and config allow, for the UI to hide
// or explain features that cannot work.
type Capabilities struct {
	Kid3         bool              // kid3-cli found: FullTags and WriteTags work
	TrashMethod  fsops.TrashMethod // how deleting to the Trash is done
	DeleteMode   fsops.Mode        // delete_mode: trash or remove
	MusicEnabled bool              // [music] enabled
}

// Capabilities reports what is available. It does not change while the
// app runs.
func (a *App) Capabilities() Capabilities {
	return Capabilities{
		Kid3:         a.tags.Available(),
		TrashMethod:  fsops.Method(),
		DeleteMode:   a.mode,
		MusicEnabled: a.musicOn,
	}
}
