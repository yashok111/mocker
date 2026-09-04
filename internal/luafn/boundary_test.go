package luafn

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyLuafnImportsGopherLua is A18 D1's seam, held by a test rather than a
// comment: the library enters this tree through this package and nowhere else,
// the way internal/wsmock's boundary_test.go holds coder/websocket and
// internal/jsonx's holds encoding/json. It walks every non-test .go file under
// cmd/ and internal/ of THIS module — not this package's own directory, which
// is the mutation that would leave it green while the seam was gone — and
// fails on an import of the library outside internal/luafn.
func TestOnlyLuafnImportsGopherLua(t *testing.T) {
	root := filepath.Join("..", "..")
	const lib = "github.com/yuin/gopher-lua"
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
			if strings.HasPrefix(filepath.ToSlash(rel), "internal/luafn/") {
				return nil
			}
			f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if perr != nil {
				return perr
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if p == lib || strings.HasPrefix(p, lib+"/") {
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
		t.Fatalf("%s is imported outside internal/luafn by: %v — the seam is one package (A18 D1)", lib, violations)
	}
}
