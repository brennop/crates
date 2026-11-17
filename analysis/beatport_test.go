package analysis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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

	a := New(Options{BeatportToken: "tok"})
	a.api = srv.URL
	r := a.Run(context.Background(), Beatport, Track{Artist: "Daft Punk", Title: "Around the World"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.BPM != 121 || len(r.Keys) != 1 || r.Keys[0].Key != "A Minor" || r.Match != "Daft Punk – Around the World (Original Mix)" {
		t.Fatalf("result %+v", r)
	}

	a.opts.BeatportToken = "bad"
	if r := a.Run(context.Background(), Beatport, Track{Artist: "x"}); r.Err == nil {
		t.Fatal("a refused token gave no error")
	}
}

func TestBeatportNeedsToken(t *testing.T) {
	r := New(Options{}).Run(context.Background(), Beatport, Track{Artist: "x"})
	if !errors.Is(r.Err, ErrNoBeatportToken) {
		t.Fatalf("err = %v", r.Err)
	}
}
