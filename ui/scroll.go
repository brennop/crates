package ui

import (
	binding "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
)

// scrolloff is how many rows the view keeps above and below the cursor,
// like vim's 'scrolloff'. On a short table it shrinks so the cursor can
// still reach every row.
const scrolloff = 5

// The Library owns the cursor and the scroll position: the table is handed
// only the rows on screen, with its cursor on the selected one. The table's
// own scrolling cannot keep a margin around the cursor or center it.

// pageHeight is how many rows fit on screen.
func (m *Library) pageHeight() int {
	return max(m.table.Height(), 1)
}

// margin is the scrolloff that fits the current height.
func (m *Library) margin() int {
	return min(scrolloff, (m.pageHeight()-1)/2)
}

// setCursor moves the cursor to row, scrolls it into view keeping the
// margin, and redraws. It does not schedule playback.
func (m *Library) setCursor(row int) {
	m.cursor = max(min(row, len(m.tracks)-1), 0)
	m.scroll()
	m.refreshRows()
}

// scroll moves the view the least it takes to show the cursor with the
// margin around it. A cursor that lands more than half a screen past the
// edge is centered instead, as vim does after a long jump (a search, G, a
// re-sort).
func (m *Library) scroll() {
	h, so := m.pageHeight(), m.margin()
	switch {
	case m.cursor < m.top-h/2 || m.cursor > m.top+h-1+h/2:
		m.top = m.cursor - h/2
	case m.cursor < m.top+so:
		m.top = m.cursor - so
	case m.cursor > m.top+h-1-so:
		m.top = m.cursor - h + 1 + so
	}
	m.clampTop()
}

// center scrolls the cursor row to the middle of the screen (zz).
func (m *Library) center() {
	m.top = m.cursor - m.pageHeight()/2
	m.clampTop()
	m.refreshRows()
}

// clampTop keeps the view on the rows: no gap above the first row or below
// the last.
func (m *Library) clampTop() {
	m.top = max(min(m.top, len(m.tracks)-m.pageHeight()), 0)
}

// moveKey handles the table's movement keys and reports whether msg was one.
// Page and half-page moves scroll the view and the cursor together, like
// vim's ctrl+f and ctrl+d.
func (m *Library) moveKey(msg tea.KeyPressMsg) bool {
	km, h := m.table.KeyMap, m.pageHeight()
	by := func(n int) {
		m.top += n
		m.clampTop()
		m.setCursor(m.cursor + n)
	}
	switch {
	case binding.Matches(msg, km.LineUp):
		m.setCursor(m.cursor - 1)
	case binding.Matches(msg, km.LineDown):
		m.setCursor(m.cursor + 1)
	case binding.Matches(msg, km.PageUp):
		by(-h)
	case binding.Matches(msg, km.PageDown):
		by(h)
	case binding.Matches(msg, km.HalfPageUp):
		by(-h / 2)
	case binding.Matches(msg, km.HalfPageDown):
		by(h / 2)
	case binding.Matches(msg, km.GotoTop):
		m.setCursor(0)
	case binding.Matches(msg, km.GotoBottom):
		m.setCursor(len(m.tracks) - 1)
	default:
		return false
	}
	return true
}

// refreshRows renders the rows on screen and puts the table's cursor on the
// selected one.
func (m *Library) refreshRows() {
	end := min(m.top+m.pageHeight(), len(m.tracks))
	rows := make([]table.Row, 0, max(end-m.top, 0))
	for i := m.top; i < end; i++ {
		rows = append(rows, m.row(m.tracks[i], i == m.cursor))
	}
	m.table.SetRows(rows)
	m.table.SetCursor(m.cursor - m.top)
}
