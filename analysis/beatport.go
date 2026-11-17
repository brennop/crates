package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// bpTrack is the part of a Beatport v4 track this package reads.
type bpTrack struct {
	Name    string  `json:"name"`
	MixName string  `json:"mix_name"`
	BPM     float64 `json:"bpm"`
	Key     *struct {
		Name string `json:"name"`
	} `json:"key"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Remixers []struct {
		Name string `json:"name"`
	} `json:"remixers"`
	LengthMS int64 `json:"length_ms"`
}

// credits names the track's artists and remixers.
func (t bpTrack) credits() []string {
	out := t.artists()
	for _, r := range t.Remixers {
		out = append(out, r.Name)
	}
	return out
}

func (t bpTrack) artists() []string {
	var out []string
	for _, a := range t.Artists {
		out = append(out, a.Name)
	}
	return out
}

func (t bpTrack) String() string {
	s := strings.Join(t.artists(), ", ") + " – " + t.Name
	if t.MixName != "" {
		s += " (" + t.MixName + ")"
	}
	return s
}

// beatport searches the catalog for "artist title" and reads BPM and key
// from the result that is the track (see bestMatch).
func (a *Analyzer) beatport(ctx context.Context, t Track) Result {
	r := Result{Job: Beatport}
	q := strings.TrimSpace(artistSep.ReplaceAllString(t.Artist, " ") + " " + t.Title)
	tok, err := a.beatportToken(ctx, false)
	if err != nil {
		r.Err = err
		return r
	}
	if q == "" {
		r.Err = fmt.Errorf("track has no artist or title to search for")
		return r
	}
	resp, err := a.search(ctx, tok, q)
	if err == nil && refused(resp) {
		// The stored token may have been revoked before it expired:
		// refresh it once and try again.
		resp.Body.Close()
		if tok, err = a.beatportToken(ctx, true); err == nil {
			resp, err = a.search(ctx, tok, q)
		}
	}
	if err != nil {
		r.Err = err
		return r
	}
	defer resp.Body.Close()
	switch {
	case refused(resp):
		r.Err = fmt.Errorf("%w (%s)", ErrBeatportRefused, resp.Status)
		return r
	case resp.StatusCode != http.StatusOK:
		r.Err = fmt.Errorf("Beatport: %s", resp.Status)
		return r
	}
	var body struct {
		Tracks []bpTrack `json:"tracks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		r.Err = fmt.Errorf("Beatport: bad response: %w", err)
		return r
	}
	m, ok := bestMatch(body.Tracks, t)
	if !ok {
		r.Err = fmt.Errorf("not found on Beatport")
		return r
	}
	r.Match, r.BPM = m.String(), m.BPM
	if m.Key != nil && m.Key.Name != "" {
		r.Keys = []KeyGuess{{Key: m.Key.Name}}
	}
	return r
}

// search runs one catalog search for q with tok.
func (a *Analyzer) search(ctx context.Context, tok, q string) (*http.Response, error) {
	u := a.api + "/catalog/search/?" + url.Values{"q": {q}, "type": {"tracks"}, "per_page": {"25"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	return a.client.Do(req)
}

func refused(resp *http.Response) bool {
	return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
}

// bestMatch picks the search result that is t: one crediting one of t's
// artists (as artist or remixer) whose title, without its version in
// parentheses, is t's. Among those it takes the one closest to t's length,
// else the one whose version t's title names, else the first. ok is false
// when no result is t; the search always returns something, and a guess
// would show another song's BPM and key.
func bestMatch(ts []bpTrack, t Track) (best bpTrack, ok bool) {
	artists := splitArtists(t.Artist)
	title := fold(baseTitle(t.Title))
	full := fold(t.Title)
	bestScore := 0.0
	for _, c := range ts {
		if fold(baseTitle(c.Name)) != title || !creditsAny(c, artists) {
			continue
		}
		score := 0.0 // lower is better
		if t.Duration > 0 && c.LengthMS > 0 {
			score = math.Abs(float64(c.LengthMS)/1000 - t.Duration.Seconds())
		}
		if c.MixName == "" || !strings.Contains(full, fold(c.MixName)) {
			score += 0.5 // a tie on length goes to the named version
		}
		if !ok || score < bestScore {
			best, bestScore, ok = c, score, true
		}
	}
	return best, ok
}

// creditsAny reports whether c credits one of artists, as artist or
// remixer. Names match when one contains the other, so "Fred Falke" matches
// "Fred Falke & Alan Braxe".
func creditsAny(c bpTrack, artists []string) bool {
	for _, a := range artists {
		for _, b := range c.credits() {
			if b = fold(b); b != "" && (strings.Contains(a, b) || strings.Contains(b, a)) {
				return true
			}
		}
	}
	return false
}

// artistSep splits an artist tag that names several artists.
var artistSep = regexp.MustCompile(`(?i)\s*(?:/|,|&|;|\bfeat\.?|\bft\.?|\bfeaturing\b)\s*`)

// splitArtists returns each artist in an artist tag, folded.
func splitArtists(s string) []string {
	var out []string
	for _, a := range artistSep.Split(s, -1) {
		if a = fold(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// baseTitle drops a title's version: trailing groups in parentheses or
// brackets, and anything after " - ", so "Tension (Extended Mix)" and
// "Tension - Extended Mix" are both "Tension".
func baseTitle(s string) string {
	if i := strings.Index(s, " - "); i > 0 {
		s = s[:i]
	}
	for {
		s = strings.TrimSpace(s)
		if s == "" || (s[len(s)-1] != ')' && s[len(s)-1] != ']') {
			return s
		}
		open := strings.LastIndexAny(s, "([")
		if open <= 0 {
			return s
		}
		s = s[:open]
	}
}

// fold lowercases s, drops accents and punctuation and collapses spaces, so
// "Faz os Vacilão" and "faz os vacilao" compare equal.
func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case r == '\'' || r == '’':
			// "Can't" and "Cant" alike
		default:
			space = true
		}
	}
	return b.String()
}
