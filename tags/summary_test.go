package tags

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brennop/crates/internal/fixture"
	"github.com/brennop/crates/model"
)

// closeDuration allows for derat/mpeg's CBR estimate. It takes the bitrate
// from the frame header (128 kbit/s), but fixture frames carry no padding, so
// their real rate is 127.7 kbit/s and the estimate runs 0.3% short. It also
// rounds to whole milliseconds.
func closeDuration(got, want time.Duration) bool {
	slack := max(10*time.Millisecond, want/200)
	return (got - want).Abs() <= slack
}

func TestSummaryFixtures(t *testing.T) {
	set, err := fixture.WriteSet(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range set {
		t.Run(name, func(t *testing.T) {
			s, d, err := Summary(f.Path)
			if err != nil {
				t.Fatalf("Summary: %v", err)
			}
			want := f.Summary
			switch f.Tag {
			case fixture.Untagged:
				want = model.TagSummary{}
			case fixture.ID3v1: // no album artist, BPM or key in ID3v1
				want.AlbumArtist, want.BPM, want.Key = "", 0, ""
			}
			if s != want {
				t.Errorf("summary\n got %+v\nwant %+v", s, want)
			}
			if !closeDuration(d, f.Duration()) {
				t.Errorf("duration %v, want %v", d, f.Duration())
			}
		})
	}
}

func TestSummaryDuration(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]fixture.Options{
		"cbr":       {Tag: fixture.ID3v24, Summary: fixture.Full, Frames: 200},
		"cbr-v1":    {Tag: fixture.ID3v1, Summary: fixture.Full, Frames: 200},
		"cbr-bare":  {Frames: 200},
		"xing":      {Tag: fixture.ID3v23, Summary: fixture.Full, Frames: 10, Xing: true},
		"xing-lies": {Tag: fixture.ID3v24, Frames: 10, Xing: true, XingFrames: 500},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name+".mp3")
			if err := os.WriteFile(p, fixture.MP3(o), 0o644); err != nil {
				t.Fatal(err)
			}
			_, d, err := Summary(p)
			if err != nil {
				t.Fatal(err)
			}
			want := o.Duration()
			if !closeDuration(d, want) {
				t.Errorf("duration %v, want %v", d, want)
			}
		})
	}
}

// Files the reader cannot make sense of return an error, which the library
// reports and indexes with an empty summary.
func TestSummaryUnreadable(t *testing.T) {
	dir := t.TempDir()
	tagged := fixture.MP3(fixture.Options{Tag: fixture.ID3v24, Summary: fixture.Full})
	badVersion := append([]byte(nil), tagged...)
	badVersion[3] = 9 // ID3v2.9
	cases := map[string][]byte{
		"empty":       nil,
		"junk":        []byte("this is not audio at all"),
		"truncated":   tagged[:30], // the tag header promises more
		"bad-version": badVersion,
	}
	for name, b := range cases {
		p := filepath.Join(dir, name+".mp3")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if s, _, err := Summary(p); err == nil {
			t.Errorf("%s: no error, summary %+v", name, s)
		}
	}
	if _, _, err := Summary(filepath.Join(dir, "missing.mp3")); !os.IsNotExist(err) {
		t.Errorf("missing file: err = %v", err)
	}
}

// Tags but no audio frame: the summary still comes back, duration 0.
func TestSummaryNoAudio(t *testing.T) {
	b := fixture.MP3(fixture.Options{Tag: fixture.ID3v24, Summary: fixture.Full, Frames: 1})
	b = append(b[:len(b)-fixture.FrameSize], make([]byte, 9000)...)
	p := filepath.Join(t.TempDir(), "noaudio.mp3")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, d, err := Summary(p)
	if err != nil || s != fixture.Full || d != 0 {
		t.Errorf("got %+v, %v, %v; want full summary, 0, nil", s, d, err)
	}
}

func TestParseBPM(t *testing.T) {
	for in, want := range map[string]float64{"": 0, "128": 128, "128.5": 128.5, "99,9": 99.9, "fast": 0, "-3": 0} {
		if got := parseBPM(in); got != want {
			t.Errorf("parseBPM(%q) = %v, want %v", in, got, want)
		}
	}
}

func BenchmarkSummary(b *testing.B) {
	set, err := fixture.WriteSet(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	p := set[fixture.V24].Path
	for b.Loop() {
		if _, _, err := Summary(p); err != nil {
			b.Fatal(err)
		}
	}
}
