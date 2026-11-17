package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// fileWith writes a master track tagged s and indexes it.
func (e *env) fileWith(name string, s model.TagSummary) model.Track {
	e.t.Helper()
	p := pathnorm.Child(e.master, name)
	if err := os.WriteFile(p, []byte("audio of "+name), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.tags.PutSummary(p, time.Second, s)
	fi, err := os.Stat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	tr, err := e.lib.Upsert(model.Track{Path: p, Size: fi.Size(), ModTime: fi.ModTime(), Summary: s, Duration: time.Second})
	if err != nil {
		e.t.Fatal(err)
	}
	return tr
}

func typeText(t *testing.T, m *Library, s string) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	for _, r := range s {
		out = append(out, feed(t, m, key(string(r)))...)
	}
	return out
}

var (
	enterKey     = tea.KeyPressMsg{Code: tea.KeyEnter}
	backspaceKey = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

// searchLibrary has four tracks sorted by artist: Alpha, Bravo, Charlie, Delta.
func searchLibrary(t *testing.T) (*env, *Library) {
	t.Helper()
	e := newEnv(t)
	e.fileWith("a.mp3", model.TagSummary{Title: "One", Artist: "Alpha", Genre: "House"})
	e.fileWith("b.mp3", model.TagSummary{Title: "Two", Artist: "Bravo", Genre: "Techno"})
	e.fileWith("c.mp3", model.TagSummary{Title: "Three", Artist: "Charlie", Genre: "House", Comment: "late\nnight"})
	e.fileWith("d.mp3", model.TagSummary{Title: "Four", Artist: "Delta", Genre: "Techno", Key: "Am"})
	m := libraryWith(t, e)
	if len(m.tracks) != 4 || m.tracks[0].Summary.Artist != "Alpha" {
		t.Fatalf("fixture: %d tracks, first %q", len(m.tracks), m.tracks[0].Summary.Artist)
	}
	return e, m
}

func TestMatches(t *testing.T) {
	tr := model.Track{Path: "/m/file name.mp3", Summary: model.TagSummary{
		Artist: "Moodymann", Genre: "Deep House", Key: "Am", Comment: "warm\nup",
	}}
	for _, c := range []struct {
		pat  string
		want bool
	}{
		{"moody", true},
		{"MOODY", false}, // smartcase: uppercase makes it exact
		{"Moody", true},
		{"deep h", true},
		{"file name", true}, // title falls back to the file name
		{"8a", true},        // key as displayed in Camelot
		{"Am", false},
		{"warm up", true}, // comment folded onto one line
		{"techno", false},
		{"", false},
	} {
		if got := matches(tr, c.pat); got != c.want {
			t.Errorf("matches(%q) = %t, want %t", c.pat, got, c.want)
		}
	}
}

func TestSearchIncremental(t *testing.T) {
	_, m := searchLibrary(t)

	feed(t, m, key("/"))
	if m.search == nil {
		t.Fatal("/ did not open the prompt")
	}
	msgs := typeText(t, m, "char")
	if _, ok := find[playTick](msgs); ok {
		t.Fatal("typing scheduled playback")
	}
	if m.table.Cursor() != 2 {
		t.Fatalf("cursor %d, want 2 (Charlie)", m.table.Cursor())
	}
	if !strings.Contains(m.View().Content, "/char█") {
		t.Error("status line lacks the prompt")
	}

	tick, ok := find[playTick](feed(t, m, enterKey))
	if !ok || tick.id != m.tracks[2].ID {
		t.Fatalf("enter: tick %+v, %v", tick, ok)
	}
	if m.search != nil || m.lastSearch != "char" || m.table.Cursor() != 2 {
		t.Fatalf("after enter: search %v, last %q, cursor %d", m.search, m.lastSearch, m.table.Cursor())
	}
}

func TestSearchEscRestores(t *testing.T) {
	_, m := searchLibrary(t)
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})

	feed(t, m, key("/"))
	typeText(t, m, "delta")
	if m.table.Cursor() != 3 {
		t.Fatalf("cursor %d, want 3", m.table.Cursor())
	}
	if msgs := feed(t, m, key("esc")); len(msgs) != 0 {
		t.Fatalf("esc ran %v", msgs)
	}
	if m.search != nil || m.table.Cursor() != 1 || m.lastSearch != "" {
		t.Fatalf("after esc: search %v, cursor %d, last %q", m.search, m.table.Cursor(), m.lastSearch)
	}

	// Backspace on an empty prompt closes it too.
	feed(t, m, key("/"))
	typeText(t, m, "d")
	feed(t, m, backspaceKey)
	feed(t, m, backspaceKey)
	if m.search != nil || m.table.Cursor() != 1 {
		t.Fatalf("after backspaces: search %v, cursor %d", m.search, m.table.Cursor())
	}
}

func TestSearchNextWraps(t *testing.T) {
	_, m := searchLibrary(t)

	if feed(t, m, key("n")); m.status != "No previous search pattern" {
		t.Fatalf("n without pattern: status %q", m.status)
	}

	feed(t, m, key("/"))
	typeText(t, m, "techno")
	feed(t, m, enterKey)
	if m.table.Cursor() != 1 || m.status != "" {
		t.Fatalf("cursor %d, status %q", m.table.Cursor(), m.status)
	}

	tick, ok := find[playTick](feed(t, m, key("n")))
	if !ok || m.table.Cursor() != 3 || m.status != "" {
		t.Fatalf("n: cursor %d, status %q, tick %v", m.table.Cursor(), m.status, ok)
	}
	if tick.id != m.tracks[3].ID {
		t.Fatalf("n played %d", tick.id)
	}
	feed(t, m, key("n"))
	if m.table.Cursor() != 1 || m.status != "search hit BOTTOM, continuing at TOP" {
		t.Fatalf("n wrap: cursor %d, status %q", m.table.Cursor(), m.status)
	}
	feed(t, m, key("N"))
	if m.table.Cursor() != 3 || m.status != "search hit TOP, continuing at BOTTOM" {
		t.Fatalf("N wrap: cursor %d, status %q", m.table.Cursor(), m.status)
	}
	feed(t, m, key("N"))
	if m.table.Cursor() != 1 || m.status != "" {
		t.Fatalf("N: cursor %d, status %q", m.table.Cursor(), m.status)
	}

	// enter on an empty prompt reuses the last pattern.
	feed(t, m, key("/"))
	feed(t, m, enterKey)
	if m.table.Cursor() != 1 || m.lastSearch != "techno" {
		t.Fatalf("empty enter: cursor %d, last %q", m.table.Cursor(), m.lastSearch)
	}

	// The pattern survives a re-sort.
	feed(t, m, key("s"))
	reload(t, m)
	feed(t, m, key("n"))
	if g := m.tracks[m.table.Cursor()].Summary.Genre; g != "Techno" {
		t.Fatalf("n after sort landed on %q", g)
	}
}

func TestSearchPromptTakesKeys(t *testing.T) {
	_, m := searchLibrary(t)
	feed(t, m, key("/"))
	for _, k := range []string{"q", "s", "x", "p", "n"} {
		for _, msg := range feed(t, m, key(k)) {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatalf("%s quit", k)
			}
		}
	}
	if m.search == nil || m.search.query != "qsxpn" || m.confirm != nil || m.dialog != nil || m.sortBy != defaultSort {
		t.Fatalf("prompt %+v, confirm %v, dialog %v, sort %d", m.search, m.confirm, m.dialog, m.sortBy)
	}
}

func TestSearchNotFound(t *testing.T) {
	_, m := searchLibrary(t)
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	feed(t, m, key("/"))
	typeText(t, m, "zzz")
	if m.table.Cursor() != 1 {
		t.Fatalf("cursor %d, want 1", m.table.Cursor())
	}
	if msgs := feed(t, m, enterKey); len(msgs) != 0 {
		t.Fatalf("enter ran %v", msgs)
	}
	if m.table.Cursor() != 1 || m.status != "Pattern not found: zzz" {
		t.Fatalf("cursor %d, status %q", m.table.Cursor(), m.status)
	}
	if !strings.Contains(m.View().Content, "Pattern not found: zzz") {
		t.Error("view lacks the not-found status")
	}
}
