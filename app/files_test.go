package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
	"github.com/brennop/crates/music"
	"github.com/brennop/crates/pathnorm"
)

func TestPromoteKeepsIDAndQueuePlace(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.inbox, "a.mp3", tagged)
	b := e.file(e.inbox, "b.mp3", tagged)
	e.app.Enqueue(a.ID, b.ID)
	if err := e.app.Play(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}

	sum := e.app.Promote(e.ctx, []model.TrackID{b.ID})
	if !slices.Equal(sum.Promoted, []model.TrackID{b.ID}) || len(sum.Collisions)+len(sum.Failures) > 0 {
		t.Fatalf("summary = %+v", sum)
	}
	got := e.get(b.ID)
	want := pathnorm.Child(e.master, "b.mp3")
	if got.Path != want || got.Role != model.Master {
		t.Fatalf("track = %s %v, want %s master", got.Path, got.Role, want)
	}
	if exists(b.Path) || !exists(want) {
		t.Fatal("file was not moved")
	}
	q := e.app.Queue()
	if !slices.Equal(q.IDs, []model.TrackID{a.ID, b.ID}) || q.Pos != 1 || q.Playing != b.ID {
		t.Fatalf("queue = %+v", q)
	}
	// The next load resolves the ID to its new path.
	if err := e.app.Previous(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.app.Next(e.ctx); err != nil {
		t.Fatal(err)
	}
	if p := e.lastLoad().Path; p != want {
		t.Fatalf("loaded %s, want %s", p, want)
	}
}

func TestPromoteMissingTags(t *testing.T) {
	e := newEnv(t)
	a := e.file(e.inbox, "a.mp3", model.TagSummary{Title: "T"})
	sum := e.app.Promote(e.ctx, []model.TrackID{a.ID})
	var m model.ErrMissingTags
	if len(sum.Failures) != 1 || !errors.As(sum.Failures[0].Err, &m) {
		t.Fatalf("summary = %+v", sum)
	}
	if !slices.Equal(m.Missing, []string{model.FieldArtist}) {
		t.Fatalf("missing = %v", m.Missing)
	}
	if !exists(a.Path) {
		t.Fatal("file moved")
	}
}

func TestPromoteCollisionNeverOverwrites(t *testing.T) {
	e := newEnv(t)
	m := e.file(e.master, "song.mp3", tagged)
	in := e.file(e.inbox, "Song.MP3", tagged) // differs only in case
	before := readFile(t, m.Path)

	sum := e.app.Promote(e.ctx, []model.TrackID{in.ID})
	want := []Collision{{Inbox: in.ID, Existing: m.ID, Name: "Song.MP3"}}
	if !slices.Equal(sum.Collisions, want) {
		t.Fatalf("collisions = %+v, want %+v", sum.Collisions, want)
	}
	if readFile(t, m.Path) != before || !exists(in.Path) {
		t.Fatal("a file was touched")
	}
	if e.get(in.ID).Role != model.Inbox {
		t.Fatal("inbox track changed role")
	}
}

func TestPromoteCollisionNotIndexedTriggersSweep(t *testing.T) {
	e := newEnv(t)
	in := e.file(e.inbox, "x.mp3", tagged)
	// A master file the index has not seen yet.
	stray := pathnorm.Child(e.master, "x.mp3")
	if err := os.WriteFile(stray, []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.tags.PutSummary(stray, 0, tagged)
	from := e.bus.mark()

	sum := e.app.Promote(e.ctx, []model.TrackID{in.ID})
	want := []Collision{{Inbox: in.ID, Existing: 0, Name: "x.mp3"}}
	if !slices.Equal(sum.Collisions, want) {
		t.Fatalf("collisions = %+v, want %+v", sum.Collisions, want)
	}
	if readFile(t, stray) != "stray" || !exists(in.Path) {
		t.Fatal("a file was touched")
	}
	// The sweep indexes the stray file.
	e.bus.waitFor(t, from, func(ev events.Event) bool {
		c, ok := ev.(events.TracksChanged)
		return ok && slices.ContainsFunc(c.Tracks, func(tr model.Track) bool { return tr.Path == stray })
	})
	if _, ok := e.lib.FindMaster("x.mp3"); !ok {
		t.Fatal("stray file not indexed")
	}
}

func TestPromoteBatchContinuesPastFailures(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "taken.mp3", tagged)
	ok1 := e.file(e.inbox, "one.mp3", tagged)
	coll := e.file(e.inbox, "taken.mp3", tagged)
	bad := e.file(e.inbox, "bad.mp3", model.TagSummary{})
	ok2 := e.file(e.inbox, "two.mp3", tagged)

	sum := e.app.Promote(e.ctx, []model.TrackID{ok1.ID, coll.ID, bad.ID, 9999, ok2.ID})
	if !slices.Equal(sum.Promoted, []model.TrackID{ok1.ID, ok2.ID}) {
		t.Fatalf("promoted = %v", sum.Promoted)
	}
	if len(sum.Collisions) != 1 || sum.Collisions[0].Inbox != coll.ID {
		t.Fatalf("collisions = %+v", sum.Collisions)
	}
	if len(sum.Failures) != 2 || sum.Failures[0].ID != bad.ID || !errors.Is(sum.Failures[1].Err, ErrUnknownTrack) {
		t.Fatalf("failures = %+v", sum.Failures)
	}
}

func TestPromoteRejectsMasterTrack(t *testing.T) {
	e := newEnv(t)
	m := e.file(e.master, "m.mp3", tagged)
	sum := e.app.Promote(e.ctx, []model.TrackID{m.ID})
	if len(sum.Failures) != 1 || !errors.As(sum.Failures[0].Err, new(model.ErrNotInbox)) {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestPromoteMoveFailureChangesNothing(t *testing.T) {
	e := newEnv(t)
	in := e.file(e.inbox, "a.mp3", tagged)
	e.fs.move = errors.New("disk full")
	sum := e.app.Promote(e.ctx, []model.TrackID{in.ID})
	if len(sum.Failures) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if got := e.get(in.ID); got.Path != in.Path || got.Role != model.Inbox {
		t.Fatalf("track = %+v", got)
	}
}

// overwriteSetup: a master track in Music and in a playlist, and an inbox
// track with the same name (different case) that is playing.
func overwriteSetup(t *testing.T) (*env, model.Track, model.Track) {
	e := newEnv(t)
	m := e.inMusic(e.file(e.master, "Tune.mp3", tagged))
	e.music.AddPlaylist(model.Playlist{ID: "P1", Name: "Set", Editable: true}, m.MusicID)
	e.snapshot()
	newTags := model.TagSummary{Title: "New", Artist: "New", Album: "New"}
	in := e.file(e.inbox, "tune.mp3", newTags)
	e.app.Enqueue(m.ID, in.ID, m.ID)
	if err := e.app.Play(e.ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	return e, m, in
}

func TestOverwrite(t *testing.T) {
	e, m, in := overwriteSetup(t)
	content := readFile(t, in.Path)

	if err := e.app.Overwrite(e.ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if readFile(t, m.Path) != content {
		t.Fatal("master file does not hold the inbox content")
	}
	if filepath.Base(m.Path) != "Tune.mp3" || exists(in.Path) {
		t.Fatal("master name not kept, or inbox file left behind")
	}
	got := e.get(m.ID)
	if got.MusicID != m.MusicID || got.Summary != in.Summary || got.Size != in.Size || !got.ModTime.Equal(in.ModTime) {
		t.Fatalf("master track = %+v", got)
	}
	if e.indexed(in.ID) {
		t.Fatal("inbox ID still indexed")
	}
	q := e.app.Queue()
	if slices.Contains(q.IDs, in.ID) || q.Playing != 0 {
		t.Fatalf("queue = %+v", q)
	}
	if !slices.Equal(q.IDs, []model.TrackID{m.ID, m.ID}) {
		t.Fatalf("queue IDs = %v", q.IDs)
	}
	if ops := e.playerOps(); ops[len(ops)-1] != "stop" {
		t.Fatalf("player ops = %v", ops)
	}
	if r := e.music.CallsOf(music.OpRefresh); len(r) != 1 || r[0].MusicIDs[0] != m.MusicID {
		t.Fatalf("refresh calls = %+v", r)
	}
	if got := e.music.Members("P1"); !slices.Equal(got, []string{m.MusicID}) {
		t.Fatalf("playlist = %v", got)
	}
	if n := len(e.music.CallsOf(music.OpPlAdd)) + len(e.music.CallsOf(music.OpPlRemove)) + len(e.music.CallsOf(music.OpDelete)); n != 0 {
		t.Fatalf("%d membership calls", n)
	}
}

func TestOverwriteBackupKeptPassesThrough(t *testing.T) {
	e, m, in := overwriteSetup(t)
	e.fs.kept = true
	err := e.app.Overwrite(e.ctx, in.ID)
	var kept model.ErrBackupKept
	if !errors.As(err, &kept) {
		t.Fatalf("err = %v", err)
	}
	if e.indexed(in.ID) || e.get(m.ID).Summary != in.Summary {
		t.Fatal("overwrite not recorded")
	}
}

func TestOverwriteReplaceFailureChangesFiles(t *testing.T) {
	e, m, in := overwriteSetup(t)
	e.fs.replace = errors.New("no space")
	if err := e.app.Overwrite(e.ctx, in.ID); err == nil {
		t.Fatal("no error")
	}
	if !e.indexed(in.ID) || e.get(m.ID).Summary != tagged || !exists(in.Path) {
		t.Fatal("state changed on a failed replace")
	}
}

func TestOverwriteWithoutCollisionPromotes(t *testing.T) {
	e := newEnv(t)
	in := e.file(e.inbox, "solo.mp3", tagged)
	if err := e.app.Overwrite(e.ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.get(in.ID); got.Role != model.Master {
		t.Fatalf("track = %+v", got)
	}
}

func TestOverwriteMissingTags(t *testing.T) {
	e := newEnv(t)
	e.file(e.master, "a.mp3", tagged)
	in := e.file(e.inbox, "a.mp3", model.TagSummary{Title: "x"})
	var m model.ErrMissingTags
	if err := e.app.Overwrite(e.ctx, in.ID); !errors.As(err, &m) {
		t.Fatalf("err = %v", err)
	}
}

// deleteSetup: a master track in Music and in a playlist, playing, in the
// queue twice.
func deleteSetup(t *testing.T) (*env, model.Track) {
	e := newEnv(t)
	m := e.inMusic(e.file(e.master, "gone.mp3", tagged))
	other := e.file(e.master, "other.mp3", tagged)
	e.music.AddPlaylist(model.Playlist{ID: "P1", Name: "Set", Editable: true}, m.MusicID)
	e.snapshot()
	e.app.Enqueue(m.ID, other.ID, m.ID)
	if err := e.app.Play(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	return e, m
}

func TestDelete(t *testing.T) {
	e, m := deleteSetup(t)
	from := e.bus.mark()
	sum := e.app.Delete(e.ctx, []model.TrackID{m.ID})
	if !slices.Equal(sum.Deleted, []model.TrackID{m.ID}) {
		t.Fatalf("summary = %+v", sum)
	}
	if exists(m.Path) || e.indexed(m.ID) {
		t.Fatal("file or index entry left")
	}
	if _, ok := e.music.Track(m.MusicID); ok {
		t.Fatal("Music entry left")
	}
	q := e.app.Queue()
	if slices.Contains(q.IDs, m.ID) || q.Playing != 0 || q.Pos != 0 {
		t.Fatalf("queue = %+v", q)
	}
	e.bus.waitFor(t, from, is[events.TrackRemoved])
	e.bus.waitFor(t, from, is[events.PlaylistsChanged])
	if ms, _ := e.app.Memberships(m.ID); ms != nil {
		t.Fatal("memberships left")
	}
}

func TestDeleteMusicFailureChangesNothing(t *testing.T) {
	e, m := deleteSetup(t)
	e.music.Fail(music.OpDelete, model.ErrMusicUnavailable{})
	sum := e.app.Delete(e.ctx, []model.TrackID{m.ID})
	var u model.ErrMusicUnavailable
	if len(sum.Failures) != 1 || !errors.As(sum.Failures[0].Err, &u) {
		t.Fatalf("summary = %+v", sum)
	}
	if !exists(m.Path) || e.get(m.ID).MusicID != m.MusicID {
		t.Fatal("file or index changed")
	}
	if got := e.music.Members("P1"); !slices.Equal(got, []string{m.MusicID}) {
		t.Fatalf("playlist = %v", got)
	}
	// Step 1 ran: the track left the queue and stopped.
	if q := e.app.Queue(); slices.Contains(q.IDs, m.ID) || q.Playing != 0 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestDeleteFileFailureAfterMusicLosesPlaylists(t *testing.T) {
	e, m := deleteSetup(t)
	e.fs.del = errors.New("trash refused")
	sum := e.app.Delete(e.ctx, []model.TrackID{m.ID})
	var lost model.ErrPlaylistsLost
	if len(sum.Failures) != 1 || !errors.As(sum.Failures[0].Err, &lost) || lost.Path != m.Path {
		t.Fatalf("summary = %+v", sum)
	}
	if _, ok := e.music.Track(m.MusicID); ok {
		t.Fatal("Music entry left")
	}
	// The file and the track stay, without a Music ID, for the reconciler.
	if !exists(m.Path) || e.get(m.ID).MusicID != "" {
		t.Fatal("file or index entry wrong")
	}
}

func TestDeleteInboxSkipsMusic(t *testing.T) {
	e := newEnv(t)
	in := e.file(e.inbox, "i.mp3", tagged)
	e.fs.del = errors.New("busy")
	sum := e.app.Delete(e.ctx, []model.TrackID{in.ID})
	if len(sum.Failures) != 1 || errors.As(sum.Failures[0].Err, new(model.ErrPlaylistsLost)) {
		t.Fatalf("summary = %+v", sum)
	}
	if !e.indexed(in.ID) || len(e.music.CallsOf(music.OpDelete)) != 0 {
		t.Fatal("state changed")
	}
	e.fs.del = nil
	if sum := e.app.Delete(e.ctx, []model.TrackID{in.ID}); len(sum.Deleted) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestDeleteMissingFileStillRemoves(t *testing.T) {
	e := newEnv(t)
	in := e.file(e.inbox, "i.mp3", tagged)
	if err := os.Remove(in.Path); err != nil {
		t.Fatal(err)
	}
	if sum := e.app.Delete(e.ctx, []model.TrackID{in.ID}); len(sum.Deleted) != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}
