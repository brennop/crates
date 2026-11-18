package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	binding "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/synth"
)

// v opens a piano in the box above the status line, where the tag
// vocabulary shows while a comment is edited. The keyboard plays notes
// like a web piano (see pianoNotes), z and x shift the octave, and v closes
// it. The track keeps playing, so the piano can be played along with it.
// With two or more notes held, the panel names the chord.
//
// The keyboard is drawn here cell by cell: bubbles has no component for it.
//
// A note sounds as long as its key is held. Terminals that speak the kitty
// keyboard protocol (Ghostty, kitty, WezTerm) report key releases, and the
// panel asks for them while open. tmux does not pass releases on, so there
// the panel guesses from the key's auto-repeat presses: a note is held
// while they keep coming. macOS repeats only the last key pressed, so the
// guess holds notes pressed together, or added while one is held, until
// the repeats stop; letting go of one key of a chord is not seen.

// noteSynth is the sound of the piano; tests swap in a silent one.
type noteSynth interface {
	Start() error
	NoteOn(midi int)
	NoteOff(midi int)
	Stop()
}

var newSynth = func() noteSynth { return synth.New() }

const (
	pianoMinOctave, pianoMaxOctave = 1, 7
	pianoStartOctave               = 4 // a plays middle C
	pianoMinWhiteW                 = 5 // columns per white key, at least
	pianoRows                      = 5 // the top 3 hold the black keys

	// Without key releases: a key pressed is held until its first repeat
	// is due (macOS waits up to about half a second), then while repeats
	// keep coming.
	pianoHoldFirst = 550 * time.Millisecond
	pianoHoldNext  = 150 * time.Millisecond
	// Notes pressed within this of each other are a chord: a new press
	// does not let them go.
	pianoChordWindow = 120 * time.Millisecond
)

type pianoPanel struct {
	octave   int
	held     map[int]*heldNote // by semitone above the octave's C
	seq      uint64            // the latest press; only its pianoLetGo lets go
	releases bool              // the terminal reports key releases
}

type heldNote struct {
	midi      int // as played: z and x do not move a held note
	pressed   time.Time
	repeating bool // auto-repeat presses have come
}

// pianoStarted reports whether the audio output opened.
type pianoStarted struct{ err error }

// pianoLetGo fires when the repeats of held keys are overdue.
type pianoLetGo struct{ seq uint64 }

func (m *Library) openPiano() tea.Cmd {
	m.piano = &pianoPanel{octave: pianoStartOctave, held: map[int]*heldNote{}}
	if m.synth == nil {
		m.synth = newSynth()
	}
	s := m.synth
	return func() tea.Msg { return pianoStarted{err: s.Start()} }
}

func (m *Library) closePiano() {
	m.piano = nil
	if m.synth != nil {
		m.synth.Stop()
	}
}

// pianoStartedMsg handles the audio output opening. A piano closed while it
// opened stops it again.
func (m *Library) pianoStartedMsg(msg pianoStarted) {
	if msg.err != nil {
		m.status = "piano: " + msg.err.Error()
		m.piano = nil
		return
	}
	if m.piano == nil {
		m.synth.Stop()
	}
}

// pianoKey handles a key press while the piano is open. Space still pauses
// the track; other keys do nothing.
func (m *Library) pianoKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.piano
	switch {
	case binding.Matches(msg, pianoKeys.Close):
		m.closePiano()
		return nil
	case binding.Matches(msg, pianoKeys.OctaveDown):
		p.octave = max(p.octave-1, pianoMinOctave)
		return nil
	case binding.Matches(msg, pianoKeys.OctaveUp):
		p.octave = min(p.octave+1, pianoMaxOctave)
		return nil
	case binding.Matches(msg, libraryKeys.Pause):
		return TogglePause(m.ctx, m.app)
	}
	for _, n := range pianoNotes {
		if binding.Matches(msg, n.key) {
			return m.notePressed(n.semi, msg.IsRepeat)
		}
	}
	return nil
}

func (m *Library) notePressed(semi int, repeat bool) tea.Cmd {
	p := m.piano
	now := time.Now()
	if h := p.held[semi]; h != nil {
		if p.releases {
			return nil // a repeat; the release ends it
		}
		// Without releases, a held key's press is its auto-repeat.
		h.repeating = true
		return p.letGoAfter(pianoHoldNext)
	}
	if repeat {
		return nil // repeats of a key let go of in a chord
	}
	if !p.releases {
		// A new key while others are tapped, not held: they are let go,
		// unless pressed with this one as a chord.
		for s, h := range p.held {
			if !h.repeating && now.Sub(h.pressed) > pianoChordWindow {
				m.noteReleased(s)
			}
		}
	}
	midi := 12*(p.octave+1) + semi
	m.synth.NoteOn(midi)
	p.held[semi] = &heldNote{midi: midi, pressed: now}
	if p.releases {
		return nil
	}
	return p.letGoAfter(pianoHoldFirst)
}

func (p *pianoPanel) letGoAfter(d time.Duration) tea.Cmd {
	p.seq++
	seq := p.seq
	return tea.Tick(d, func(time.Time) tea.Msg { return pianoLetGo{seq} })
}

func (m *Library) noteReleased(semi int) {
	if h := m.piano.held[semi]; h != nil {
		m.synth.NoteOff(h.midi)
		delete(m.piano.held, semi)
	}
}

// pianoKeyUp handles a key release, from terminals that report them.
func (m *Library) pianoKeyUp(msg tea.KeyReleaseMsg) {
	m.piano.releases = true
	for _, n := range pianoNotes {
		if binding.Matches(msg, n.key) {
			m.noteReleased(n.semi)
		}
	}
}

// pianoLetGoMsg lets go of every held note once no repeat came in time.
func (m *Library) pianoLetGoMsg(msg pianoLetGo) {
	if m.piano == nil || m.piano.releases || msg.seq != m.piano.seq {
		return
	}
	for s := range m.piano.held {
		m.noteReleased(s)
	}
}

var (
	pianoWhite    = lipgloss.NewStyle().Background(lipgloss.Color("255")).Foreground(lipgloss.Color("236"))
	pianoWhiteSep = lipgloss.NewStyle().Background(lipgloss.Color("255")).Foreground(lipgloss.Color("246"))
	pianoBlack    = lipgloss.NewStyle().Background(lipgloss.Color("235")).Foreground(lipgloss.Color("250"))
	pianoPressed  = lipgloss.NewStyle().Background(lipgloss.Color("212")).Foreground(lipgloss.Color("0"))
)

// view renders the panel in a box screen width wide, the keyboard filling it.
func (p *pianoPanel) view(width int) string {
	inner := max(width-dialogChrome, 10)
	faint := lipgloss.NewStyle().Faint(true)
	bold := lipgloss.NewStyle().Bold(true)
	head := bold.Render("Piano") + faint.Render(fmt.Sprintf(" · C%d–F%d", p.octave, p.octave+1))
	if name, notes := p.chord(); name != "" {
		head += faint.Render(" · ") + bold.Render(name) + faint.Render(" · "+notes)
	}
	foot := faint.Render(hints(inner, pianoKeys.OctaveDown, pianoKeys.OctaveUp, pianoKeys.Close))
	lines := []string{ansi.Truncate(head, inner, "…")}
	for _, row := range p.keyboard(inner) {
		lines = append(lines, ansi.Truncate(row, inner, ""))
	}
	lines = append(lines, foot)
	return dialogStyle.Width(width).Render(strings.Join(lines, "\n"))
}

// chord names the held notes when there are two or more, and lists them
// low to high.
func (p *pianoPanel) chord() (name, notes string) {
	var midis []int
	for _, h := range p.held {
		midis = append(midis, h.midi)
	}
	if len(midis) < 2 {
		return "", ""
	}
	slices.Sort(midis)
	parts := make([]string, len(midis))
	for i, n := range midis {
		parts[i] = fmt.Sprintf("%s%d", noteNames[n%12], n/12-1)
	}
	return chordName(midis), strings.Join(parts, " ")
}

var noteNames = []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// chordShapes are the chords chordName knows, as intervals above the root
// in semitones, mod 12.
var chordShapes = []struct {
	suffix    string
	intervals []int
}{
	{"", []int{0, 4, 7}},
	{"m", []int{0, 3, 7}},
	{"dim", []int{0, 3, 6}},
	{"aug", []int{0, 4, 8}},
	{"sus2", []int{0, 2, 7}},
	{"sus4", []int{0, 5, 7}},
	{"5", []int{0, 7}},
	{"maj7", []int{0, 4, 7, 11}},
	{"7", []int{0, 4, 7, 10}},
	{"m7", []int{0, 3, 7, 10}},
	{"m7b5", []int{0, 3, 6, 10}},
	{"dim7", []int{0, 3, 6, 9}},
	{"mMaj7", []int{0, 3, 7, 11}},
	{"6", []int{0, 4, 7, 9}},
	{"m6", []int{0, 3, 7, 9}},
	{"7sus4", []int{0, 5, 7, 10}},
	{"add9", []int{0, 2, 4, 7}},
	{"madd9", []int{0, 2, 3, 7}},
	{"9", []int{0, 2, 4, 7, 10}},
	{"maj9", []int{0, 2, 4, 7, 11}},
	{"m9", []int{0, 2, 3, 7, 10}},
}

// intervalNames name two notes that are not a chord, by the semitones
// between them.
var intervalNames = []string{"unison", "minor 2nd", "major 2nd", "minor 3rd", "major 3rd",
	"perfect 4th", "tritone", "perfect 5th", "minor 6th", "major 6th", "minor 7th", "major 7th"}

// chordName names the chord of midis, sorted low to high. Each note is
// tried as the root, the lowest first; a root that is not the lowest note
// is written as a slash chord over it. Two notes that are no chord are
// named by their interval, and anything else is "?".
func chordName(midis []int) string {
	var pcs []int
	for _, n := range midis {
		if pc := n % 12; !slices.Contains(pcs, pc) {
			pcs = append(pcs, pc)
		}
	}
	bass := pcs[0]
	for _, root := range pcs {
		var ivs []int
		for _, pc := range pcs {
			ivs = append(ivs, (pc-root+12)%12)
		}
		slices.Sort(ivs)
		for _, s := range chordShapes {
			if slices.Equal(ivs, s.intervals) {
				name := noteNames[root] + s.suffix
				if root != bass {
					name += "/" + noteNames[bass]
				}
				return name
			}
		}
	}
	switch len(pcs) {
	case 1:
		return "octave"
	case 2:
		return intervalNames[(pcs[1]-pcs[0]+12)%12]
	}
	return "?"
}

// pianoCell is one character of the keyboard drawing.
type pianoCell struct {
	r  rune
	st *lipgloss.Style
}

// keyboard draws the keys of pianoNotes as wide as fits in width, each
// labelled with the key that plays it, and the Cs with their octave.
func (p *pianoPanel) keyboard(width int) []string {
	var whites, blacks []pianoNote
	for _, n := range pianoNotes {
		switch n.semi % 12 {
		case 1, 3, 6, 8, 10:
			blacks = append(blacks, n)
		default:
			whites = append(whites, n)
		}
	}
	ww := max((width-1)/len(whites), pianoMinWhiteW)
	bw := max(ww*3/5, 3) | 1 // odd, so it centers on the border
	cols := len(whites)*ww + 1
	grid := make([][]pianoCell, pianoRows)
	for y := range grid {
		grid[y] = make([]pianoCell, cols)
		for x := range grid[y] {
			if x%ww == 0 {
				grid[y][x] = pianoCell{'│', &pianoWhiteSep}
			} else {
				grid[y][x] = pianoCell{' ', &pianoWhite}
			}
		}
	}
	label := func(y, x int, s string, st *lipgloss.Style) {
		for i, r := range s {
			if x+i < cols {
				grid[y][x+i] = pianoCell{r, st}
			}
		}
	}
	white := map[int]int{} // semitone to white key index
	for i, n := range whites {
		white[n.semi] = i
		st := &pianoWhite
		if p.held[n.semi] != nil {
			st = &pianoPressed
		}
		for y := range pianoRows {
			for x := i*ww + 1; x < (i+1)*ww; x++ {
				grid[y][x] = pianoCell{' ', st}
			}
		}
		x := i*ww + ww/2
		label(pianoRows-1, x, n.key.Help().Key, st)
		if n.semi%12 == 0 {
			label(pianoRows-2, x-1, fmt.Sprintf("C%d", p.octave+n.semi/12), st)
		}
	}
	for _, n := range blacks {
		st := &pianoBlack
		if p.held[n.semi] != nil {
			st = &pianoPressed
		}
		b := (white[n.semi-1] + 1) * ww // the border it sits on
		for y := range pianoRows - 2 {
			for x := b - bw/2; x <= b+bw/2; x++ {
				grid[y][x] = pianoCell{' ', st}
			}
		}
		label(pianoRows-3, b, n.key.Help().Key, st)
	}
	out := make([]string, pianoRows)
	for y, row := range grid {
		var sb strings.Builder
		for x := 0; x < len(row); {
			st := row[x].st
			var run []rune
			for ; x < len(row) && row[x].st == st; x++ {
				run = append(run, row[x].r)
			}
			sb.WriteString(st.Render(string(run)))
		}
		out[y] = sb.String()
	}
	return out
}
