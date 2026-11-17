package analysis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBeatportPicksArtistMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if q := r.URL.Query().Get("q"); q != "Daft Punk Around the World" {
			t.Errorf("q = %q", q)
		}
		w.Write([]byte(`{"tracks":[
			{"name":"Around the World","mix_name":"Cover","bpm":100,"key":{"name":"C Major"},"artists":[{"name":"Someone"}]},
			{"name":"Around the World","mix_name":"Original Mix","bpm":121,"key":{"name":"A Minor"},"artists":[{"name":"Daft Punk"}]}
		]}`))
	}))
	defer srv.Close()

	a := New(Options{BeatportTokenFile: filepath.Join(t.TempDir(), "beatport.json")})
	a.api = srv.URL
	if a.HasBeatportToken() {
		t.Fatal("a token before any was saved")
	}
	if err := a.SaveBeatportToken(" tok\n"); err != nil || !a.HasBeatportToken() {
		t.Fatalf("save a bare token: %v", err)
	}
	r := a.Run(context.Background(), Beatport, Track{Artist: "Daft Punk", Title: "Around the World"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.BPM != 121 || len(r.Keys) != 1 || r.Keys[0].Key != "A Minor" || r.Match != "Daft Punk – Around the World (Original Mix)" {
		t.Fatalf("result %+v", r)
	}

	// Refused, with no refresh token to try.
	a.SaveBeatportToken("bad")
	if r := a.Run(context.Background(), Beatport, Track{Artist: "x"}); !errors.Is(r.Err, ErrBeatportRefused) {
		t.Fatalf("a refused token: %v", r.Err)
	}
}

func TestBeatportNeedsToken(t *testing.T) {
	r := New(Options{}).Run(context.Background(), Beatport, Track{Artist: "x"})
	if !errors.Is(r.Err, ErrNoBeatportToken) {
		t.Fatalf("err = %v", r.Err)
	}
}

// A stored token past its expiry is refreshed with its refresh token and the
// client named in the JWT, the new pair is saved, and a refused token is
// refreshed once more and retried.
func TestBeatportRefreshesStoredToken(t *testing.T) {
	jwt := "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"client_id":"cid"}`)) + ".s"
	valid, refreshes := "", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/o/token/" {
			r.ParseForm()
			want := fmt.Sprintf("rt%d", refreshes)
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != want || r.Form.Get("client_id") != "cid" {
				t.Errorf("refresh form %v, want refresh_token %s", r.Form, want)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			refreshes++
			valid = fmt.Sprintf("at%d", refreshes)
			fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt%d","expires_in":600}`, valid, refreshes)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"tracks":[{"name":"T","bpm":120,"artists":[{"name":"A"}]}]}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "beatport.json")
	a := New(Options{BeatportTokenFile: path})
	a.api = srv.URL
	if err := a.SaveBeatportToken(`{"access_token":"` + jwt + `","refresh_token":"rt0","expires_in":600}`); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if r := a.Run(context.Background(), Beatport, Track{Artist: "A", Title: "T"}); r.Err != nil || r.BPM != 120 {
			t.Fatalf("result %+v", r)
		}
	}
	saved := func() bpToken {
		var tok bpToken
		data, _ := os.ReadFile(path)
		json.Unmarshal(data, &tok)
		return tok
	}

	run() // the access token is refused: refreshed
	if tok := saved(); refreshes != 1 || tok.AccessToken != "at1" || tok.RefreshToken != "rt1" || tok.ClientID != "cid" || tok.ExpiresAt < time.Now().Unix()+500 {
		t.Fatalf("after first run: %d refreshes, saved %+v", refreshes, tok)
	}
	run() // still valid: no refresh
	if refreshes != 1 {
		t.Fatalf("refreshed a valid token")
	}
	valid = "revoked"
	run() // refused: refresh once and retry
	if tok := saved(); refreshes != 2 || tok.RefreshToken != "rt2" {
		t.Fatalf("after a refusal: %d refreshes, saved %+v", refreshes, tok)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v", fi.Mode())
	}
}

// Cases from the real library where the first artist match was wrong.
func TestBestMatch(t *testing.T) {
	tr := func(artists, name, mix string, secs int64) bpTrack {
		var c bpTrack
		c.Name, c.MixName, c.LengthMS = name, mix, secs*1000
		for _, a := range strings.Split(artists, ", ") {
			c.Artists = append(c.Artists, struct {
				Name string `json:"name"`
			}{a})
		}
		return c
	}
	kylie := []bpTrack{
		tr("Kylie Minogue", "Tension", "Extended Mix", 278),
		tr("Kylie Minogue", "Tension", "Original Mix", 216),
		tr("Kylie Minogue", "Tension", "Chromeo Remix", 246),
	}
	chemise := []bpTrack{
		tr("Purple Disco Machine, Chemise", "She Can't Love You", "Purple Disco Machine Edit", 270),
		tr("Chemise", "She Can't Love You", "Original Mix", 326),
		tr("Chemise", "She Can't Love You", "Remix", 377),
	}
	for _, c := range []struct {
		name string
		in   []bpTrack
		t    Track
		want string // "" = not found
	}{
		{"closest length", kylie, Track{Artist: "Kylie Minogue", Title: "Tension", Duration: 216 * time.Second}, "Original Mix"},
		{"named version without a length", chemise, Track{Artist: "Chemise", Title: "She Can't Love You (Remix)"}, "Remix"},
		{"closest length over the first credit", chemise, Track{Artist: "Chemise", Title: "She Can't Love You (Remix)", Duration: 377 * time.Second}, "Remix"},
		{"another song by the artist", []bpTrack{tr("JonEcks, Lotso", "klaSH", "", 178)}, Track{Artist: "Lotso", Title: "boogiewondaland"}, ""},
		{"the title by another artist", []bpTrack{tr("Oppidan, camoufly", "hold tight", "Original Mix", 148)}, Track{Artist: "Azuro", Title: "Hold Tight (Edit)"}, ""},
		{"several artists, accents", []bpTrack{tr("Traumer, Anton, antraum", "Ju", "Traumer Sunset Mix", 349), tr("Rita Lee, Roberto De Carvalho", "Lança Perfume", "Original Mix", 314)},
			Track{Artist: "Rita Lee/Roberto de Carvalho", Title: "Lanca perfume"}, "Original Mix"},
	} {
		m, ok := bestMatch(c.in, c.t)
		if got := map[bool]string{true: m.MixName}[ok]; got != c.want || ok != (c.want != "") {
			t.Errorf("%s: picked %q (ok %v), want %q", c.name, m.String(), ok, c.want)
		}
	}
}
