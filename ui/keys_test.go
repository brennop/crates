package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestKeysMatchBindings keeps key handling on key.Binding: no key press
// may be matched by comparing its String() to a literal. Bindings are what
// the hint lines are drawn from (see keys.go), so a key matched by string
// would be missing from the hints, or the hints would name a key that
// does not work.
func TestKeysMatchBindings(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			keys := keyPressNames(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "String" {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && keys[id.Name] {
					t.Errorf("%s: %s.String() matches a key by string; add a binding to keys.go and use key.Matches",
						fset.Position(call.Pos()), id.Name)
				}
				return true
			})
		}
	}
}

// keyPressNames returns the names fn binds to a tea.KeyPressMsg: its
// parameters of that type, and type-switch variables with a KeyPressMsg case.
func keyPressNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	isKeyPress := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "KeyPressMsg"
	}
	for _, p := range fn.Type.Params.List {
		if isKeyPress(p.Type) {
			for _, n := range p.Names {
				names[n.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSwitchStmt)
		if !ok {
			return true
		}
		as, ok := ts.Assign.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		for _, c := range ts.Body.List {
			for _, typ := range c.(*ast.CaseClause).List {
				if isKeyPress(typ) {
					names[as.Lhs[0].(*ast.Ident).Name] = true
				}
			}
		}
		return true
	})
	return names
}
