package ui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/model"
)

// searchPrompt is the open / prompt: what has been typed so far and the row
// the cursor was on when it opened, where the search starts and esc returns.
type searchPrompt struct {
	query  string
	origin int
}

// matches reports whether pat is a substring of one of t's searched fields:
// title (or file name), artist, genre, key as displayed, and comment. Like
// vim's smartcase, the match ignores case unless pat has an uppercase letter.
func matches(t model.Track, pat string) bool {
	if pat == "" {
		return false
	}
	s := t.Summary
	title := s.Title
	if title == "" {
		title = baseName(t.Path)
	}
	key := s.Key
	if c, ok := model.ParseCamelot(key); ok {
		key = c.String()
	}
	fold := !strings.ContainsFunc(pat, unicode.IsUpper)
	if fold {
		pat = strings.ToLower(pat)
	}
	for _, f := range []string{title, s.Artist, s.Genre, key, oneLine(s.Comment)} {
		if fold {
			f = strings.ToLower(f)
		}
		if strings.Contains(f, pat) {
			return true
		}
	}
	return false
}

// findMatch returns the first row matching pat, scanning from row from
// forward or backward and wrapping around the ends. wrapped is set when the
// match was found only after wrapping.
func (m *Library) findMatch(pat string, from int, forward bool) (row int, wrapped, ok bool) {
	n := len(m.tracks)
	if n == 0 || pat == "" {
		return 0, false, false
	}
	step := 1
	if !forward {
		step = -1
	}
	// from may be one past either end (n from the last row, N from the
	// first); reaching it already counts as wrapping.
	for k := range n {
		i := from + k*step
		if i >= n || i < 0 {
			wrapped = true
		}
		i = ((i % n) + n) % n
		if matches(m.tracks[i], pat) {
			return i, wrapped, true
		}
	}
	return 0, false, false
}

// jumpTo puts the cursor on row without scheduling playback.
func (m *Library) jumpTo(row int) {
	prev := m.table.Cursor()
	m.table.SetCursor(row)
	m.cursorMoved(prev)
}

// openSearch opens the / prompt at the cursor.
func (m *Library) openSearch() {
	m.search = &searchPrompt{origin: m.table.Cursor()}
}

// searchKey handles a key while the / prompt is open. Typing moves the
// cursor to the first match from where the prompt opened (incsearch); enter
// accepts and plays it, esc puts the cursor back.
func (m *Library) searchKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.search
	switch msg.String() {
	case "esc":
		m.search = nil
		m.jumpTo(s.origin)
		return nil
	case "enter":
		m.search = nil
		pat := s.query
		if pat == "" {
			pat = m.lastSearch
		}
		if pat == "" {
			m.jumpTo(s.origin)
			m.status = "No previous search pattern"
			return nil
		}
		m.lastSearch = pat
		row, _, ok := m.findMatch(pat, s.origin, true)
		if !ok {
			m.jumpTo(s.origin)
			m.status = "Pattern not found: " + pat
			return nil
		}
		m.status = ""
		m.jumpTo(row)
		return m.selectionMoved()
	case "backspace":
		if s.query == "" {
			m.search = nil
			m.jumpTo(s.origin)
			return nil
		}
		r := []rune(s.query)
		s.query = string(r[:len(r)-1])
	default:
		if msg.Text == "" {
			return nil
		}
		s.query += msg.Text
	}
	row, _, ok := m.findMatch(s.query, s.origin, true)
	if !ok {
		row = s.origin
	}
	m.jumpTo(row)
	return nil
}

// searchNext moves to the next match of the last pattern, forward for n and
// backward for N, and plays it.
func (m *Library) searchNext(forward bool) tea.Cmd {
	if m.lastSearch == "" {
		m.status = "No previous search pattern"
		return nil
	}
	from := m.table.Cursor() + 1
	if !forward {
		from = m.table.Cursor() - 1
	}
	row, wrapped, ok := m.findMatch(m.lastSearch, from, forward)
	if !ok {
		m.status = "Pattern not found: " + m.lastSearch
		return nil
	}
	switch {
	case wrapped && forward:
		m.status = "search hit BOTTOM, continuing at TOP"
	case wrapped:
		m.status = "search hit TOP, continuing at BOTTOM"
	default:
		m.status = ""
	}
	m.jumpTo(row)
	return m.selectionMoved()
}
