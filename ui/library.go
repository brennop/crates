package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/fsops"
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

	sortBy int // index into sortOrders

	dialog *playlistDialog // nil when closed

	confirm     *deleteConfirm // nil when closed
	deleteMode  fsops.Mode     // delete_mode, for the confirm text
	afterDelete deletedRow     // where the cursor goes once the deleted row is gone

	width, height int
	cols          []table.Column // current widths, so row can fill its cells
}

// deletedRow is the last track deleted from the table and the row it was on.
type deletedRow struct {
	id  model.TrackID
	row int
}

// sortOrder is one step of the s key's cycle: the field Query sorts on and
// the column whose header shows the marker.
type sortOrder struct {
	field string
	col   int
	name  string
}

// sortOrders is the cycle s steps through, in column order.
var sortOrders = []sortOrder{
	{model.FieldTitle, colTitle, "title"},
	{model.FieldArtist, colArtist, "artist"},
	{model.FieldGenre, colGenre, "genre"},
	{model.FieldKey, colKey, "key"},
	{model.FieldBPM, colBPM, "BPM"},
	{"duration", colLength, "length"},
}

// defaultSort is the sortOrders entry the table starts with: artist.
const defaultSort = 1

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
	cols := columns(100, sortOrders[defaultSort].col)
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)
	return &Library{
		ctx: ctx, app: a, bus: bus, table: t, cols: cols, sortBy: defaultSort,
		deleteMode: a.Capabilities().DeleteMode,
	}
}

func (m *Library) Init() tea.Cmd {
	return tea.Batch(WaitForEvent(m.bus), m.load())
}

// load queries the master tracks in the current sort order and reads them
// in one go.
func (m *Library) load() tea.Cmd {
	m.gen++
	gen, ctx, a, field := m.gen, m.ctx, m.app, sortOrders[m.sortBy].field
	return func() tea.Msg {
		master := model.Master
		ids, err := a.Query(ctx, app.Filter{Role: &master}, app.Sort{Field: field})
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
		m.cols = columns(msg.Width, sortOrders[m.sortBy].col)
		m.table.SetColumns(m.cols)
		m.refreshRows(m.table.Cursor()) // cells are padded to the column widths
		m.table.SetWidth(msg.Width)
		m.table.SetHeight(max(msg.Height-2, 3)) // title line and status line
		return m, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.dialog != nil {
			closed, cmd := m.dialog.update(m.ctx, m.app, msg)
			if closed {
				m.dialog = nil
			}
			return m, cmd
		}
		if m.confirm != nil {
			closed, ok := m.confirm.update(msg)
			if !closed {
				return m, nil
			}
			id := m.confirm.id
			m.confirm = nil
			if !ok {
				return m, nil
			}
			return m, m.delete(id)
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "p":
			return m, m.openDialog()
		case "x":
			if i := m.table.Cursor(); i >= 0 && i < len(m.tracks) {
				m.confirm = newDeleteConfirm(m.tracks[i])
			}
			return m, nil
		case "X":
			return m, m.delete(m.selected())
		case "space":
			return m, TogglePause(m.ctx, m.app)
		case "s":
			m.sortBy = (m.sortBy + 1) % len(sortOrders)
			m.cols = columns(m.width, sortOrders[m.sortBy].col)
			m.table.SetColumns(m.cols)
			return m, m.load() // setTracks keeps the cursor on the same track
		}
		prev := m.table.Cursor()
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		m.cursorMoved(prev)
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
	case playlistDialogLoaded:
		if m.dialog != nil {
			m.dialog.loaded(msg)
		}
		return m, nil
	case AddToPlaylistResult:
		m.playlistWritten(msg.IDs, msg.PlaylistID, true, msg.Err)
		return m, nil
	case RemoveFromPlaylistResult:
		m.playlistWritten(msg.IDs, msg.PlaylistID, false, msg.Err)
		return m, nil
	case DeleteResult:
		m.deleted(msg.Summary)
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
		m.refreshRows(m.table.Cursor()) // the playing marker moved or changed
	case events.LoadFailed:
		m.status = fmt.Sprintf("cannot play %s: %s", e.Path, e.Reason)
	case events.PlayerDown:
		m.status = "player restarting: " + e.Err.Error()
		m.requested = 0
	case events.Error:
		m.status = e.Op + ": " + e.Err.Error()
	case events.PlaylistsChanged:
		if m.dialog != nil {
			return m.dialog.load(m.app)
		}
	}
	return nil
}

// openDialog opens the playlist dialog on the selected track.
func (m *Library) openDialog() tea.Cmd {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.tracks) {
		return nil
	}
	t := m.tracks[i]
	if t.MusicID == "" {
		m.status = "playlists: track is not in Music yet"
		return nil
	}
	m.dialog = newPlaylistDialog(t)
	return m.dialog.load(m.app)
}

// delete deletes track id, remembering its row so the cursor stays there
// (on the following track) after the reload.
func (m *Library) delete(id model.TrackID) tea.Cmd {
	if id == 0 {
		return nil
	}
	for i, t := range m.tracks {
		if t.ID == id {
			m.afterDelete = deletedRow{id: id, row: i}
			break
		}
	}
	return Delete(m.ctx, m.app, []model.TrackID{id})
}

// deleted reports a delete's failures in the status line. The row itself
// goes away through the TrackRemoved event's reload.
func (m *Library) deleted(sum app.DeleteSummary) {
	switch n := len(sum.Failures); {
	case n == 0:
		m.status = ""
	case n == 1:
		m.status = "delete: " + sum.Failures[0].Err.Error()
	default:
		m.status = fmt.Sprintf("delete: %d tracks failed: %s", n, sum.Failures[0].Err.Error())
	}
}

// playlistWritten routes a playlist write's result to the dialog, or to
// the status line when the dialog was not waiting on it.
func (m *Library) playlistWritten(ids []model.TrackID, pid string, add bool, err error) {
	if m.dialog != nil && m.dialog.done(ids, pid, add, err) {
		return
	}
	if err != nil {
		m.status = "playlist: " + err.Error()
	}
}

// setTracks replaces the rows, keeping the cursor on the same track when it
// is still listed, and plays whatever ends up selected.
func (m *Library) setTracks(ts []model.Track) tea.Cmd {
	keep := m.selected()
	m.tracks = ts
	cursor, found := 0, false
	for i, t := range ts {
		if t.ID == keep {
			cursor, found = i, true
			break
		}
	}
	if !found && keep != 0 && keep == m.afterDelete.id {
		cursor = max(min(m.afterDelete.row, len(ts)-1), 0)
	}
	m.refreshRows(cursor)
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

// refreshRows rebuilds every row; cursor is the row the table will select.
func (m *Library) refreshRows(cursor int) {
	rows := make([]table.Row, len(m.tracks))
	for i, t := range m.tracks {
		rows[i] = m.row(t, i == cursor)
	}
	m.table.SetRows(rows)
}

// cursorMoved rebuilds the rows the cursor left and landed on, since the
// selected row is drawn without the Key and BPM tints.
func (m *Library) cursorMoved(prev int) {
	cur := m.table.Cursor()
	if cur == prev {
		return
	}
	rows := m.table.Rows()
	for _, i := range []int{prev, cur} {
		if i >= 0 && i < len(rows) && i < len(m.tracks) {
			rows[i] = m.row(m.tracks[i], i == cur)
		}
	}
	m.table.SetRows(rows)
}

// row renders t. Each cell carries its own one-space padding, so the Key and
// BPM backgrounds fill the whole cell. The selected row stays untinted: a
// tint's closing reset would clear the selection background for the rest of
// the row.
func (m *Library) row(t model.Track, selected bool) table.Row {
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
	key := s.Key
	if c, ok := model.ParseCamelot(key); ok {
		key = c.String()
	}
	row := table.Row{mark + title, s.Artist, s.Genre, key, bpm, clock(t.Duration), oneLine(s.Comment)}
	for i := range row {
		w := 0
		if i < len(m.cols) {
			w = m.cols[i].Width
		}
		row[i] = pad(row[i], w)
	}
	if !selected && len(m.cols) > colBPM {
		if h, ok := keyHue(s.Key); ok {
			row[colKey] = fill(row[colKey], m.cols[colKey].Width, h)
		}
		if s.BPM > 0 {
			row[colBPM] = fill(row[colBPM], m.cols[colBPM].Width, bpmHue(s.BPM))
		}
	}
	return row
}

func (m *Library) View() tea.View {
	title := titleStyle.Render(fmt.Sprintf("Library · %d tracks", len(m.tracks))) +
		statusStyle.Render("sorted by "+sortOrders[m.sortBy].name+" · s to change")
	content := lipgloss.JoinVertical(lipgloss.Left, title, m.table.View(), m.statusLine())
	if m.dialog != nil {
		content = overlay(content, m.dialog.view(m.width, m.height), m.width, m.height)
	}
	if m.confirm != nil {
		content = overlay(content, m.confirm.view(m.width, m.deleteMode), m.width, m.height)
	}
	v := tea.NewView(content)
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
		return statusStyle.Render("stopped · space pause · s sort · p playlists · x delete · q quit")
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

// Column indexes.
const (
	colTitle = iota
	colArtist
	colGenre
	colKey
	colBPM
	colLength
	colComments
)

// columns sizes the seven columns to fill width and marks the sorted one
// with "▲". Widths include one space of padding on both sides, which the
// cells carry themselves (see pad) so a tinted background covers it too.
func columns(width, sorted int) []table.Column {
	const p = 2 // padding
	const genre, key, bpm, length = 14 + p, 4 + p, 5 + p, 7 + p
	free := width - genre - key - bpm - length
	titleW := max(free*35/100, 10+p)
	artistW := max(free*25/100, 8+p)
	commentW := max(free-titleW-artistW, 8+p)
	cols := []table.Column{
		{Title: " Title", Width: titleW},
		{Title: " Artist", Width: artistW},
		{Title: " Genre", Width: genre},
		{Title: " Key", Width: key},
		{Title: " BPM", Width: bpm},
		{Title: " Length", Width: length},
		{Title: " Comments", Width: commentW},
	}
	if sorted >= 0 && sorted < len(cols) {
		cols[sorted].Title += " ▲"
	}
	return cols
}

// pad fits s into a cell w wide: a space each side, truncated with "…".
// The table pads the right side out to w.
func pad(s string, w int) string {
	return " " + ansi.Truncate(s, max(w-2, 0), "…")
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	statusStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Padding(0, 1)
)

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Cell = s.Cell.Padding(0) // cells pad themselves; see columns
	s.Header = s.Header.
		Padding(0).
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
