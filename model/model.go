// Package model holds the types shared by every package: tracks, tag
// summaries, playlists and the typed errors the UI matches with errors.As.
// It imports nothing from this module.
package model

import (
	"strconv"
	"time"
)

// TrackID identifies a track for the lifetime of the cache. It never changes
// when the track's path does. Zero means "no track".
type TrackID int64

// Role says which kind of root a track's path sits under.
type Role uint8

const (
	Inbox Role = iota
	Master
)

func (r Role) String() string {
	switch r {
	case Inbox:
		return "inbox"
	case Master:
		return "master"
	}
	return "role(" + strconv.Itoa(int(r)) + ")"
}

// Track is passed by value everywhere. Nothing outside the library holds a
// pointer into the index.
type Track struct {
	ID       TrackID
	Path     string // absolute, normalized; changes on promote
	Role     Role   // derived from the root Path sits under
	Size     int64
	ModTime  time.Time
	Summary  TagSummary
	Duration time.Duration // 0 = not known yet
	MusicID  string        // Music persistent ID; "" = not in the library
}

// Playlist is a Music user playlist. It is addressed by persistent ID, never
// by name.
type Playlist struct {
	ID       string
	Name     string
	Editable bool // false for smart playlists and folders
}

// MusicState is the availability of Apple Music as the app sees it.
type MusicState uint8

const (
	MusicAvailable MusicState = iota
	MusicUnavailable
	MusicDenied
	MusicCopySettingOn
	MusicDisabled
)

func (s MusicState) String() string {
	switch s {
	case MusicAvailable:
		return "available"
	case MusicUnavailable:
		return "unavailable"
	case MusicDenied:
		return "denied"
	case MusicCopySettingOn:
		return "copy setting on"
	case MusicDisabled:
		return "disabled"
	}
	return "music_state(" + strconv.Itoa(int(s)) + ")"
}
