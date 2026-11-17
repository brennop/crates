package fsops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Stale is what CleanStale found in master.
type Stale struct {
	Removed []string // copy temps a crash left behind, now deleted
	Kept    []string // old files from Replace that could not be trashed; never deleted here
}

// CleanStale tidies master at startup, before any scan or move runs. It removes
// copy temps from a crash and empty replace dirs. It lists the old files that
// Replace kept, so the app can report them, and leaves them in place.
func CleanStale(master string) (Stale, error) {
	var st Stale
	entries, err := os.ReadDir(master)
	if err != nil {
		return st, err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		p := filepath.Join(master, name)
		switch {
		case strings.HasPrefix(name, copyPrefix) && !e.IsDir():
			if err := os.Remove(p); err != nil {
				errs = append(errs, err)
			} else {
				st.Removed = append(st.Removed, p)
			}
		case strings.HasPrefix(name, replacePrefix) && e.IsDir():
			kept, err := os.ReadDir(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if len(kept) == 0 {
				if err := os.Remove(p); err != nil {
					errs = append(errs, err)
				}
				continue
			}
			for _, f := range kept {
				st.Kept = append(st.Kept, filepath.Join(p, f.Name()))
			}
		}
	}
	return st, errors.Join(errs...)
}
