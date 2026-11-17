package library

import (
	"os"
	"path/filepath"

	"github.com/brennop/crates/model"
	"github.com/brennop/crates/pathnorm"
)

// root is one configured directory, normalized once at Open.
type root struct {
	dir           string
	role          model.Role
	recursive     bool
	caseSensitive bool

	// pending holds inbox files seen once, by comparison key, with the size
	// they had. A file is indexed once its size holds across two sweeps.
	// Only the watch goroutine touches it.
	pending map[string]int64
}

// newRoot normalizes dir. The directory need not exist yet, so case
// sensitivity is read from its nearest existing ancestor.
func newRoot(dir string, role model.Role, recursive bool) (*root, error) {
	abs, err := pathnorm.Normalize(dir)
	if err != nil {
		return nil, err
	}
	cs, err := caseSensitiveAt(abs)
	if err != nil {
		return nil, err
	}
	return &root{
		dir:           abs,
		role:          role,
		recursive:     recursive,
		caseSensitive: cs,
		pending:       make(map[string]int64),
	}, nil
}

func caseSensitiveAt(dir string) (bool, error) {
	p := dir
	for {
		if _, err := os.Stat(p); err == nil {
			return pathnorm.CaseSensitive(p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return true, nil
		}
		p = parent
	}
}
