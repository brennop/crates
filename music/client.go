package music

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/brennop/crates/model"
)

// Client is Apple Music, reached one call at a time. OSA (darwin) runs the
// embedded scripts; Disabled stands in when Music is off; Fake stands in
// for it in app tests.
//
// Errors are model typed errors where one fits: ErrMusicUnavailable when
// Music is not running and autolaunch is off, ErrAutomationDenied for
// -1743, ErrCopySettingOn when an add lands outside master, ErrVerifyFailed
// when a write does not read back as expected, ErrNotEditable for a smart
// playlist or a folder.
type Client interface {
	// Status reports whether Music can be used, without launching it.
	Status(ctx context.Context) (model.MusicState, error)
	// Launch starts Music.
	Launch(ctx context.Context) error
	// Snapshot reads every file track's location and every user playlist
	// with its members.
	Snapshot(ctx context.Context) (Snapshot, error)
	// Add adds the file at path, which must be normalized, and returns its
	// persistent ID. Adding a file already in the library returns the
	// existing ID. If Music put its own copy somewhere else, it returns
	// ErrCopySettingOn and refuses further adds.
	Add(ctx context.Context, path string) (string, error)
	// AddToPlaylist adds tracks by persistent ID, skipping ones already in
	// the playlist, and returns the whole membership read back after the
	// write. A membership other than the expected one is ErrVerifyFailed.
	AddToPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error)
	// RemoveFromPlaylist removes every occurrence of the tracks and returns
	// the membership read back, verified as for AddToPlaylist.
	RemoveFromPlaylist(ctx context.Context, playlistID string, musicIDs []string) ([]string, error)
	// DeleteTrack removes the library entry, never the file, and confirms
	// it is gone.
	DeleteTrack(ctx context.Context, musicID string) error
	// Refresh makes Music re-read the file behind an entry.
	Refresh(ctx context.Context, musicID string) error
}

// Snapshot is Music's library as last read. app keeps one and patches it
// after its own successful writes. Treat a Snapshot as a value: Clone it
// before changing a shared one.
type Snapshot struct {
	Taken     time.Time
	Tracks    map[string]string   // normalized path -> MusicID; tracks with no location are left out
	Playlists []model.Playlist    // user playlists, in Music's order
	Members   map[string][]string // playlist ID -> MusicIDs, in playlist order
}

// Clone returns a deep copy.
func (s Snapshot) Clone() Snapshot {
	c := Snapshot{
		Taken:     s.Taken,
		Tracks:    make(map[string]string, len(s.Tracks)),
		Playlists: slices.Clone(s.Playlists),
		Members:   make(map[string][]string, len(s.Members)),
	}
	maps.Copy(c.Tracks, s.Tracks)
	for k, v := range s.Members {
		c.Members[k] = slices.Clone(v)
	}
	return c
}

// Playlist returns the playlist with persistent ID id.
func (s Snapshot) Playlist(id string) (model.Playlist, bool) {
	for _, p := range s.Playlists {
		if p.ID == id {
			return p, true
		}
	}
	return model.Playlist{}, false
}

// Memberships returns the playlists that hold musicID, in Music's order.
func (s Snapshot) Memberships(musicID string) []model.Playlist {
	var out []model.Playlist
	for _, p := range s.Playlists {
		if slices.Contains(s.Members[p.ID], musicID) {
			out = append(out, p)
		}
	}
	return out
}

// SetMembers replaces a playlist's membership, after a verified write.
func (s *Snapshot) SetMembers(playlistID string, musicIDs []string) {
	if s.Members == nil {
		s.Members = map[string][]string{}
	}
	s.Members[playlistID] = slices.Clone(musicIDs)
}

// RemoveTrack drops a deleted entry from the tracks and every playlist.
func (s *Snapshot) RemoveTrack(musicID string) {
	for p, id := range s.Tracks {
		if id == musicID {
			delete(s.Tracks, p)
		}
	}
	for pid, ids := range s.Members {
		s.Members[pid] = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == musicID })
	}
}
