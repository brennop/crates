package fsops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/brennop/crates/model"
)

// replacePrefix starts the names of the temp dirs Replace keeps the old file
// in. CleanStale lists what they hold and never deletes it.
const replacePrefix = ".crates-replace-"

// Replace puts the content of src at dst's path. dst keeps its name, and its
// path is never empty. The old file is hard-linked into a temp dir in dst's
// directory first, so it reaches the Trash or is removed under its real name,
// per mode.
//
// A failure before the swap leaves src and dst untouched. If the old file
// cannot be trashed or removed afterwards, the swap stands and the returned
// error is a model.ErrBackupKept naming the file.
func Replace(src, dst string, mode Mode) error {
	dir := filepath.Dir(dst)
	hold, err := os.MkdirTemp(dir, replacePrefix)
	if err != nil {
		return err
	}
	link := filepath.Join(hold, filepath.Base(dst))
	if err := os.Link(dst, link); err != nil {
		os.Remove(hold)
		return err
	}

	// Swap. Past this point dst has the new content and link holds the old.
	tmp := ""
	err = rename(src, dst)
	if errors.Is(err, syscall.EXDEV) {
		tmp, err = copyTemp(src, dir)
		if err == nil {
			if err = rename(tmp, dst); err != nil {
				os.Remove(tmp)
			}
		}
	}
	if err != nil {
		os.RemoveAll(hold) // drops only the link; dst still holds the old file
		return err
	}

	var errs []error
	if tmp != "" {
		if err := os.Remove(src); err != nil {
			errs = append(errs, fmt.Errorf("replaced %s, but the source could not be removed: %w", dst, err))
		}
	}
	if err := Delete(link, mode); err != nil {
		errs = append(errs, model.ErrBackupKept{Path: link, Err: err})
		return errors.Join(errs...)
	}
	os.Remove(hold) // empty now; a failure here leaves a dir CleanStale removes
	return errors.Join(errs...)
}
