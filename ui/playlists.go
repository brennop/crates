package ui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/model"
)

// playlistDialog lists Music's editable playlists with a checkbox each for
// one track. Toggling a box adds the track to that playlist or removes it.
type playlistDialog struct {
	id   model.TrackID
	name string // "artist – title", for the box title

	loading   bool
	gen       uint64 // latest load; older results are dropped
	playlists []model.Playlist
	member    map[string]bool // playlist ID → track is in it
	busy      map[string]bool // playlist ID → write in flight
	cursor    int
	top       int // first visible row
	err       string
}

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
	return &playlistDialog{
		id:      t.ID,
		name:    trackName(t),
		loading: true,
		member:  map[string]bool{},
		busy:    map[string]bool{},
	}
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
// optimistic state until the write's result arrives.
func (d *playlistDialog) loaded(msg playlistDialogLoaded) {
	if msg.gen != d.gen || msg.id != d.id {
		return
	}
	d.loading = false
	if msg.err != nil {
		d.err = msg.err.Error()
		return
	}
	var editable []model.Playlist
	for _, p := range msg.playlists {
		if p.Editable {
			editable = append(editable, p)
		}
	}
	member := make(map[string]bool, len(msg.members))
	for _, p := range msg.members {
		member[p.ID] = true
	}
	for pid := range d.busy {
		member[pid] = d.member[pid]
	}
	d.playlists, d.member = editable, member
	d.cursor = min(d.cursor, max(len(editable)-1, 0))
}

// update handles a key. It reports whether the dialog should close.
func (d *playlistDialog) update(ctx context.Context, a *app.App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	switch k.String() {
	case "esc", "p", "q":
		return true, nil
	case "up", "k":
		d.cursor = max(d.cursor-1, 0)
	case "down", "j":
		d.cursor = min(d.cursor+1, max(len(d.playlists)-1, 0))
	case "g", "home":
		d.cursor = 0
	case "G", "end":
		d.cursor = max(len(d.playlists)-1, 0)
	case "space", "enter":
		return false, d.toggle(ctx, a)
	}
	return false, nil
}

// toggle flips the row under the cursor and writes the change.
func (d *playlistDialog) toggle(ctx context.Context, a *app.App) tea.Cmd {
	if d.loading || d.cursor >= len(d.playlists) {
		return nil
	}
	pid := d.playlists[d.cursor].ID
	if d.busy[pid] {
		return nil
	}
	add := !d.member[pid]
	d.member[pid] = add
	d.busy[pid] = true
	d.err = ""
	ids := []model.TrackID{d.id}
	if add {
		return AddToPlaylist(ctx, a, ids, pid)
	}
	return RemoveFromPlaylist(ctx, a, ids, pid)
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

var (
	dialogStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	dialogTitleStyle = lipgloss.NewStyle().Bold(true)
	dialogErrStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dialogHintStyle  = lipgloss.NewStyle().Faint(true)
)

// view renders the box to fit a width × height screen.
func (d *playlistDialog) view(width, height int) string {
	const chrome = 4 // border and padding
	inner := lipgloss.Width("Playlists · " + d.name)
	for _, p := range d.playlists {
		inner = max(inner, lipgloss.Width("[x] "+p.Name))
	}
	inner = min(max(inner, 32), max(width-chrome-4, 10), 60)
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }

	lines := []string{dialogTitleStyle.Render(fit("Playlists · " + d.name)), ""}
	switch {
	case d.loading:
		lines = append(lines, dialogHintStyle.Render("Loading…"))
	case len(d.playlists) == 0:
		lines = append(lines, dialogHintStyle.Render("No editable playlists in Music."))
	default:
		visible := max(height-6, 1)
		if d.err != "" {
			visible = max(visible-1, 1)
		}
		if d.cursor < d.top {
			d.top = d.cursor
		}
		if d.cursor >= d.top+visible {
			d.top = d.cursor - visible + 1
		}
		d.top = max(min(d.top, len(d.playlists)-visible), 0)
		selected := tableStyles().Selected
		for i := d.top; i < min(d.top+visible, len(d.playlists)); i++ {
			p := d.playlists[i]
			box := "[ ]"
			switch {
			case d.busy[p.ID]:
				box = "[…]"
			case d.member[p.ID]:
				box = "[x]"
			}
			row := fit(box + " " + p.Name)
			if i == d.cursor {
				row = selected.Width(inner).Render(row)
			}
			lines = append(lines, row)
		}
	}
	if d.err != "" {
		lines = append(lines, dialogErrStyle.Render(fit(d.err)))
	}
	lines = append(lines, "", dialogHintStyle.Render(fit("space toggle · esc close")))
	return dialogStyle.Render(strings.Join(lines, "\n"))
}

// overlay draws box centered over base.
func overlay(base, box string, width, height int) string {
	x := max((width-lipgloss.Width(box))/2, 0)
	y := max((height-lipgloss.Height(box))/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}
