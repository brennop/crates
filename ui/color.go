package ui

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// Key and BPM cells are filled in OKLCH at a fixed lightness and chroma, so
// only the hue carries the value and every tint reads equally bright.
const (
	cellL = 0.78
	cellC = 0.12

	// bpmHuePerBeat spreads tempos around the hue wheel. BPM spans a wide
	// range, so the hue wraps every 360/4 = 90 BPM; nearby tempos stay apart
	// (5 BPM = 20°) at the cost of 90 BPM apart sharing a color.
	bpmHuePerBeat = 4.0
)

// cellText is the dark text drawn over the light cell backgrounds.
var cellText = lipgloss.Color(colorful.OkLch(0.25, 0, 0).Clamped().Hex())

// fill renders the cell s, w wide, on the OKLCH hue h (degrees),
// gamut-clamped to sRGB.
func fill(s string, w int, h float64) string {
	c := colorful.OkLch(cellL, cellC, h).Clamped()
	return lipgloss.NewStyle().
		Background(lipgloss.Color(c.Hex())).
		Foreground(cellText).
		Width(w).MaxWidth(w).
		Render(s)
}

// keyHue places a key on the Camelot wheel, one turn for the twelve
// positions, so harmonically adjacent keys get adjacent hues and a key and
// its relative major/minor share one. ok is false for text it cannot read.
func keyHue(key string) (h float64, ok bool) {
	n, ok := camelot(key)
	if !ok {
		return 0, false
	}
	return float64(n-1) * 30, true
}

func bpmHue(bpm float64) float64 {
	return math.Mod(bpm*bpmHuePerBeat, 360)
}

// camelot returns the Camelot number (1–12) of a key written in Camelot
// ("8A"), Open Key ("1m", "1d") or musical notation ("Am", "C#m", "Bb",
// "F# minor").
func camelot(key string) (int, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return 0, false
	}

	// Camelot or Open Key: a number 1–12 and a letter.
	if d := strings.IndexFunc(k, func(r rune) bool { return r < '0' || r > '9' }); d > 0 {
		n, err := strconv.Atoi(k[:d])
		if err != nil || n < 1 || n > 12 {
			return 0, false
		}
		switch k[d:] {
		case "a", "b":
			return n, true
		case "m", "d": // Open Key 1 is C / Am, Camelot 8
			return (n-1+7)%12 + 1, true
		}
		return 0, false
	}

	pc, ok := map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}[k[0]]
	if !ok {
		return 0, false
	}
	rest := k[1:]
	if r, size := utf8.DecodeRuneInString(rest); r == '#' || r == '♯' {
		pc, rest = pc+1, rest[size:]
	} else if r == 'b' || r == '♭' {
		pc, rest = pc+11, rest[size:]
	}
	minor := false
	switch strings.TrimSpace(rest) {
	case "", "maj", "major":
	case "m", "min", "minor":
		minor = true
	default:
		return 0, false
	}
	// Each step around the wheel is a fifth (7 semitones). C major and A
	// minor are both 8.
	offset := 7
	if minor {
		offset = 4
	}
	return (pc%12*7+offset)%12 + 1, true
}
