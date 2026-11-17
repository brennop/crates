package fsops

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCleanStale(t *testing.T) {
	master := t.TempDir()
	copyTmp := filepath.Join(master, ".crates-copy-123")
	kept := filepath.Join(master, ".crates-replace-456", "Song.mp3")
	emptyHold := filepath.Join(master, ".crates-replace-789")
	real := filepath.Join(master, "Song.mp3")
	other := filepath.Join(master, "Other.mp3")

	writeFile(t, copyTmp, "partial")
	if err := os.Mkdir(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, kept, "old")
	if err := os.Mkdir(emptyHold, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, real, "keep")
	writeFile(t, other, "keep")

	st, err := CleanStale(master)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Removed, []string{copyTmp}) {
		t.Errorf("Removed = %v, want [%s]", st.Removed, copyTmp)
	}
	if !slices.Equal(st.Kept, []string{kept}) {
		t.Errorf("Kept = %v, want [%s]", st.Kept, kept)
	}
	mustMissing(t, copyTmp)
	mustMissing(t, emptyHold)
	if got := readFile(t, kept); got != "old" {
		t.Errorf("kept backup was touched: %q", got)
	}
	if got := readFile(t, real); got != "keep" {
		t.Errorf("master file was touched: %q", got)
	}
	if got := readFile(t, other); got != "keep" {
		t.Errorf("master file was touched: %q", got)
	}
}

func TestCleanStaleMissingMaster(t *testing.T) {
	if _, err := CleanStale(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing master should be an error")
	}
}
