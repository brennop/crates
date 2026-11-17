// Package pathnorm is the one place paths are normalized. Every path that is
// stored, compared or received from Music goes through Normalize.
package pathnorm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

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

// WithinKey is Within for paths that may differ from dir only in case: both
// are compared by Key. Both must already be normalized.
func WithinKey(path, dir string, caseSensitive bool) bool {
	kp, kd := Key(path, caseSensitive), Key(dir, caseSensitive)
	if kp == kd {
		return true
	}
	if !strings.HasSuffix(kd, string(filepath.Separator)) {
		kd += string(filepath.Separator)
	}
	return strings.HasPrefix(kp, kd)
}

// Key is the comparison key for a normalized path: the path itself on a
// case-sensitive volume, case-folded on a case-insensitive one.
func Key(path string, caseSensitive bool) string {
	if caseSensitive {
		return path
	}
	return strings.ToLower(path)
}

// Child joins name, found by listing dir, onto dir and returns it in NFC.
// dir must already be normalized. Symlinks are not resolved: the entry is
// the link itself, so a link inside a root stays inside it.
func Child(dir, name string) string {
	return norm.NFC.String(filepath.Join(dir, name))
}

// CaseSensitive reports whether the volume holding dir distinguishes names
// that differ only in case. It stats dir with its letters' case swapped: if
// that names the same file, the volume is case-insensitive. The check only
// reads. A path with no letters cannot be tested and is reported as
// case-sensitive.
func CaseSensitive(dir string) (bool, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return true, err
	}
	swapped := swapCase(dir)
	if swapped == dir {
		return true, nil
	}
	other, err := os.Stat(swapped)
	if err != nil {
		return true, nil
	}
	return !os.SameFile(st, other), nil
}

func swapCase(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsUpper(r):
			return unicode.ToLower(r)
		case unicode.IsLower(r):
			return unicode.ToUpper(r)
		}
		return r
	}, s)
}
