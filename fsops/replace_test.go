package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/brennop/crates/model"
)

// replaceSetup returns a master dir holding Song.mp3 with "old" content and an
// inbox dir holding song.mp3 with "new" content, in a different case.
func replaceSetup(t *testing.T) (master, inbox, dst, src string) {
	t.Helper()
	master = t.TempDir()
	inbox = t.TempDir()
	dst = filepath.Join(master, "Song.mp3")
	src = filepath.Join(inbox, "song.mp3")
	writeFile(t, dst, "old")
	writeFile(t, src, "new")
	return master, inbox, dst, src
}

// fakeTrash replaces the trash runner with one that records each call and,
// like the real command, removes the file it is given. The recorded content is
// what the file held when it was trashed.
func fakeTrash(t *testing.T, fail error) *[]string {
	t.Helper()
	var calls []string
	realRun, realHas := run, hasTrashBin
	hasTrashBin = func() bool { return true }
	run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		p := args[len(args)-1]
		b, _ := os.ReadFile(p)
		calls = append(calls, name+" "+p+" "+string(b))
		if fail != nil {
			return nil, fail
		}
		return nil, os.Remove(p)
	}
	t.Cleanup(func() { run, hasTrashBin = realRun, realHas })
	return &calls
}

func TestReplaceTrashesOldFile(t *testing.T) {
	master, _, dst, src := replaceSetup(t)
	calls := fakeTrash(t, nil)

	if err := Replace(src, dst, ModeTrash); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "new" {
		t.Errorf("dst = %q, want new content", got)
	}
	mustMissing(t, src)
	if len(*calls) != 1 {
		t.Fatalf("trash calls = %v, want one", *calls)
	}
	// The old content was trashed from a replace dir, under the master file's name.
	f := strings.Fields((*calls)[0])
	if len(f) != 3 || f[0] != trashBin || f[2] != "old" {
		t.Fatalf("trash call = %q, want %s <path> old", (*calls)[0], trashBin)
	}
	if filepath.Base(f[1]) != "Song.mp3" || !strings.HasPrefix(filepath.Base(filepath.Dir(f[1])), ".crates-replace-") {
		t.Errorf("trashed path %q, want Song.mp3 inside a .crates-replace- dir", f[1])
	}
	if filepath.Dir(filepath.Dir(f[1])) != master {
		t.Errorf("replace dir is outside master %s: %q", master, f[1])
	}
	assertNoTemps(t, master)
}

func TestReplaceRemovesOldFile(t *testing.T) {
	master, _, dst, src := replaceSetup(t)

	if err := Replace(src, dst, ModeRemove); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "new" {
		t.Errorf("dst = %q", got)
	}
	mustMissing(t, src)
	entries, _ := os.ReadDir(master)
	for _, e := range entries {
		if e.Name() != "Song.mp3" {
			t.Errorf("unexpected entry %q left in master", e.Name())
		}
	}
}

func TestReplaceKeepsMasterName(t *testing.T) {
	master, _, dst, src := replaceSetup(t)
	fakeTrash(t, nil)

	if err := Replace(src, dst, ModeTrash); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(master)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "Song.mp3" {
		t.Errorf("master entries = %v, want exactly Song.mp3", entries)
	}
}

func TestReplaceBackupKept(t *testing.T) {
	_, _, dst, src := replaceSetup(t)
	fakeTrash(t, errors.New("trash refused"))

	err := Replace(src, dst, ModeTrash)
	var bk model.ErrBackupKept
	if !errors.As(err, &bk) {
		t.Fatalf("Replace error = %v, want ErrBackupKept", err)
	}
	if got := readFile(t, bk.Path); got != "old" {
		t.Errorf("kept file %s = %q, want the old content", bk.Path, got)
	}
	if !strings.Contains(bk.Path, ".crates-replace-") {
		t.Errorf("kept file %s is not in a replace dir", bk.Path)
	}
	// The swap stands.
	if got := readFile(t, dst); got != "new" {
		t.Errorf("dst = %q, want the swap to stand", got)
	}
	mustMissing(t, src)
}

func TestReplaceFailureBeforeSwapTouchesNothing(t *testing.T) {
	master, _, dst, src := replaceSetup(t)
	failRename(t, errors.New("disk on fire"))

	if err := Replace(src, dst, ModeRemove); err == nil {
		t.Fatal("Replace succeeded with a failing rename")
	}
	if got := readFile(t, dst); got != "old" {
		t.Errorf("dst = %q, want old content", got)
	}
	if got := readFile(t, src); got != "new" {
		t.Errorf("src = %q, want new content", got)
	}
	entries, _ := os.ReadDir(master)
	if len(entries) != 1 {
		t.Errorf("master has extra entries after a failed swap: %v", entries)
	}
}

func TestReplaceAcrossVolumes(t *testing.T) {
	master, _, dst, src := replaceSetup(t)
	exdevOnSrc(t, src)

	if err := Replace(src, dst, ModeRemove); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "new" {
		t.Errorf("dst = %q", got)
	}
	mustMissing(t, src)
	entries, _ := os.ReadDir(master)
	if len(entries) != 1 || entries[0].Name() != "Song.mp3" {
		t.Errorf("master entries = %v, want exactly Song.mp3", entries)
	}
}

func TestReplaceAcrossVolumesFailureBeforeSwap(t *testing.T) {
	master, _, dst, src := replaceSetup(t)
	exdevOnSrc(t, src)
	real := rename
	rename = func(old, new string) error {
		if old == src {
			return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
		}
		return errors.New("rename refused")
	}
	t.Cleanup(func() { rename = real })

	if err := Replace(src, dst, ModeRemove); err == nil {
		t.Fatal("Replace succeeded when the swap failed")
	}
	if got := readFile(t, dst); got != "old" {
		t.Errorf("dst = %q, want old content", got)
	}
	if got := readFile(t, src); got != "new" {
		t.Errorf("src = %q, want it kept", got)
	}
	assertNoTemps(t, master)
}
