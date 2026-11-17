package ui

import (
	"path/filepath"
	"strings"

	binding "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/model"
)

// The playlist dialog and the delete confirm are boxes drawn over the
// table: a bold title, a body and a hint line, in a rounded border.

const (
	dialogChrome   = 4 // border and padding
	dialogMinWidth = 36
	dialogMaxWidth = 60
)

var (
	dialogStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	dialogTitleStyle = lipgloss.NewStyle().Bold(true)
	dialogErrStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dialogHintStyle  = lipgloss.NewStyle().Faint(true)
)

// dialogWidth is the inner width of a box whose widest line is content
// wide, on a screen width wide.
func dialogWidth(content, width int) int {
	return min(max(content, dialogMinWidth), max(width-dialogChrome-4, 10), dialogMaxWidth)
}

// dialogBox draws a box inner wide: title, a blank line, body, a blank
// line and the hints for keys. The title is truncated to fit; body lines
// must already fit (see fitLine).
func dialogBox(inner int, title string, body []string, keys ...binding.Binding) string {
	lines := []string{dialogTitleStyle.Render(fitLine(title, inner)), ""}
	lines = append(lines, body...)
	lines = append(lines, "", dialogHintStyle.Render(hints(inner, keys...)))
	return dialogStyle.Render(strings.Join(lines, "\n"))
}

// fitLine truncates s to w cells with "…".
func fitLine(s string, w int) string {
	return ansi.Truncate(s, w, "…")
}

// overlay draws box centered over base.
func overlay(base, box string, width, height int) string {
	x := max((width-lipgloss.Width(box))/2, 0)
	y := max((height-lipgloss.Height(box))/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}

// trackName is "artist – title", falling back to the file name for a
// missing title.
func trackName(t model.Track) string {
	title := t.Summary.Title
	if title == "" {
		title = filepath.Base(t.Path)
	}
	if t.Summary.Artist != "" {
		return t.Summary.Artist + " – " + title
	}
	return title
}
