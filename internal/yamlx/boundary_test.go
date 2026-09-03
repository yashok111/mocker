package yamlx_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoDirectYAMLImport keeps this package the single YAML boundary it
// claims to be (A8), the same shape internal/jsonx's boundary test has for
// encoding/json and internal/wsmock's for the WebSocket library: the
// decoder is one module admitted on a measurement, and the measurement
// holds only while exactly one package imports it. Test files are exempt
// for the same reason jsonx's are.
func TestNoDirectYAMLImport(t *testing.T) {
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
		if strings.HasPrefix(rel, "internal/yamlx/") {
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
			// The decoder's own path and its predecessor; internal/yamlx's
			// own import path also contains "yaml" and is exactly the one
			// everyone else is meant to use.
			if strings.Contains(imp.Path.Value, "go.yaml.in/") || strings.Contains(imp.Path.Value, "gopkg.in/yaml") {
				offenders = append(offenders, rel+" imports "+imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these production files import a YAML package directly instead of going through internal/yamlx:\n  %s\n\n"+
			"yamlx exists so the spec pipeline has one root type and one error set; a second importer is a second YAML data model",
			strings.Join(offenders, "\n  "))
	}
}
