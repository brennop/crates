package fixture

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"golang.org/x/text/unicode/norm"

	"github.com/brennop/crates/model"
)

// File is one fixture: where it was written and what it was built from.
type File struct {
	Path string
	Options
}

// Standard fixture names, the keys of the map WriteSet returns.
const (
	V23       = "v23"       // ID3v2.3, Latin-1 text
	V23UTF16  = "v23-utf16" // ID3v2.3 with UTF-16 text
	V24       = "v24"       // ID3v2.4, UTF-8 text
	V1        = "v1"        // ID3v1 only
	Bare      = "untagged"
	Xing      = "xing"       // ID3v2.4 with a Xing header that claims 200 frames
	Quotes    = "quotes"     // quotes in the file name and the tags
	NFD       = "nfd"        // NFD-decomposed file name
	CaseLower = "case-lower" // case/a/Case Track.mp3
	CaseUpper = "case-upper" // case/b/CASE TRACK.mp3; same name, other case
)

// Full is a summary with every field set.
var Full = model.TagSummary{
	Title: "Song", Artist: "Artist", Album: "Album", AlbumArtist: "Album Artist",
	TrackNumber: 3, Year: 2001, Genre: "Rock",
}

// WriteSet writes the standard fixtures under dir and returns them by name.
// The two case variants go in separate subdirectories, since they cannot
// share a directory on a case-insensitive volume.
func WriteSet(dir string) (map[string]File, error) {
	uni := model.TagSummary{Title: "Café Ünïcødé ☃", Artist: "Björk", Album: "日本語", Year: 1997, TrackNumber: 1}
	quoted := model.TagSummary{Title: `It's "Quoted"`, Artist: `O'Brien & "Co"`, Album: `Album's "Best"`}
	set := map[string]struct {
		name string
		Options
	}{
		V23:       {"v23.mp3", Options{Tag: ID3v23, Summary: Full}},
		V23UTF16:  {"v23-utf16.mp3", Options{Tag: ID3v23, Summary: uni}},
		V24:       {"v24.mp3", Options{Tag: ID3v24, Summary: uni}},
		V1:        {"v1.mp3", Options{Tag: ID3v1, Summary: Full}},
		Bare:      {"untagged.mp3", Options{}},
		Xing:      {"xing.mp3", Options{Tag: ID3v24, Summary: Full, Xing: true, XingFrames: 200}},
		Quotes:    {`It's "Quoted".mp3`, Options{Tag: ID3v24, Summary: quoted}},
		NFD:       {norm.NFD.String("Café Été.mp3"), Options{Tag: ID3v23, Summary: Full}},
		CaseLower: {filepath.Join("case", "a", "Case Track.mp3"), Options{Tag: ID3v23, Summary: Full}},
		CaseUpper: {filepath.Join("case", "b", "CASE TRACK.mp3"), Options{Tag: ID3v24, Summary: Full}},
	}
	out := make(map[string]File, len(set))
	for key, f := range set {
		p := filepath.Join(dir, f.name)
		if err := write(p, f.Options); err != nil {
			return nil, err
		}
		out[key] = File{Path: p, Options: f.Options}
	}
	return out, nil
}

// GenerateLibrary writes n small tagged files into dir (created if needed),
// varying the tag format, and returns them. The same seed gives the same
// files.
func GenerateLibrary(dir string, n int, seed int64) ([]File, error) {
	rng := rand.New(rand.NewSource(seed))
	kinds := []TagKind{ID3v23, ID3v24, ID3v24, ID3v1, Untagged}
	genres := []string{"Rock", "Pop", "Jazz", "Electronic", "Blues"}
	out := make([]File, 0, n)
	for i := 0; i < n; i++ {
		artist := fmt.Sprintf("Artist %03d", rng.Intn(n/10+1))
		o := Options{
			Tag:    kinds[rng.Intn(len(kinds))],
			Frames: 4 + rng.Intn(8),
			Xing:   rng.Intn(3) == 0,
			Summary: model.TagSummary{
				Title:       fmt.Sprintf("Title %05d", i),
				Artist:      artist,
				Album:       fmt.Sprintf("Album %03d", rng.Intn(n/8+1)),
				AlbumArtist: artist,
				TrackNumber: 1 + rng.Intn(20),
				Year:        1960 + rng.Intn(65),
				Genre:       genres[rng.Intn(len(genres))],
			},
		}
		if o.Tag == Untagged {
			o.Summary = model.TagSummary{}
		}
		p := filepath.Join(dir, fmt.Sprintf("%05d %s - Title %05d.mp3", i, artist, i))
		if err := write(p, o); err != nil {
			return nil, err
		}
		out = append(out, File{Path: p, Options: o})
	}
	return out, nil
}

func write(path string, o Options) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, MP3(o), 0o644)
}
