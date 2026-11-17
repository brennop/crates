package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/brennop/crates/model"
)

// editField is a tag the status line can edit: the label its prompt shows
// and the kid3 frame it writes.
type editField struct {
	label string
	frame string
}

var (
	editTitle   = editField{"title", "Title"}
	editArtist  = editField{"artist", "Artist"}
	editGenre   = editField{"genre", "Genre"}
	editComment = editField{"comment", "Comment"}
)

// editPrompt is an open tag edit in the status line. It holds the track's
// ID, not its row, so a reload while typing cannot move the edit to another
// track. orig is the value it opened with; enter on an unchanged value
// writes nothing. The line itself is a textinput, which brings the
// readline keys (ctrl+a/e/b/f/d/w/k/u, alt+b/f/d).
type editPrompt struct {
	id    model.TrackID
	field editField
	input textinput.Model
	orig  string
}

// openEdit opens a prompt for f on the selected track, prefilled with its
// current value.
func (m *Library) openEdit(f editField) {
	i := m.cursor
	if i < 0 || i >= len(m.tracks) {
		return
	}
	t := m.tracks[i]
	s := t.Summary
	var v string
	switch f {
	case editTitle:
		v = s.Title // not the file name the table falls back to
	case editArtist:
		v = s.Artist
	case editGenre:
		v = s.Genre
	case editComment:
		v = oneLine(s.Comment)
	}

	in := textinput.New()
	in.Prompt = f.label + ": "
	st := textinput.DefaultDarkStyles()
	st.Focused.Prompt = lipgloss.NewStyle()
	st.Focused.Text = lipgloss.NewStyle()
	st.Cursor.Blink = false // a blinking cursor needs tick messages routed back to it
	in.SetStyles(st)
	in.SetValue(v) // puts the cursor at the end
	in.Focus()
	m.edit = &editPrompt{id: t.ID, field: f, input: in, orig: v}
	m.sizeEdit()
}

// sizeEdit fits the input to the status line, so a long value scrolls
// sideways and keeps the cursor on screen.
func (m *Library) sizeEdit() {
	if m.edit == nil {
		return
	}
	// One space of padding on each side, the prompt, and a cell for the
	// cursor past the end.
	w := m.width - 2 - lipgloss.Width(m.edit.input.Prompt) - 1
	m.edit.input.SetWidth(max(w, 1))
}

// editKey handles a key while an edit prompt is open: enter writes the
// value, esc drops it, and the rest edit the line. An empty value removes
// the tag.
func (m *Library) editKey(msg tea.KeyPressMsg) tea.Cmd {
	e := m.edit
	switch msg.String() {
	case "esc":
		m.edit = nil
		return nil
	case "enter":
		m.edit = nil
		v := e.input.Value()
		if v == e.orig {
			return nil // keep a multi-line comment as it is
		}
		change := model.FrameChange{Name: e.field.frame, Value: v}
		return WriteTags(m.ctx, m.app, []model.TrackID{e.id}, []model.FrameChange{change})
	case "backspace":
		if e.input.Value() == "" {
			m.edit = nil
			return nil
		}
	}
	return m.editUpdate(msg)
}

// editUpdate passes msg to the input. Its one command is the ctrl+v
// clipboard read, whose result Update routes back here.
func (m *Library) editUpdate(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.edit.input, cmd = m.edit.input.Update(msg)
	return cmd
}
