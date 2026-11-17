package ui

import (
	"fmt"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The Library has one spinner for whatever it is waiting on: a library
// scan that has files to read, the playlist dialog loading, or analysis
// jobs running. It ticks only
// while one of those is in progress.

func newSpinner() spinner.Model {
	return spinner.New(spinner.WithSpinner(spinner.MiniDot))
}

// busy reports whether anything the spinner shows is in progress.
func (m *Library) busy() bool {
	return m.scanning() || (m.dialog != nil && m.dialog.loading) || m.analysis.pending()
}

// scanning reports whether a scan is reading files. A sweep that finds
// nothing changed reports every file resolved at once, so it never shows.
func (m *Library) scanning() bool {
	return !m.scan.Done && m.scan.Scanned < m.scan.Total
}

// spin starts the spinner if something is in progress and it is not
// already ticking.
func (m *Library) spin() tea.Cmd {
	if m.spinning || !m.busy() {
		return nil
	}
	m.spinning = true
	return m.spinner.Tick
}

// spinTick advances the spinner, and lets it stop once nothing is in
// progress.
func (m *Library) spinTick(msg spinner.TickMsg) tea.Cmd {
	if !m.busy() {
		m.spinning = false
		return nil
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// scanView is the scan's part of the title line: the spinner, a bar and the
// counts, padded like the rest of the line. It is empty when no scan is
// reading files. The text is faint on each side of the bar, since the bar's
// colors end in a reset.
func (m *Library) scanView() string {
	if !m.scanning() {
		return ""
	}
	pct := float64(m.scan.Scanned) / float64(m.scan.Total)
	faint := lipgloss.NewStyle().Faint(true)
	s := faint.Render(m.spinner.View()+" scanning ") + bar(12, pct) +
		faint.Render(fmt.Sprintf(" %d/%d", m.scan.Scanned, m.scan.Total))
	return lipgloss.NewStyle().Padding(0, 1).Render(s)
}

// barColor fills the progress bars, in the selected row's color.
var barColor = lipgloss.Color("57")

// bar renders a progress bar w cells wide, pct (0–1) full.
func bar(w int, pct float64) string {
	p := progress.New(
		progress.WithWidth(w),
		progress.WithoutPercentage(),
		progress.WithColors(barColor),
		progress.WithFillCharacters(progress.DefaultFullCharFullBlock, progress.DefaultEmptyCharBlock),
	)
	return p.ViewAs(min(max(pct, 0), 1))
}
