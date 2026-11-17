package ui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// While a comment is being edited, the tag vocabulary from TAGS.md sits in
// a box just above the status line, so the words to type are on screen.

// tagSection is one "# Heading" of TAGS.md and the text under it.
type tagSection struct {
	name string
	tags string
}

// DefaultTags is the vocabulary used when the user has no TAGS.md of their
// own. The root package sets it to the repo's TAGS.md, embedded.
var DefaultTags string

// loadTags reads path, fresh on every comment edit so changes to the file
// show without a restart. Without a readable file it falls back to
// DefaultTags.
func loadTags(path string) []tagSection {
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return parseTags(string(data))
		}
	}
	return parseTags(DefaultTags)
}

// parseTags splits markdown into sections at each heading. Lines under a
// heading are joined with spaces; text before the first heading is a
// section with no name.
func parseTags(s string) []tagSection {
	var out []tagSection
	var cur *tagSection
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			out = append(out, tagSection{name: strings.TrimSpace(strings.TrimLeft(line, "#"))})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			out = append(out, tagSection{})
			cur = &out[len(out)-1]
		}
		if cur.tags != "" {
			cur.tags += " "
		}
		cur.tags += line
	}
	return out
}

var tagNameStyle = lipgloss.NewStyle().Bold(true)

// tagsPanel draws the sections as a box screen width wide: one line per
// section, the heading in a column of its own and the tags wrapped beside
// it.
func tagsPanel(secs []tagSection, width int) string {
	inner := max(width-dialogChrome, 10)
	nameW := 0
	for _, s := range secs {
		nameW = max(nameW, lipgloss.Width(s.name))
	}
	gap := 0
	if nameW > 0 {
		gap = 2
	}
	tagW := max(inner-nameW-gap, 10)
	var lines []string
	for _, s := range secs {
		wrapped := strings.Split(ansi.Wordwrap(s.tags, tagW, ""), "\n")
		for i, w := range wrapped {
			name := ""
			if i == 0 {
				name = s.name
			}
			head := tagNameStyle.Render(name) + strings.Repeat(" ", nameW-lipgloss.Width(name)+gap)
			lines = append(lines, fitLine(head+strings.TrimSpace(w), inner))
		}
	}
	return dialogStyle.Width(width).Render(strings.Join(lines, "\n"))
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
