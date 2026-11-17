package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

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
// writes nothing.
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
	in := newPromptInput(f.label+": ", v, m.width)
	m.edit = &editPrompt{id: t.ID, field: f, input: in, orig: v}
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
