package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	binding "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
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
//
// Opening the panel with no Beatport token saved, or a Beatport answer that
// the token was refused, opens a prompt in the status line for a new one: a
// token response as JSON, or a bare access token. Enter saves it, then opens
// the panel or runs the Beatport job again; esc opens the panel without it,
// or leaves the refusal showing.

// analysisPanel is the open panel: the track it analyzes and what has come
// back so far.
type analysisPanel struct {
	track   model.Track
	results map[analysis.Job]analysis.Result
	ctx     context.Context // the jobs', cancelled on close
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
	if !m.app.HasBeatportToken() {
		m.openTokenPrompt()
		return nil
	}
	return m.openAnalysis()
}

// openAnalysis opens the panel on the selected track and starts the jobs.
func (m *Library) openAnalysis() tea.Cmd {
	i := m.cursor
	if i < 0 || i >= len(m.tracks) {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	p := &analysisPanel{track: m.tracks[i], results: map[analysis.Job]analysis.Result{}, ctx: ctx, cancel: cancel}
	m.analysis = p
	cmds := []tea.Cmd{m.spin()}
	for _, job := range analysis.Jobs {
		cmds = append(cmds, Analyze(ctx, m.app, p.track.ID, job))
	}
	return tea.Batch(cmds...)
}

// analyzed records a job's result, if the panel is still open on its track,
// and asks for a new token when Beatport refused the saved one.
func (m *Library) analyzed(msg AnalyzeResult) {
	if len(msg.Result.Keys) > 0 {
		if m.keyGuesses == nil {
			m.keyGuesses = map[model.TrackID][]string{}
		}
		keys := m.keyGuesses[msg.ID]
		for _, k := range msg.Result.Keys {
			keys = append(keys, k.Key)
		}
		m.keyGuesses[msg.ID] = uniq(keys)
	}
	if p := m.analysis; p != nil && p.track.ID == msg.ID {
		p.results[msg.Result.Job] = msg.Result
		if errors.Is(msg.Result.Err, analysis.ErrBeatportRefused) && m.search == nil && m.edit == nil {
			m.openTokenPrompt()
		}
	}
}

// openTokenPrompt asks for a Beatport token in the status line.
func (m *Library) openTokenPrompt() {
	m.token = &tokenPrompt{input: newPromptInput("Beatport token: ", "", m.width)}
}

// tokenPrompt is the open Beatport token prompt.
type tokenPrompt struct{ input textinput.Model }

// tokenKey handles a key while the token prompt is open: enter saves the
// token, esc drops the prompt (opening the panel without a token if it
// was not open yet), and the rest edit the line.
func (m *Library) tokenKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case binding.Matches(msg, promptKeys.Cancel):
		m.token = nil
		if m.analysis == nil {
			return m.openAnalysis()
		}
		return nil
	case binding.Matches(msg, promptKeys.Accept):
		v := m.token.input.Value()
		m.token = nil
		return SaveBeatportToken(m.app, v)
	}
	return m.tokenUpdate(msg)
}

// tokenUpdate passes msg to the token prompt's input.
func (m *Library) tokenUpdate(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.token.input, cmd = m.token.input.Update(msg)
	return cmd
}

// tokenSaved opens the panel, or runs the Beatport job again on the open
// one, once a token is saved.
func (m *Library) tokenSaved(msg SaveBeatportTokenResult) tea.Cmd {
	if msg.Err != nil {
		m.status = "Beatport token: " + msg.Err.Error()
		return nil
	}
	m.status = ""
	p := m.analysis
	if p == nil {
		return m.openAnalysis()
	}
	delete(p.results, analysis.Beatport)
	return tea.Batch(m.spin(), Analyze(p.ctx, m.app, p.track.ID, analysis.Beatport))
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
