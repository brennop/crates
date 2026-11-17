package ui

import (
	binding "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/brennop/crates/fsops"
	"github.com/brennop/crates/model"
)

// deleteConfirm asks before deleting one track.
type deleteConfirm struct {
	id   model.TrackID
	name string // "artist – title", for the box title
}

func newDeleteConfirm(t model.Track) *deleteConfirm {
	return &deleteConfirm{id: t.ID, name: trackName(t)}
}

// update handles a key. It reports whether the dialog should close, and
// whether the delete was confirmed. Other keys are ignored.
func (d *deleteConfirm) update(k tea.KeyPressMsg) (closed, confirmed bool) {
	switch {
	case binding.Matches(k, confirmKeys.Yes):
		return true, true
	case binding.Matches(k, confirmKeys.No):
		return true, false
	}
	return false, false
}

// view renders the box to fit a screen width wide.
func (d *deleteConfirm) view(width int, mode fsops.Mode) string {
	body := "Moves the file to the Trash and removes it from Music."
	if mode == fsops.ModeRemove {
		body = "Deletes the file for good and removes it from Music."
	}
	title := "Delete · " + d.name
	inner := dialogWidth(max(lipgloss.Width(title), lipgloss.Width(body)), width)
	return dialogBox(inner, title, []string{lipgloss.NewStyle().Width(inner).Render(body)},
		confirmKeys.Yes, confirmKeys.No)
}
