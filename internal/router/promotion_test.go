package router_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestPromotedHelpersHaveOneDefinition is D13 clause 40 (P3a decisions.md):
// the promotion of ListFamily, DetailIDParam and IsJSONMediaType out of
// internal/mockplane/respond.go left exactly ONE definition of each, in
// internal/router (the first two) and internal/httpx (the third) — and
// respond.go calls all three rather than carrying its own copy.
//
// Why a test and not the "run rg by hand" line decisions.md names: an
// unexported shadow copy with a live caller is not `unused`, so `go vet`,
// gofmt and golangci-lint all stay green over a regression here — the
// clause's own text concedes this. Nothing else in the tree walks the
// source for a second definition of these three names the way
// internal/jsonx/boundary_test.go does for a stray encoding/json import, or
// internal/mockplane/seam_test.go's TestAssembleResponseIsTheOnlySeam does
// for assembleResponse's call sites. This mirrors both: an AST walk over
// non-test production files, turning the manual command into a bar that
// runs under `make test`.
func TestPromotedHelpersHaveOneDefinition(t *testing.T) {
	root := filepath.Join("..", "..")

	type hit struct {
		pkg  string // relative path of the package directory, e.g. "internal/router"
		file string
	}

	defs := map[string][]hit{
		"ListFamily":      nil,
		"DetailIDParam":   nil,
		"IsJSONMediaType": nil,
	}

	// respond.go's own call sites, gathered in the same walk so a single test
	// proves both halves of the clause: one definition, and the promoted
	// definition is what gets called rather than a local reimplementation.
	respondCalls := map[string]bool{
		"ListFamily":      false,
		"DetailIDParam":   false,
		"IsJSONMediaType": false,
	}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "dist", "bin":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "cmd/") && !strings.HasPrefix(rel, "internal/") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}

		pkgDir := filepath.ToSlash(filepath.Dir(rel))

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil { // package-level functions only, per the rg pattern
				continue
			}
			name := fn.Name.Name
			switch {
			case name == "ListFamily":
				defs["ListFamily"] = append(defs["ListFamily"], hit{pkg: pkgDir, file: rel})
			case name == "DetailIDParam":
				defs["DetailIDParam"] = append(defs["DetailIDParam"], hit{pkg: pkgDir, file: rel})
			case len(name) > 0 && (name[0] == 'I' || name[0] == 'i') && name[1:] == "sJSONMediaType":
				defs["IsJSONMediaType"] = append(defs["IsJSONMediaType"], hit{pkg: pkgDir, file: rel})
			}
		}

		if rel == "internal/mockplane/respond.go" {
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if _, isCall := respondCalls[sel.Sel.Name]; isCall {
					respondCalls[sel.Sel.Name] = true
				}
				return true
			})
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	wantPkg := map[string]string{
		"ListFamily":      "internal/router",
		"DetailIDParam":   "internal/router",
		"IsJSONMediaType": "internal/httpx",
	}

	for name, want := range wantPkg {
		hits := defs[name]
		if len(hits) != 1 {
			locs := make([]string, 0, len(hits))
			for _, h := range hits {
				locs = append(locs, h.file)
			}
			t.Errorf("%s: got %d definition(s) %v, want exactly 1 — a second definition "+
				"(e.g. a private shadow copy with its own caller) compiles clean and stays "+
				"invisible to go vet/gofmt/golangci-lint", name, len(hits), locs)
			continue
		}
		if hits[0].pkg != want {
			t.Errorf("%s: defined in %s, want %s", name, hits[0].pkg, want)
		}
	}

	for name, called := range respondCalls {
		if !called {
			t.Errorf("internal/mockplane/respond.go never calls %s — expected it to use the "+
				"promoted definition rather than a local reimplementation", name)
		}
	}
}
