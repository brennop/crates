package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
// from the best match: the first result that credits the track's artist,
// else the first result.
func (a *Analyzer) beatport(ctx context.Context, t Track) Result {
	r := Result{Job: Beatport}
	if a.opts.BeatportToken == "" {
		r.Err = ErrNoBeatportToken
		return r
	}
	q := strings.TrimSpace(t.Artist + " " + t.Title)
	if q == "" {
		r.Err = fmt.Errorf("track has no artist or title to search for")
		return r
	}
	u := a.api + "/catalog/search/?" + url.Values{"q": {q}, "type": {"tracks"}, "per_page": {"10"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		r.Err = err
		return r
	}
	req.Header.Set("Authorization", "Bearer "+a.opts.BeatportToken)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		r.Err = err
		return r
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		r.Err = fmt.Errorf("Beatport refused the token (%s): get a fresh one", resp.Status)
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
	m, ok := bestMatch(body.Tracks, t.Artist)
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

func bestMatch(ts []bpTrack, artist string) (bpTrack, bool) {
	if len(ts) == 0 {
		return bpTrack{}, false
	}
	want := strings.ToLower(artist)
	for _, t := range ts {
		for _, a := range t.artists() {
			if a = strings.ToLower(a); a != "" && want != "" && (strings.Contains(want, a) || strings.Contains(a, want)) {
				return t, true
			}
		}
	}
	return ts[0], true
}
