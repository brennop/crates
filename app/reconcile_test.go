package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
)

// masters writes n master tracks.
func (e *env) masters(n int) []model.Track {
	e.t.Helper()
	out := make([]model.Track, n)
	for i := range out {
		out[i] = e.file(e.master, fmt.Sprintf("t%03d.mp3", i), tagged)
	}
	return out
}

// pass reads a fresh snapshot and runs one reconcile pass.
func (e *env) pass() {
	e.t.Helper()
	e.snapshot()
	e.app.reconcile(e.ctx)
}

func (e *env) musicIDs(ts []model.Track) (in, out int) {
	for _, t := range ts {
		if e.get(t.ID).MusicID != "" {
			in++
		} else {
			out++
		}
	}
	return in, out
}

func TestReconcileAddsAndAttaches(t *testing.T) {
	e := newEnv(t)
	ts := e.masters(3)
	// t000 is already in Music; its ID is attached, not re-added.
	known, err := e.music.Add(e.ctx, ts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	in := e.file(e.inbox, "in.mp3", tagged)
	e.pass()

	if got := e.get(ts[0].ID).MusicID; got != known {
		t.Errorf("t000 MusicID = %q, want %q", got, known)
	}
	if n, _ := e.musicIDs(ts); n != 3 {
		t.Errorf("%d of 3 master tracks in Music", n)
	}
	if adds := e.music.CallsOf(music.OpAdd); len(adds) != 3 { // the setup add, then two
		t.Errorf("adds = %d, want 3", len(adds))
	}
	if e.get(in.ID).MusicID != "" {
		t.Error("an inbox track was added to Music")
	}
	// The snapshot is patched, so a second pass on it adds nothing.
	e.app.reconcile(e.ctx)
	if adds := e.music.CallsOf(music.OpAdd); len(adds) != 3 {
		t.Errorf("second pass added again: %d adds", len(adds))
	}
}

func TestReconcileValve(t *testing.T) {
	e := newEnv(t)
	ts := e.masters(importThreshold + 1)
	mark := e.bus.mark()
	e.pass()

	ev := e.bus.waitFor(t, mark, is[events.ImportPending]).(events.ImportPending)
	if ev.Count != importThreshold+1 || e.app.PendingImport() != importThreshold+1 {
		t.Errorf("pending = %d / %d", ev.Count, e.app.PendingImport())
	}
	if len(e.music.CallsOf(music.OpAdd)) != 0 {
		t.Fatal("the valve tripped but tracks were added")
	}

	// Cancel: nothing is added, and the next fresh pass asks again.
	e.app.CancelImport()
	if e.app.PendingImport() != 0 {
		t.Error("cancel left the import pending")
	}
	e.app.reconcile(e.ctx)
	mark = e.bus.mark()
	e.pass()
	e.bus.waitFor(t, mark, is[events.ImportPending])
	if len(e.music.CallsOf(music.OpAdd)) != 0 {
		t.Fatal("added after cancel")
	}

	// Confirm: the next pass adds everything.
	e.app.ConfirmImport()
	e.app.reconcile(e.ctx)
	if in, out := e.musicIDs(ts); out != 0 {
		t.Errorf("after confirm: %d in Music, %d not", in, out)
	}
	// The confirmation is used up: a later mass gap asks again.
	e.app.recMu.Lock()
	confirmed := e.app.confirmed
	e.app.recMu.Unlock()
	if confirmed {
		t.Error("confirmation outlived the import")
	}
	if e.app.ConfirmImport(); e.app.PendingImport() != 0 {
		t.Error("confirm with nothing pending changed state")
	}
}

func TestReconcileValveBoundary(t *testing.T) {
	e := newEnv(t)
	ts := e.masters(importThreshold)
	e.pass()
	if _, out := e.musicIDs(ts); out != 0 || e.app.PendingImport() != 0 {
		t.Errorf("%d tracks at the threshold: %d left out, pending %d", importThreshold, out, e.app.PendingImport())
	}
}

func TestReconcileDeadEntries(t *testing.T) {
	e := newEnv(t)
	live := e.masters(1)[0]
	gone := e.file(e.master, "gone.mp3", tagged)
	e.inMusic(live)
	gone = e.inMusic(gone)
	// Outside master: never a dead entry of ours.
	if _, err := e.music.Add(e.ctx, "/elsewhere/x.mp3"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone.Path); err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SweepMaster(e.ctx); err != nil {
		t.Fatal(err)
	}
	if e.indexed(gone.ID) {
		t.Fatal("sweep kept the missing file")
	}
	mark := e.bus.mark()
	e.pass()

	want := events.DeadEntry{MusicID: gone.MusicID, Path: gone.Path}
	if got := e.bus.waitFor(t, mark, is[events.DeadEntry]); got != want {
		t.Errorf("event = %+v, want %+v", got, want)
	}
	if got := e.app.DeadEntries(); !slices.Equal(got, []events.DeadEntry{want}) {
		t.Errorf("DeadEntries = %+v", got)
	}
	if len(e.music.CallsOf(music.OpDelete)) != 0 {
		t.Error("a dead entry was deleted")
	}
	if _, ok := e.music.Track(gone.MusicID); !ok {
		t.Error("the dead entry left Music")
	}

	// Reported once, listed while it lasts.
	mark = e.bus.mark()
	e.pass()
	for _, ev := range e.bus.all()[mark:] {
		if is[events.DeadEntry](ev) {
			t.Error("dead entry reported twice")
		}
	}
	if len(e.app.DeadEntries()) != 1 {
		t.Error("dead entry dropped from the list")
	}
	// The file comes back: no longer dead.
	if err := os.WriteFile(gone.Path, []byte("back"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.pass()
	if len(e.app.DeadEntries()) != 0 {
		t.Errorf("DeadEntries = %+v after the file came back", e.app.DeadEntries())
	}
}

func TestReconcileHoldsWhileUnavailable(t *testing.T) {
	e := newEnv(t)
	ts := e.masters(2)
	e.snapshot()
	e.music.SetRunning(false)
	e.app.reconcile(e.ctx)
	if in, _ := e.musicIDs(ts); in != 0 {
		t.Fatalf("%d tracks added while Music was down", in)
	}
	if n := len(e.music.CallsOf(music.OpAdd)); n != 1 {
		t.Errorf("adds tried = %d, want 1 (the first one holds the rest)", n)
	}
	if e.app.MusicStatus() != model.MusicUnavailable {
		t.Errorf("status = %v", e.app.MusicStatus())
	}

	e.music.SetRunning(true)
	mark := e.bus.mark()
	e.pass()
	if in, _ := e.musicIDs(ts); in != 2 {
		t.Errorf("%d of 2 added after Music came back", in)
	}
	ev := e.bus.waitFor(t, mark, is[events.MusicStatus]).(events.MusicStatus)
	if ev.State != model.MusicAvailable {
		t.Errorf("status event = %v", ev.State)
	}
}

func TestReconcileIgnoresStaleSnapshot(t *testing.T) {
	e := newEnv(t)
	ts := e.masters(2)
	e.snapshot()
	if err := e.lib.SweepMaster(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.app.reconcile(e.ctx)
	if n := len(e.music.CallsOf(music.OpAdd)); n != 0 {
		t.Errorf("%d adds from a snapshot older than the sweep", n)
	}
	e.pass()
	if in, _ := e.musicIDs(ts); in != 2 {
		t.Errorf("%d of 2 added on a fresh snapshot", in)
	}
}

func TestReconcileCopySettingLatch(t *testing.T) {
	e := newEnv(t)
	e.masters(3)
	e.music.SetCopySetting(true)
	mark := e.bus.mark()
	e.pass()

	if n := len(e.music.CallsOf(music.OpAdd)); n != 1 {
		t.Errorf("adds = %d, want 1: the first add finds the setting on", n)
	}
	errEv := e.bus.waitFor(t, mark, is[events.Error]).(events.Error)
	if !errors.As(errEv.Err, new(model.ErrCopySettingOn)) {
		t.Errorf("error event = %v", errEv.Err)
	}
	e.bus.waitFor(t, mark, func(ev events.Event) bool {
		s, ok := ev.(events.MusicStatus)
		return ok && s.State == model.MusicCopySettingOn
	})

	// Even with the setting fixed, adds stay off until restart, and the
	// status does not flip back on the next good call.
	e.music.SetCopySetting(false)
	e.pass()
	tr := e.file(e.inbox, "p.mp3", tagged)
	e.app.Promote(e.ctx, []model.TrackID{tr.ID})
	e.app.reconcile(e.ctx)
	if n := len(e.music.CallsOf(music.OpAdd)); n != 1 {
		t.Errorf("adds = %d after the latch", n)
	}
	if e.app.MusicStatus() != model.MusicCopySettingOn {
		t.Errorf("status = %v", e.app.MusicStatus())
	}
}

func TestReconcilePromoteAndReAdd(t *testing.T) {
	e := newEnv(t)
	e.snapshot()
	tr := e.file(e.inbox, "p.mp3", tagged)
	if sum := e.app.Promote(e.ctx, []model.TrackID{tr.ID}); len(sum.Promoted) != 1 {
		t.Fatalf("promote: %+v", sum)
	}
	// The snapshot is older than nothing new: the hand-off adds it anyway.
	e.app.reconcile(e.ctx)
	id := e.get(tr.ID).MusicID
	if id == "" {
		t.Fatal("promoted track not added")
	}
	if adds := e.music.CallsOf(music.OpAdd); len(adds) != 1 || filepath.Dir(adds[0].Path) != e.master {
		t.Errorf("adds = %+v", adds)
	}

	// Deleted in Music.app: the next fresh pass clears the stale ID and
	// re-adds the file.
	if err := e.music.DeleteTrack(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	e.pass()
	if got := e.get(tr.ID).MusicID; got == "" || got == id {
		t.Errorf("MusicID after re-add = %q (old %q)", got, id)
	}
}

func TestReconcileReAddsAfterPlaylistsLost(t *testing.T) {
	e := newEnv(t)
	tr := e.inMusic(e.masters(1)[0])
	e.snapshot()
	e.fs.mu.Lock()
	e.fs.del = errors.New("disk says no")
	e.fs.mu.Unlock()
	sum := e.app.Delete(e.ctx, []model.TrackID{tr.ID})
	if len(sum.Failures) != 1 || !errors.As(sum.Failures[0].Err, new(model.ErrPlaylistsLost)) {
		t.Fatalf("delete: %+v", sum)
	}
	e.app.reconcile(e.ctx)
	if got := e.get(tr.ID).MusicID; got == "" || got == tr.MusicID {
		t.Errorf("MusicID = %q, want a new entry", got)
	}
}

func TestStartRunsFirstPass(t *testing.T) {
	e := newEnv(t)
	ts := e.masters(2)
	mark := e.bus.mark()
	if err := e.app.Start(); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Start(); err == nil {
		t.Error("second Start accepted")
	}
	e.bus.waitFor(t, mark, func(ev events.Event) bool {
		c, ok := ev.(events.TracksChanged)
		return ok && len(c.Tracks) == 1 && c.Tracks[0].ID == ts[1].ID && c.Tracks[0].MusicID != ""
	})
	if s := e.app.MusicStatus(); s != model.MusicAvailable {
		t.Errorf("status = %v", s)
	}

	// A promote after Start is added without a refresh.
	tr := e.file(e.inbox, "p.mp3", tagged)
	e.app.Promote(e.ctx, []model.TrackID{tr.ID})
	waitUntil(t, func() bool { return e.get(tr.ID).MusicID != "" })

	// Refresh: sweep, then snapshot, then a pass. A track dropped into
	// master by hand is added.
	hand := e.file(e.master, "hand.mp3", tagged)
	if err := e.app.Refresh(e.ctx); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return e.get(hand.ID).MusicID != "" })
}

func TestDisabledMusic(t *testing.T) {
	e := newEnvWith(t, func(c *config.Config) { c.Music.Enabled = false })
	e.app.music = trackedClient{Client: music.Disabled{}, a: e.app}
	e.masters(2)
	if s := e.app.MusicStatus(); s != model.MusicDisabled {
		t.Errorf("status before Start = %v", s)
	}
	mark := e.bus.mark()
	if err := e.app.Start(); err != nil {
		t.Fatal(err)
	}
	ev := e.bus.waitFor(t, mark, is[events.MusicStatus]).(events.MusicStatus)
	if ev.State != model.MusicDisabled {
		t.Errorf("status event = %v", ev.State)
	}
	if err := e.app.Refresh(e.ctx); err != nil {
		t.Errorf("Refresh: %v", err)
	}
	e.app.reconcile(e.ctx)
	if err := e.app.LaunchMusic(e.ctx); !errors.Is(err, music.ErrDisabled) {
		t.Errorf("LaunchMusic: %v", err)
	}
	if len(e.music.Calls()) != 0 {
		t.Errorf("calls to Music while disabled: %+v", e.music.Calls())
	}
}

func TestMusicStatusFollowsCalls(t *testing.T) {
	e := newEnv(t)
	e.music.SetDenied(true)
	mark := e.bus.mark()
	if err := e.app.refreshSnapshot(e.ctx); !errors.As(err, new(model.ErrAutomationDenied)) {
		t.Fatalf("err = %v", err)
	}
	e.bus.waitFor(t, mark, func(ev events.Event) bool {
		s, ok := ev.(events.MusicStatus)
		return ok && s.State == model.MusicDenied
	})
	e.music.SetDenied(false)
	e.music.SetRunning(false)
	if err := e.app.LaunchMusic(e.ctx); err != nil {
		t.Fatal(err)
	}
	if e.music.Launches() != 1 || e.app.MusicStatus() != model.MusicAvailable {
		t.Errorf("launches = %d, status = %v", e.music.Launches(), e.app.MusicStatus())
	}
}

func TestCloseStopsReconciler(t *testing.T) {
	e := newEnv(t)
	e.masters(1)
	if err := e.app.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		e.app.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
	select {
	case <-e.app.recDone:
	default:
		t.Error("reconciler still running after Close")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatal("timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
