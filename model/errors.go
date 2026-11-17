package model

import (
	"fmt"
	"strings"
)

// The errors below are value types; match them with errors.As:
//
//	var c model.ErrCollision
//	if errors.As(err, &c) { ... c.Existing ... }

// ErrCollision: promote found a master file with the same name.
type ErrCollision struct {
	Name     string  // the clashing file name
	Existing TrackID // the master track; 0 if it is not indexed yet
}

func (e ErrCollision) Error() string {
	if e.Existing == 0 {
		return fmt.Sprintf("%q already exists in master", e.Name)
	}
	return fmt.Sprintf("%q already exists in master (track %d)", e.Name, e.Existing)
}

// ErrMissingTags: a required tag is unset.
type ErrMissingTags struct {
	Missing []string
}

func (e ErrMissingTags) Error() string {
	return "missing required tags: " + strings.Join(e.Missing, ", ")
}

// ErrPlaylistsLost: delete removed the Music entry, then failed to delete the
// file. The reconciler will re-add the file without its playlist memberships.
type ErrPlaylistsLost struct {
	Path string
	Err  error
}

func (e ErrPlaylistsLost) Error() string {
	return fmt.Sprintf("removed %s from Music but could not delete the file (%v); it will be re-added without its playlists", e.Path, e.Err)
}

func (e ErrPlaylistsLost) Unwrap() error { return e.Err }

// ErrBackupKept: overwrite replaced the master file, but the old file could
// not be trashed or removed. It is kept at Path and never deleted
// automatically.
type ErrBackupKept struct {
	Path string
	Err  error
}

func (e ErrBackupKept) Error() string {
	return fmt.Sprintf("overwrite done, but the replaced file could not be deleted (%v); it is kept at %s", e.Err, e.Path)
}

func (e ErrBackupKept) Unwrap() error { return e.Err }

// ErrMusicUnavailable: Music is not running and autolaunch is off.
type ErrMusicUnavailable struct{}

func (ErrMusicUnavailable) Error() string { return "Apple Music is not running" }

// ErrAutomationDenied: macOS denied the automation permission (error -1743).
type ErrAutomationDenied struct{}

func (ErrAutomationDenied) Error() string {
	return "not allowed to control Music: allow this terminal in System Settings > Privacy & Security > Automation"
}

// ErrCopySettingOn: Music copied an added file into its Media folder.
type ErrCopySettingOn struct {
	Path     string // the master file
	Location string // where Music put its copy
}

func (e ErrCopySettingOn) Error() string {
	return fmt.Sprintf("Music copied %s to %s: turn off \"Copy files to Media folder when adding to library\" in Music > Settings > Files", e.Path, e.Location)
}

// ErrVerifyFailed: a Music change did not read back as expected.
type ErrVerifyFailed struct {
	What string
}

func (e ErrVerifyFailed) Error() string { return "Music did not confirm the change: " + e.What }

// ErrNotEditable: the playlist is smart or a folder.
type ErrNotEditable struct {
	PlaylistID string
}

func (e ErrNotEditable) Error() string {
	return fmt.Sprintf("playlist %s cannot be edited", e.PlaylistID)
}

// ErrNotInMusic: the track has no Music entry.
type ErrNotInMusic struct {
	ID TrackID
}

func (e ErrNotInMusic) Error() string { return fmt.Sprintf("track %d is not in Music", e.ID) }

// ErrKid3Missing: kid3-cli was not found, so full tag reads and writes are off.
type ErrKid3Missing struct{}

func (ErrKid3Missing) Error() string { return "kid3-cli not found: tag editing is unavailable" }
