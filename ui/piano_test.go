package ui

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type fakeSynth struct {
	started bool
	on      map[int]bool
	played  []int
}

func (f *fakeSynth) Start() error     { f.started = true; return nil }
func (f *fakeSynth) NoteOn(midi int)  { f.on[midi] = true; f.played = append(f.played, midi) }
func (f *fakeSynth) NoteOff(midi int) { delete(f.on, midi) }
func (f *fakeSynth) Stop()            { f.started = false; clear(f.on) }

func (f *fakeSynth) sounding() []int { return slices.Sorted(maps.Keys(f.on)) }

// pianoLibrary opens the piano on a library with one track.
func pianoLibrary(t *testing.T) (*Library, *fakeSynth) {
	fs := &fakeSynth{on: map[int]bool{}}
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
	return m, fs
}

func release(s string) tea.KeyReleaseMsg { return tea.KeyReleaseMsg(key(s)) }

func TestPianoPlaysAndCloses(t *testing.T) {
	m, fs := pianoLibrary(t)
	v := m.View()
	if !v.KeyboardEnhancements.ReportEventTypes || !v.KeyboardEnhancements.ReportAllKeysAsEscapeCodes {
		t.Fatal("piano does not ask for key releases")
	}
	if c := ansi.Strip(v.Content); !strings.Contains(c, "Piano · C4–F5") || !strings.Contains(c, "z octave down") {
		t.Fatalf("panel not shown:\n%s", c)
	}

	// a is middle C, w the C# above; k is the next C, ' the F above it.
	m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyReportEventTypes})
	for _, k := range []string{"a", "w", "k", "'", "x", "a", "z", "z", "a", "q"} {
		m.Update(key(k))
		m.Update(release(k))
	}
	if want := []int{60, 61, 72, 77, 72, 48}; !slices.Equal(fs.played, want) {
		t.Fatalf("played %v, want %v", fs.played, want)
	}
	if m.selected() == 0 || m.edit != nil {
		t.Fatal("piano keys reached the library")
	}

	// m works over the piano. Update alone: running the commands would
	// start the real analysis scripts.
	if err := m.app.SaveBeatportToken("tok"); err != nil {
		t.Fatal(err)
	}
	m.Update(key("m"))
	if m.analysis == nil || m.piano == nil {
		t.Fatal("m did not open the analysis panel with the piano open")
	}
	m.Update(key("m"))
	if m.analysis != nil {
		t.Fatal("m did not close the analysis panel")
	}

	m.Update(key("v"))
	if m.piano != nil || fs.started {
		t.Fatal("v did not close the piano and stop the audio")
	}
	if v := m.View(); strings.Contains(ansi.Strip(v.Content), "Piano") || v.KeyboardEnhancements.ReportEventTypes {
		t.Fatal("panel still drawn, or still asking for releases")
	}
}

func TestPianoHoldsUntilRelease(t *testing.T) {
	m, fs := pianoLibrary(t)
	m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyReportEventTypes})

	// C E G held: a C chord.
	for _, k := range []string{"a", "d", "g"} {
		m.Update(key(k))
	}
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a", IsRepeat: true})
	if got := fs.sounding(); !slices.Equal(got, []int{60, 64, 67}) {
		t.Fatalf("sounding %v", got)
	}
	if c := ansi.Strip(m.View().Content); !strings.Contains(c, "C · C4 E4 G4") {
		t.Fatalf("chord not shown:\n%s", c)
	}
	m.pianoLetGoMsg(pianoLetGo{m.piano.seq}) // no guessing once releases come
	m.Update(release("d"))
	if got := fs.sounding(); !slices.Equal(got, []int{60, 67}) {
		t.Fatalf("after letting go of E, sounding %v", got)
	}
	if c := ansi.Strip(m.View().Content); !strings.Contains(c, "C5 · C4 G4") {
		t.Fatalf("chord not updated:\n%s", c)
	}
	m.Update(release("a"))
	if c := ansi.Strip(m.View().Content); strings.Contains(c, "G4") {
		t.Fatalf("one note shown as a chord:\n%s", c)
	}

	// Held across an octave change, the note let go is the one played.
	m.Update(key("x"))
	m.Update(release("g"))
	if got := fs.sounding(); len(got) != 0 {
		t.Fatalf("still sounding %v", got)
	}
}

func TestPianoHoldsWhileRepeating(t *testing.T) {
	m, fs := pianoLibrary(t) // no KeyboardEnhancementsMsg: as under tmux

	// Pressed together, then one key repeating: a chord, held.
	m.Update(key("a"))
	m.Update(key("d"))
	m.Update(key("d")) // auto-repeat
	if got := fs.sounding(); !slices.Equal(got, []int{60, 64}) || len(fs.played) != 2 {
		t.Fatalf("sounding %v, played %v", got, fs.played)
	}
	seq := m.piano.seq
	m.Update(key("d"))
	m.Update(pianoLetGo{seq}) // overtaken by the repeat
	if len(fs.sounding()) != 2 {
		t.Fatal("let go while repeats still came")
	}
	m.Update(pianoLetGo{m.piano.seq})
	if got := fs.sounding(); len(got) != 0 {
		t.Fatalf("repeats stopped, still sounding %v", got)
	}

	// A tap, then another key: the tap is let go.
	m.Update(key("a"))
	time.Sleep(pianoChordWindow + 20*time.Millisecond)
	m.Update(key("s"))
	if got := fs.sounding(); !slices.Equal(got, []int{62}) {
		t.Fatalf("sounding %v, want only D", got)
	}
}

func TestChordName(t *testing.T) {
	for _, c := range []struct {
		midis []int
		want  string
	}{
		{[]int{60, 64, 67}, "C"},
		{[]int{57, 60, 64, 67}, "Am7"},
		{[]int{64, 67, 72}, "C/E"},
		{[]int{62, 65, 69, 72}, "Dm7"},
		{[]int{67, 71, 74, 77}, "G7"},
		{[]int{60, 67}, "C5"},
		{[]int{60, 64}, "major 3rd"},
		{[]int{60, 72}, "octave"},
		{[]int{60, 61, 62}, "?"},
	} {
		if got := chordName(c.midis); got != c.want {
			t.Errorf("chordName(%v) = %q, want %q", c.midis, got, c.want)
		}
	}
}

func TestPianoKeyboardFillsWidth(t *testing.T) {
	p := &pianoPanel{octave: 4, held: map[int]*heldNote{3: {midi: 63}}}
	for _, w := range []int{40, 56, 116, 200} {
		rows := p.keyboard(w)
		if len(rows) != pianoRows {
			t.Fatalf("%d rows", len(rows))
		}
		want := max(w, 11*pianoMinWhiteW+1)
		for _, r := range rows {
			if got := ansi.StringWidth(r); got > want || got < want-10 {
				t.Fatalf("width %d: row width %d: %q", w, got, ansi.Strip(r))
			}
		}
	}
}
