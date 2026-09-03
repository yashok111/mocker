package wsmock

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyWsmockImportsCoderWebsocket is D1's seam, held by a test rather
// than a comment (decisions.md mocker-p6d-websocket D1, A2): the library
// enters this tree through this package and nowhere else, the way
// internal/jsonx's boundary_test.go holds encoding/json to one importer.
// It walks every non-test .go file under cmd/ and internal/ of THIS module
// and fails on an import of the library outside internal/wsmock.
func TestOnlyWsmockImportsCoderWebsocket(t *testing.T) {
	root := filepath.Join("..", "..")
	const lib = "github.com/coder/websocket"
	var violations []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if strings.HasPrefix(filepath.ToSlash(rel), "internal/wsmock/") {
				return nil
			}
			f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) == lib || strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), lib+"/") {
					violations = append(violations, rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("%s is imported outside internal/wsmock by: %v — the seam is one package (D1)", lib, violations)
	}
}
