package responserules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestEvaluatorHasNoRuntimeOrPersistenceImports(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"mockplane", "livestate", "entities", "traffic", "probe", "store", "apidesign", "checkpoints", "scenarios", "workspaces", "resources"}
	for _, pkg := range packages {
		for file, node := range pkg.Files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			ast.Inspect(node, func(n ast.Node) bool {
				imp, ok := n.(*ast.ImportSpec)
				if !ok {
					return true
				}
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
				return true
			})
		}
	}
}
