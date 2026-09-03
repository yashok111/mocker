package openapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/testspec"
)

// readFixture reads a file from testdata. Fixtures are plain files read as
// raw bytes — the .json extension is a label, not a parser choice; the YAML
// and garbage cases below are inline byte literals instead, since they are
// deliberately not JSON.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// loadFixture reads and Loads a fixture that is expected to succeed.
func loadFixture(t *testing.T, name string) *openapi.Document {
	t.Helper()
	doc, _, err := openapi.Load(readFixture(t, name))
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return doc
}

func TestDetect(t *testing.T) {
	garbage := []byte("this is not json, not yaml, just noise")
	yamlDoc := []byte("openapi: 3.0.3\ninfo:\n  title: Test\n  version: '1.0'\npaths: {}\n")

	tests := []struct {
		name    string
		raw     []byte
		want    openapi.Format
		wantErr error
	}{
		{name: "openapi 3.1", raw: readFixture(t, "oas31_minimal.json"), want: openapi.FormatOAS31},
		{name: "openapi 3.0", raw: readFixture(t, "oas30_minimal.json"), want: openapi.FormatOAS30},
		{name: "swagger 2.0", raw: readFixture(t, "swagger2_minimal.json"), want: openapi.FormatSwagger2},
		{name: "json object that is none of them", raw: readFixture(t, "not_a_document.json"), wantErr: openapi.ErrNotADocument},
		{name: "not json at all", raw: garbage, wantErr: openapi.ErrNotADocument},
		{name: "yaml input names openapi in its first line (A8: converted, not refused)", raw: yamlDoc, want: openapi.FormatOAS30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openapi.Detect(tt.raw)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Detect() error = %v, want wrapping %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoad_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		raw     []byte
		wantErr error
	}{
		{
			name:    "swagger 2.0 is refused, not silently imported",
			raw:     readFixture(t, "swagger2_minimal.json"),
			wantErr: openapi.ErrUnsupportedFormat,
		},
		{
			name:    "rubbish is not a document",
			raw:     []byte("¯\\_(ツ)_/¯ neither json nor yaml"),
			wantErr: openapi.ErrNotADocument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, report, err := openapi.Load(tt.raw)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Load() error = %v, want wrapping %v", err, tt.wantErr)
			}
			if doc != nil {
				t.Errorf("Load() returned a non-nil Document alongside an error")
			}
			if report != nil {
				t.Errorf("Load() returned a non-nil Report alongside an error")
			}
		})
	}
}

func TestLoad_Basics(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		wantFormat  openapi.Format
		wantTitle   string
		wantVersion string
	}{
		{name: "oas 3.0", fixture: "oas30_minimal.json", wantFormat: openapi.FormatOAS30, wantTitle: "Minimal 3.0", wantVersion: "1.0.0"},
		{name: "oas 3.1", fixture: "oas31_minimal.json", wantFormat: openapi.FormatOAS31, wantTitle: "Minimal 3.1", wantVersion: "2.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := readFixture(t, tt.fixture)
			doc, report, err := openapi.Load(raw)
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if doc.Format() != tt.wantFormat {
				t.Errorf("Format() = %v, want %v", doc.Format(), tt.wantFormat)
			}
			if doc.Title() != tt.wantTitle {
				t.Errorf("Title() = %q, want %q", doc.Title(), tt.wantTitle)
			}
			if doc.Version() != tt.wantVersion {
				t.Errorf("Version() = %q, want %q", doc.Version(), tt.wantVersion)
			}
			if !bytes.Equal(doc.Raw(), raw) {
				t.Errorf("Raw() does not equal the input bytes unchanged")
			}
			var probe map[string]any
			if err := json.Unmarshal(doc.Normalized(), &probe); err != nil {
				t.Fatalf("Normalized() is not valid JSON: %v", err)
			}
			if report.Format != tt.wantFormat {
				t.Errorf("Report.Format = %v, want %v", report.Format, tt.wantFormat)
			}
		})
	}
}

func TestLoad_BasePath(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantPath   string
		wantOrigin openapi.BasePathOrigin
		wantWarn   bool
	}{
		{
			name:       "root servers present, first one wins",
			fixture:    "basepath_root.json",
			wantPath:   "/api/v1",
			wantOrigin: openapi.BaseFromRootServers,
		},
		{
			name:       "no root servers, path items agree",
			fixture:    "basepath_path_agree.json",
			wantPath:   "/api/v1",
			wantOrigin: openapi.BaseFromPathServers,
		},
		{
			name:       "no root servers, path items disagree",
			fixture:    "basepath_path_disagree.json",
			wantPath:   "",
			wantOrigin: openapi.BaseAmbiguous,
			wantWarn:   true,
		},
		{
			name:       "empty root servers array, no path servers either",
			fixture:    "basepath_empty_servers.json",
			wantPath:   "",
			wantOrigin: openapi.BaseAbsent,
		},
		{
			name:       "root server url has a {variable} with a default",
			fixture:    "basepath_variable.json",
			wantPath:   "/api/v1",
			wantOrigin: openapi.BaseFromRootServers,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, report, err := openapi.Load(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			path, origin := doc.BasePath()
			if path != tt.wantPath {
				t.Errorf("BasePath() path = %q, want %q", path, tt.wantPath)
			}
			if origin != tt.wantOrigin {
				t.Errorf("BasePath() origin = %v, want %v", origin, tt.wantOrigin)
			}
			if report.BasePath != tt.wantPath || report.BasePathOrigin != tt.wantOrigin {
				t.Errorf("Report base path = (%q, %v), want (%q, %v)",
					report.BasePath, report.BasePathOrigin, tt.wantPath, tt.wantOrigin)
			}
			if !tt.wantWarn {
				return
			}
			if len(report.Warnings) == 0 {
				t.Fatalf("expected a base-path warning, got none")
			}
			msg := report.Warnings[0].Message
			if !strings.Contains(msg, "/api/v1") || !strings.Contains(msg, "/api/v2") {
				t.Errorf("warning message %q does not name both candidates", msg)
			}
		})
	}
}

func TestLoad_DialectNormalization(t *testing.T) {
	doc, _, err := openapi.Load(readFixture(t, "dialect.json"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(doc.Normalized(), &normalized); err != nil {
		t.Fatalf("unmarshal Normalized(): %v", err)
	}
	schemas := normalized["components"].(map[string]any)["schemas"].(map[string]any)

	t.Run("boolean exclusiveMinimum/exclusiveMaximum become the numeric form", func(t *testing.T) {
		bounded := schemas["Bounded"].(map[string]any)
		if _, has := bounded["minimum"]; has {
			t.Errorf("minimum should have been absorbed into exclusiveMinimum, still present: %v", bounded)
		}
		if got, want := bounded["exclusiveMinimum"], float64(0); got != want {
			t.Errorf("exclusiveMinimum = %v (%T), want numeric %v", got, got, want)
		}
		if _, has := bounded["exclusiveMaximum"]; has {
			t.Errorf("exclusiveMaximum: false should have been dropped, still present: %v", bounded)
		}
		if got, want := bounded["maximum"], float64(100); got != want {
			t.Errorf("maximum = %v, want %v (untouched)", got, want)
		}
	})

	t.Run("singular example becomes a one-element examples array", func(t *testing.T) {
		sampled := schemas["Sampled"].(map[string]any)
		if _, has := sampled["example"]; has {
			t.Errorf("example should have been removed, still present: %v", sampled)
		}
		examples, ok := sampled["examples"].([]any)
		if !ok || len(examples) != 1 || examples[0] != "hello" {
			t.Errorf(`examples = %v, want ["hello"]`, sampled["examples"])
		}
	})

	t.Run("nullable becomes x-nullable plus an honest union type", func(t *testing.T) {
		maybe := schemas["Maybe"].(map[string]any)
		if _, has := maybe["nullable"]; has {
			t.Errorf("nullable should have been removed, still present: %v", maybe)
		}
		if maybe["x-nullable"] != true {
			t.Errorf("x-nullable = %v, want true", maybe["x-nullable"])
		}
		types, ok := maybe["type"].([]any)
		if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
			t.Errorf(`type = %v, want ["string","null"]`, maybe["type"])
		}
	})
}

func TestResolver_ChainSucceedsUnderDefaultBudget(t *testing.T) {
	doc := loadFixture(t, "ref_chain_12.json")
	r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	got, err := r.Resolve("#/components/schemas/Hop0")
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["description"] != "terminal" {
		t.Fatalf("Resolve() = %v, want the Hop12 terminal node", got)
	}
}

func TestResolver_BudgetExhausted(t *testing.T) {
	doc := loadFixture(t, "ref_chain_short.json")

	t.Run("a chain longer than the budget fails, not hangs", func(t *testing.T) {
		r := openapi.NewResolver(doc, 3)
		_, err := r.Resolve("#/components/schemas/Step0")
		if !errors.Is(err, openapi.ErrBudgetExhausted) {
			t.Fatalf("Resolve() error = %v, want ErrBudgetExhausted", err)
		}
	})

	t.Run("the same document resolves under a generous budget", func(t *testing.T) {
		r := openapi.NewResolver(doc, openapi.DefaultRefBudget)
		got, err := r.Resolve("#/components/schemas/Step0")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if m, ok := got.(map[string]any); !ok || m["description"] != "terminal" {
			t.Fatalf("Resolve() = %v, want the Step5 terminal node", got)
		}
	})
}

func TestResolver_Cycle(t *testing.T) {
	doc := loadFixture(t, "ref_cycle.json")
	r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	// chase() is an iterative loop guarded by a per-call visited-pointer set,
	// so a cycle terminates in a bounded number of steps instead of hanging
	// or recursing off the stack — this call returning at all, quickly, is
	// as much the point of the test as the error it returns.
	_, err := r.Resolve("#/components/schemas/A")
	if !errors.Is(err, openapi.ErrCycle) {
		t.Fatalf("Resolve() error = %v, want ErrCycle", err)
	}
}

func TestResolver_SamePointerFromTwoBranches(t *testing.T) {
	doc := loadFixture(t, "ref_branch.json")
	r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	a, err := r.Resolve("#/components/schemas/BranchA")
	if err != nil {
		t.Fatalf("Resolve(BranchA): %v", err)
	}
	b, err := r.Resolve("#/components/schemas/BranchB")
	if err != nil {
		t.Fatalf("Resolve(BranchB): %v", err)
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok || am["type"] != "string" || bm["type"] != "string" {
		t.Fatalf("both branches should resolve to Common, got a=%v b=%v", a, b)
	}
}

// TestResolver_MemoDoesNotSmuggleBudget guards the exact bug the per-branch
// depth budget exists to prevent: a pointer already resolved once, cheaply,
// must not let a LATER, more expensive chain reuse that success and skip
// paying for its own hops.
func TestResolver_MemoDoesNotSmuggleBudget(t *testing.T) {
	doc := loadFixture(t, "ref_smuggle.json") // Entry -> Mid -> Terminal
	r := openapi.NewResolver(doc, 2)

	if _, err := r.Resolve("#/components/schemas/Mid"); err != nil {
		t.Fatalf("Resolve(Mid): %v", err)
	}

	_, err := r.Resolve("#/components/schemas/Entry")
	if !errors.Is(err, openapi.ErrBudgetExhausted) {
		t.Fatalf("Resolve(Entry) error = %v, want ErrBudgetExhausted (memo smuggled a value past the budget)", err)
	}
}

func TestResolver_RefWithSiblingIsStillFollowed(t *testing.T) {
	doc := loadFixture(t, "ref_sibling.json")
	r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	node := map[string]any{
		"$ref":        "#/components/responses/Foo",
		"description": "ignored sibling, per OAS 3.0 §4.8.23.1",
	}
	got, err := r.ResolveNode(node)
	if err != nil {
		t.Fatalf("ResolveNode(): %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["description"] != "the real response" {
		t.Fatalf("ResolveNode() = %v, want Foo's own content with the sibling ignored", got)
	}
}

// TestResolver_ResolveNodePointer guards the fact ResolveNode itself cannot
// report: which pointer a $ref chain actually bottoms out at. A caller that
// anchors a document pointer on the wrong hop (the first instead of the
// last) builds one that does not dereference — the exact defect this method
// exists to let callers avoid (see [specs]'s resolveResponseContent).
func TestResolver_ResolveNodePointer(t *testing.T) {
	t.Run("chained ref reports the LAST hop, not the first", func(t *testing.T) {
		doc := loadFixture(t, "ref_chain_12.json")
		r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

		node := map[string]any{"$ref": "#/components/schemas/Hop0"}
		pointer, resolved, err := r.ResolveNodePointer(node)
		if err != nil {
			t.Fatalf("ResolveNodePointer(): %v", err)
		}
		const want = "#/components/schemas/Hop12"
		if pointer != want {
			t.Errorf("pointer = %q, want %q (the terminal hop)", pointer, want)
		}
		if m, ok := resolved.(map[string]any); !ok || m["description"] != "terminal" {
			t.Errorf("resolved = %v, want the Hop12 terminal node", resolved)
		}
	})

	t.Run("single-hop ref reports that one hop", func(t *testing.T) {
		doc := loadFixture(t, "ref_sibling.json")
		r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

		node := map[string]any{"$ref": "#/components/responses/Foo"}
		pointer, _, err := r.ResolveNodePointer(node)
		if err != nil {
			t.Fatalf("ResolveNodePointer(): %v", err)
		}
		const want = "#/components/responses/Foo"
		if pointer != want {
			t.Errorf("pointer = %q, want %q", pointer, want)
		}
	})

	t.Run("a non-$ref node reports no hop at all", func(t *testing.T) {
		doc := loadFixture(t, "ref_sibling.json")
		r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

		node := map[string]any{"type": "object"}
		pointer, resolved, err := r.ResolveNodePointer(node)
		if err != nil {
			t.Fatalf("ResolveNodePointer(): %v", err)
		}
		if pointer != "" {
			t.Errorf("pointer = %q, want \"\" (node was never a $ref)", pointer)
		}
		if m, ok := resolved.(map[string]any); !ok || m["type"] != "object" {
			t.Errorf("resolved = %v, want node unchanged", resolved)
		}
	})
}

// TestLoad_AcceptanceDocument is a smoke test against the acceptance
// corpus (internal/testspec: 110 paths, 781 $refs — the same counts the
// P1a launch notes measured with jq on the original document). Only small,
// targeted facts about it are asserted.
func TestLoad_AcceptanceDocument(t *testing.T) {
	specPath := "internal/testspec/testdata/acceptance.json"
	raw := testspec.Bytes(t)

	doc, report, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("Load(%s): %v", specPath, err)
	}
	if doc.Format() != openapi.FormatOAS30 {
		t.Errorf("Format() = %v, want %v", doc.Format(), openapi.FormatOAS30)
	}
	if !bytes.Equal(doc.Raw(), raw) {
		t.Errorf("Raw() does not equal the input bytes unchanged")
	}
	if paths, ok := doc.Root()["paths"].(map[string]any); !ok || len(paths) != 110 {
		t.Errorf("paths count = %d, want 110", len(paths))
	}

	// No root servers[]; the 110 path items disagree on their own servers[]
	// path component (97 -> "", 9 -> "/api/v1", 4 empty arrays contribute
	// nothing either way), so the base path must fall back to ambiguous
	// rather than silently pick a winner.
	base, origin := doc.BasePath()
	if origin != openapi.BaseAmbiguous {
		t.Errorf("BasePath() origin = %v, want %v", origin, openapi.BaseAmbiguous)
	}
	if base != "" {
		t.Errorf("BasePath() path = %q, want empty", base)
	}
	if report.BasePathOrigin != openapi.BaseAmbiguous {
		t.Errorf("Report.BasePathOrigin = %v, want %v", report.BasePathOrigin, openapi.BaseAmbiguous)
	}

	// DELETE /api/v1/account's 200 response is a $ref carrying a
	// "description" sibling — the launch notes call out this exact shape as
	// the one every hand-written fixture can miss (siblings must be
	// ignored, the $ref still followed) and it appears exactly once in the
	// real document.
	r := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	del, ok := doc.Root()["paths"].(map[string]any)["/api/v1/account"].(map[string]any)["delete"].(map[string]any)
	if !ok {
		t.Fatalf("DELETE /api/v1/account is missing from the acceptance document")
	}
	resp200, ok := del["responses"].(map[string]any)["200"]
	if !ok {
		t.Fatalf("DELETE /api/v1/account 200 response is missing")
	}
	resolved, err := r.ResolveNode(resp200)
	if err != nil {
		t.Fatalf("ResolveNode(DELETE /api/v1/account 200): %v", err)
	}
	body, ok := resolved.(map[string]any)
	if !ok {
		t.Fatalf("resolved response is not an object: %v", resolved)
	}
	if _, hasContent := body["content"]; !hasContent {
		t.Errorf("resolved response has no content — the $ref's sibling was followed instead of the $ref itself")
	}
}

func TestResolver_PointerEdgeCases(t *testing.T) {
	doc := loadFixture(t, "ref_pointer_edge.json")
	r := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	t.Run("a pointer that misses returns ErrPointerNotFound", func(t *testing.T) {
		_, err := r.Resolve("#/nope/nope")
		if !errors.Is(err, openapi.ErrPointerNotFound) {
			t.Fatalf("Resolve() error = %v, want ErrPointerNotFound", err)
		}
	})

	t.Run("~1 escapes a literal slash in a key", func(t *testing.T) {
		got, err := r.Resolve("#/components/schemas/weird~1name")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if m, ok := got.(map[string]any); !ok || m["description"] != "slash in key" {
			t.Fatalf("Resolve() = %v, want the slash-keyed schema", got)
		}
	})

	t.Run("~0 escapes a literal tilde in a key", func(t *testing.T) {
		got, err := r.Resolve("#/components/schemas/weird~0name")
		if err != nil {
			t.Fatalf("Resolve(): %v", err)
		}
		if m, ok := got.(map[string]any); !ok || m["description"] != "tilde in key" {
			t.Fatalf("Resolve() = %v, want the tilde-keyed schema", got)
		}
	})
}

// TestEscapePointerToken pins RFC 6901's escape order — "~" first (to
// "~0"), then "/" (to "~1") — against the two production copies of this
// exact rule (internal/gen, internal/specs) this slice deliberately leaves
// standing rather than converting: all three must keep agreeing on the same
// four cases, or a pointer built with one and parsed with another would
// silently point at the wrong node.
func TestEscapePointerToken(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plainToken", "plainToken"},
		{"a~b", "a~0b"},
		{"a/b", "a~1b"},
		{"a~/b", "a~0~1b"}, // order matters: escaping "/" first would corrupt the "~1" just produced
	}
	for _, tc := range tests {
		if got := openapi.EscapePointerToken(tc.in); got != tc.want {
			t.Errorf("EscapePointerToken(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLoad_yamlIsConvertedNotRefused is A8's flip of the case
// TestLoad_Refusals used to hold: a YAML document loads, its normalized
// form is JSON, and an integer-keyed responses map (`200:`) survives as
// the string key the rest of the pipeline expects.
func TestLoad_yamlIsConvertedNotRefused(t *testing.T) {
	t.Parallel()
	raw := []byte("# banner comment\nopenapi: 3.0.3\ninfo: {title: y, version: '1'}\npaths:\n  /w:\n    get:\n      responses:\n        200: {description: ok}\n")
	doc, report, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("Load(yaml) error = %v", err)
	}
	if report.Format != openapi.FormatOAS30 {
		t.Errorf("format = %q", report.Format)
	}
	if !strings.Contains(string(doc.Normalized()), `"200"`) {
		t.Errorf("normalized document lost the integer-keyed response: %s", doc.Normalized())
	}
}
