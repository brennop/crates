package ui

import (
	"os"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// While a comment is being edited, the tag vocabulary from TAGS.md sits in
// a box just above the status line, rendered as markdown, so the words to
// type are on screen.

// DefaultTags is the vocabulary used when the user has no TAGS.md of their
// own. The root package sets it to the repo's TAGS.md, embedded.
var DefaultTags string

// loadTags reads path, fresh on every comment edit so changes to the file
// show without a restart. Without a readable file it falls back to
// DefaultTags.
func loadTags(path string) string {
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return DefaultTags
}

// tagsPanel renders the vocabulary in a box screen width wide. It is
// cached by width, since View runs on every key.
func (e *editPrompt) tagsPanel(width int) string {
	if e.vocabW != width || e.vocabBox == "" {
		e.vocabBox, e.vocabW = renderTags(e.vocab, width), width
	}
	return e.vocabBox
}

func renderTags(md string, width int) string {
	inner := max(width-dialogChrome, 10)
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(inner))
	if err != nil {
		return ""
	}
	out, err := r.Render(md)
	if err != nil {
		return ""
	}
	return dialogStyle.Width(width).Render(strings.Trim(out, "\n"))
}

// overlayBottom draws box over base, flush left and resting on the status
// line, the last of height lines.
func overlayBottom(base, box string, height int) string {
	y := max(height-1-lipgloss.Height(box), 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(0).Y(y).Z(1),
	).Render()
}
