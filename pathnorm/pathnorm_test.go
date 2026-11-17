package pathnorm

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestNormalize(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	nfd := norm.NFD.String("Beyoncé.mp3")

	cases := []struct{ in, want string }{
		{filepath.Join(root, "real", "..", "real", "a.mp3"), filepath.Join(real, "a.mp3")},
		{filepath.Join(link, "a.mp3"), filepath.Join(real, "a.mp3")},
		{filepath.Join(link, "missing", "deeper", "b.mp3"), filepath.Join(real, "missing", "deeper", "b.mp3")},
		{filepath.Join(real, nfd), filepath.Join(real, norm.NFC.String("Beyoncé.mp3"))},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	t.Chdir(real)
	got, err := Normalize("x.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(real, "x.mp3") {
		t.Errorf("relative: got %q", got)
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"/a/b", "/a", true},
		{"/a", "/a", true},
		{"/ab", "/a", false},
		{"/a", "/a/b", false},
		{"/..x/y", "/", true},
		{"/a/..b", "/a", true},
	}
	for _, c := range cases {
		if got := Within(c.path, c.dir); got != c.want {
			t.Errorf("Within(%q, %q) = %v", c.path, c.dir, got)
		}
	}
}
