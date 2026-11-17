package tags

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/model"
)

func TestFakeSummaryAndWrite(t *testing.T) {
	f := NewFake()
	f.PutSummary("/a.mp3", 3*time.Second, fixture.Full)
	f.PutSummary("/b.mp3", time.Second, model.TagSummary{Title: "B"})
	ctx := context.Background()

	s, d, err := f.Summary("/a.mp3")
	if err != nil || s != fixture.Full || d != 3*time.Second {
		t.Fatalf("Summary = %+v, %v, %v", s, d, err)
	}

	ch := []model.FrameChange{
		{Name: "title", Value: "New"},            // any case, like kid3
		{Name: "Comment"},                        // delete
		{Name: "Artist", Index: 1, Value: "Two"}, // a second artist
		{Name: "Track Number", Value: "7/12"},    // parsed as 7
		{Name: "Initial Key", Value: "8A"},
	}
	if err := f.Write(ctx, []string{"/a.mp3", "/b.mp3"}, ch); err != nil {
		t.Fatal(err)
	}
	s, _, _ = f.Summary("/a.mp3")
	if s.Title != "New" || s.Comment != "" || s.Artist != "Artist" || s.TrackNumber != 7 || s.Key != "8A" {
		t.Errorf("a after write: %+v", s)
	}
	frames, _ := f.Full(ctx, "/b.mp3")
	m := frameMap(frames)
	// b has no Artist, so Artist[1] adds the first one, as kid3 does.
	if m["Title"] != "New" || m["Artist"] != "Two" || m["Initial Key"] != "8A" {
		t.Errorf("b after write: %v", m)
	}
	if w := f.Writes(); len(w) != 1 || !slices.Equal(w[0].Paths, []string{"/a.mp3", "/b.mp3"}) {
		t.Errorf("Writes = %+v", w)
	}

	// Deleting the first of two frames renumbers the second.
	f.Write(ctx, []string{"/a.mp3"}, []model.FrameChange{{Name: "Artist"}})
	frames, _ = f.Full(ctx, "/a.mp3")
	if m := frameMap(frames); m["Artist"] != "Two" || m["Artist[1]"] != "" {
		t.Errorf("after delete: %v", m)
	}
}

func TestFakeFailures(t *testing.T) {
	f := NewFake()
	f.PutSummary("/a.mp3", 0, model.TagSummary{Title: "A"})
	f.PutSummary("/b.mp3", 0, model.TagSummary{Title: "B"})
	ctx := context.Background()
	boom := errors.New("boom")

	f.Fail(OpWrite, "/b.mp3", boom)
	err := f.Write(ctx, []string{"/a.mp3", "/b.mp3"}, []model.FrameChange{{Name: "Title", Value: "X"}})
	if !errors.Is(err, boom) {
		t.Errorf("Write err = %v", err)
	}
	if s, _, _ := f.Summary("/a.mp3"); s.Title != "A" {
		t.Error("a failed write changed a file")
	}
	if err := f.Write(ctx, []string{"/a.mp3"}, []model.FrameChange{{Name: "Title", Value: "X"}}); err != nil {
		t.Errorf("a failure is used once: %v", err)
	}

	f.Fail(OpFull, "", boom, boom) // any path, twice
	for i := range 2 {
		if _, err := f.Full(ctx, "/b.mp3"); !errors.Is(err, boom) {
			t.Errorf("Full %d: %v", i, err)
		}
	}
	if _, err := f.Full(ctx, "/b.mp3"); err != nil {
		t.Errorf("Full after failures: %v", err)
	}
	if f.FullCalls() != 3 {
		t.Errorf("FullCalls = %d", f.FullCalls())
	}

	f.Fail(OpSummary, "/a.mp3", boom)
	if _, _, err := f.Summary("/a.mp3"); !errors.Is(err, boom) {
		t.Errorf("Summary err = %v", err)
	}

	if _, _, err := f.Summary("/gone.mp3"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing Summary: %v", err)
	}
	if err := f.Write(ctx, []string{"/a.mp3", "/gone.mp3"}, []model.FrameChange{{Name: "Title", Value: "Y"}}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing Write: %v", err)
	}

	f.SetMissing(true)
	var missing model.ErrKid3Missing
	if _, err := f.Full(ctx, "/a.mp3"); !errors.As(err, &missing) {
		t.Errorf("Full when missing: %v", err)
	}
	if err := f.Write(ctx, []string{"/a.mp3"}, []model.FrameChange{{Name: "Title", Value: "Z"}}); !errors.As(err, &missing) {
		t.Errorf("Write when missing: %v", err)
	}
	if _, _, err := f.Summary("/a.mp3"); err != nil || f.Available() {
		t.Errorf("Summary still works natively when kid3 is missing: %v", err)
	}
}
