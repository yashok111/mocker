package backendblob

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestOwnerSQLUsesExplicitAdapters(t *testing.T) {
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && !strings.HasPrefix(d.Name(), "backend") {
				return filepath.SkipDir
			}
			if d.Name() == "backendblob" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		set := token.NewFileSet()
		f, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				sql, err := strconv.Unquote(literal.Value)
				if err != nil {
					return true
				}
				for _, o := range owners {
					from := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+` + o.Table + `\b`)
					if from.MatchString(sql) {
						for _, c := range o.Columns {
							if c.Payload && regexp.MustCompile(`\b`+c.Name+`\b`).MatchString(sql) {
								t.Errorf("direct payload read %s at %s", o.Table, set.Position(n.Pos()))
							}
						}
					}
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "ExecContext" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok {
				return true
			}
			sql, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, o := range owners {
				if regexp.MustCompile(`(?i)\bINSERT\s+(?:OR\s+IGNORE\s+)?INTO\s+` + o.Table + `\b`).MatchString(sql) {
					t.Errorf("direct owner insert %s at %s", o.Table, set.Position(n.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
