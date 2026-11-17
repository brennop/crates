package tags

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"codeberg.org/derat/mpeg"
	"github.com/dhowden/tag"

	"github.com/brennop/crates/model"
)

// Summary reads the tag summary and the audio duration of one MP3 natively,
// with no process per file. A file with no tags is not an error: it returns
// an empty summary. A tag that cannot be parsed is. Duration is best effort:
// from the Xing header when there is one, otherwise assuming constant
// bitrate; it is 0 when no MPEG-1 Layer III frame is found, and mpv corrects
// it on first play.
func Summary(path string) (model.TagSummary, time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.TagSummary{}, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return model.TagSummary{}, 0, err
	}

	var s model.TagSummary
	m, err := tag.ReadFrom(f)
	switch {
	case errors.Is(err, tag.ErrNoTagsFound):
	case err != nil:
		return model.TagSummary{}, 0, fmt.Errorf("read tags: %w", err)
	default:
		s = summaryOf(m)
	}
	return s, duration(f, fi), nil
}

func summaryOf(m tag.Metadata) model.TagSummary {
	track, _ := m.Track()
	s := model.TagSummary{
		Title:       clean(m.Title()),
		Artist:      clean(m.Artist()),
		Album:       clean(m.Album()),
		AlbumArtist: clean(m.AlbumArtist()),
		TrackNumber: track,
		Year:        m.Year(),
		Genre:       clean(m.Genre()),
		Comment:     clean(m.Comment()),
	}
	raw := m.Raw()
	// ID3v2.3 and 2.4 use four-letter frame IDs, ID3v2.2 three-letter ones.
	s.BPM = parseBPM(rawText(raw, "TBPM", "TBP"))
	s.Key = rawText(raw, "TKEY", "TKE")
	return s
}

// rawText returns the first of the named frames that holds text.
func rawText(raw map[string]any, ids ...string) string {
	for _, id := range ids {
		if v, ok := raw[id].(string); ok {
			if v = clean(v); v != "" {
				return v
			}
		}
	}
	return ""
}

// clean trims spaces and the NUL padding and separators some taggers leave.
func clean(s string) string {
	return strings.TrimSpace(strings.Trim(s, "\x00"))
}

// parseBPM reads a TBPM value. The spec says an integer, but taggers write
// decimals, sometimes with a comma. Anything else is unset.
func parseBPM(v string) float64 {
	if v == "" {
		return 0
	}
	n, err := strconv.ParseFloat(strings.Replace(v, ",", ".", 1), 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// duration computes the audio length, skipping a leading ID3v2 tag and a
// trailing ID3v1 tag. It returns 0 when it cannot tell.
func duration(f *os.File, fi os.FileInfo) time.Duration {
	header, err := id3v2Length(f)
	if err != nil {
		return 0
	}
	var footer int64
	if fi.Size() >= mpeg.ID3v1Length {
		buf := make([]byte, 3)
		if _, err := f.ReadAt(buf, fi.Size()-mpeg.ID3v1Length); err == nil && string(buf) == "TAG" {
			footer = mpeg.ID3v1Length
		}
	}
	d, _, err := mpeg.ComputeAudioDuration(f, fi, header, footer)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// id3v2Length returns the size of the ID3v2 tag at the start of f, header
// and footer included, or 0 if there is none.
func id3v2Length(f *os.File) (int64, error) {
	var h [10]byte
	if _, err := f.ReadAt(h[:], 0); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, nil
		}
		return 0, err
	}
	if !bytes.Equal(h[:3], []byte("ID3")) {
		return 0, nil
	}
	n := int64(h[6]&0x7f)<<21 | int64(h[7]&0x7f)<<14 | int64(h[8]&0x7f)<<7 | int64(h[9]&0x7f)
	n += 10
	if h[5]&0x10 != 0 { // footer present (v2.4)
		n += 10
	}
	return n, nil
}
