package mockplane_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssembleResponseIsTheOnlySeam is C8: it proves structurally, not just
// behaviourally, that [Plane.serveGenerated] and [Plane.Preview] are the ONLY
// two callers of [Plane.assembleResponse], and that [gen.Body] itself is
// called from exactly one place — inside assembleResponse.
//
// Why an AST walk and not a call-count fixture test: a preview handler that
// re-implements the recipes/patched-schema/pinned-vs-generated assembly by
// hand, but happens to route its own single gen.Body call through some
// smaller shared helper, would pass a body-count-only check while duplicating
// everything around it (C8's own reasoning, decisions.md). Requiring the
// enclosing FuncDecl of assembleResponse's call sites to be exactly
// {serveGenerated, Preview} is the one assertion that would actually go red
// in that case.
//
// This walks internal/mockplane's own package files, non-test only, mirroring
// internal/jsonx/boundary_test.go:22-73#TestNoDirectEncodingJSONImport, whose
// exclusion of _test.go is the half that makes the claim true here too:
// respond_test.go and preview's own tests call gen.Body directly to build
// fixtures, and those calls are legitimate and must not be counted.
//
// Asserted against function NAMES (package-level, stable under a local
// variable rename), never against an expression like rt.gen or p.gen — the
// receiver's static type is not recoverable from syntax alone, so a check
// written against it would be brittle in exactly the way this one is not.
func TestAssembleResponseIsTheOnlySeam(t *testing.T) {
	callers, bodySites := scanSeam(t, ".")

	// P7a (decisions.md mocker-p7-api-design D4): serveCustomGenerated is
	// the THIRD caller — a custom endpoint's schema enters the seam
	// instead of opening a third gen.Body site, which is what the body
	// assertion below still refuses.
	//
	// A18 (D7) adds no fourth: the endpoint-function branch (function.go) is
	// a SIBLING of this seam, not a caller of it. It produces the bytes
	// assembleResponse would have produced — from Lua rather than from a
	// schema — and writes them through its own tail, so nothing it does can
	// be expressed as recipes, a patched schema, a pinned body or an
	// envelope. That is also why it opens no gen.Body site: it never
	// generates from a schema at all.
	wantCallers := map[string]bool{"serveGenerated": true, "Preview": true, "serveCustomGenerated": true}
	if len(callers) != len(wantCallers) {
		t.Fatalf("assembleResponse call sites: got callers %v, want exactly %v", setKeys(callers), setKeys(wantCallers))
	}
	for name := range wantCallers {
		if !callers[name] {
			t.Errorf("assembleResponse is never called from %s — expected it to be one of the two callers", name)
		}
	}
	for name := range callers {
		if !wantCallers[name] {
			t.Errorf("assembleResponse is called from %s, which is none of serveGenerated, Preview, serveCustomGenerated — "+
				"a third caller means something is bypassing the shared assembly", name)
		}
	}

	// Two sites since P6b (decisions.md mocker-p6b-sse-mock D4), and the
	// second is not a hole in C8: assembleResponse is the one seam for a
	// RESPONSE — recipes, schemaPatch, pinned-versus-generated, the ref
	// resolver — and a stream's tick frame has none of that assembly to
	// duplicate: it is gen.Body over an inline schema with the workspace
	// settings and nothing else (stream.go's newTickGenerator). Routing it
	// through assembleResponse would mean a third caller of that function
	// (the assertion above would go red for the right reason) carrying a
	// synthetic variant so that the assembly it does not need could skip
	// itself. What this guard still refuses is a THIRD site: any new body
	// producer that is a response must go through assembleResponse.
	// Generate is A19's mock.generate: a function asks for a BODY and not a
	// response, so it is a gen.Body site of its own and not a fourth
	// assembleResponse caller — the method's own comment says why.
	wantBodySites := map[string]bool{"Plane.assembleResponse": true, "newTickGenerator": true, "luaHost.Generate": true}
	if len(bodySites) != len(wantBodySites) {
		t.Fatalf("gen.Body call sites in production code: got %d (%v), want exactly %v", len(bodySites), bodySites, setKeys(wantBodySites))
	}
	for _, site := range bodySites {
		if !wantBodySites[site] {
			t.Errorf("gen.Body is called inside %s, which is none of assembleResponse (a response's one seam), "+
				"newTickGenerator (a stream tick's, P6b D4) and Generate (a Lua function's body, A19) — a response body must be produced in exactly one place", site)
		}
	}
}

// scanSeam walks dir's own .go files (non-test only) and returns:
//   - callers: the set of enclosing FuncDecl names containing a call whose
//     selector is named "assembleResponse"
//   - bodySites: the enclosing FuncDecl name for each call whose selector is
//     named "Body" (gen.Body's method name) — one entry per call site
//
// receiverName is the bare type name of a method receiver, pointer or not.
func receiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "?"
}

func scanSeam(t *testing.T, dir string) (map[string]bool, []string) {
	t.Helper()

	callers := map[string]bool{}
	var bodySites []string

	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "assembleResponse":
					callers[fn.Name.Name] = true
				case "Body":
					// Qualified by receiver, because a bare method name is not
					// unique: Generate is luafn.Host's name for the Lua site
					// and any future type could carry one too (A19 review).
					site := fn.Name.Name
					if fn.Recv != nil && len(fn.Recv.List) > 0 {
						site = receiverName(fn.Recv.List[0].Type) + "." + site
					}
					bodySites = append(bodySites, site)
				}
				return true
			})
		}
	}
	return callers, bodySites
}

func setKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
