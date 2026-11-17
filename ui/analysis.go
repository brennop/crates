package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/brennop/crates/analysis"
	"github.com/brennop/crates/model"
)

// m toggles the analysis panel. Opening it runs every analysis job on the
// selected track in parallel; each row shows a spinner until its job
// answers. The panel sits in the top right corner and takes no keys, so
// the rest of the Library works while it is open. Closing it cancels the
// jobs still running.

// analysisPanel is the open panel: the track it analyzes and what has come
// back so far.
type analysisPanel struct {
	track   model.Track
	results map[analysis.Job]analysis.Result
	cancel  context.CancelFunc
}

// analysisClose is the panel's hint: the m that opened it closes it.
var analysisClose = newBinding("m", "close", "m")

const (
	analysisInner = 54 // inner width of the box, on a wide enough screen
	analysisLabel = 16 // width of the job column
)

// toggleAnalysis closes the panel, or opens it on the selected track and
// starts the jobs.
func (m *Library) toggleAnalysis() tea.Cmd {
	if m.analysis != nil {
		m.analysis.cancel()
		m.analysis = nil
		return nil
	}
	i := m.cursor
	if i < 0 || i >= len(m.tracks) {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	p := &analysisPanel{track: m.tracks[i], results: map[analysis.Job]analysis.Result{}, cancel: cancel}
	m.analysis = p
	cmds := []tea.Cmd{m.spin()}
	for _, job := range analysis.Jobs {
		cmds = append(cmds, Analyze(ctx, m.app, p.track.ID, job))
	}
	return tea.Batch(cmds...)
}

// analyzed records a job's result, if the panel is still open on its track.
func (m *Library) analyzed(msg AnalyzeResult) {
	if p := m.analysis; p != nil && p.track.ID == msg.ID {
		p.results[msg.Result.Job] = msg.Result
	}
}

// pending reports whether a job has not answered yet.
func (p *analysisPanel) pending() bool {
	return p != nil && len(p.results) < len(analysis.Jobs)
}

// view renders the box for a screen width wide.
func (p *analysisPanel) view(width int, spin string) string {
	inner := min(analysisInner, max(width-dialogChrome, 20))
	valueW := max(inner-analysisLabel, 5)
	var rows [][]string
	for _, job := range analysis.Jobs {
		r, ok := p.results[job]
		var lines []string
		switch {
		case !ok:
			lines = []string{dialogHintStyle.Render(spin + " running…")}
		case r.Err != nil:
			lines = []string{dialogErrStyle.Render(fitLine(r.Err.Error(), valueW))}
		default:
			lines = resultLines(r)
		}
		for i, l := range lines {
			label := ""
			if i == 0 {
				label = job.String()
			}
			rows = append(rows, []string{label, fitLine(l, valueW)})
		}
	}
	t := table.New().
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderHeader(false).BorderColumn(false).
		StyleFunc(func(_, col int) lipgloss.Style {
			if col == 0 {
				return lipgloss.NewStyle().Width(analysisLabel).Faint(true)
			}
			return lipgloss.NewStyle()
		}).
		Width(inner).
		Rows(rows...)
	body := strings.Split(t.Render(), "\n")
	return dialogBox(inner, "Analysis · "+trackName(p.track), body, analysisClose)
}

// resultLines writes a finished job's findings, one line each.
func resultLines(r analysis.Result) []string {
	var lines []string
	if r.BPM > 0 {
		l := fmt.Sprintf("%.1f BPM", r.BPM)
		if r.Job == analysis.BPM {
			l += fmt.Sprintf("  confidence %.2f", r.Confidence)
		}
		lines = append(lines, l)
	}
	for _, k := range r.Keys {
		l := k.Key
		if c, ok := model.ParseCamelot(k.Key); ok {
			l += "  " + c.String()
		}
		if k.Strength > 0 {
			l += fmt.Sprintf("  %.2f", k.Strength)
		}
		if k.Label != "" {
			l = fmt.Sprintf("%-6s %s", k.Label, l)
		}
		lines = append(lines, l)
	}
	if r.Match != "" {
		lines = append(lines, dialogHintStyle.Render(r.Match))
	}
	if len(lines) == 0 {
		lines = []string{dialogHintStyle.Render("nothing found")}
	}
	return lines
}

// overlayTopRight draws box over base in the top right corner, below the
// title line.
func overlayTopRight(base, box string, width int) string {
	x := max(width-lipgloss.Width(box), 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(1).Z(1),
	).Render()
}
