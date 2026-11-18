package ui

import (
	"context"
	"fmt"
	"io"

	binding "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/model"
)

// playlistDialog lists Music's editable playlists with a checkbox each for
// one track. Toggling a box adds the track to that playlist or removes it.
// For an inbox track (promote set) toggling writes nothing: the boxes are
// the playlists to add the track to once closing promotes it (see chosen).
// The rows are a list.Model, which brings paging and / to filter by name.
type playlistDialog struct {
	id      model.TrackID
	name    string // "artist – title", for the box title
	promote bool   // an inbox track: boxes are only collected

	loading bool
	gen     uint64 // latest load; older results are dropped
	list    list.Model
	member  map[string]bool // playlist ID → track is in it
	busy    map[string]bool // playlist ID → write in flight
	err     string
	inner   int // box width inside border and padding; see layout
}

// playlistItem is one row of the dialog's list.
type playlistItem model.Playlist

func (p playlistItem) FilterValue() string { return p.Name }

// playlistDialogLoaded carries the playlists and the track's memberships,
// read together.
type playlistDialogLoaded struct {
	gen       uint64
	id        model.TrackID
	playlists []model.Playlist
	members   []model.Playlist
	err       error
}

func newPlaylistDialog(t model.Track) *playlistDialog {
	d := &playlistDialog{
		id:      t.ID,
		name:    trackName(t),
		loading: true,
		member:  map[string]bool{},
		busy:    map[string]bool{},
	}
	l := list.New(nil, playlistDelegate{d}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowFilter(false) // view draws the filter line itself, under the box title
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
	l.Styles.PaginationStyle = lipgloss.NewStyle()
	l.FilterInput = newPromptInput("/", "", 0)
	d.list = l
	return d
}

// playlistDelegate draws a row as a checkbox and the playlist name, with
// the characters a filter matched underlined.
type playlistDelegate struct{ d *playlistDialog }

func (playlistDelegate) Height() int                         { return 1 }
func (playlistDelegate) Spacing() int                        { return 0 }
func (playlistDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (pd playlistDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	p, ok := item.(playlistItem)
	if !ok {
		return
	}
	box := "[ ]"
	switch {
	case pd.d.busy[p.ID]:
		box = "[…]"
	case pd.d.member[p.ID]:
		box = "[x]"
	}
	// The selected row styles every run with its background, so a run's
	// reset cannot clear it partway along the row.
	base := lipgloss.NewStyle()
	if index == m.Index() {
		base = tableStyles().Selected
	}
	name := lipgloss.StyleRunes(p.Name, m.MatchesForItem(index), base.Underline(true), base)
	row := fitLine(base.Render(box+" ")+name, m.Width())
	fmt.Fprint(w, base.Width(m.Width()).Render(row))
}

// load reads the playlists and the track's memberships.
func (d *playlistDialog) load(a *app.App) tea.Cmd {
	d.gen++
	gen, id := d.gen, d.id
	return func() tea.Msg {
		ms, err := a.Memberships(id)
		return playlistDialogLoaded{gen: gen, id: id, playlists: a.Playlists(), members: ms, err: err}
	}
}

// loaded applies a load result. Rows with a write in flight keep their
// optimistic state until the write's result arrives. The command refilters
// the rows when a filter is set.
func (d *playlistDialog) loaded(msg playlistDialogLoaded) tea.Cmd {
	if msg.gen != d.gen || msg.id != d.id {
		return nil
	}
	d.loading = false
	if msg.err != nil {
		d.err = msg.err.Error()
		return nil
	}
	var editable []list.Item
	for _, p := range msg.playlists {
		if p.Editable {
			editable = append(editable, playlistItem(p))
		}
	}
	member := make(map[string]bool, len(msg.members))
	for _, p := range msg.members {
		member[p.ID] = true
	}
	for pid := range d.busy {
		member[pid] = d.member[pid]
	}
	if !d.promote { // an inbox track's boxes are the user's picks
		d.member = member
	}
	cmd := d.list.SetItems(editable)
	if n := len(d.list.VisibleItems()); d.list.Index() >= n {
		d.list.Select(max(n-1, 0))
	}
	return cmd
}

// update handles a key. It reports whether the dialog should close. While
// a filter is being typed every key goes to it, and esc clears a filter
// before it closes the dialog.
func (d *playlistDialog) update(ctx context.Context, a *app.App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch {
	case d.list.SettingFilter():
	case d.list.IsFiltered() && binding.Matches(k, d.list.KeyMap.ClearFilter):
	case binding.Matches(k, dialogKeys.Close):
		return true, nil
	case binding.Matches(k, dialogKeys.Toggle):
		return false, d.toggle(ctx, a)
	}
	return false, d.updateList(k)
}

// updateList passes msg to the list: its keys, and its own messages such
// as filter results.
func (d *playlistDialog) updateList(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.list, cmd = d.list.Update(msg)
	return cmd
}

// toggle flips the selected row and writes the change.
func (d *playlistDialog) toggle(ctx context.Context, a *app.App) tea.Cmd {
	p, ok := d.list.SelectedItem().(playlistItem)
	if d.loading || !ok {
		return nil
	}
	pid := p.ID
	if d.busy[pid] {
		return nil
	}
	add := !d.member[pid]
	d.member[pid] = add
	if d.promote {
		return nil
	}
	d.busy[pid] = true
	d.err = ""
	ids := []model.TrackID{d.id}
	if add {
		return AddToPlaylist(ctx, a, ids, pid)
	}
	return RemoveFromPlaylist(ctx, a, ids, pid)
}

// chosen lists the checked playlists, in list order.
func (d *playlistDialog) chosen() []string {
	var pids []string
	for _, it := range d.list.Items() {
		if p, ok := it.(playlistItem); ok && d.member[p.ID] {
			pids = append(pids, p.ID)
		}
	}
	return pids
}

// done applies a write's result. It reports false when the dialog was not
// waiting on that write, so the caller can report an error elsewhere.
func (d *playlistDialog) done(ids []model.TrackID, pid string, add bool, err error) bool {
	if len(ids) != 1 || ids[0] != d.id || !d.busy[pid] {
		return false
	}
	delete(d.busy, pid)
	if err != nil {
		d.member[pid] = !add
		d.err = err.Error()
	}
	return true
}

// layout fits the box to a width × height screen: its inner width, and the
// list sized to the rows that fit, so paging follows the screen. Call it on
// every change to the screen size, the rows, the filter or the error line.
func (d *playlistDialog) layout(width, height int) {
	items := d.list.Items()
	inner := max(lipgloss.Width(d.title()), lipgloss.Width(hints(0, d.hints()...)))
	for _, it := range items {
		inner = max(inner, lipgloss.Width("[x] "+it.FilterValue()))
	}
	d.inner = dialogWidth(inner, width)

	rows := max(height-6, 1) // border, title, blank, blank, hints
	if d.err != "" {
		rows = max(rows-1, 1)
	}
	if d.list.FilterState() != list.Unfiltered {
		rows = max(rows-1, 1)
	}
	// Shrink to the rows when they all fit, with no page dots, since the
	// list pads itself to its height.
	fits := len(items) <= rows
	if fits {
		rows = max(len(items), 1)
	}
	d.list.SetShowPagination(!fits)
	d.list.SetSize(d.inner, rows)
}

// view renders the box laid out for a width × height screen, with spin as
// the spinner frame while it loads.
func (d *playlistDialog) view(width, height int, spin string) string {
	d.layout(width, height)
	var lines []string
	switch {
	case d.loading:
		lines = append(lines, dialogHintStyle.Render(spin+" Loading…"))
	case len(d.list.Items()) == 0:
		lines = append(lines, dialogHintStyle.Render("No editable playlists in Music."))
	default:
		if d.list.FilterState() != list.Unfiltered {
			lines = append(lines, d.list.FilterInput.View())
		}
		lines = append(lines, d.list.View())
	}
	if d.err != "" {
		lines = append(lines, dialogErrStyle.Render(fitLine(d.err, d.inner)))
	}
	return dialogBox(d.inner, d.title(), lines, d.hints()...)
}

func (d *playlistDialog) title() string { return "Playlists · " + d.name }

// hints are the keys the dialog's hint line lists, which change while a
// filter is typed or set.
func (d *playlistDialog) hints() []binding.Binding {
	lk := d.list.KeyMap
	switch {
	case d.list.SettingFilter():
		return []binding.Binding{lk.AcceptWhileFiltering, lk.CancelWhileFiltering}
	case d.list.IsFiltered():
		// esc clears the filter, so name another close key.
		clear, close := lk.ClearFilter, dialogKeys.Close
		clear.SetHelp("esc", "clear")
		close.SetHelp("q", "close")
		return []binding.Binding{dialogKeys.Toggle, clear, close}
	}
	close := dialogKeys.Close
	if d.promote && len(d.chosen()) > 0 {
		close.SetHelp("esc", "promote")
	}
	return []binding.Binding{dialogKeys.Toggle, lk.Filter, close}
}
