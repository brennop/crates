package ui

import (
	"regexp"
	"strings"

	binding "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/model"
)

// editField is a tag the status line can edit: the label its prompt shows
// and the kid3 frame it writes.
type editField struct {
	label string
	frame string
}

var (
	editTitle      = editField{"title", "Title"}
	editArtist     = editField{"artist", "Artist"}
	editGenre      = editField{"genre", "Genre"}
	editComment    = editField{"comment", "Comment"}
	editInitialKey = editField{"key", "Initial Key"}
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
	vocab string // TAGS.md, for a comment; "" otherwise

	// words complete the part of the value after the last of seps; see
	// suggest.
	words []string
	seps  string

	vocabBox string // vocab rendered, vocabW wide
	vocabW   int
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
	case editInitialKey:
		v = s.Key
	}
	in := newPromptInput(f.label+": ", v, m.width)
	m.edit = &editPrompt{id: t.ID, field: f, input: in, orig: v}
	switch f {
	case editComment:
		m.edit.vocab = loadTags(m.tagsPath)
		m.edit.words, m.edit.seps = tagWords(m.edit.vocab), ", "
	case editGenre:
		m.edit.words, m.edit.seps = m.libraryGenres(), ","
	case editInitialKey:
		m.edit.words = m.keyGuesses[t.ID]
	}
	if len(m.edit.words) > 0 {
		m.edit.input.ShowSuggestions = true
		m.edit.suggest()
	}
}

// wordRE is what counts as a word of TAGS.md: a run of letters, digits,
// apostrophes and hyphens, starting with a letter.
var wordRE = regexp.MustCompile(`\pL[\pL\pN'-]*`)

// tagWords returns the words of vocab, each once, in the order they first
// appear. It reads no markdown structure, so headings count too.
func tagWords(vocab string) []string {
	return uniq(wordRE.FindAllString(vocab, -1))
}

// libraryGenres returns every genre in the library, master and inbox, with
// a comma-separated genre split into its parts.
func (m *Library) libraryGenres() []string {
	ids, err := m.app.Query(m.ctx, app.Filter{}, app.Sort{})
	if err != nil {
		return nil
	}
	var genres []string
	for _, t := range m.app.Tracks(ids) {
		for _, g := range strings.Split(t.Summary.Genre, ",") {
			if g = strings.TrimSpace(g); g != "" {
				genres = append(genres, g)
			}
		}
	}
	return uniq(genres)
}

// uniq drops repeats from ss, ignoring case, keeping the first of each.
func uniq(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		k := strings.ToLower(s)
		if !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}

// suggest gives the input its suggestions for the value as it stands. The
// textinput completes the whole value by prefix, so each suggestion is the
// value up to and including its last separator (and the spaces after it)
// followed by one of the words: "House, De" offers "House, Deep".
func (e *editPrompt) suggest() {
	if len(e.words) == 0 {
		return
	}
	v := e.input.Value()
	head := ""
	if i := strings.LastIndexAny(v, e.seps); i >= 0 {
		rest := v[i+1:]
		head = v[:len(v)-len(strings.TrimLeft(rest, " "))]
	}
	sugs := make([]string, len(e.words))
	for i, w := range e.words {
		sugs[i] = head + w
	}
	e.input.SetSuggestions(sugs)
}

// editKey handles a key while an edit prompt is open: enter writes the
// value, esc drops it, and the rest edit the line. An empty value removes
// the tag.
func (m *Library) editKey(msg tea.KeyPressMsg) tea.Cmd {
	e := m.edit
	switch {
	case binding.Matches(msg, promptKeys.Cancel):
		m.edit = nil
		return nil
	case binding.Matches(msg, promptKeys.Accept):
		m.edit = nil
		v := e.input.Value()
		if v == e.orig {
			return nil // keep a multi-line comment as it is
		}
		change := model.FrameChange{Name: e.field.frame, Value: v}
		return WriteTags(m.ctx, m.app, []model.TrackID{e.id}, []model.FrameChange{change})
	case binding.Matches(msg, promptKeys.Erase):
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
	m.edit.suggest()
	return cmd
}
