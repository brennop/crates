package fsops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/brennop/crates/model"
)

// writeFile creates path with content and fails the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFile returns the content of path, or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// mustMissing fails the test if path exists.
func mustMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s should not exist (err %v)", path, err)
	}
}

// exdevOnSrc makes rename fail with EXDEV for the move out of src, as if the
// two paths were on different volumes. Other renames go through.
func exdevOnSrc(t *testing.T, src string) {
	t.Helper()
	real := rename
	rename = func(old, new string) error {
		if old == src {
			return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
		}
		return real(old, new)
	}
	t.Cleanup(func() { rename = real })
}

// failRename makes every rename fail with err.
func failRename(t *testing.T, err error) {
	t.Helper()
	real := rename
	rename = func(old, new string) error { return err }
	t.Cleanup(func() { rename = real })
}

// caseInsensitive reports whether dir's volume ignores case in names.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	writeFile(t, filepath.Join(dir, "probe-case"), "")
	defer os.Remove(filepath.Join(dir, "probe-case"))
	_, err := os.Stat(filepath.Join(dir, "PROBE-CASE"))
	return err == nil
}

func TestMoveSameVolume(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp3")
	dst := filepath.Join(dir, "master.mp3")
	writeFile(t, src, "new")

	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "new" {
		t.Errorf("dst = %q, want %q", got, "new")
	}
	mustMissing(t, src)
}

func TestMoveNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp3")
	dst := filepath.Join(dir, "master.mp3")
	writeFile(t, src, "new")
	writeFile(t, dst, "old")

	err := Move(src, dst)
	var c model.ErrCollision
	if !errors.As(err, &c) {
		t.Fatalf("Move error = %v, want ErrCollision", err)
	}
	if c.Name != "master.mp3" {
		t.Errorf("collision name = %q", c.Name)
	}
	if got := readFile(t, dst); got != "old" {
		t.Errorf("dst was touched: %q", got)
	}
	if got := readFile(t, src); got != "new" {
		t.Errorf("src was touched: %q", got)
	}
}

func TestMoveCaseOnlyCollision(t *testing.T) {
	dir := t.TempDir()
	if !caseInsensitive(t, dir) {
		t.Skip("temp volume is case-sensitive")
	}
	inbox := t.TempDir()
	src := filepath.Join(inbox, "song.mp3")
	writeFile(t, filepath.Join(dir, "Song.mp3"), "old")
	writeFile(t, src, "new")

	var c model.ErrCollision
	if err := Move(src, filepath.Join(dir, "song.mp3")); !errors.As(err, &c) {
		t.Fatalf("Move error = %v, want ErrCollision for a case-only clash", err)
	}
	if got := readFile(t, filepath.Join(dir, "Song.mp3")); got != "old" {
		t.Errorf("Song.mp3 was touched: %q", got)
	}
}

func TestMoveFailedRenameLeavesNoPlaceholder(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp3")
	dst := filepath.Join(dir, "master.mp3")
	writeFile(t, src, "new")
	failRename(t, errors.New("disk on fire"))

	if err := Move(src, dst); err == nil {
		t.Fatal("Move succeeded with a failing rename")
	}
	mustMissing(t, dst)
	if got := readFile(t, src); got != "new" {
		t.Errorf("src was touched: %q", got)
	}
}

func TestMoveMissingSourceLeavesNoPlaceholder(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "master.mp3")

	if err := Move(filepath.Join(dir, "absent.mp3"), dst); err == nil {
		t.Fatal("Move succeeded with no source")
	}
	mustMissing(t, dst)
}

func TestMoveAcrossVolumesInjected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp3")
	dst := filepath.Join(dir, "master.mp3")
	writeFile(t, src, "payload")
	if err := os.Chmod(src, 0o640); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	exdevOnSrc(t, src)

	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "payload" {
		t.Errorf("dst = %q", got)
	}
	mustMissing(t, src)
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", fi.Mode().Perm())
	}
	if !fi.ModTime().Equal(mtime) {
		t.Errorf("mtime = %v, want %v", fi.ModTime(), mtime)
	}
	assertNoTemps(t, dir)
}

func TestMoveAcrossFailureKeepsSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp3")
	dst := filepath.Join(dir, "master.mp3")
	writeFile(t, src, "payload")
	exdevOnSrc(t, src)
	real := rename
	rename = func(old, new string) error {
		if old == src {
			return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
		}
		// The temp-over-placeholder rename fails.
		return errors.New("rename refused")
	}
	t.Cleanup(func() { rename = real })

	if err := Move(src, dst); err == nil {
		t.Fatal("Move succeeded when the final rename failed")
	}
	if got := readFile(t, src); got != "payload" {
		t.Errorf("src = %q, want it left in place", got)
	}
	mustMissing(t, dst)
	assertNoTemps(t, dir)
}

// TestMoveAcrossRealVolume moves a file onto a second, real volume. It attaches
// a small disk image, so it runs only on macOS and is skipped with -short.
func TestMoveAcrossRealVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("attaches a disk image")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("needs hdiutil")
	}
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("hdiutil not found")
	}
	image := filepath.Join(t.TempDir(), "vol.dmg")
	mnt := t.TempDir()
	if out, err := exec.Command("hdiutil", "create", "-quiet", "-size", "8m", "-fs", "APFS",
		"-volname", "crates-fsops-test", image).CombinedOutput(); err != nil {
		t.Skipf("hdiutil create: %v: %s", err, out)
	}
	if out, err := exec.Command("hdiutil", "attach", "-quiet", "-nobrowse",
		"-mountpoint", mnt, image).CombinedOutput(); err != nil {
		t.Skipf("hdiutil attach: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("hdiutil", "detach", "-quiet", "-force", mnt).Run()
	})

	src := filepath.Join(t.TempDir(), "in.mp3")
	dst := filepath.Join(mnt, "master.mp3")
	writeFile(t, src, "cross-volume payload")

	if err := os.Rename(src, filepath.Join(mnt, "probe")); !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("precondition: rename across volumes returned %v, want EXDEV", err)
	}
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "cross-volume payload" {
		t.Errorf("dst = %q", got)
	}
	mustMissing(t, src)
	assertNoTemps(t, mnt)
}

// assertNoTemps fails the test if dir holds a copy temp or a placeholder left
// by a move.
func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".crates-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("temps left behind: %v", matches)
	}
}
