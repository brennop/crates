package pathnorm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaseSensitive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Music")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cs, err := CaseSensitive(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Probe the expected answer directly: create a file and look for it
	// with its case changed.
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(dir, "PROBE"))
	wantSensitive := err != nil
	if cs != wantSensitive {
		t.Errorf("CaseSensitive(%q) = %v, probe says %v", dir, cs, wantSensitive)
	}
}

func TestCaseSensitiveMissingAndNoLetters(t *testing.T) {
	if _, err := CaseSensitive(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("missing dir: want an error")
	}
	if cs, err := CaseSensitive("/"); err != nil || !cs {
		t.Errorf("/ = %v, %v; want sensitive, nil", cs, err)
	}
}

func TestKeyAndWithinKey(t *testing.T) {
	if Key("/A/b.mp3", true) == Key("/a/B.mp3", true) {
		t.Error("sensitive keys must differ by case")
	}
	if Key("/A/b.mp3", false) != Key("/a/B.mp3", false) {
		t.Error("insensitive keys must match by case")
	}
	cases := []struct {
		path, dir string
		cs, want  bool
	}{
		{"/Music/x.mp3", "/music", false, true},
		{"/Music/x.mp3", "/music", true, false},
		{"/music", "/music", true, true},
		{"/musicx/a.mp3", "/music", false, false},
		{"/a/b", "/", true, true},
	}
	for _, c := range cases {
		if got := WithinKey(c.path, c.dir, c.cs); got != c.want {
			t.Errorf("WithinKey(%q, %q, %v) = %v", c.path, c.dir, c.cs, got)
		}
	}
}

func TestChild(t *testing.T) {
	nfd := "é.mp3"
	if got, want := Child("/m", nfd), "/m/é.mp3"; got != want {
		t.Errorf("Child = %q, want %q", got, want)
	}
}
