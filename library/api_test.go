package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/brennop/crates/events"
	"github.com/brennop/crates/model"
)

func isChanged(e events.Event) bool { _, ok := e.(events.TracksChanged); return ok }

func TestUpsert(t *testing.T) {
	e := newEnv(t)
	l := e.open()
	p := filepath.Join(e.master, "a.mp3")
	put(t, p, 10)
	tr, err := l.Upsert(model.Track{Path: p, Size: 10, Summary: model.TagSummary{Title: "A"}})
	if err != nil || tr.ID == 0 || tr.Role != model.Master {
		t.Fatalf("upsert = %+v, %v", tr, err)
	}
	if err := l.SetMusicID(tr.ID, "M1"); err != nil {
		t.Fatal(err)
	}
	n := e.sink.count(isChanged)
	same := tr
	same.MusicID = "M1"
	if _, err := l.Upsert(same); err != nil || e.sink.count(isChanged) != n {
		t.Fatal("no-op upsert published")
	}
	tr.Summary.Title = "B"
	got, err := l.Upsert(tr) // MusicID "" in the argument is ignored
	if err != nil || got.MusicID != "M1" || got.Summary.Title != "B" || e.sink.count(isChanged) != n+1 {
		t.Fatalf("upsert = %+v, %v", got, err)
	}
	if _, err := l.Upsert(model.Track{Path: "/elsewhere/x.mp3"}); err == nil {
		t.Fatal("path outside roots accepted")
	}
}

func TestSetMusicID(t *testing.T) {
	e := newEnv(t)
	src := filepath.Join(e.inbox, "s.mp3")
	put(t, src, 10)
	l := e.open()
	tr, err := l.Upsert(model.Track{Path: src, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetMusicID(tr.ID, "M"); err != nil {
		t.Fatal(err)
	}
	n := e.sink.count(isChanged)
	if err := l.SetMusicID(tr.ID, "M"); err != nil || e.sink.count(isChanged) != n {
		t.Fatal("same ID published")
	}
	dst := filepath.Join(e.master, "s.mp3")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := l.Move(tr.ID, dst); err != nil {
		t.Fatal(err)
	}
	if got := l.Get(tr.ID)[0]; got.MusicID != "M" {
		t.Fatalf("after Move: %+v", got)
	}
	if err := l.SetMusicID(999, "x"); err != ErrUnknownTrack {
		t.Fatalf("err = %v", err)
	}
}

func TestFindMaster(t *testing.T) {
	e := newEnv(t)
	l := e.open()
	m := filepath.Join(e.master, "Café Song.mp3")
	put(t, m, 10)
	tr, _ := l.Upsert(model.Track{Path: m, Size: 10})
	in := filepath.Join(e.inbox, "only.mp3")
	put(t, in, 10)
	l.Upsert(model.Track{Path: in, Size: 10})

	for _, name := range []string{"café song.MP3", norm.NFD.String("CAFÉ SONG.mp3")} {
		if got, ok := l.FindMaster(name); !ok || got.ID != tr.ID {
			t.Errorf("FindMaster(%q) = %+v, %v", name, got, ok)
		}
	}
	if _, ok := l.FindMaster("only.mp3"); ok {
		t.Error("inbox track found in master")
	}
	if l.MasterDir() != e.master {
		t.Errorf("MasterDir = %s", l.MasterDir())
	}
}

// signalReader blocks every read until gate closes, and says when the first
// read starts.
type signalReader struct {
	entered chan struct{}
	gate    chan struct{}
	once    bool
}

func (r *signalReader) Summary(path string) (model.TagSummary, time.Duration, error) {
	if !r.once {
		r.once = true
		close(r.entered)
	}
	<-r.gate
	return model.TagSummary{Title: filepath.Base(path)}, time.Second, nil
}

// A promote that lands while a sweep is reading must survive the sweep: the
// sweep may neither remove the promoted master track nor bring the inbox
// file back at its old path.
func TestSweepDuringPromote(t *testing.T) {
	e := newEnv(t)
	r := &signalReader{entered: make(chan struct{}), gate: make(chan struct{})}
	cfg := e.config()
	cfg.Reader = r
	l, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	// An indexed inbox file that then grows, so the sweep must re-read it.
	src := filepath.Join(e.inbox, "p.mp3")
	put(t, src, 10)
	tr, err := l.Upsert(model.Track{Path: src, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	put(t, src, 20)
	scan(t, l, l.inboxRoot()) // first sighting of the new size
	// A new master file keeps the master sweep busy reading.
	put(t, filepath.Join(e.master, "busy.mp3"), 5)

	done := make(chan struct{})
	go func() {
		defer close(done)
		scan(t, l, l.inboxRoot())
		scan(t, l, l.masterRoot())
	}()
	<-r.entered // the inbox sweep has listed p.mp3 and is reading it

	dst := filepath.Join(e.master, "p.mp3")
	err = l.Exclusive(func() error {
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		return l.Move(tr.ID, dst)
	})
	if err != nil {
		t.Fatal(err)
	}
	close(r.gate)
	<-done

	got := l.Get(tr.ID)
	if len(got) != 1 || got[0].Path != dst {
		t.Fatalf("promoted track = %+v", got)
	}
	for _, x := range all(l) {
		if x.Path == src {
			t.Fatalf("inbox file came back as track %d", x.ID)
		}
	}
	// The master sweep listed after the move; run one that listed before it.
	r2 := &signalReader{entered: make(chan struct{}), gate: make(chan struct{})}
	l.reader = r2
	put(t, filepath.Join(e.master, "busy2.mp3"), 5)
	other := filepath.Join(e.inbox, "q.mp3")
	put(t, other, 10)
	q, _ := l.Upsert(model.Track{Path: other, Size: 10})
	done = make(chan struct{})
	go func() {
		defer close(done)
		scan(t, l, l.masterRoot())
	}()
	<-r2.entered
	dst2 := filepath.Join(e.master, "q.mp3")
	l.Exclusive(func() error {
		if err := os.Rename(other, dst2); err != nil {
			return err
		}
		return l.Move(q.ID, dst2)
	})
	close(r2.gate)
	<-done
	if got := l.Get(q.ID); len(got) != 1 || got[0].Path != dst2 {
		t.Fatalf("master sweep removed the promoted track: %+v", got)
	}

}
