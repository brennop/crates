package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/analysis"
)

func TestAnalysisPanelToggles(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "a.mp3")
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	reload(t, m)
	if err := e.app.SaveBeatportToken("tok"); err != nil {
		t.Fatal(err)
	}

	// Update alone: running the commands would start the real scripts.
	m.Update(key("m"))
	p := m.analysis
	if p == nil || p.track.ID != m.selected() || !m.busy() {
		t.Fatalf("m did not open a running panel on the selection: %+v", p)
	}
	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "running…") || !strings.Contains(v, "Key · S-KEY") {
		t.Fatalf("panel not shown with pending rows:\n%s", v)
	}

	id := p.track.ID
	m.Update(AnalyzeResult{ID: id, Result: analysis.Result{Job: analysis.BPM, BPM: 124, Confidence: 3.5}})
	m.Update(AnalyzeResult{ID: id, Result: analysis.Result{Job: analysis.SKey, Keys: []analysis.KeyGuess{{Key: "A minor"}}}})
	m.Update(AnalyzeResult{ID: id, Result: analysis.Result{Job: analysis.EssentiaKey, Keys: []analysis.KeyGuess{{Label: "edma", Key: "C major", Strength: 0.8}}}})
	m.Update(AnalyzeResult{ID: id, Result: analysis.Result{Job: analysis.Beatport, Err: errors.New("no token")}})
	m.Update(AnalyzeResult{ID: id + 100, Result: analysis.Result{Job: analysis.BPM, BPM: 1}}) // another track's
	if m.analysis.pending() {
		t.Fatal("panel still pending after every job answered")
	}
	v := ansi.Strip(m.View().Content)
	for _, want := range []string{"124.0 BPM", "A minor  8A", "edma   C major  8B  0.80", "no token"} {
		if !strings.Contains(v, want) {
			t.Errorf("panel lacks %q:\n%s", want, v)
		}
	}

	// The panel takes no keys: s still sorts.
	sortBy := m.sortBy
	m.Update(key("s"))
	if m.sortBy == sortBy || m.analysis == nil {
		t.Fatal("s did not sort with the panel open")
	}

	m.Update(key("m"))
	if m.analysis != nil {
		t.Fatal("second m did not close the panel")
	}
}

// With no token saved, m asks for one first; esc opens the panel without
// it, and a saved token opens it too. A refused token asks again, and the
// new one reruns only the Beatport job.
func TestAnalysisAsksForBeatportToken(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "a.mp3")
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	reload(t, m)

	m.Update(key("m"))
	if m.token == nil || m.analysis != nil || !strings.Contains(ansi.Strip(m.View().Content), "Beatport token:") {
		t.Fatal("m with no token did not ask for one")
	}
	m.Update(key("esc"))
	if m.token != nil || m.analysis == nil {
		t.Fatal("esc did not open the panel without a token")
	}
	m.Update(key("m"))

	m.Update(key("m"))
	for _, r := range "tok" {
		m.Update(key(string(r)))
	}
	_, cmd := m.Update(enterKey)
	m.Update(run[SaveBeatportTokenResult](t, cmd))
	if m.token != nil || m.analysis == nil || !e.app.HasBeatportToken() {
		t.Fatalf("enter did not save the token and open the panel (status %q)", m.status)
	}

	p := m.analysis
	for _, job := range analysis.Jobs {
		m.Update(AnalyzeResult{ID: p.track.ID, Result: analysis.Result{Job: job}})
	}
	m.Update(AnalyzeResult{ID: p.track.ID, Result: analysis.Result{Job: analysis.Beatport, Err: fmt.Errorf("%w (401)", analysis.ErrBeatportRefused)}})
	if m.token == nil {
		t.Fatal("a refused token did not ask for a new one")
	}
	m.Update(key("x"))
	_, cmd = m.Update(enterKey)
	m.Update(run[SaveBeatportTokenResult](t, cmd))
	if _, ok := p.results[analysis.Beatport]; ok || len(p.results) != len(analysis.Jobs)-1 {
		t.Fatalf("the new token did not rerun just the Beatport job: %v", p.results)
	}
}
