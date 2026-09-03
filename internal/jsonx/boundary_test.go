package jsonx_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoDirectEncodingJSONImport keeps this package the single JSON boundary it
// claims to be. Stated in a doc comment the rule survives exactly until the
// next person types the import they have typed ten thousand times before; the
// consequence is not stylistic, because sonic's default configuration does not
// sort map keys, and a body encoded outside this package would break the
// byte-identical-output promise in a way only a golden re-run would reveal.
//
// Test files are exempt on purpose: a test that builds its expectation with
// encoding/json and compares it to what jsonx produced is a continuous
// cross-check of the backend, and jsonx_test.go is exactly that.
func TestNoDirectEncodingJSONImport(t *testing.T) {
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
		// This package is where the import is allowed to live.
		if strings.HasPrefix(rel, "internal/jsonx/") {
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
			if imp.Path.Value == `"encoding/json"` {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these production files import encoding/json directly instead of internal/jsonx:\n  %s\n\n"+
			"jsonx exists because sonic's default config does not sort map keys; anything encoded outside it "+
			"is outside the guarantee the golden pins.", strings.Join(offenders, "\n  "))
	}
}
