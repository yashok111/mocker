package testkit_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoProductionImport keeps this package, and its adminkit subpackage,
// what their doc comments claim: test-support only. adminkit.NewAdminServer
// builds a real admin.Server, so nothing stops a production file from
// importing it as a shortcut past admin.New's real wiring — except this
// test, modelled on internal/jsonx/boundary_test.go's identical guard on
// encoding/json. Test files are exempt on purpose: every _test.go file in
// this tree is exactly the caller these packages exist for.
func TestNoProductionImport(t *testing.T) {
	root := filepath.Join("..", "..")

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
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
		// internal/testkit/ itself is where these imports are allowed to
		// live — adminkit.go (a production-shaped .go file, since adminkit
		// has no _test.go of its own to carry NewAdminServer in) imports the
		// base testkit package for NewDBAt, and that is composition within
		// this test-support tree, not a leak into what ships.
		if strings.HasPrefix(rel, "internal/testkit/") {
			return nil
		}
		if !strings.HasPrefix(rel, "cmd/") && !strings.HasPrefix(rel, "internal/") {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "github.com/yashok111/mocker/internal/testkit" ||
				strings.HasPrefix(path, "github.com/yashok111/mocker/internal/testkit/") {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these production files import internal/testkit (or a subpackage) instead of building their own fixtures:\n  %s\n\n"+
			"testkit exists for _test.go files only — a production import means test-only wiring (a fresh, "+
			"unauthenticated admin.Server included) has leaked into what ships.", strings.Join(offenders, "\n  "))
	}
}
