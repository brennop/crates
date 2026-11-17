package ui

import (
	"math"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"

	"github.com/brennop/crates/model"
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
	c, ok := model.ParseCamelot(key)
	if !ok {
		return 0, false
	}
	return float64(c.Number-1) * 30, true
}

func bpmHue(bpm float64) float64 {
	return math.Mod(bpm*bpmHuePerBeat, 360)
}
