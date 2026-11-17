// Package events defines the events every package publishes and the bus that
// carries them to the UI. It imports only model.
package events

import (
	"time"

	"github.com/brennop/crates/model"
)

// Event is any value published on the bus.
type Event interface{ isEvent() }

// StateEvent is latest-wins: the bus keeps one slot per kind and overwrites it
// while the consumer is behind. Every other Event is a change event, queued
// and never dropped.
type StateEvent interface {
	Event
	slot() slot
}

// Sink is what producers publish through. Publish must never block.
type Sink interface {
	Publish(Event)
}

type slot uint8

// Slots in the order Next returns them when several are filled.
const (
	slotPlayer slot = iota
	slotScan
	slotMusic
	numSlots
)

// ---- State events ----

// PlayerState folds mpv's observed properties. Throttled by the player.
type PlayerState struct {
	TrackID  model.TrackID // filled in by app; 0 when nothing is loaded
	Path     string
	Paused   bool
	Idle     bool // nothing loaded
	Position time.Duration
	Duration time.Duration
	Volume   float64 // 0–100
}

// ScanProgress reports a library scan.
type ScanProgress struct {
	Scanned int  // files looked at so far
	Total   int  // files found so far; grows while directories are read
	Done    bool // the scan finished
}

// MusicStatus is the current availability of Apple Music.
type MusicStatus struct {
	State model.MusicState
}

func (PlayerState) slot() slot  { return slotPlayer }
func (ScanProgress) slot() slot { return slotScan }
func (MusicStatus) slot() slot  { return slotMusic }

// ---- Change events ----

// LibraryLoaded: the cache is in the index; the list can be shown.
type LibraryLoaded struct {
	Count int
}

// TracksChanged carries new or updated tracks, batched during scans.
type TracksChanged struct {
	Tracks []model.Track
}

// TrackRemoved: these tracks are gone from the index.
type TrackRemoved struct {
	IDs []model.TrackID
}

// PlaylistsChanged: the playlist list or memberships changed; re-read them.
type PlaylistsChanged struct{}

// DeadEntry: a Music entry points inside master at a file that is missing.
type DeadEntry struct {
	MusicID string
	Path    string
}

// ImportPending: the reconciler wants to add Count tracks to Music and waits
// for ConfirmImport or CancelImport.
type ImportPending struct {
	Count int
}

// PlayerDown: mpv went away unexpectedly and is being restarted.
type PlayerDown struct {
	Err error
}

// TrackEnded: the loaded track played to its end.
type TrackEnded struct {
	TrackID model.TrackID // filled in by app
	Path    string
}

// LoadFailed: mpv could not play the loaded track.
type LoadFailed struct {
	TrackID model.TrackID // filled in by app
	Path    string
	Reason  string
}

// Error reports a failure in background work, where no caller gets a return
// value.
type Error struct {
	Op  string
	Err error
}

func (PlayerState) isEvent()      {}
func (ScanProgress) isEvent()     {}
func (MusicStatus) isEvent()      {}
func (LibraryLoaded) isEvent()    {}
func (TracksChanged) isEvent()    {}
func (TrackRemoved) isEvent()     {}
func (PlaylistsChanged) isEvent() {}
func (DeadEntry) isEvent()        {}
func (ImportPending) isEvent()    {}
func (PlayerDown) isEvent()       {}
func (TrackEnded) isEvent()       {}
func (LoadFailed) isEvent()       {}
func (Error) isEvent()            {}
