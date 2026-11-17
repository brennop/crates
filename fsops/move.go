package fsops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/brennop/crates/model"
)

// rename is os.Rename. It is a variable so tests can fail it on demand.
var rename = os.Rename

// copyPrefix starts the names of copy temps. They are dotfiles, so scans skip
// them, and CleanStale removes the ones a crash leaves behind.
const copyPrefix = ".crates-copy-"

// Move moves src to dst without ever replacing an existing dst. If dst exists
// it returns model.ErrCollision and touches neither file. Across volumes it
// copies, then removes src.
//
// The name is claimed first with O_EXCL, so a bare rename cannot silently
// overwrite a file that appears between the check and the move.
func Move(src, dst string) error {
	if err := claim(dst); err != nil {
		return err
	}
	err := rename(src, dst)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EXDEV) {
		return moveAcross(src, dst)
	}
	os.Remove(dst) // the placeholder is ours, so this is safe
	return err
}

// claim creates dst as an empty placeholder. An existing name is a collision.
func claim(dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return model.ErrCollision{Name: filepath.Base(dst)}
	}
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// moveAcross finishes a Move whose rename failed with EXDEV. The placeholder at
// dst is already claimed. On any failure it removes the temp and the
// placeholder and leaves src in place.
func moveAcross(src, dst string) error {
	tmp, err := copyTemp(src, filepath.Dir(dst))
	if err != nil {
		os.Remove(dst)
		return err
	}
	if err := rename(tmp, dst); err != nil {
		os.Remove(tmp)
		os.Remove(dst)
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("moved to %s, but the source could not be removed: %w", dst, err)
	}
	return nil
}

// copyTemp copies src into a new dot-prefixed file in dir, keeping its mode and
// mtime, and fsyncs it. The caller renames the temp into place or removes it.
func copyTemp(src, dir string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return "", err
	}
	out, err := os.CreateTemp(dir, copyPrefix+"*")
	if err != nil {
		return "", err
	}
	tmp := out.Name()
	if err := writeAll(out, in, info); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeAll copies in to out, syncs and closes out, then sets the mode and mtime.
func writeAll(out *os.File, in io.Reader, info fs.FileInfo) error {
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(out.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(out.Name(), info.ModTime(), info.ModTime())
}
