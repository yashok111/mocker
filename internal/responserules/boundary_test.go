package responserules

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestEvaluatorHasNoRuntimeOrPersistenceImports(t *testing.T) {
	// File by file rather than parser.ParseDir, which is deprecated since Go
	// 1.25; the set walked is the same: every non-test .go file in the package.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"mockplane", "livestate", "entities", "traffic", "probe", "store", "apidesign", "checkpoints", "scenarios", "workspaces", "resources"}
	for _, entry := range entries {
		file := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		node, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range node.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range banned {
				if strings.HasSuffix(path, "/internal/"+name) {
					t.Errorf("%s imports %s", file, path)
				}
			}
			if path == "time" {
				t.Errorf("%s imports timers", file)
			}
		}
	}
}
