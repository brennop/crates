package ui

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/model"
)

// searchPrompt is the open / prompt: the line being typed, and the cursor
// row and view it opened on. The search starts from origin, and esc puts
// both back.
type searchPrompt struct {
	input     textinput.Model
	origin    int
	originTop int
}

// query is the pattern typed so far.
func (s *searchPrompt) query() string { return s.input.Value() }

// moveFrom moves the cursor to row, scrolling from the view the prompt
// opened on. As in vim, every keystroke places the match against that view,
// not against wherever the last keystroke left it.
func (m *Library) moveFrom(s *searchPrompt, row int) {
	m.top = s.originTop
	m.setCursor(row)
}

// restore puts the cursor and the view back where the prompt opened.
func (m *Library) restore(s *searchPrompt) {
	m.moveFrom(s, s.origin)
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

// openSearch opens the / prompt at the cursor.
func (m *Library) openSearch() {
	m.search = &searchPrompt{input: newPromptInput("/", "", m.width), origin: m.cursor, originTop: m.top}
}

// searchKey handles a key while the / prompt is open. Editing the line
// moves the cursor to the first match from where the prompt opened
// (incsearch); enter accepts and plays it, esc puts the cursor back.
func (m *Library) searchKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.search
	switch msg.String() {
	case "esc":
		m.search = nil
		m.restore(s)
		return nil
	case "enter":
		m.search = nil
		pat := s.query()
		if pat == "" {
			pat = m.lastSearch
		}
		if pat == "" {
			m.restore(s)
			m.status = "No previous search pattern"
			return nil
		}
		m.lastSearch = pat
		row, _, ok := m.findMatch(pat, s.origin, true)
		if !ok {
			m.restore(s)
			m.status = "Pattern not found: " + pat
			return nil
		}
		m.status = ""
		m.moveFrom(s, row)
		return m.selectionMoved()
	case "backspace":
		if s.query() == "" {
			m.search = nil
			m.restore(s)
			return nil
		}
	}
	return m.searchUpdate(msg)
}

// searchUpdate passes msg to the input, then moves the cursor to the first
// match of the new pattern, or back to where the prompt opened.
func (m *Library) searchUpdate(msg tea.Msg) tea.Cmd {
	s := m.search
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if row, _, ok := m.findMatch(s.query(), s.origin, true); ok {
		m.moveFrom(s, row)
	} else {
		m.restore(s)
	}
	return cmd
}

// searchNext moves to the next match of the last pattern, forward for n and
// backward for N, and plays it.
func (m *Library) searchNext(forward bool) tea.Cmd {
	if m.lastSearch == "" {
		m.status = "No previous search pattern"
		return nil
	}
	from := m.cursor + 1
	if !forward {
		from = m.cursor - 1
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
	m.setCursor(row)
	return m.selectionMoved()
}
