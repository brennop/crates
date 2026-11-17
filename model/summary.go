package model

import "strconv"

// TagSummary is the handful of tag fields the track list shows, sorts and
// filters on. The full frame set is read on demand through kid3-cli.
type TagSummary struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	TrackNumber int // 0 = unset
	Year        int // 0 = unset
	Genre       string
	Comment     string
	BPM         float64 // 0 = unset; the TBPM frame, parsed as a number
	Key         string  // the TKEY frame as text, e.g. "Am" or "8A"
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
	FieldComment     = "comment"
	FieldBPM         = "bpm"
	FieldKey         = "key"
)

// SummaryFields lists every TagSummary field name in display order.
var SummaryFields = []string{
	FieldTitle, FieldArtist, FieldAlbum, FieldAlbumArtist,
	FieldTrackNumber, FieldYear, FieldGenre,
	FieldComment, FieldBPM, FieldKey,
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
	case FieldComment:
		return s.Comment, true
	case FieldBPM:
		if s.BPM == 0 {
			return "", true
		}
		return strconv.FormatFloat(s.BPM, 'f', -1, 64), true
	case FieldKey:
		return s.Key, true
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
