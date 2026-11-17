package library

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/brennop/crates/model"
)

// queryLibrary returns a library with a few tracks in both roots, built
// straight into the index.
func queryLibrary(t testing.TB) *Library {
	t.Helper()
	x := testIndex(true)
	l := &Library{idx: x}
	add := func(path, title, artist, key string, n int, role model.Role) {
		tr := track(path, title)
		tr.Summary.Artist = artist
		tr.Summary.Key = key
		tr.Summary.TrackNumber = n
		tr.Summary.Year = 2000 + n
		tr.Duration = time.Duration(n) * time.Second
		if _, _, err := x.upsert(tr); err != nil {
			t.Fatal(err)
		}
	}
	add("/lib/master/one.mp3", "Zebra", "The Band", "Am", 10, model.Master)
	add("/lib/master/two.mp3", "apple", "Other", "3B", 2, model.Master)
	add("/lib/inbox/three.mp3", "Mango", "THE BAND", "10A", 1, model.Inbox)
	return l
}

func titlesFor(t *testing.T, l *Library, ids []model.TrackID) []string {
	t.Helper()
	var out []string
	for _, tr := range l.Get(ids...) {
		out = append(out, tr.Summary.Title)
	}
	return out
}

func TestQueryFilters(t *testing.T) {
	l := queryLibrary(t)

	ids, err := l.Query(Filter{Text: "the band"}, Sort{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ids); got != 2 {
		t.Errorf("text filter matched %d tracks, want 2 (case-insensitive, over artist)", got)
	}

	inbox := model.Inbox
	ids, _ = l.Query(Filter{Role: &inbox}, Sort{})
	if got := titlesFor(t, l, ids); len(got) != 1 || got[0] != "Mango" {
		t.Errorf("role filter = %v, want [Mango]", got)
	}

	ids, _ = l.Query(Filter{}, Sort{})
	if len(ids) != 3 {
		t.Errorf("zero filter matched %d, want all 3", len(ids))
	}
}

func TestQuerySorts(t *testing.T) {
	l := queryLibrary(t)
	cases := []struct {
		name string
		s    Sort
		want []string
	}{
		{"title ascending ignores case", Sort{Field: model.FieldTitle}, []string{"apple", "Mango", "Zebra"}},
		{"title descending", Sort{Field: model.FieldTitle, Desc: true}, []string{"Zebra", "Mango", "apple"}},
		{"track number numerically", Sort{Field: model.FieldTrackNumber}, []string{"Mango", "apple", "Zebra"}},
		{"year numerically, descending", Sort{Field: model.FieldYear, Desc: true}, []string{"Zebra", "apple", "Mango"}},
		{"key around the Camelot wheel", Sort{Field: model.FieldKey}, []string{"apple", "Zebra", "Mango"}},
		{"duration", Sort{Field: "duration"}, []string{"Mango", "apple", "Zebra"}},
		{"path", Sort{Field: "path"}, []string{"Mango", "Zebra", "apple"}},
		{"role (inbox sorts first)", Sort{Field: "role"}, []string{"Mango", "Zebra", "apple"}},
		{"ID by default", Sort{}, []string{"Zebra", "apple", "Mango"}},
	}
	for _, c := range cases {
		ids, err := l.Query(Filter{}, c.s)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := titlesFor(t, l, ids); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestQueryRejectsUnknownSort(t *testing.T) {
	l := queryLibrary(t)
	if _, err := l.Query(Filter{}, Sort{Field: "bitrate"}); err == nil {
		t.Error("sort by an unknown column: want an error")
	}
}

func TestQueryConcurrentWithWrites(t *testing.T) {
	l := queryLibrary(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			tr := track(fmt.Sprintf("/lib/master/w%03d.mp3", i), "W")
			l.idx.upsert(tr)
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := l.Query(Filter{Text: "w"}, Sort{Field: model.FieldTitle}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// BenchmarkQuery times a text filter and sort over 5000 tracks.
func BenchmarkQuery(b *testing.B) {
	x := testIndex(true)
	l := &Library{idx: x}
	for i := 0; i < 5000; i++ {
		tr := track(fmt.Sprintf("/lib/master/%05d.mp3", i), fmt.Sprintf("Title %05d", i))
		tr.Summary.Artist = fmt.Sprintf("Artist %03d", i%300)
		tr.Summary.TrackNumber = i % 20
		x.upsert(tr)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Query(Filter{Text: "artist 12"}, Sort{Field: model.FieldArtist}); err != nil {
			b.Fatal(err)
		}
	}
}
