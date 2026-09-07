package resources

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// notAnEntityWriteRefusal names every Err* sentinel of this package that is
// deliberately NOT in entityWriteCodes, with the reason. It exists so the
// completeness test below can be exhaustive without asserting that all
// fifteen sentinels are entity-write refusals: a new sentinel appears in
// neither map and fails, and the author has to decide which side it is on
// rather than defaulting to store_failed by omission.
var notAnEntityWriteRefusal = map[string]string{
	"ErrWorkspaceNotFound":   "a Confirm/Decline argument refusal, never returned by an entity write",
	"ErrUnknownFamily":       "Confirm/Decline's own 404 over route_family; an entity write has already resolved the family and answers ErrResourceGone instead",
	"ErrAlreadyConfirmed":    "Confirm only",
	"ErrStaleConfig":         "Confirm only",
	"ErrPopulationFailed":    "Confirm's generation half (populate.go), never a single-row write",
	"ErrConfirmSlugRequired": "Decline only",
	"ErrConfirmSlugMismatch": "Decline only",
	"ErrParentNotConfirmed":  "Confirm only (nested families)",
	"ErrChildConfirmed":      "Decline only (nested families)",
	"ErrBaseScopeUndeclared": "scope derivation, raised before any write is attempted",
}

// TestEntityWriteCodes_coverEverySentinel is the guard the three
// hand-written copies of this switch never had: a sixth sentinel that an
// entity write can return used to have to be remembered in
// mockplane/resource.go, mockplane/function.go and
// admin/entity_write_handlers.go independently, and the copy that forgot it
// answered store_failed or an unnamed 500. The list is read from the
// package's own source rather than typed here, so ADDING a sentinel — in
// any file of this package — is what trips it.
func TestEntityWriteCodes_coverEverySentinel(t *testing.T) {
	t.Parallel()

	inTable := map[string]bool{}
	for _, c := range entityWriteCodes {
		inTable[sentinelName(t, c.sentinel)] = true
	}

	for _, name := range packageSentinelNames(t) {
		if inTable[name] {
			continue
		}
		if _, ok := notAnEntityWriteRefusal[name]; ok {
			continue
		}
		t.Errorf("sentinel %s is in neither entityWriteCodes (codes.go) nor "+
			"notAnEntityWriteRefusal (this file): decide whether an entity write "+
			"can return it, and give it a row or a reason", name)
	}

	// The reverse direction: a reason left behind for a sentinel that no
	// longer exists is stale documentation the next reader would trust.
	live := map[string]bool{}
	for _, name := range packageSentinelNames(t) {
		live[name] = true
	}
	for name := range notAnEntityWriteRefusal {
		if !live[name] {
			t.Errorf("notAnEntityWriteRefusal names %s, which this package no longer declares", name)
		}
	}
}

// TestEntityWriteCodes_areDistinct pins that no two rows share a code or a
// Lua word: two sentinels answering the same name is exactly the drift the
// table exists to make visible, and it would otherwise read as agreement.
func TestEntityWriteCodes_areDistinct(t *testing.T) {
	t.Parallel()
	codes, luas := map[string]bool{}, map[string]bool{}
	for _, c := range entityWriteCodes {
		if c.code == "" || c.lua == "" {
			t.Fatalf("row %s has an empty name", sentinelName(t, c.sentinel))
		}
		if codes[c.code] {
			t.Errorf("code %q appears twice", c.code)
		}
		if luas[c.lua] {
			t.Errorf("lua word %q appears twice", c.lua)
		}
		codes[c.code], luas[c.lua] = true, true
	}
	// store_failed is WriteRefusalLuaCode's catch-all; a row claiming it
	// would make an unmapped failure indistinguishable from a mapped one.
	if luas["store_failed"] {
		t.Error(`no row may use "store_failed": it is the catch-all for everything NOT in the table`)
	}
}

// sentinelName maps a sentinel back to the identifier it was declared under,
// by its message — the messages are unique within this package and the test
// asserts as much by failing on a miss.
func sentinelName(t *testing.T, err error) string {
	t.Helper()
	for name, msg := range sentinelMessages(t) {
		if msg == err.Error() {
			return name
		}
	}
	t.Fatalf("no package-level sentinel declares the message %q", err.Error())
	return ""
}

// The walk is done once for the whole package: both tests below run with
// t.Parallel(), so the cache needs a sync.Once and not an `if nil` guard —
// the latter is a data race the -race build would fail on.
var (
	parseOnce      sync.Once
	parsedNames    []string
	parsedMessages map[string]string
	errParse       error
)

// packageSentinelNames returns every package-level `var ErrX = errors.New(...)`
// this package declares, read from the source files themselves.
func packageSentinelNames(t *testing.T) []string {
	t.Helper()
	parsePackageSentinels(t)
	return parsedNames
}

func sentinelMessages(t *testing.T) map[string]string {
	t.Helper()
	parsePackageSentinels(t)
	return parsedMessages
}

func parsePackageSentinels(t *testing.T) {
	t.Helper()
	parseOnce.Do(func() {
		entries, err := os.ReadDir(".")
		if err != nil {
			errParse = fmt.Errorf("read package directory: %w", err)
			return
		}
		fset := token.NewFileSet()
		names := []string(nil)
		messages := map[string]string{}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, perr := parser.ParseFile(fset, name, nil, 0)
			if perr != nil {
				errParse = fmt.Errorf("parse %s: %w", name, perr)
				return
			}
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, ident := range vs.Names {
						if !strings.HasPrefix(ident.Name, "Err") || i >= len(vs.Values) {
							continue
						}
						msg, ok := errorsNewLiteral(vs.Values[i])
						if !ok {
							continue
						}
						names = append(names, ident.Name)
						messages[ident.Name] = msg
					}
				}
			}
		}
		if len(names) == 0 {
			errParse = errors.New("found no Err* sentinels: the AST walk stopped matching, which would make this test vacuous")
			return
		}
		parsedNames, parsedMessages = names, messages
	})
	if errParse != nil {
		t.Fatal(errParse)
	}
}

// errorsNewLiteral recognises `errors.New("...")` and returns the literal.
// Anything else (a wrapped or computed error) is not a sentinel this test
// can name, and is skipped rather than guessed at.
func errorsNewLiteral(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "errors" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	msg, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return msg, true
}
