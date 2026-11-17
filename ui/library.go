package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

// playDelay is how long the cursor must rest on a row before it plays, so
// scrolling through the list does not load every track it passes.
const playDelay = 150 * time.Millisecond

// Library is the Bubble Tea model: a table of the master tracks. The track
// under the cursor is the one playing.
type Library struct {
	ctx context.Context
	app *app.App
	bus *events.Bus

	table  table.Model
	tracks []model.Track // the table's rows, in order
	gen    uint64        // latest library load; older results are dropped

	playSeq   uint64        // bumped on every cursor move; only the latest tick plays
	requested model.TrackID // the track last sent to Play
	player    events.PlayerState
	status    string // last error, shown in the status line

	width, height int
}

// libraryLoaded carries a query and the tracks it named, read together.
type libraryLoaded struct {
	gen    uint64
	tracks []model.Track
	err    error
}

// playTick fires playDelay after a cursor move.
type playTick struct {
	seq uint64
	id  model.TrackID
}

// New builds the model. Its signature matches the newUI hook in the root package.
func New(ctx context.Context, a *app.App, bus *events.Bus) tea.Model {
	t := table.New(
		table.WithColumns(columns(100)),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)
	return &Library{ctx: ctx, app: a, bus: bus, table: t}
}

func (m *Library) Init() tea.Cmd {
	return tea.Batch(WaitForEvent(m.bus), m.load())
}

// load queries the master tracks sorted by artist and reads them in one go.
func (m *Library) load() tea.Cmd {
	m.gen++
	gen, ctx, a := m.gen, m.ctx, m.app
	return func() tea.Msg {
		master := model.Master
		ids, err := a.Query(ctx, app.Filter{Role: &master}, app.Sort{Field: model.FieldArtist})
		if err != nil {
			return libraryLoaded{gen: gen, err: err}
		}
		return libraryLoaded{gen: gen, tracks: a.Tracks(ids)}
	}
}

func (m *Library) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.table.SetColumns(columns(msg.Width))
		m.table.SetWidth(msg.Width)
		m.table.SetHeight(max(msg.Height-2, 3)) // title line and status line
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "space":
			return m, TogglePause(m.ctx, m.app)
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, tea.Batch(cmd, m.selectionMoved())

	case libraryLoaded:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.err != nil {
			m.status = "library: " + msg.err.Error()
			return m, nil
		}
		return m, m.setTracks(msg.tracks)

	case playTick:
		if msg.seq != m.playSeq || msg.id == m.requested {
			return m, nil
		}
		m.requested = msg.id
		// Clear first so the queue holds only the selected track; Play alone
		// would keep inserting every track the cursor has visited.
		return m, tea.Sequence(ClearQueue(m.ctx, m.app), Play(m.ctx, m.app, msg.id))

	case PlayResult:
		if msg.Err != nil {
			m.status = "play: " + msg.Err.Error()
			m.requested = 0 // let the next move retry
		} else {
			m.status = ""
		}
		return m, nil
	case ClearQueueResult:
		if msg.Err != nil {
			m.status = "stop: " + msg.Err.Error()
		}
		return m, nil
	case TogglePauseResult:
		if msg.Err != nil {
			m.status = "pause: " + msg.Err.Error()
		}
		return m, nil

	case events.Event:
		return m, tea.Batch(m.handleEvent(msg), WaitForEvent(m.bus))
	}
	return m, nil
}

func (m *Library) handleEvent(e events.Event) tea.Cmd {
	switch e := e.(type) {
	case events.LibraryLoaded, events.TracksChanged, events.TrackRemoved:
		return m.load()
	case events.PlayerState:
		m.player = e
		m.refreshRows() // the playing marker moved or changed
	case events.LoadFailed:
		m.status = fmt.Sprintf("cannot play %s: %s", e.Path, e.Reason)
	case events.PlayerDown:
		m.status = "player restarting: " + e.Err.Error()
		m.requested = 0
	case events.Error:
		m.status = e.Op + ": " + e.Err.Error()
	}
	return nil
}

// setTracks replaces the rows, keeping the cursor on the same track when it
// is still listed, and plays whatever ends up selected.
func (m *Library) setTracks(ts []model.Track) tea.Cmd {
	keep := m.selected()
	m.tracks = ts
	m.refreshRows()
	cursor := 0
	for i, t := range ts {
		if t.ID == keep {
			cursor = i
			break
		}
	}
	if len(ts) > 0 {
		m.table.SetCursor(cursor)
	}
	return m.selectionMoved()
}

// selectionMoved schedules playback of the selected track. Any tick still
// pending is voided first, so returning to the playing row cancels a move.
func (m *Library) selectionMoved() tea.Cmd {
	m.playSeq++
	id := m.selected()
	if id == 0 || id == m.requested {
		return nil
	}
	seq := m.playSeq
	return tea.Tick(playDelay, func(time.Time) tea.Msg { return playTick{seq: seq, id: id} })
}

func (m *Library) selected() model.TrackID {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.tracks) {
		return 0
	}
	return m.tracks[i].ID
}

func (m *Library) refreshRows() {
	rows := make([]table.Row, len(m.tracks))
	for i, t := range m.tracks {
		rows[i] = m.row(t)
	}
	m.table.SetRows(rows)
}

func (m *Library) row(t model.Track) table.Row {
	s := t.Summary
	title := s.Title
	if title == "" {
		title = baseName(t.Path)
	}
	mark := "  "
	if t.ID == m.player.TrackID && !m.player.Idle {
		mark = "▶ "
		if m.player.Paused {
			mark = "‖ "
		}
	}
	bpm, _ := s.Field(model.FieldBPM)
	return table.Row{mark + title, s.Artist, s.Genre, s.Key, bpm, clock(t.Duration), oneLine(s.Comment)}
}

func (m *Library) View() tea.View {
	title := titleStyle.Render(fmt.Sprintf("Library · %d tracks", len(m.tracks)))
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, title, m.table.View(), m.statusLine()))
	v.AltScreen = true
	v.WindowTitle = "crates"
	return v
}

func (m *Library) statusLine() string {
	if m.status != "" {
		return errorStyle.Width(m.width).MaxWidth(m.width).Render(m.status)
	}
	p := m.player
	if p.Idle || p.TrackID == 0 {
		return statusStyle.Render("stopped · space pause · q quit")
	}
	name := baseName(p.Path)
	for _, t := range m.tracks {
		if t.ID == p.TrackID {
			name = t.Summary.Artist + " – " + t.Summary.Title
			break
		}
	}
	state := "▶"
	if p.Paused {
		state = "‖"
	}
	line := fmt.Sprintf("%s %s  %s / %s", state, name, clock(p.Position), clock(p.Duration))
	return statusStyle.Width(m.width).MaxWidth(m.width).Render(line)
}

// columns sizes the seven columns to fill width. Each cell has one space of
// padding on both sides.
func columns(width int) []table.Column {
	const genre, key, bpm, length = 14, 4, 5, 7
	free := width - 7*2 - genre - key - bpm - length
	titleW := max(free*35/100, 10)
	artistW := max(free*25/100, 8)
	commentW := max(free-titleW-artistW, 8)
	return []table.Column{
		{Title: "Title", Width: titleW},
		{Title: "Artist", Width: artistW},
		{Title: "Genre", Width: genre},
		{Title: "Key", Width: key},
		{Title: "BPM", Width: bpm},
		{Title: "Length", Width: length},
		{Title: "Comments", Width: commentW},
	}
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	statusStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Padding(0, 1)
)

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("240"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	return s
}

// clock formats a duration as m:ss, or h:mm:ss from an hour up; zero is
// blank (not known yet).
func clock(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// oneLine folds a multi-line comment onto one row.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
