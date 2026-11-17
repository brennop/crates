package main_test

// Enforces the package layout's import rules:
//
//   - No package under app imports another, except the leaf packages events,
//     model and pathnorm, which any package may import.
//   - Calls only go down: cmd (and the root main package) > ui > app > core
//     packages > leaves.
//   - Nothing below ui imports Bubble Tea (or any other Charm package).

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/brennop/crates"

var (
	leaves   = []string{"events", "model", "pathnorm"}
	tuiRoots = []string{"charm.land/", "github.com/charmbracelet/"}
)

// rank orders the layers. A package may import a package of lower rank;
// leaves may also import each other. Any directory not named here is a core
// package (rank 0), so a new package starts out under the strictest rule.
func rank(pkg string) int {
	switch {
	case pkg == "." || pkg == "cmd" || strings.HasPrefix(pkg, "cmd/"):
		return 3
	case pkg == "ui":
		return 2
	case pkg == "app":
		return 1
	case slices.Contains(leaves, pkg):
		return -1
	}
	return 0
}

// violations checks a map from package (path relative to the module) to its
// imports and returns one message per broken rule.
func violations(pkgs map[string][]string) []string {
	var out []string
	for pkg, imps := range pkgs {
		for _, imp := range imps {
			if rel, ok := strings.CutPrefix(imp, module+"/"); ok {
				r, d := rank(pkg), rank(rel)
				if d < r || (r == -1 && d == -1) {
					continue
				}
				out = append(out, pkg+" must not import "+rel)
				continue
			}
			if rank(pkg) < 2 {
				for _, root := range tuiRoots {
					if strings.HasPrefix(imp, root) {
						out = append(out, pkg+" is below ui and must not import "+imp)
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// moduleImports parses every non-test Go file in the module and returns each
// package's imports.
func moduleImports(t *testing.T) map[string][]string {
	t.Helper()
	pkgs := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		for _, is := range f.Imports {
			imp, _ := strconv.Unquote(is.Path.Value)
			pkgs[pkg] = append(pkgs[pkg], imp)
		}
		if _, ok := pkgs[pkg]; !ok {
			pkgs[pkg] = nil
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

func TestImportRules(t *testing.T) {
	pkgs := moduleImports(t)
	for _, want := range []string{".", "config", "ui", "app", "library", "tags", "player", "music", "fsops", "events", "model", "pathnorm"} {
		if _, ok := pkgs[want]; !ok {
			t.Errorf("package %s is missing", want)
		}
	}
	for _, v := range violations(pkgs) {
		t.Error(v)
	}
}

func TestImportRulesCatchBadImports(t *testing.T) {
	bad := map[string][]string{
		"library":  {module + "/tags"},
		"music":    {module + "/library"},
		"tags":     {"charm.land/bubbletea/v2"},
		"app":      {"github.com/charmbracelet/bubbletea", module + "/ui"},
		"model":    {module + "/library"},
		"config":   {module + "/app"},
		"newpkg":   {module + "/fsops"},
		"events":   {module + "/model"},               // allowed: leaf to leaf
		"ui":       {"charm.land/bubbletea/v2"},       // allowed
		"cmd/x":    {module + "/ui", module + "/app"}, // allowed
		"player":   {module + "/events", "os"},        // allowed
		"pathnorm": {"golang.org/x/text/unicode/norm"},
	}
	got := violations(bad)
	want := []string{
		"app is below ui and must not import github.com/charmbracelet/bubbletea",
		"app must not import ui",
		"config must not import app",
		"library must not import tags",
		"model must not import library",
		"music must not import library",
		"newpkg must not import fsops",
		"tags is below ui and must not import charm.land/bubbletea/v2",
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations:\n got  %q\n want %q", got, want)
	}
}

func TestMain(m *testing.M) {
	// The walk is relative to the module root, where this file lives.
	if _, err := os.Stat("go.mod"); err != nil {
		panic("imports_test must run from the module root")
	}
	os.Exit(m.Run())
}
