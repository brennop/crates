package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/model"
)

func TestCamelot(t *testing.T) {
	for key, want := range map[string]int{
		"8A": 8, "8B": 8, "08a": 8, "12B": 12, "1A": 1,
		"1m": 8, "1d": 8, "6m": 1, // Open Key
		"Am": 8, "C": 8, "Cmaj": 8, "A minor": 8,
		"Abm": 1, "G#m": 1, "B": 1, "F#": 2, "Gb": 2, "Dbm": 12, "C#m": 12,
		"E": 12, "Em": 9, "G": 9, "Bb": 6, "Gm": 6, "B♭": 6, "Fm": 4,
	} {
		if got, ok := camelot(key); !ok || got != want {
			t.Errorf("camelot(%q) = %d, %v; want %d", key, got, ok, want)
		}
	}
	for _, key := range []string{"", "13A", "0B", "8C", "H", "Am7", "x"} {
		if n, ok := camelot(key); ok {
			t.Errorf("camelot(%q) = %d, want not ok", key, n)
		}
	}
}

func TestBPMHueWraps(t *testing.T) {
	if a, b := bpmHue(120), bpmHue(210); a != b {
		t.Errorf("120 and 210 BPM: hues %v, %v; want equal", a, b)
	}
	if h := bpmHue(128.5); h < 0 || h >= 360 {
		t.Errorf("hue %v out of range", h)
	}
}

func TestRowFills(t *testing.T) {
	m := &Library{cols: columns(120, colArtist)}
	tr := model.Track{Summary: model.TagSummary{Title: "t", Key: "8A", BPM: 124}}
	row := m.row(tr, false)
	for _, i := range []int{colKey, colBPM} {
		if !strings.Contains(row[i], "\x1b[48;2;") {
			t.Errorf("cell %d has no background: %q", i, row[i])
		}
		if w := ansi.StringWidth(row[i]); w != m.cols[i].Width {
			t.Errorf("cell %d is %d wide, want the full column %d", i, w, m.cols[i].Width)
		}
	}
	if sel := m.row(tr, true); sel[colKey] != " 8A" || sel[colBPM] != " 124" {
		t.Errorf("selected row filled: %q %q", sel[colKey], sel[colBPM])
	}
}
