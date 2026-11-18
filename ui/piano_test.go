package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeSynth struct {
	started bool
	played  []int
}

func (f *fakeSynth) Start() error  { f.started = true; return nil }
func (f *fakeSynth) Play(midi int) { f.played = append(f.played, midi) }
func (f *fakeSynth) Stop()         { f.started = false }

func TestPianoPlaysAndCloses(t *testing.T) {
	fs := &fakeSynth{}
	old := newSynth
	newSynth = func() noteSynth { return fs }
	t.Cleanup(func() { newSynth = old })

	e := newEnv(t)
	e.file(e.master, "a.mp3")
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	reload(t, m)

	_, cmd := m.Update(key("v"))
	if m.piano == nil || cmd == nil {
		t.Fatal("v did not open the piano")
	}
	m.Update(cmd())
	if !fs.started {
		t.Fatal("audio not started")
	}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "Piano · C4–F5") || !strings.Contains(v, "z octave down") {
		t.Fatalf("panel not shown:\n%s", v)
	}

	// a is middle C, w the C# above; k is the next C, ' the F above it.
	for _, k := range []string{"a", "w", "k", "'", "x", "a", "z", "z", "a", "q"} {
		m.Update(key(k))
	}
	if want := []int{60, 61, 72, 77, 72, 48}; !slices.Equal(fs.played, want) {
		t.Fatalf("played %v, want %v", fs.played, want)
	}
	if m.selected() == 0 || m.edit != nil {
		t.Fatal("piano keys reached the library")
	}

	m.Update(key("v"))
	if m.piano != nil || fs.started {
		t.Fatal("v did not close the piano and stop the audio")
	}
	if strings.Contains(ansi.Strip(m.View().Content), "Piano") {
		t.Fatal("panel still drawn")
	}
}

func TestPianoKeyboardWidth(t *testing.T) {
	p := &pianoPanel{octave: 4, lit: 3}
	rows := p.keyboard()
	if len(rows) != pianoRows {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		if w := ansi.StringWidth(r); w != 11*pianoWhiteW+1 {
			t.Fatalf("row width %d: %q", w, ansi.Strip(r))
		}
	}
}
