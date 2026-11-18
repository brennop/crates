package ui

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	zone "github.com/lrstanley/bubblezone/v2"
)

// exec runs cmd and every command it batches or sequences, in order, and
// returns the leaf messages.
func exec(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	switch v := reflect.ValueOf(msg); {
	case v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]():
		var out []tea.Msg
		for i := range v.Len() {
			c, _ := v.Index(i).Interface().(tea.Cmd)
			out = append(out, exec(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// feed sends msg to m and returns the messages its command produced,
// leaving out the event pump, which would block.
func feed(t *testing.T, m *Library, msg tea.Msg) []tea.Msg {
	t.Helper()
	_, cmd := m.Update(msg)
	return exec(cmd)
}

func find[T any](msgs []tea.Msg) (T, bool) {
	for _, msg := range msgs {
		if v, ok := msg.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func TestLibraryTablePlaysSelection(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	b := e.file(e.master, "b.mp3")
	e.file(e.inbox, "c.mp3") // inbox tracks are not in the Library
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})

	loaded, ok := find[libraryLoaded](exec(m.load()))
	if !ok {
		t.Fatal("load returned no libraryLoaded")
	}
	if got := len(loaded.tracks); got != 2 {
		t.Fatalf("loaded %d tracks, want the 2 master tracks", got)
	}
	tick, ok := find[playTick](feed(t, m, loaded))
	if !ok || tick.id != m.selected() {
		t.Fatalf("no play tick for the first row: %+v", tick)
	}
	first := tick.id

	play, ok := find[PlayResult](feed(t, m, tick))
	if !ok || play.Err != nil || play.ID != first {
		t.Fatalf("play = %+v, %v", play, ok)
	}
	if q := e.app.Queue(); !slices.Equal(q.IDs, []model.TrackID{first}) || q.Playing != first {
		t.Fatalf("queue = %+v, want only %d playing", q, first)
	}

	// Down moves to the other track and plays it, replacing the queue.
	tick, ok = find[playTick](feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown}))
	if !ok || tick.id == first {
		t.Fatalf("down: tick = %+v, %v", tick, ok)
	}
	second := tick.id
	if !slices.Contains([]model.TrackID{a.ID, b.ID}, second) {
		t.Fatalf("second = %d", second)
	}
	feed(t, m, tick)
	if q := e.app.Queue(); !slices.Equal(q.IDs, []model.TrackID{second}) || q.Playing != second {
		t.Fatalf("queue = %+v, want only %d playing", q, second)
	}

	// A move that comes back before the tick fires plays nothing.
	upTick, _ := find[playTick](feed(t, m, tea.KeyPressMsg{Code: tea.KeyUp}))
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if msgs := feed(t, m, upTick); len(msgs) != 0 {
		t.Fatalf("stale tick ran %v", msgs)
	}

	// A reload keeps the cursor on the same track and does not replay it.
	if msgs := feed(t, m, loaded); len(msgs) != 0 || m.selected() != second {
		t.Fatalf("reload: msgs %v, selected %d, want %d", msgs, m.selected(), second)
	}

	view := m.View().Content
	for _, col := range []string{"Title", "Artist", "Genre", "Key", "BPM", "Length", "Comments", "Library · 2 tracks"} {
		if !strings.Contains(view, col) {
			t.Errorf("view lacks %q", col)
		}
	}
}

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                             "",
		59 * time.Second:              "0:59",
		4*time.Minute + 5*time.Second: "4:05",
		time.Hour + 2*time.Minute + 3*time.Second: "1:02:03",
	} {
		if got := clock(d); got != want {
			t.Errorf("clock(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestLibrarySortCycles(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "a.mp3")
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})

	if !strings.Contains(m.View().Content, "sorted by artist") {
		t.Fatal("default sort is not artist")
	}
	for i := range sortOrders {
		o := sortOrders[(defaultSort+i)%len(sortOrders)]
		for c, col := range m.cols {
			if got := strings.HasSuffix(col.Title, "▲"); got != (c == o.col) {
				t.Errorf("sort %s: column %q marked %t", o.name, col.Title, got)
			}
		}
		if !strings.Contains(m.View().Content, "sorted by "+o.name) {
			t.Errorf("view does not name sort %s", o.name)
		}
		loaded, ok := find[libraryLoaded](exec(m.load()))
		if !ok || loaded.err != nil || len(loaded.tracks) != 1 {
			t.Fatalf("sort %s: load = %+v", o.name, loaded)
		}
		msgs := feed(t, m, tea.KeyPressMsg{Code: 's', Text: "s"})
		if _, ok := find[libraryLoaded](msgs); !ok {
			t.Fatalf("s after %s did not reload", o.name)
		}
	}
	if m.sortBy != defaultSort {
		t.Fatalf("cycle ended on %d, want back at artist", m.sortBy)
	}
}

// reload loads the library and feeds the result to m.
func reload(t *testing.T, m *Library) {
	t.Helper()
	loaded, ok := find[libraryLoaded](exec(m.load()))
	if !ok {
		t.Fatal("load returned no libraryLoaded")
	}
	feed(t, m, loaded)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestLibraryDeleteCancel(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	m := libraryWith(t, e)

	if msgs := feed(t, m, key("x")); len(msgs) != 0 || m.confirm == nil {
		t.Fatalf("x: msgs %v, confirm %v", msgs, m.confirm)
	}
	if !strings.Contains(m.View().Content, "Delete ·") {
		t.Error("view lacks the confirm dialog")
	}
	feed(t, m, key("z")) // ignored
	if m.confirm == nil {
		t.Fatal("an unbound key closed the dialog")
	}
	if _, ok := find[DeleteResult](feed(t, m, key("n"))); ok {
		t.Fatal("n deleted")
	}
	if m.confirm != nil || !exists(a.Path) {
		t.Fatalf("after n: confirm %v, file there %t", m.confirm, exists(a.Path))
	}
}

func TestLibraryDeleteConfirm(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	m := libraryWith(t, e)

	feed(t, m, key("x"))
	res, ok := find[DeleteResult](feed(t, m, key("y")))
	if !ok || !slices.Equal(res.Summary.Deleted, []model.TrackID{a.ID}) {
		t.Fatalf("y: %+v, %v", res, ok)
	}
	if m.confirm != nil || exists(a.Path) {
		t.Fatalf("after y: confirm %v, file there %t", m.confirm, exists(a.Path))
	}
}

func TestLibraryDeleteWithoutAsking(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.master, "a.mp3")
	m := libraryWith(t, e)

	res, ok := find[DeleteResult](feed(t, m, key("X")))
	if !ok || !slices.Equal(res.Summary.Deleted, []model.TrackID{a.ID}) || m.confirm != nil {
		t.Fatalf("X: %+v, %v, confirm %v", res, ok, m.confirm)
	}
	if exists(a.Path) {
		t.Fatal("file still there")
	}
}

func TestLibraryDeleteKeepsRow(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		e.file(e.master, n)
	}
	m := libraryWith(t, e)
	feed(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	next := m.tracks[2].ID

	res, _ := find[DeleteResult](feed(t, m, key("X")))
	m.Update(res)
	reload(t, m)
	if len(m.tracks) != 2 || m.cursor != 1 || m.selected() != next {
		t.Fatalf("after delete: %d rows, cursor %d on %d, want row 1 on %d",
			len(m.tracks), m.cursor, m.selected(), next)
	}

	// Deleting the last row lands on the new last row.
	res, _ = find[DeleteResult](feed(t, m, key("X")))
	m.Update(res)
	reload(t, m)
	if len(m.tracks) != 1 || m.cursor != 0 {
		t.Fatalf("after deleting the last row: %d rows, cursor %d", len(m.tracks), m.cursor)
	}
}

func TestScanProgressSpins(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "a.mp3")
	m := libraryWith(t, e)
	// handleEvent, not Update, so the event pump does not run and block.
	report := func(p events.ScanProgress) []tea.Msg { return exec(m.handleEvent(p)) }

	// A sweep that finds nothing to read shows nothing and does not spin.
	if msgs := report(events.ScanProgress{Scanned: 4, Total: 4}); len(msgs) != 0 || m.spinning {
		t.Fatalf("a no-change sweep spins: %v", msgs)
	}
	if strings.Contains(m.View().Content, "scanning") {
		t.Fatal("a no-change sweep shows as a scan")
	}

	tick, ok := find[spinner.TickMsg](report(events.ScanProgress{Scanned: 1, Total: 4}))
	if !ok {
		t.Fatal("scan did not start the spinner")
	}
	if v := m.View().Content; !strings.Contains(v, "scanning") || !strings.Contains(v, "1/4") {
		t.Errorf("title lacks the scan:\n%s", v)
	}
	if _, cmd := m.Update(tick); cmd == nil {
		t.Fatal("spinner stopped mid-scan")
	}

	// A second report while spinning must not start another tick loop.
	if msgs := report(events.ScanProgress{Scanned: 2, Total: 4}); len(msgs) != 0 {
		t.Fatalf("a second report started a second spinner: %v", msgs)
	}

	report(events.ScanProgress{Scanned: 4, Total: 4, Done: true})
	if strings.Contains(m.View().Content, "scanning") {
		t.Error("finished scan still shown")
	}
	if _, cmd := m.Update(tick); cmd != nil || m.spinning {
		t.Error("spinner kept ticking after the scan")
	}
}

func TestPlayingLine(t *testing.T) {
	plain := func(s string) string { return ansi.Strip(s) }
	wide := plain(playingLine("▶ Artist – Title", 0, 4*time.Minute, 100, nil))
	// The bar fills everything the name and the times leave:
	// 100 - 2 padding - 16 name - 2 gap - "0:00 " - " 4:00" = 70.
	if lipgloss.Width(wide) != 100 || !strings.HasPrefix(wide, " ▶ Artist – Title  0:00 ") ||
		!strings.HasSuffix(wide, "0:00 "+strings.Repeat("░", 70)+" 4:00 ") {
		t.Errorf("wide line = %q", wide)
	}
	half := plain(playingLine("▶ x", 2*time.Minute, 4*time.Minute, 100, nil))
	if lipgloss.Width(half) != 100 || !strings.Contains(half, "2:00 "+strings.Repeat("█", 42)+strings.Repeat("░", 41)+" 4:00") {
		t.Errorf("half-way line = %q", half)
	}
	// Narrow, or the length not known yet: no bar.
	for _, line := range []string{
		plain(playingLine("▶ x", time.Minute, 4*time.Minute, 40, nil)),
		plain(playingLine("▶ x", time.Minute, 0, 100, nil)),
	} {
		if strings.ContainsAny(line, "█░") || !strings.Contains(line, "1:00 / ") {
			t.Errorf("line = %q, want no bar", line)
		}
	}
	// A long name is cut, never the bar or the times.
	long := plain(playingLine("▶ "+strings.Repeat("Name ", 40), time.Minute, 4*time.Minute, 80, nil))
	if lipgloss.Width(long) != 80 || !strings.Contains(long, "…") || !strings.HasSuffix(long, " 4:00 ") {
		t.Errorf("long line = %q", long)
	}
}

// waitZone renders m and waits for bubblezone, which records zones on its
// own goroutine, to know where id is.
func waitZone(t *testing.T, m *Library, id string) *zone.ZoneInfo {
	t.Helper()
	m.View()
	for range 100 {
		if z := m.zones.Get(id); !z.IsZero() {
			return z
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("zone %q never recorded", id)
	return nil
}

func TestClickOnBarSeeks(t *testing.T) {
	e := newEnv(t)
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m.player = events.PlayerState{TrackID: 1, Path: "/x/a.mp3", Position: time.Minute, Duration: 4 * time.Minute}

	// The zone is where the bar is drawn.
	z := waitZone(t, m, zoneBar)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if z.StartY != z.EndY || z.StartY != len(lines)-1 {
		t.Fatalf("bar zone %+v, want on the last of %d lines", z, len(lines))
	}
	line := []rune(lines[z.StartY])
	if line[z.StartX-1] != ' ' || line[z.StartX] != '█' || line[z.EndX] != '░' || line[z.EndX+1] != ' ' {
		t.Fatalf("bar zone %+v on %q", z, lines[z.StartY])
	}

	click := func(x, y int) tea.Msg {
		_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if cmd == nil {
			return nil
		}
		return cmd()
	}
	if _, ok := click(z.StartX, z.StartY).(SeekResult); !ok {
		t.Error("click on the bar did not seek")
	}
	for _, at := range [][2]int{{z.StartX - 1, z.StartY}, {z.EndX + 1, z.StartY}} {
		if msg := click(at[0], at[1]); msg != nil {
			t.Errorf("click at %v, off the bar, gave %T", at, msg)
		}
	}
	m.dialog = &playlistDialog{}
	if msg := click(z.StartX, z.StartY); msg != nil {
		t.Errorf("click under an open dialog gave %T", msg)
	}
}

func TestClickOnRowSelectsIt(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		e.file(e.master, name)
	}
	m := New(e.ctx, e.app, e.bus).(*Library)
	feed(t, m, tea.WindowSizeMsg{Width: 120, Height: 20})
	loaded, ok := find[libraryLoaded](exec(m.load()))
	if !ok {
		t.Fatal("load returned no libraryLoaded")
	}
	feed(t, m, loaded)

	z := waitZone(t, m, zoneTable)
	header := z.EndY - z.StartY + 1 - m.table.Height()
	click := func(y int) []tea.Msg {
		return feed(t, m, tea.MouseClickMsg{X: 5, Y: y, Button: tea.MouseLeft})
	}
	tick, ok := find[playTick](click(z.StartY + header + 2))
	if m.cursor != 2 || !ok || tick.id != m.selected() {
		t.Fatalf("click on the third row: cursor %d, tick %+v", m.cursor, tick)
	}
	// The header and the space under the last row select nothing.
	for _, y := range []int{z.StartY, z.StartY + header + 3} {
		click(y)
		if m.cursor != 2 {
			t.Errorf("click at y=%d moved the cursor to %d", y, m.cursor)
		}
	}
}
