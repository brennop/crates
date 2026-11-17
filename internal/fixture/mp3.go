// Package fixture builds small, valid MP3 files for tests: repeated silent
// CBR frames (MPEG-1 Layer III, 128 kbit/s, 44.1 kHz, mono), with or without
// a Xing header, tagged with ID3v2.3, ID3v2.4, ID3v1 or nothing.
package fixture

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/brennop/crates/model"
)

// Frame layout constants.
const (
	FrameSize       = 417 // 144 * 128000 / 44100, no padding
	SamplesPerFrame = 1152
	SampleRate      = 44100
	sideInfoMono    = 17
)

// FrameDuration is the play time of one frame.
const FrameDuration = time.Duration(SamplesPerFrame) * time.Second / SampleRate

// TagKind picks the tag format.
type TagKind int

const (
	Untagged TagKind = iota
	ID3v23
	ID3v24
	ID3v1
)

func (k TagKind) String() string {
	return [...]string{"untagged", "id3v2.3", "id3v2.4", "id3v1"}[k]
}

// Options describe one file.
type Options struct {
	Tag     TagKind
	Summary model.TagSummary
	// Frames is the number of audio frames; 0 means 40 (about 1 s).
	Frames int
	// Xing prepends a Xing frame that states the frame and byte counts.
	Xing bool
	// XingFrames, when non-zero, is the frame count the Xing header claims
	// instead of the real one, so a test can tell the header was used.
	XingFrames int
}

func (o Options) frames() int {
	if o.Frames == 0 {
		return 40
	}
	return o.Frames
}

// Duration is the audio length a correct reader should report: from the
// Xing frame count when there is a Xing header, else from the real frames.
func (o Options) Duration() time.Duration {
	n := o.frames()
	if o.Xing && o.XingFrames != 0 {
		n = o.XingFrames
	}
	return time.Duration(n) * FrameDuration
}

// MP3 returns the bytes of a file built from o.
func MP3(o Options) []byte {
	var b bytes.Buffer
	switch o.Tag {
	case ID3v23:
		b.Write(id3v2(3, o.Summary))
	case ID3v24:
		b.Write(id3v2(4, o.Summary))
	}
	n := o.frames()
	if o.Xing {
		claim := n
		if o.XingFrames != 0 {
			claim = o.XingFrames
		}
		b.Write(xingFrame(claim, (n+1)*FrameSize))
	}
	silent := silentFrame()
	for i := 0; i < n; i++ {
		b.Write(silent)
	}
	if o.Tag == ID3v1 {
		b.Write(id3v1(o.Summary))
	}
	return b.Bytes()
}

// frameHeader: sync, MPEG-1, Layer III, no CRC; 128 kbit/s, 44.1 kHz, no
// padding; mono.
var frameHeader = [4]byte{0xFF, 0xFB, 0x90, 0xC0}

// silentFrame is a header followed by zeros: zero side info means every
// granule has no main data, which decodes as silence.
func silentFrame() []byte {
	f := make([]byte, FrameSize)
	copy(f, frameHeader[:])
	return f
}

func xingFrame(frames, size int) []byte {
	f := silentFrame()
	x := f[4+sideInfoMono:]
	copy(x, "Xing")
	binary.BigEndian.PutUint32(x[4:], 0x3) // frames and bytes fields present
	binary.BigEndian.PutUint32(x[8:], uint32(frames))
	binary.BigEndian.PutUint32(x[12:], uint32(size))
	return f
}

// ---- ID3v2 ----

func id3v2(version byte, s model.TagSummary) []byte {
	yearID := "TYER"
	if version == 4 {
		yearID = "TDRC"
	}
	var frames bytes.Buffer
	addRaw := func(id string, data []byte) {
		frames.WriteString(id)
		var size [4]byte
		if version == 4 {
			size = syncsafe(len(data))
		} else {
			binary.BigEndian.PutUint32(size[:], uint32(len(data)))
		}
		frames.Write(size[:])
		frames.Write([]byte{0, 0}) // flags
		frames.Write(data)
	}
	add := func(id, val string) {
		if val != "" {
			addRaw(id, textFrameData(version, val))
		}
	}
	add("TIT2", s.Title)
	add("TPE1", s.Artist)
	add("TALB", s.Album)
	add("TPE2", s.AlbumArtist)
	add("TRCK", itoa(s.TrackNumber))
	add(yearID, itoa(s.Year))
	add("TCON", s.Genre)
	if s.Comment != "" {
		addRaw("COMM", commentFrameData(version, s.Comment))
	}
	if s.BPM != 0 {
		add("TBPM", strconv.FormatFloat(s.BPM, 'f', -1, 64))
	}
	add("TKEY", s.Key)

	var b bytes.Buffer
	b.WriteString("ID3")
	b.Write([]byte{version, 0, 0})
	size := syncsafe(frames.Len())
	b.Write(size[:])
	b.Write(frames.Bytes())
	return b.Bytes()
}

// textFrameData encodes a text frame body: Latin-1 when it fits, else UTF-16
// with BOM (v2.3) or UTF-8 (v2.4).
func textFrameData(version byte, val string) []byte {
	if l, ok := latin1(val); ok {
		return append([]byte{0}, l...)
	}
	if version == 4 {
		return append([]byte{3}, val...)
	}
	out := []byte{1, 0xFF, 0xFE} // UTF-16, little-endian BOM
	for _, u := range utf16.Encode([]rune(val)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// commentFrameData encodes a COMM body: encoding, language "eng", an empty
// short description, then the text in the same encoding.
func commentFrameData(version byte, val string) []byte {
	data := textFrameData(version, val)
	enc := data[0]
	out := append([]byte{enc}, "eng"...)
	if enc == 1 { // UTF-16 empty description is a double zero
		out = append(out, 0, 0)
	} else {
		out = append(out, 0)
	}
	return append(out, data[1:]...)
}

func syncsafe(n int) [4]byte {
	return [4]byte{byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
}

// ---- ID3v1 ----

// v1Genres maps a few genre names to their ID3v1 index.
var v1Genres = map[string]byte{"Blues": 0, "Jazz": 8, "Pop": 13, "Rock": 17, "Electronic": 52}

func id3v1(s model.TagSummary) []byte {
	t := make([]byte, 128)
	copy(t, "TAG")
	put := func(off, n int, v string) {
		l, _ := latin1(v)
		copy(t[off:off+n], l)
	}
	put(3, 30, s.Title)
	put(33, 30, s.Artist)
	put(63, 30, s.Album)
	put(93, 4, itoa(s.Year))
	put(97, 30, s.Comment)
	// then a zero byte and the track number (ID3v1.1)
	t[126] = byte(s.TrackNumber)
	t[127] = 255
	if g, ok := v1Genres[s.Genre]; ok {
		t[127] = g
	}
	return t
}

// latin1 converts s to ISO-8859-1; ok is false if some rune does not fit (the
// rune is then written as '?').
func latin1(s string) (out []byte, ok bool) {
	ok = true
	for _, r := range s {
		if r > 0xFF {
			r, ok = '?', false
		}
		out = append(out, byte(r))
	}
	return out, ok
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
