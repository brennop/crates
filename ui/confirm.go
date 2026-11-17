package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	switch k.String() {
	case "y", "enter":
		return true, true
	case "n", "esc", "q", "x":
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
	const chrome = 4 // border and padding
	inner := max(lipgloss.Width(title), lipgloss.Width(body))
	inner = min(max(inner, 32), max(width-chrome-4, 10), 60)
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	lines := []string{
		dialogTitleStyle.Render(fit(title)),
		"",
		lipgloss.NewStyle().Width(inner).Render(body),
		"",
		dialogHintStyle.Render(fit("y delete · n cancel")),
	}
	return dialogStyle.Render(strings.Join(lines, "\n"))
}

// trackName is "artist – title", falling back to the file name for a
// missing title.
func trackName(t model.Track) string {
	title := t.Summary.Title
	if title == "" {
		title = baseName(t.Path)
	}
	if t.Summary.Artist != "" {
		return t.Summary.Artist + " – " + title
	}
	return title
}
