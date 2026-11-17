package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/model"
)

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
	if row := m.row(model.Track{Summary: model.TagSummary{Key: "C#m"}}, true); row[colKey] != " 12A" {
		t.Errorf("key C#m shown as %q, want Camelot 12A", row[colKey])
	}
	if row := m.row(model.Track{Summary: model.TagSummary{Key: "x"}}, true); row[colKey] != " x" {
		t.Errorf("unreadable key shown as %q, want the tag text", row[colKey])
	}
}
