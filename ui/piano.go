package ui

import (
	"fmt"
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
//
// The keyboard is drawn here cell by cell: bubbles has no component for it.

// noteSynth is the sound of the piano; tests swap in a silent one.
type noteSynth interface {
	Start() error
	Play(midi int)
	Stop()
}

var newSynth = func() noteSynth { return synth.New() }

const (
	pianoMinOctave, pianoMaxOctave = 1, 7
	pianoStartOctave               = 4 // a plays middle C
	pianoLit                       = 180 * time.Millisecond
	pianoWhiteW                    = 5 // columns per white key
	pianoRows                      = 5 // the top 3 hold the black keys
)

type pianoPanel struct {
	octave int
	lit    int    // semitone of the key last pressed, above the octave's C; -1 for none
	litSeq uint64 // only the latest pianoUnlit clears lit
}

// pianoStarted reports whether the audio output opened.
type pianoStarted struct{ err error }

// pianoUnlit ends a pressed key's highlight.
type pianoUnlit struct{ seq uint64 }

func (m *Library) openPiano() tea.Cmd {
	m.piano = &pianoPanel{octave: pianoStartOctave, lit: -1}
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

// pianoKey handles a key while the piano is open. Space still pauses the
// track; other keys do nothing.
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
	if msg.IsRepeat {
		return nil // a held key, where the terminal reports repeats
	}
	for _, n := range pianoNotes {
		if binding.Matches(msg, n.key) {
			m.synth.Play(12*(p.octave+1) + n.semi)
			p.lit = n.semi
			p.litSeq++
			seq := p.litSeq
			return tea.Tick(pianoLit, func(time.Time) tea.Msg { return pianoUnlit{seq} })
		}
	}
	return nil
}

var (
	pianoWhite    = lipgloss.NewStyle().Background(lipgloss.Color("255")).Foreground(lipgloss.Color("236"))
	pianoWhiteSep = lipgloss.NewStyle().Background(lipgloss.Color("255")).Foreground(lipgloss.Color("246"))
	pianoBlack    = lipgloss.NewStyle().Background(lipgloss.Color("235")).Foreground(lipgloss.Color("250"))
	pianoPressed  = lipgloss.NewStyle().Background(lipgloss.Color("212")).Foreground(lipgloss.Color("0"))
)

// view renders the panel in a box screen width wide.
func (p *pianoPanel) view(width int) string {
	inner := max(width-dialogChrome, 10)
	first := fmt.Sprintf("C%d", p.octave)
	last := fmt.Sprintf("F%d", p.octave+1)
	faint := lipgloss.NewStyle().Faint(true)
	head := lipgloss.NewStyle().Bold(true).Render("Piano") + faint.Render(" · "+first+"–"+last)
	foot := faint.Render(hints(inner, pianoKeys.OctaveDown, pianoKeys.OctaveUp, pianoKeys.Close))
	lines := []string{head}
	for _, row := range p.keyboard() {
		lines = append(lines, ansi.Truncate(row, inner, ""))
	}
	lines = append(lines, foot)
	return dialogStyle.Width(width).Render(strings.Join(lines, "\n"))
}

// pianoCell is one character of the keyboard drawing.
type pianoCell struct {
	r  rune
	st *lipgloss.Style
}

// keyboard draws the keys of pianoNotes, each labelled with the key that
// plays it, and the Cs with their octave.
func (p *pianoPanel) keyboard() []string {
	var whites, blacks []pianoNote
	for _, n := range pianoNotes {
		switch n.semi % 12 {
		case 1, 3, 6, 8, 10:
			blacks = append(blacks, n)
		default:
			whites = append(whites, n)
		}
	}
	cols := len(whites)*pianoWhiteW + 1
	grid := make([][]pianoCell, pianoRows)
	for y := range grid {
		grid[y] = make([]pianoCell, cols)
		for x := range grid[y] {
			if x%pianoWhiteW == 0 {
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
		if n.semi == p.lit {
			st = &pianoPressed
		}
		for y := range pianoRows {
			for x := i*pianoWhiteW + 1; x < (i+1)*pianoWhiteW; x++ {
				grid[y][x] = pianoCell{' ', st}
			}
		}
		x := i*pianoWhiteW + pianoWhiteW/2
		label(pianoRows-1, x, n.key.Help().Key, st)
		if n.semi%12 == 0 {
			label(pianoRows-2, x-1, fmt.Sprintf("C%d", p.octave+n.semi/12), st)
		}
	}
	for _, n := range blacks {
		st := &pianoBlack
		if n.semi == p.lit {
			st = &pianoPressed
		}
		b := (white[n.semi-1] + 1) * pianoWhiteW // the border it sits on
		for y := range pianoRows - 2 {
			for x := b - 1; x <= b+1; x++ {
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
