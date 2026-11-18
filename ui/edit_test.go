package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/tags"
)

var ctrlU = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}

// lastWrite returns the fake's only write, failing unless there is exactly one.
func lastWrite(t *testing.T, e *env) tags.FakeWrite {
	t.Helper()
	ws := e.tags.Writes()
	if len(ws) != 1 {
		t.Fatalf("%d writes, want 1: %+v", len(ws), ws)
	}
	return ws[0]
}

func TestEditTitle(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("j")) // Bravo
	want := m.tracks[1]

	feed(t, m, key("t"))
	if m.edit == nil || m.edit.input.Value() != "Two" {
		t.Fatalf("t: edit %+v", m.edit)
	}
	if !strings.Contains(m.View().Content, "title: Two") {
		t.Error("status line lacks the prompt")
	}
	feed(t, m, ctrlU)
	typeText(t, m, "Dos q") // q and space are text here
	if !strings.Contains(m.View().Content, "title: Dos q") {
		t.Error("status line lacks the typed value")
	}
	res, ok := find[WriteTagsResult](feed(t, m, enterKey))
	if !ok || res.Err != nil || m.edit != nil {
		t.Fatalf("enter: %+v, %v, edit %v", res, ok, m.edit)
	}
	w := lastWrite(t, e)
	if len(w.Paths) != 1 || w.Paths[0] != want.Path {
		t.Fatalf("wrote %v, want %s", w.Paths, want.Path)
	}
	if len(w.Changes) != 1 || w.Changes[0] != (model.FrameChange{Name: "Title", Value: "Dos q"}) {
		t.Fatalf("changes %+v", w.Changes)
	}
	feed(t, m, res)
	reload(t, m) // what the TracksChanged event triggers
	if got := m.tracks[m.cursor].Summary.Title; got != "Dos q" || m.tracks[m.cursor].ID != want.ID {
		t.Fatalf("row shows %q", got)
	}
}

func TestEditCancelAndUnchanged(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("j"))
	feed(t, m, key("j")) // Charlie, comment "late\nnight"

	feed(t, m, key("a"))
	typeText(t, m, "xyz")
	if msgs := feed(t, m, key("esc")); len(msgs) != 0 || m.edit != nil {
		t.Fatalf("esc: msgs %v, edit %v", msgs, m.edit)
	}

	feed(t, m, key("c"))
	if v := m.edit.input.Value(); v != "late night" {
		t.Fatalf("comment prefill %q", v)
	}
	if msgs := feed(t, m, enterKey); len(msgs) != 0 {
		t.Fatalf("unchanged enter ran %v", msgs)
	}

	// Backspace past the start closes the prompt, as in search.
	feed(t, m, key("a"))
	feed(t, m, ctrlU)
	feed(t, m, backspaceKey)
	if m.edit != nil {
		t.Fatal("backspace on empty did not close")
	}
	if n := len(e.tags.Writes()); n != 0 {
		t.Fatalf("%d writes", n)
	}
}

func TestEditClearRemovesComment(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("j"))
	feed(t, m, key("j"))
	feed(t, m, key("c"))
	feed(t, m, ctrlU)
	feed(t, m, enterKey)
	w := lastWrite(t, e)
	if w.Changes[0] != (model.FrameChange{Name: "Comment", Value: ""}) {
		t.Fatalf("changes %+v", w.Changes)
	}
}

func TestEditPaste(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("c"))
	feed(t, m, tea.PasteMsg{Content: "warm\nup"})
	feed(t, m, enterKey)
	if w := lastWrite(t, e); w.Changes[0].Value != "warm up" {
		t.Fatalf("pasted %q", w.Changes[0].Value)
	}
}

func TestEditGPrefix(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("j"))
	feed(t, m, key("j"))

	feed(t, m, key("g"))
	feed(t, m, key("j"))
	if m.cursor != 2 || m.gPending {
		t.Fatalf("gj: cursor %d, pending %t", m.cursor, m.gPending)
	}

	feed(t, m, key("g"))
	feed(t, m, key("e"))
	if m.edit == nil || m.edit.field != editGenre || m.edit.input.Value() != "House" {
		t.Fatalf("ge: edit %+v", m.edit)
	}
	feed(t, m, ctrlU)
	typeText(t, m, "Garage")
	feed(t, m, enterKey)
	if w := lastWrite(t, e); w.Changes[0] != (model.FrameChange{Name: "Genre", Value: "Garage"}) {
		t.Fatalf("changes %+v", w.Changes)
	}

	feed(t, m, key("g"))
	if _, ok := find[playTick](feed(t, m, key("g"))); !ok || m.cursor != 0 {
		t.Fatalf("gg: cursor %d, tick %t", m.cursor, ok)
	}
}

func TestEditFailure(t *testing.T) {
	e, m := searchLibrary(t)
	e.tags.Fail(tags.OpWrite, "", errors.New("disk full"))
	feed(t, m, key("t"))
	typeText(t, m, "x")
	res, _ := find[WriteTagsResult](feed(t, m, enterKey))
	feed(t, m, res)
	if !strings.Contains(m.statusLine(), "edit:") || !strings.Contains(m.statusLine(), "disk full") {
		t.Fatalf("status %q", m.status)
	}
}

func TestEditLongValueKeepsCursor(t *testing.T) {
	_, m := searchLibrary(t)
	feed(t, m, key("t"))
	typeText(t, m, strings.Repeat("x", 200)+"END")
	if line := m.statusLine(); !strings.Contains(line, "END") {
		t.Fatalf("tail not visible: %q", line)
	}
}

func TestEditReadlineKeys(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("t")) // "One"
	ctrl := func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
	feed(t, m, ctrl('a'))
	feed(t, m, ctrl('d')) // "ne"
	typeText(t, m, "Gone Do")
	feed(t, m, ctrl('w')) // "Gone ne"
	feed(t, m, ctrl('e'))
	typeText(t, m, "!")
	if v := m.edit.input.Value(); v != "Gone ne!" {
		t.Fatalf("value %q", v)
	}
	feed(t, m, enterKey)
	if w := lastWrite(t, e); w.Changes[0].Value != "Gone ne!" {
		t.Fatalf("wrote %q", w.Changes[0].Value)
	}
}

func TestEditCommentShowsTags(t *testing.T) {
	_, m := searchLibrary(t)
	m.tagsPath = filepath.Join(t.TempDir(), "TAGS.md")
	if err := os.WriteFile(m.tagsPath, []byte("# Mood\n\nBright, Dreamy\n\n# Groove\n\nGroovy, Swung\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	feed(t, m, key("t"))
	if strings.Contains(m.View().Content, "Dreamy") {
		t.Error("title edit shows the tags")
	}
	feed(t, m, key("esc"))

	feed(t, m, key("c"))
	v := m.View().Content
	for _, want := range []string{"Mood", "Bright, Dreamy", "Groove", "Groovy, Swung", "comment: "} {
		if !strings.Contains(v, want) {
			t.Errorf("comment edit lacks %q", want)
		}
	}
	feed(t, m, key("esc"))
	if strings.Contains(m.View().Content, "Dreamy") {
		t.Error("tags stay after esc")
	}

	// No file: the default vocabulary, or no panel without one.
	m.tagsPath = filepath.Join(t.TempDir(), "missing.md")
	defer func(d string) { DefaultTags = d }(DefaultTags)
	DefaultTags = "# Default\nPlain\n"
	feed(t, m, key("c"))
	if !strings.Contains(m.View().Content, "Plain") {
		t.Error("missing file: default tags not shown")
	}
	feed(t, m, key("esc"))
	DefaultTags = ""
	feed(t, m, key("c"))
	if m.edit == nil || m.edit.vocab != "" {
		t.Fatalf("no file, no default: edit %+v", m.edit)
	}
}

func TestEditInitialKey(t *testing.T) {
	e, m := searchLibrary(t)
	feed(t, m, key("j"))
	feed(t, m, key("j"))
	feed(t, m, key("j")) // Delta, in Am

	feed(t, m, key("e"))
	if m.edit == nil || m.edit.field != editInitialKey || m.edit.input.Value() != "Am" {
		t.Fatalf("e: edit %+v", m.edit)
	}
	if !strings.Contains(m.View().Content, "key: Am") {
		t.Error("status line lacks the prompt")
	}
	feed(t, m, ctrlU)
	typeText(t, m, "8A")
	feed(t, m, enterKey)
	if w := lastWrite(t, e); w.Changes[0] != (model.FrameChange{Name: "Initial Key", Value: "8A"}) {
		t.Fatalf("changes %+v", w.Changes)
	}
}

var tabKey = tea.KeyPressMsg{Code: tea.KeyTab}

func TestEditGenreSuggestsLibraryGenres(t *testing.T) {
	_, m := searchLibrary(t)
	feed(t, m, key("g"))
	feed(t, m, key("e"))
	feed(t, m, ctrlU)
	typeText(t, m, "Acid, Deep")
	feed(t, m, enterKey)
	reload(t, m)

	feed(t, m, key("j")) // Bravo, Techno
	feed(t, m, key("g"))
	feed(t, m, key("e"))
	typeText(t, m, ", de")
	if got := m.edit.input.CurrentSuggestion(); got != "Techno, Deep" {
		t.Errorf("suggestion %q, want the part of a comma-separated genre", got)
	}
	feed(t, m, tabKey)
	if v := m.edit.input.Value(); v != "Techno, deep" {
		t.Errorf("tab: %q", v)
	}
	feed(t, m, ctrlU)
	typeText(t, m, "h")
	if got := m.edit.input.CurrentSuggestion(); got != "House" {
		t.Errorf("suggestion %q, want House", got)
	}
}

func TestEditCommentSuggestsTagWords(t *testing.T) {
	_, m := searchLibrary(t)
	m.tagsPath = filepath.Join(t.TempDir(), "TAGS.md")
	if err := os.WriteFile(m.tagsPath, []byte("**Mood** Bright, Dreamy\n\n**Groove** Groovy, Swung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	feed(t, m, key("c"))
	typeText(t, m, "Dre")
	feed(t, m, tabKey)
	typeText(t, m, " gr")
	if got := m.edit.input.MatchedSuggestions(); len(got) != 2 || got[0] != "Dreamy Groove" || got[1] != "Dreamy Groovy" {
		t.Errorf("matched %q", got)
	}
	feed(t, m, tabKey)
	if v := m.edit.input.Value(); v != "Dreamy groove" {
		t.Errorf("tab: %q", v)
	}
}

func TestEditKeySuggestsAnalysis(t *testing.T) {
	_, m := searchLibrary(t)
	id := m.tracks[0].ID
	feed(t, m, AnalyzeResult{ID: id, Result: analysis.Result{Job: analysis.SKey, Keys: []analysis.KeyGuess{{Key: "F# minor"}}}})
	feed(t, m, AnalyzeResult{ID: id, Result: analysis.Result{Job: analysis.Beatport, Keys: []analysis.KeyGuess{{Key: "F# Minor"}, {Key: "A Major"}}}})
	if got := m.keyGuesses[id]; len(got) != 4 { // F# minor, 11A, A Major, 11B
		t.Fatalf("guesses %q", got)
	}
	feed(t, m, key("e"))
	typeText(t, m, "a")
	if got := m.edit.input.CurrentSuggestion(); got != "A Major" {
		t.Errorf("suggestion %q", got)
	}
	feed(t, m, ctrlU)
	typeText(t, m, "11")
	if got := m.edit.input.MatchedSuggestions(); len(got) != 2 || got[0] != "11A" || got[1] != "11B" {
		t.Errorf("Camelot matches %q", got)
	}
	feed(t, m, key("esc"))

	feed(t, m, key("j")) // never analyzed
	feed(t, m, key("e"))
	if m.edit.input.ShowSuggestions {
		t.Error("suggestions on a track with no analysis")
	}
}
