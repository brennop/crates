package ui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The / search and the tag edits are prompts in the status line, each a
// textinput, which brings the readline keys (ctrl+a/e/b/f/d/w/k/u,
// alt+b/f/d, ctrl+v) and keeps the cursor on screen on a long line.

// newPromptInput returns a focused input showing prompt, holding value with
// the cursor at its end, sized for a status line width wide.
func newPromptInput(prompt, value string, width int) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	st := textinput.DefaultDarkStyles()
	st.Focused.Prompt = lipgloss.NewStyle()
	st.Focused.Text = lipgloss.NewStyle()
	st.Cursor.Blink = false // a blinking cursor needs tick messages routed back to it
	in.SetStyles(st)
	in.SetValue(value)
	in.Focus()
	sizePromptInput(&in, width)
	return in
}

// sizePromptInput fits in to a status line width wide: one space of padding
// on each side, the prompt, and a cell for the cursor past the end.
func sizePromptInput(in *textinput.Model, width int) {
	in.SetWidth(max(width-2-lipgloss.Width(in.Prompt)-1, 1))
}

// promptView renders in as the status line.
func promptView(in textinput.Model, width int) string {
	return lipgloss.NewStyle().Padding(0, 1).Width(width).MaxWidth(width).Render(in.View())
}

// promptUpdate passes a non-key message, such as a paste, to the open
// prompt, if any.
func (m *Library) promptUpdate(msg tea.Msg) tea.Cmd {
	switch {
	case m.search != nil:
		return m.searchUpdate(msg)
	case m.edit != nil:
		return m.editUpdate(msg)
	case m.token != nil:
		return m.tokenUpdate(msg)
	}
	return nil
}

// sizePrompts refits the open prompt after a resize.
func (m *Library) sizePrompts() {
	if m.search != nil {
		sizePromptInput(&m.search.input, m.width)
	}
	if m.edit != nil {
		sizePromptInput(&m.edit.input, m.width)
	}
	if m.token != nil {
		sizePromptInput(&m.token.input, m.width)
	}
}
