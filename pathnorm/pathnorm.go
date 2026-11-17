// Package pathnorm is the one place paths are normalized. Every path that is
// stored, compared or received from Music goes through Normalize.
package pathnorm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/text/unicode/norm"
)

// Normalize returns path made absolute and clean, with symlinks resolved and
// in Unicode NFC. The path need not exist: the deepest existing ancestor is
// resolved and the rest appended.
func Normalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := resolveExisting(abs)
	if err != nil {
		return "", err
	}
	return norm.NFC.String(resolved), nil
}

// resolveExisting resolves symlinks in the longest existing prefix of the
// clean absolute path p and appends the missing tail.
func resolveExisting(p string) (string, error) {
	var tail []string
	cur := p
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r, nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// Within reports whether path is dir itself or inside it. Both must already
// be normalized.
func Within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !startsWithDotDot(rel))
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && rel[2] == filepath.Separator
}
