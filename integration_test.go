//go:build darwin && cratesint

// Integration tests: app driven headless with real mpv, kid3-cli and, where
// asked, the real Music app. Run by hand:
//
//	go test -tags cratesint -run Integration -v -timeout 10m .
//
// TestIntegrationLocal needs mpv and kid3-cli and leaves Music alone (it
// runs with [music] enabled = false). Its trash-mode delete sends one
// generated MP3 to the Trash.
//
// The Music tests change the real library and need:
//
//	CRATES_MUSIC_MASTER    a scratch master dir outside Music's Media folder,
//	                       holding no more than a few files
//	CRATES_MUSIC_PLAYLIST  the persistent ID of a plain test playlist
//
// Music must be running with the copy setting off, unless a test says
// otherwise. Steps that need a hand on the Mac are behind
// CRATES_INT_EXPECT; set it to one state per run, set that state up first,
// and restore it afterwards:
//
//	quit-midrun  TestIntegrationMusic asks you to quit Music partway (in -v
//	             output) and to start it again
//	copy-on      "Copy files to Media folder" is on: the first add returns
//	             ErrCopySettingOn and latches. Remove the copy Music made.
//	denied       Automation for this terminal is off for Music (and, with
//	             CRATES_INT_FINDER=1 in ./fsops, for Finder): -1743 maps to
//	             ErrAutomationDenied
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brennop/crates/app"
	"github.com/brennop/crates/config"
	"github.com/brennop/crates/events"
	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/pathnorm"
)

// ---- rig ----

// rig is one running app plus a recorder of its events.
type rig struct {
	t      *testing.T
	ctx    context.Context
	cfg    config.Config
	opts   options
	a      *app.App
	rec    *recorder
	stop   context.CancelFunc
	pumped chan struct{}
}

type recorder struct {
	mu   sync.Mutex
	evs  []events.Event
	wake chan struct{}
}

func (r *recorder) add(e events.Event) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	close(r.wake)
	r.wake = make(chan struct{})
	r.mu.Unlock()
}

func (r *recorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.evs)
}

// waitFor returns the first event from index from on that matches.
func (r *recorder) waitFor(t *testing.T, from int, d time.Duration, pred func(events.Event) bool) events.Event {
	t.Helper()
	deadline := time.After(d)
	for {
		r.mu.Lock()
		for _, e := range r.evs[from:] {
			if pred(e) {
				r.mu.Unlock()
				return e
			}
		}
		wake := r.wake
		r.mu.Unlock()
		select {
		case <-wake:
		case <-deadline:
			t.Fatalf("timed out after %v waiting for an event", d)
		}
	}
}

func (r *recorder) count(from int, pred func(events.Event) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.evs[from:] {
		if pred(e) {
			n++
		}
	}
	return n
}

func is[T events.Event](e events.Event) bool { _, ok := e.(T); return ok }

func isErr[T error](e events.Event) bool {
	er, ok := e.(events.Error)
	return ok && errors.As(er.Err, new(T))
}

// dirs is a temp master, inbox, cache and pidfile.
func dirs(t *testing.T) (root, master, inbox string) {
	t.Helper()
	root, err := pathnorm.Normalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	master, inbox = filepath.Join(root, "master"), filepath.Join(root, "inbox")
	for _, d := range []string{master, inbox} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, master, inbox
}

func baseConfig(master, inbox string, mode config.DeleteMode, musicOn bool) config.Config {
	cfg := config.Defaults()
	cfg.MasterDir, cfg.InboxDirs, cfg.DeleteMode = master, []string{inbox}, mode
	cfg.Music.Enabled = musicOn
	return cfg
}

func baseOptions(root string) options {
	return options{
		CachePath: filepath.Join(root, "cache", "library.db"),
		PidFile:   filepath.Join(root, "mpv.pid"),
		MPVArgs:   []string{"--ao=null"},
	}
}

func start(t *testing.T, cfg config.Config, opts options) *rig {
	t.Helper()
	a, bus, err := build(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	r := &rig{t: t, ctx: context.Background(), cfg: cfg, opts: opts, a: a,
		rec: &recorder{wake: make(chan struct{})}, stop: stop, pumped: make(chan struct{})}
	go func() {
		defer close(r.pumped)
		for {
			e, err := bus.Next(ctx)
			if err != nil {
				return
			}
			if er, ok := e.(events.Error); ok {
				t.Logf("event: Error %s: %v", er.Op, er.Err)
			}
			r.rec.add(e)
		}
	}()
	t.Cleanup(r.close)
	return r
}

func (r *rig) close() {
	if r.a == nil {
		return
	}
	if err := r.a.Close(); err != nil {
		r.t.Errorf("close: %v", err)
	}
	r.stop()
	<-r.pumped
	r.a = nil
}

// find returns the first indexed track whose file is named name.
func (r *rig) find(name string) (model.Track, bool) { return r.findWhere(name, anyTrack) }

// findWhere returns the first indexed track named name that satisfies ok.
// Two tracks can share a name, one in an inbox and one in master.
func (r *rig) findWhere(name string, ok func(model.Track) bool) (model.Track, bool) {
	ids, err := r.a.Query(r.ctx, app.Filter{}, app.Sort{})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, tr := range r.a.Tracks(ids) {
		if filepath.Base(tr.Path) == name && ok(tr) {
			return tr, true
		}
	}
	return model.Track{}, false
}

// waitTrack polls until the track named name satisfies ok.
func (r *rig) waitTrack(name string, d time.Duration, ok func(model.Track) bool) model.Track {
	r.t.Helper()
	deadline := time.Now().Add(d)
	for {
		if tr, found := r.findWhere(name, ok); found {
			return tr
		}
		if time.Now().After(deadline) {
			tr, found := r.find(name)
			r.t.Fatalf("%s: timed out after %v (indexed=%v, %+v)", name, d, found, tr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func anyTrack(model.Track) bool { return true }

// mp3 is a tagged, silent MP3 of about secs seconds.
func mp3(title string, secs int) []byte {
	return fixture.MP3(fixture.Options{Tag: fixture.ID3v24, Xing: true, Frames: 40 * secs,
		Summary: model.TagSummary{Title: title, Artist: "crates", Album: "integration"}})
}

func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mpvPID(t *testing.T, pidfile string) int {
	t.Helper()
	b, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.SplitN(string(b), "\n", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func needTools(t *testing.T) {
	for _, bin := range []string{"mpv", "kid3-cli"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

// ---- local: no Music ----

func TestIntegrationLocal(t *testing.T) {
	needTools(t)
	root, master, inbox := dirs(t)
	cfg := baseConfig(master, inbox, config.DeleteRemove, false)
	opts := baseOptions(root)
	write(t, filepath.Join(master, "m1.mp3"), mp3("m1", 30))
	write(t, filepath.Join(master, "m2.mp3"), mp3("m2", 30))

	// Cold start.
	began := time.Now()
	r := start(t, cfg, opts)
	r.rec.waitFor(t, 0, 5*time.Second, func(e events.Event) bool {
		l, ok := e.(events.LibraryLoaded)
		return ok && l.Count == 0
	})
	m1 := r.waitTrack("m1.mp3", 10*time.Second, anyTrack)
	r.waitTrack("m2.mp3", 10*time.Second, anyTrack)
	t.Logf("cold start: master indexed in %v", time.Since(began))
	if m1.Role != model.Master || m1.Duration == 0 || m1.Summary.Title != "m1" {
		t.Errorf("m1 = %+v", m1)
	}

	// A download stays hidden until its size holds across two sweeps.
	dl := filepath.Join(inbox, "dl.mp3")
	full := mp3("dl", 10)
	for i := 1; i <= 4; i++ {
		write(t, dl, full[:len(full)*i/4])
		time.Sleep(3500 * time.Millisecond) // past one inbox sweep
		if tr, ok := r.find("dl.mp3"); ok && i < 4 {
			t.Fatalf("growing download indexed at step %d: %+v", i, tr)
		}
	}
	got := r.waitTrack("dl.mp3", 10*time.Second, anyTrack)
	if got.Size != int64(len(full)) {
		t.Errorf("download indexed at %d bytes, want %d", got.Size, len(full))
	}

	// Promote one.
	sum := r.a.Promote(r.ctx, []model.TrackID{got.ID})
	if !slices.Equal(sum.Promoted, []model.TrackID{got.ID}) {
		t.Fatalf("promote dl: %+v", sum)
	}
	if tr := r.waitTrack("dl.mp3", time.Second, anyTrack); tr.ID != got.ID || tr.Role != model.Master {
		t.Errorf("promoted dl = %+v", tr)
	}

	// A batch with a collision, then overwrite it.
	write(t, filepath.Join(inbox, "x.mp3"), mp3("x", 5))
	write(t, filepath.Join(inbox, "sub", "m1.mp3"), mp3("m1 replacement", 5))
	x := r.waitTrack("x.mp3", 10*time.Second, anyTrack)
	clash := r.waitTrack("m1.mp3", 10*time.Second, func(tr model.Track) bool { return tr.Role == model.Inbox })
	sum = r.a.Promote(r.ctx, []model.TrackID{x.ID, clash.ID})
	if !slices.Equal(sum.Promoted, []model.TrackID{x.ID}) || len(sum.Collisions) != 1 ||
		sum.Collisions[0].Inbox != clash.ID || sum.Collisions[0].Existing != m1.ID {
		t.Fatalf("batch promote: %+v", sum)
	}
	if err := r.a.Overwrite(r.ctx, clash.ID); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if tr := r.waitTrack("m1.mp3", time.Second, func(tr model.Track) bool { return tr.Role == model.Master }); tr.ID != m1.ID || tr.Summary.Title != "m1 replacement" {
		t.Errorf("after overwrite m1 = %+v", tr)
	}
	if len(r.a.Tracks([]model.TrackID{clash.ID})) != 0 {
		t.Error("the inbox track is still indexed after overwrite")
	}

	// Edit tags on the playing track.
	m2, _ := r.find("m2.mp3")
	mark := r.rec.mark()
	if err := r.a.Play(r.ctx, m2.ID); err != nil {
		t.Fatal(err)
	}
	playing := func(e events.Event) bool {
		s, ok := e.(events.PlayerState)
		return ok && s.TrackID == m2.ID && !s.Idle && s.Position > 0
	}
	r.rec.waitFor(t, mark, 5*time.Second, playing)
	if err := r.a.WriteTags(r.ctx, []model.TrackID{m2.ID}, []model.FrameChange{{Name: "Title", Value: "m2 edited"}}); err != nil {
		t.Fatalf("write tags: %v", err)
	}
	if tr := r.waitTrack("m2.mp3", time.Second, anyTrack); tr.Summary.Title != "m2 edited" {
		t.Errorf("after write m2 title = %q", tr.Summary.Title)
	}
	mark = r.rec.mark()
	r.rec.waitFor(t, mark, 5*time.Second, playing) // still (or again) playing
	if q := r.a.Queue(); q.Playing != m2.ID {
		t.Errorf("after the tag edit playing = %d, want %d", q.Playing, m2.ID)
	}

	// Kill mpv mid-play: PlayerDown, a new mpv, playback can resume.
	pid := mpvPID(t, opts.PidFile)
	mark = r.rec.mark()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	r.rec.waitFor(t, mark, 5*time.Second, is[events.PlayerDown])
	deadline := time.Now().Add(10 * time.Second)
	for {
		if b, err := os.ReadFile(opts.PidFile); err == nil {
			if p, _ := strconv.Atoi(strings.SplitN(string(b), "\n", 2)[0]); p != pid && alive(p) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("mpv was not restarted")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The pidfile names the new mpv before the player has reconnected to
	// it; calls meanwhile fail, so retry for a while.
	mark = r.rec.mark()
	for err := r.a.Play(r.ctx, m2.ID); err != nil; err = r.a.Play(r.ctx, m2.ID) {
		if time.Now().After(deadline) {
			t.Fatalf("play after restart: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.rec.waitFor(t, mark, 10*time.Second, playing)

	// Delete (remove mode) the playing track and a master track.
	if s := r.a.Delete(r.ctx, []model.TrackID{m2.ID, x.ID}); len(s.Deleted) != 2 {
		t.Fatalf("delete: %+v", s)
	}
	for _, n := range []string{"m2.mp3", "x.mp3"} {
		if _, err := os.Stat(filepath.Join(master, n)); !os.IsNotExist(err) {
			t.Errorf("%s still on disk: %v", n, err)
		}
	}
	if q := r.a.Queue(); q.Playing != 0 {
		t.Errorf("still playing %d after deleting it", q.Playing)
	}
	ids := map[string]model.TrackID{}
	for _, n := range []string{"m1.mp3", "dl.mp3"} {
		tr, _ := r.find(n)
		ids[n] = tr.ID
	}
	r.close()

	// Warm start in trash mode: same IDs from the cache.
	cfg.DeleteMode = config.DeleteTrash
	began = time.Now()
	r = start(t, cfg, opts)
	r.rec.waitFor(t, 0, 5*time.Second, func(e events.Event) bool {
		l, ok := e.(events.LibraryLoaded)
		return ok && l.Count == 2
	})
	t.Logf("warm start: cache loaded in %v", time.Since(began))
	for n, id := range ids {
		if tr, ok := r.find(n); !ok || tr.ID != id {
			t.Errorf("%s after restart: %+v, want ID %d", n, tr, id)
		}
	}
	trashName := fmt.Sprintf("crates-integration-%d.mp3", time.Now().UnixNano())
	write(t, filepath.Join(master, trashName), mp3("trash me", 1))
	if err := r.a.Refresh(r.ctx); err != nil {
		t.Fatal(err)
	}
	tm := r.waitTrack(trashName, time.Second, anyTrack)
	if s := r.a.Delete(r.ctx, []model.TrackID{tm.ID}); len(s.Deleted) != 1 {
		t.Fatalf("trash delete: %+v", s)
	}
	if _, err := os.Stat(tm.Path); !os.IsNotExist(err) {
		t.Errorf("trashed file still in master: %v", err)
	}
	t.Logf("sent %s to the Trash (%s)", trashName, r.a.Capabilities().TrashMethod)
}

// ---- kill the app, restart: the orphan mpv is cleaned up ----

// TestIntegrationChild is the app process TestIntegrationOrphan kills. It
// does nothing unless that test runs it.
func TestIntegrationChild(t *testing.T) {
	root := os.Getenv("CRATES_INT_CHILD")
	if root == "" {
		t.Skip("run by TestIntegrationOrphan")
	}
	cfg := baseConfig(filepath.Join(root, "master"), filepath.Join(root, "inbox"), config.DeleteRemove, false)
	if _, _, err := build(cfg, baseOptions(root)); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	select {} // killed by the parent
}

func TestIntegrationOrphan(t *testing.T) {
	needTools(t)
	root, master, inbox := dirs(t)
	opts := baseOptions(root)
	child := exec.Command(os.Args[0], "-test.run=^TestIntegrationChild$", "-test.v")
	child.Env = append(os.Environ(), "CRATES_INT_CHILD="+root)
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		var seen string
		for {
			n, err := out.Read(buf)
			seen += string(buf[:n])
			if strings.Contains(seen, "ready") {
				close(ready)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		child.Process.Kill()
		t.Fatal("the child app never came up")
	}
	pid := mpvPID(t, opts.PidFile)
	if err := child.Process.Kill(); err != nil { // SIGKILL: no shutdown path runs
		t.Fatal(err)
	}
	child.Wait()
	time.Sleep(200 * time.Millisecond)
	if !alive(pid) {
		t.Fatalf("mpv %d died with the app; nothing to clean up", pid)
	}
	t.Logf("orphan mpv %d is alive after the app was killed", pid)

	r := start(t, baseConfig(master, inbox, config.DeleteRemove, false), opts)
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL) // this test's own orphan
			t.Fatalf("the restart left orphan mpv %d running", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p := mpvPID(t, opts.PidFile); p == pid || !alive(p) {
		t.Errorf("new mpv pid %d (orphan was %d)", p, pid)
	}
	r.close()
}

// ---- Music ----

func musicEnv(t *testing.T) (master, playlist string) {
	t.Helper()
	master, playlist = os.Getenv("CRATES_MUSIC_MASTER"), os.Getenv("CRATES_MUSIC_PLAYLIST")
	if master == "" || playlist == "" {
		t.Skip("set CRATES_MUSIC_MASTER and CRATES_MUSIC_PLAYLIST")
	}
	needTools(t)
	m, err := pathnorm.Normalize(master)
	if err != nil {
		t.Fatal(err)
	}
	return m, playlist
}

func expect(state string) bool { return os.Getenv("CRATES_INT_EXPECT") == state }

func TestIntegrationMusic(t *testing.T) {
	master, pl := musicEnv(t)
	if expect("copy-on") || expect("denied") {
		t.Skip("needs Music in its normal state")
	}
	root, _, inbox := dirs(t)
	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("crates it's \"%d\" ção.mp3", stamp)
	write(t, filepath.Join(inbox, name), mp3("crates music test", 5))

	r := start(t, baseConfig(master, inbox, config.DeleteRemove, true), baseOptions(root))
	in := r.waitTrack(name, 10*time.Second, anyTrack)
	if sum := r.a.Promote(r.ctx, []model.TrackID{in.ID}); len(sum.Promoted) != 1 {
		t.Fatalf("promote: %+v", sum)
	}
	tr := r.waitTrack(name, 30*time.Second, func(tr model.Track) bool { return tr.MusicID != "" })
	t.Logf("added to Music as %s", tr.MusicID)

	if err := r.a.AddToPlaylist(r.ctx, []model.TrackID{tr.ID}, pl); err != nil {
		t.Fatalf("add to playlist: %v", err)
	}
	ps, err := r.a.Memberships(tr.ID)
	if err != nil || !slices.ContainsFunc(ps, func(p model.Playlist) bool { return p.ID == pl }) {
		t.Errorf("memberships = %+v, %v", ps, err)
	}

	mark := r.rec.mark()
	if err := r.a.WriteTags(r.ctx, []model.TrackID{tr.ID}, []model.FrameChange{{Name: "Title", Value: "crates music edited"}}); err != nil {
		t.Errorf("write tags: %v", err)
	}
	if n := r.rec.count(mark, is[events.Error]); n != 0 {
		t.Errorf("%d errors after the tag write (Music refresh?)", n)
	}

	if expect("quit-midrun") {
		t.Log(">>> Quit Music now (Cmd-Q). Waiting up to 2 minutes.")
		waitState(t, r, model.MusicUnavailable, 2*time.Minute)
		err := r.a.RemoveFromPlaylist(r.ctx, []model.TrackID{tr.ID}, pl)
		if !errors.As(err, new(model.ErrMusicUnavailable)) {
			t.Errorf("playlist edit with Music quit: err = %v, want ErrMusicUnavailable", err)
		}
		if s := r.a.Delete(r.ctx, []model.TrackID{tr.ID}); len(s.Failures) != 1 || !errors.As(s.Failures[0].Err, new(model.ErrMusicUnavailable)) {
			t.Errorf("delete with Music quit: %+v", s)
		}
		if _, err := os.Stat(tr.Path); err != nil {
			t.Errorf("a failed Music delete touched the file: %v", err)
		}
		t.Log(">>> Start Music again. Waiting up to 2 minutes.")
		waitState(t, r, model.MusicAvailable, 2*time.Minute)
	}

	if err := r.a.RemoveFromPlaylist(r.ctx, []model.TrackID{tr.ID}, pl); err != nil {
		t.Fatalf("remove from playlist: %v", err)
	}
	if s := r.a.Delete(r.ctx, []model.TrackID{tr.ID}); len(s.Deleted) != 1 {
		t.Fatalf("delete (remove): %+v", s)
	}
	gone(t, tr)
	r.close()

	// Trash mode.
	name2 := fmt.Sprintf("crates-integration-trash-%d.mp3", stamp)
	write(t, filepath.Join(inbox, name2), mp3("crates trash test", 1))
	r = start(t, baseConfig(master, inbox, config.DeleteTrash, true), baseOptions(root))
	in = r.waitTrack(name2, 10*time.Second, anyTrack)
	if sum := r.a.Promote(r.ctx, []model.TrackID{in.ID}); len(sum.Promoted) != 1 {
		t.Fatalf("promote: %+v", sum)
	}
	tr = r.waitTrack(name2, 30*time.Second, func(tr model.Track) bool { return tr.MusicID != "" })
	if s := r.a.Delete(r.ctx, []model.TrackID{tr.ID}); len(s.Deleted) != 1 {
		t.Fatalf("delete (trash): %+v", s)
	}
	gone(t, tr)
}

// waitState refreshes until Music reports state.
func waitState(t *testing.T, r *rig, want model.MusicState, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for r.a.MusicStatus() != want {
		if time.Now().After(deadline) {
			t.Fatalf("Music state %v, want %v", r.a.MusicStatus(), want)
		}
		r.a.Refresh(r.ctx)
		time.Sleep(time.Second)
	}
}

// gone checks a deleted track left master and Music.
func gone(t *testing.T, tr model.Track) {
	t.Helper()
	if _, err := os.Stat(tr.Path); !os.IsNotExist(err) {
		t.Errorf("%s still on disk: %v", tr.Path, err)
	}
	c := music.New(music.Options{})
	defer c.Close()
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := snap.Tracks[tr.Path]; ok {
		t.Errorf("Music still has %s as %s", tr.Path, id)
	}
}

func TestIntegrationCopySettingOn(t *testing.T) {
	master, _ := musicEnv(t)
	if !expect("copy-on") {
		t.Skip("set CRATES_INT_EXPECT=copy-on with the copy setting on")
	}
	root, _, inbox := dirs(t)
	stamp := time.Now().UnixNano()
	var names []string
	for i := range 2 {
		n := fmt.Sprintf("crates-integration-copy-%d-%d.mp3", stamp, i)
		names = append(names, n)
		write(t, filepath.Join(inbox, n), mp3("crates copy test", 1))
	}
	r := start(t, baseConfig(master, inbox, config.DeleteRemove, true), baseOptions(root))
	var ids []model.TrackID
	for _, n := range names {
		ids = append(ids, r.waitTrack(n, 10*time.Second, anyTrack).ID)
	}
	mark := r.rec.mark()
	if sum := r.a.Promote(r.ctx, ids[:1]); len(sum.Promoted) != 1 {
		t.Fatalf("promote: %+v", sum)
	}
	ev := r.rec.waitFor(t, mark, 30*time.Second, isErr[model.ErrCopySettingOn]).(events.Error)
	var c model.ErrCopySettingOn
	errors.As(ev.Err, &c)
	t.Logf("Music copied %s to %s: remove that entry and file by hand", c.Path, c.Location)
	waitState(t, r, model.MusicCopySettingOn, 10*time.Second)

	// Latched: the second promote reaches Music no more.
	if sum := r.a.Promote(r.ctx, ids[1:]); len(sum.Promoted) != 1 {
		t.Fatalf("promote: %+v", sum)
	}
	time.Sleep(3 * time.Second)
	if tr, _ := r.find(names[1]); tr.MusicID != "" {
		t.Errorf("added %s to Music while latched", names[1])
	}
	if r.a.MusicStatus() != model.MusicCopySettingOn {
		t.Errorf("state = %v, want CopySettingOn (latched)", r.a.MusicStatus())
	}
}

func TestIntegrationDenied(t *testing.T) {
	master, _ := musicEnv(t)
	if !expect("denied") {
		t.Skip("set CRATES_INT_EXPECT=denied with Automation for Music turned off")
	}
	root, _, inbox := dirs(t)
	r := start(t, baseConfig(master, inbox, config.DeleteRemove, true), baseOptions(root))
	err := r.a.Refresh(r.ctx)
	if !errors.As(err, new(model.ErrAutomationDenied)) {
		t.Errorf("refresh: err = %v, want ErrAutomationDenied", err)
	}
	if r.a.MusicStatus() != model.MusicDenied {
		t.Errorf("state = %v, want denied", r.a.MusicStatus())
	}
}
