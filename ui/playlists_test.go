package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// libraryWith builds a Library over e and loads its tracks.
func libraryWith(t *testing.T, e *env) *Library {
	t.Helper()
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	loaded, ok := find[libraryLoaded](exec(m.load()))
	if !ok || loaded.err != nil {
		t.Fatalf("load = %+v", loaded)
	}
	feed(t, m, loaded)
	return m
}

func TestPlaylistDialog(t *testing.T) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3"))
	e.music.AddPlaylist(model.Playlist{ID: "p1", Name: "Warmup", Editable: true}, a.MusicID)
	e.music.AddPlaylist(model.Playlist{ID: "p2", Name: "Peak", Editable: true})
	e.music.AddPlaylist(model.Playlist{ID: "p3", Name: "Smart", Editable: false})
	noErr(t, "refresh", run[RefreshResult](t, Refresh(e.ctx, e.app)).Err)
	m := libraryWith(t, e)

	msgs := feed(t, m, key("p"))
	loaded, ok := find[playlistDialogLoaded](msgs)
	if m.dialog == nil || !ok {
		t.Fatalf("p did not open and load the dialog: %v", msgs)
	}
	feed(t, m, loaded)
	view := m.View().Content
	for _, want := range []string{"Playlists · Artist – Title", "[x] Warmup", "[ ] Peak", "space toggle"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Smart") {
		t.Error("view lists the non-editable playlist")
	}
	var order []string
	for _, it := range m.dialog.list.Items() {
		order = append(order, it.FilterValue())
	}
	peak := slices.Index(order, "Peak")

	// Move to Peak and toggle it on; space must not pause.
	for range peak {
		feed(t, m, key("down"))
	}
	msgs = feed(t, m, key("space"))
	if _, ok := find[TogglePauseResult](msgs); ok {
		t.Fatal("space paused playback with the dialog open")
	}
	if !strings.Contains(m.View().Content, "[…] Peak") {
		t.Error("busy row is not marked")
	}
	add, ok := find[AddToPlaylistResult](msgs)
	if !ok || add.Err != nil {
		t.Fatalf("add = %+v, %v", add, ok)
	}
	feed(t, m, add)
	if !slices.Contains(e.music.Members("p2"), a.MusicID) {
		t.Fatalf("p2 members = %v, want %s", e.music.Members("p2"), a.MusicID)
	}
	if !strings.Contains(m.View().Content, "[x] Peak") {
		t.Error("Peak is not checked after the add")
	}

	// PlaylistsChanged reloads the dialog.
	msgs = feed(t, m, events.PlaylistsChanged{})
	reload, ok := find[playlistDialogLoaded](msgs)
	if !ok {
		t.Fatal("PlaylistsChanged did not reload the dialog")
	}
	feed(t, m, reload)

	// Toggle it off.
	rm, ok := find[RemoveFromPlaylistResult](feed(t, m, key("space")))
	if !ok || rm.Err != nil {
		t.Fatalf("remove = %+v, %v", rm, ok)
	}
	feed(t, m, rm)
	if slices.Contains(e.music.Members("p2"), a.MusicID) {
		t.Fatalf("p2 still holds the track: %v", e.music.Members("p2"))
	}
	if !strings.Contains(m.View().Content, "[ ] Peak") {
		t.Error("Peak is still checked after the remove")
	}

	// A failed write reverts the box and shows the error.
	e.music.Fail(music.OpPlAdd, errors.New("boom"))
	add, ok = find[AddToPlaylistResult](feed(t, m, key("space")))
	if !ok || add.Err == nil {
		t.Fatalf("failing add = %+v, %v", add, ok)
	}
	feed(t, m, add)
	view = m.View().Content
	if !strings.Contains(view, "[ ] Peak") || !strings.Contains(view, "boom") {
		t.Errorf("failed add not reverted or not reported:\n%s", view)
	}

	// esc closes it.
	feed(t, m, key("esc"))
	if m.dialog != nil || strings.Contains(m.View().Content, "Playlists ·") {
		t.Fatal("esc did not close the dialog")
	}
}

func TestPlaylistDialogFilter(t *testing.T) {
	e := newEnv(t)
	a := e.inMusic(e.file(e.master, "a.mp3"))
	for _, n := range []string{"Warmup", "Peak", "Closing"} {
		e.music.AddPlaylist(model.Playlist{ID: n, Name: n, Editable: true})
	}
	noErr(t, "refresh", run[RefreshResult](t, Refresh(e.ctx, e.app)).Err)
	m := libraryWith(t, e)
	loaded, _ := find[playlistDialogLoaded](feed(t, m, key("p")))
	feed(t, m, loaded)

	// Typing the filter must not close the dialog on p or q.
	for _, k := range []string{"/", "p", "e", "a"} {
		// Feed the filter results back, as the program would.
		if fm, ok := find[list.FilterMatchesMsg](feed(t, m, key(k))); ok {
			feed(t, m, fm)
		}
	}
	if m.dialog == nil {
		t.Fatal("typing the filter closed the dialog")
	}
	view := m.View().Content
	if !strings.Contains(view, "/pea") || strings.Contains(view, "Warmup") {
		t.Fatalf("filter not applied:\n%s", view)
	}
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // accept the filter
	add, ok := find[AddToPlaylistResult](feed(t, m, key("space")))
	if !ok || add.PlaylistID != "Peak" {
		t.Fatalf("space on the filtered row = %+v, %v", add, ok)
	}
	feed(t, m, add)
	if !slices.Contains(e.music.Members("Peak"), a.MusicID) {
		t.Fatal("track not added to Peak")
	}

	// esc clears the filter first, then closes.
	feed(t, m, key("esc"))
	if m.dialog == nil || !strings.Contains(m.View().Content, "Warmup") {
		t.Fatal("esc did not just clear the filter")
	}
	feed(t, m, key("esc"))
	if m.dialog != nil {
		t.Fatal("second esc did not close the dialog")
	}
}

func TestPlaylistDialogQClosesNotQuits(t *testing.T) {
	e := newEnv(t)
	e.inMusic(e.file(e.master, "a.mp3"))
	m := libraryWith(t, e)
	feed(t, m, key("p"))
	_, cmd := m.Update(key("q"))
	if m.dialog != nil || cmd != nil {
		t.Fatalf("q: dialog %v, cmd %v; want closed and no quit", m.dialog, cmd)
	}
}

func TestPlaylistDialogNeedsMusic(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "a.mp3")
	m := libraryWith(t, e)
	feed(t, m, key("p"))
	if m.dialog != nil {
		t.Fatal("dialog opened for a track not in Music")
	}
	if !strings.Contains(m.status, "not in Music") {
		t.Errorf("status = %q", m.status)
	}
}

func TestPlaylistDialogEmpty(t *testing.T) {
	e := newEnv(t)
	e.inMusic(e.file(e.master, "a.mp3"))
	m := libraryWith(t, e)
	loaded, _ := find[playlistDialogLoaded](feed(t, m, key("p")))
	feed(t, m, loaded)
	if v := m.View().Content; !strings.Contains(v, "No editable playlists in Music.") {
		t.Errorf("view:\n%s", v)
	}
}

func TestPlaylistDialogPromotesInboxTrack(t *testing.T) {
	e := newEnv(t)
	e.music.AddPlaylist(model.Playlist{ID: "p1", Name: "Warmup", Editable: true})
	e.music.AddPlaylist(model.Playlist{ID: "p2", Name: "Peak", Editable: true})
	noErr(t, "refresh", run[RefreshResult](t, Refresh(e.ctx, e.app)).Err)
	c := e.file(e.inbox, "c.mp3")
	m := libraryWith(t, e)
	feed(t, m, key("i"))
	reload(t, m)

	loaded, ok := find[playlistDialogLoaded](feed(t, m, key("p")))
	if m.dialog == nil || !ok {
		t.Fatal("p did not open the dialog on an inbox track")
	}
	feed(t, m, loaded)
	feed(t, m, key("down")) // Peak
	if msgs := feed(t, m, key("space")); len(msgs) != 0 {
		t.Fatalf("toggle on an inbox track wrote: %v", msgs)
	}
	if !strings.Contains(m.View().Content, "esc promote") {
		t.Errorf("hints do not say esc promotes:\n%s", m.View().Content)
	}
	// A playlist reload keeps the pick.
	feed(t, m, find2[playlistDialogLoaded](t, exec(m.dialog.load(e.app))))

	res, ok := find[PromoteResult](feed(t, m, key("esc")))
	if m.dialog != nil || !ok || !slices.Equal(res.Summary.Promoted, []model.TrackID{c.ID}) {
		t.Fatalf("esc: dialog %v, promote %+v", m.dialog, res)
	}
	feed(t, m, res)

	// The reconciler (not running here) adds the track to Music; the
	// TracksChanged that carries its Music ID sends the playlist add.
	if msgs := exec(m.handleEvent(events.TracksChanged{Tracks: e.app.Tracks([]model.TrackID{c.ID})})); len(msgs) != 1 {
		t.Fatalf("before the Music ID: %v, want only the reload", msgs)
	}
	tr := e.inMusic(e.app.Tracks([]model.TrackID{c.ID})[0])
	add, ok := find[AddToPlaylistResult](exec(m.handleEvent(events.TracksChanged{Tracks: []model.Track{tr}})))
	if !ok || add.Err != nil || add.PlaylistID != "p2" {
		t.Fatalf("add = %+v, %v", add, ok)
	}
	if !slices.Equal(e.music.Members("p2"), []string{tr.MusicID}) || len(e.music.Members("p1")) != 0 {
		t.Errorf("members p1 %v, p2 %v", e.music.Members("p1"), e.music.Members("p2"))
	}
	// A second TracksChanged adds nothing again.
	if _, ok := find[AddToPlaylistResult](exec(m.handleEvent(events.TracksChanged{Tracks: []model.Track{tr}}))); ok {
		t.Error("added twice")
	}
}

func find2[T any](t *testing.T, msgs []tea.Msg) T {
	t.Helper()
	v, ok := find[T](msgs)
	if !ok {
		t.Fatalf("no %T in %v", v, msgs)
	}
	return v
}

func TestPlaylistDialogRefusesUnpromotable(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "taken.mp3")
	e.file(e.inbox, "taken.mp3")
	m := libraryWith(t, e)
	feed(t, m, key("i"))
	reload(t, m)
	if msgs := feed(t, m, key("p")); len(msgs) != 0 || m.dialog != nil {
		t.Fatalf("p on a collision opened the dialog: %v", msgs)
	}
	if !strings.Contains(m.status, "already exists in master") {
		t.Errorf("status = %q", m.status)
	}
}
