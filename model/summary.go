package model

import "strconv"

// TagSummary is the handful of tag fields the track list shows. The full frame
// set is read on demand through kid3-cli.
type TagSummary struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	TrackNumber int // 0 = unset
	Year        int // 0 = unset
	Genre       string
}

// Summary field names, as used by required_tags in config.
const (
	FieldTitle       = "title"
	FieldArtist      = "artist"
	FieldAlbum       = "album"
	FieldAlbumArtist = "album_artist"
	FieldTrackNumber = "track"
	FieldYear        = "year"
	FieldGenre       = "genre"
)

// SummaryFields lists every TagSummary field name in display order.
var SummaryFields = []string{
	FieldTitle, FieldArtist, FieldAlbum, FieldAlbumArtist,
	FieldTrackNumber, FieldYear, FieldGenre,
}

// IsSummaryField reports whether name is a TagSummary field name.
func IsSummaryField(name string) bool {
	_, ok := TagSummary{}.Field(name)
	return ok
}

// Field returns the named field as text ("" when unset) and whether the name
// is known.
func (s TagSummary) Field(name string) (string, bool) {
	switch name {
	case FieldTitle:
		return s.Title, true
	case FieldArtist:
		return s.Artist, true
	case FieldAlbum:
		return s.Album, true
	case FieldAlbumArtist:
		return s.AlbumArtist, true
	case FieldTrackNumber:
		return itoaNonZero(s.TrackNumber), true
	case FieldYear:
		return itoaNonZero(s.Year), true
	case FieldGenre:
		return s.Genre, true
	}
	return "", false
}

// Missing returns the names in required whose fields are unset, in the order
// given. Unknown names count as missing.
func (s TagSummary) Missing(required []string) []string {
	var out []string
	for _, name := range required {
		if v, ok := s.Field(name); !ok || v == "" {
			out = append(out, name)
		}
	}
	return out
}

func itoaNonZero(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
