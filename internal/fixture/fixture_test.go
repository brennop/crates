package fixture

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

func TestFrameLayout(t *testing.T) {
	o := Options{Frames: 5}
	b := MP3(o)
	if len(b) != 5*FrameSize {
		t.Fatalf("len = %d, want %d", len(b), 5*FrameSize)
	}
	for i := 0; i < 5; i++ {
		if !bytes.Equal(b[i*FrameSize:i*FrameSize+4], frameHeader[:]) {
			t.Fatalf("no frame header at frame %d", i)
		}
	}
	if FrameDuration.Round(time.Microsecond) != 26122*time.Microsecond {
		t.Errorf("FrameDuration = %v", FrameDuration)
	}
}

func TestID3v2Header(t *testing.T) {
	for _, v := range []TagKind{ID3v23, ID3v24} {
		b := MP3(Options{Tag: v, Summary: Full, Frames: 1})
		if string(b[:3]) != "ID3" {
			t.Fatalf("%v: no ID3 header", v)
		}
		s := b[6:10]
		size := int(s[0])<<21 | int(s[1])<<14 | int(s[2])<<7 | int(s[3])
		if got := len(b) - 10 - size; got != FrameSize {
			t.Errorf("%v: tag size %d leaves %d bytes of audio", v, size, got)
		}
	}
}

func TestID3v1Trailer(t *testing.T) {
	b := MP3(Options{Tag: ID3v1, Summary: Full, Frames: 1})
	tag := b[len(b)-128:]
	if string(tag[:3]) != "TAG" || string(bytes.TrimRight(tag[3:33], "\x00")) != "Song" || tag[126] != 3 || tag[127] != 17 {
		t.Errorf("bad ID3v1 tag: %q", tag)
	}
}

func TestXingHeader(t *testing.T) {
	b := MP3(Options{Xing: true, XingFrames: 200, Frames: 3})
	x := b[4+sideInfoMono:]
	if string(x[:4]) != "Xing" || binary.BigEndian.Uint32(x[8:]) != 200 {
		t.Errorf("bad Xing header")
	}
	if len(b) != 4*FrameSize {
		t.Errorf("len = %d", len(b))
	}
}

func TestWriteSet(t *testing.T) {
	dir := t.TempDir()
	set, err := WriteSet(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range set {
		if _, err := os.Stat(f.Path); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if base := filepath.Base(set[NFD].Path); !norm.NFD.IsNormalString(base) || norm.NFC.IsNormalString(base) {
		t.Errorf("NFD name is not decomposed: %q", base)
	}
	if filepath.Base(set[CaseLower].Path) == filepath.Base(set[CaseUpper].Path) {
		t.Error("case variants have the same name")
	}
}

func TestGenerateLibrary(t *testing.T) {
	dir := t.TempDir()
	files, err := GenerateLibrary(dir, 300, 1)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(files) != 300 || len(entries) != 300 {
		t.Fatalf("got %d files, %d entries", len(files), len(entries))
	}
	again, _ := GenerateLibrary(t.TempDir(), 300, 1)
	if again[42].Summary != files[42].Summary || again[42].Tag != files[42].Tag {
		t.Error("same seed gave different files")
	}
}

// TestFFprobe checks the fixtures against an independent decoder.
func TestFFprobe(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	set, err := WriteSet(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range set {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-print_format", "json",
				"-show_format", "-show_streams", f.Path).Output()
			if err != nil {
				t.Fatalf("ffprobe: %v", err)
			}
			var p struct {
				Streams []struct {
					CodecName  string `json:"codec_name"`
					SampleRate string `json:"sample_rate"`
				} `json:"streams"`
				Format struct {
					Duration string            `json:"duration"`
					Tags     map[string]string `json:"tags"`
				} `json:"format"`
			}
			if err := json.Unmarshal(out, &p); err != nil {
				t.Fatal(err)
			}
			if len(p.Streams) != 1 || p.Streams[0].CodecName != "mp3" || p.Streams[0].SampleRate != "44100" {
				t.Fatalf("streams = %+v", p.Streams)
			}
			d, _ := strconv.ParseFloat(p.Format.Duration, 64)
			got := time.Duration(d * float64(time.Second))
			if diff := got - f.Duration(); diff < -30*time.Millisecond || diff > 30*time.Millisecond {
				t.Errorf("duration = %v, want about %v", got, f.Duration())
			}
			if f.Tag != Untagged {
				tags := p.Format.Tags
				if tags["title"] != f.Summary.Title || tags["artist"] != f.Summary.Artist || tags["album"] != f.Summary.Album {
					t.Errorf("tags = %v, want %+v", tags, f.Summary)
				}
			}
		})
	}
}
