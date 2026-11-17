package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/model"
)

// scrollLibrary has 40 tracks, artists A00…A39 in order, on a screen with
// 10 table rows, where the scrolloff shrinks to 4.
func scrollLibrary(t *testing.T) *Library {
	t.Helper()
	e := newEnv(t)
	for i := range 40 {
		e.fileWith(fmt.Sprintf("%02d.mp3", i), model.TagSummary{
			Title: fmt.Sprintf("Track %02d", i), Artist: fmt.Sprintf("A%02d", i),
		})
	}
	m := libraryWith(t, e)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 14})
	if len(m.tracks) != 40 || m.pageHeight() != 10 {
		t.Fatalf("fixture: %d tracks, %d rows on screen", len(m.tracks), m.pageHeight())
	}
	return m
}

func press(t *testing.T, m *Library, k string, n int) {
	t.Helper()
	for range n {
		feed(t, m, key(k))
	}
}

// at checks the cursor and first row on screen, and that the screen shows
// exactly the rows from top on, with the cursor's row selected.
func at(t *testing.T, m *Library, what string, cursor, top int) {
	t.Helper()
	if m.cursor != cursor || m.top != top {
		t.Fatalf("%s: cursor %d top %d, want cursor %d top %d", what, m.cursor, m.top, cursor, top)
	}
	view := m.View().Content
	first, last := fmt.Sprintf("Track %02d", top), fmt.Sprintf("Track %02d", top+9)
	if !strings.Contains(view, first) || !strings.Contains(view, last) ||
		(top > 0 && strings.Contains(view, fmt.Sprintf("Track %02d", top-1))) ||
		(top+10 < 40 && strings.Contains(view, fmt.Sprintf("Track %02d", top+10))) {
		t.Fatalf("%s: screen does not show rows %d–%d:\n%s", what, top, top+9, view)
	}
	if got := m.table.Cursor(); got != cursor-top {
		t.Fatalf("%s: table cursor %d, want %d", what, got, cursor-top)
	}
}

func TestScrolloff(t *testing.T) {
	m := scrollLibrary(t)

	if m.margin() != 4 {
		t.Fatalf("margin %d, want 4", m.margin())
	}
	press(t, m, "j", 5)
	at(t, m, "5 down", 5, 0) // still 4 rows below the cursor
	press(t, m, "j", 1)
	at(t, m, "6 down", 6, 1)
	press(t, m, "G", 1)
	at(t, m, "G", 39, 30) // the margin gives way at the ends
	press(t, m, "k", 5)
	at(t, m, "5 up from the end", 34, 30)
	press(t, m, "k", 1)
	at(t, m, "6 up from the end", 33, 29)
	press(t, m, "g", 1)
	at(t, m, "g", 0, 0)

	// A page down moves the view and the cursor together.
	press(t, m, "j", 5)
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	at(t, m, "pgdown", 15, 10)
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	at(t, m, "pgup", 5, 0)
}

func TestSearchScrolls(t *testing.T) {
	m := scrollLibrary(t)

	// A jump far off screen centers the match.
	feed(t, m, key("/"))
	typeText(t, m, "a30")
	at(t, m, "incsearch", 30, 25)
	feed(t, m, enterKey)
	at(t, m, "enter", 30, 25)

	// A match just past the margin scrolls the least it takes.
	feed(t, m, key("/"))
	typeText(t, m, "a36")
	feed(t, m, enterKey)
	at(t, m, "near match", 36, 30)
	feed(t, m, key("/"))
	typeText(t, m, "a29")
	feed(t, m, enterKey)
	at(t, m, "near match above", 29, 25)

	// esc, and a pattern with no match, put the view back too.
	feed(t, m, key("/"))
	typeText(t, m, "a02")
	at(t, m, "incsearch up", 2, 0)
	typeText(t, m, "x")
	at(t, m, "no match", 29, 25)
	feed(t, m, backspaceKey)
	at(t, m, "backspace", 2, 0)
	feed(t, m, key("esc"))
	at(t, m, "esc", 29, 25)
}

func TestZZCenters(t *testing.T) {
	m := scrollLibrary(t)
	press(t, m, "j", 12)
	press(t, m, "k", 4)
	at(t, m, "12 down, 4 up", 8, 4)

	if msgs := feed(t, m, key("z")); len(msgs) != 0 || !m.zPending {
		t.Fatalf("z: msgs %v, pending %t", msgs, m.zPending)
	}
	if msgs := feed(t, m, key("z")); len(msgs) != 0 {
		t.Fatalf("zz scheduled playback: %v", msgs)
	}
	at(t, m, "zz", 8, 3)

	// The view stays on the rows at the ends.
	press(t, m, "g", 1)
	press(t, m, "j", 2)
	press(t, m, "z", 2)
	at(t, m, "zz near the top", 2, 0)

	// z followed by another key does nothing, and the key is used up.
	press(t, m, "z", 1)
	press(t, m, "j", 1)
	at(t, m, "zj", 2, 0)
	if m.zPending {
		t.Fatal("z still pending")
	}
}
