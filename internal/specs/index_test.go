package specs_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

// loadAndIndex loads raw as an OpenAPI document and runs [specs.Index] over
// it with a fresh resolver — the same two calls [specs.Repo.Import] and
// [specs.Repo.Report] both make.
func loadAndIndex(t *testing.T, raw string) ([]*specs.Operation, map[int][]*specs.Response, *openapi.Report) {
	t.Helper()
	doc, report, err := openapi.Load([]byte(raw))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	ops, resp := specs.Index(doc, res, report)
	return ops, resp, report
}

// oasDoc builds a minimal OAS 3.0 document with a single "GET /x" operation
// whose "responses" object is exactly responsesJSON, so every selection-rule
// test case only has to say what varies.
func oasDoc(responsesJSON string) string {
	return `{
	  "openapi": "3.0.3",
	  "info": {"title": "T", "version": "1.0.0"},
	  "paths": {
	    "/x": {
	      "get": {
	        "operationId": "getX",
	        "responses": ` + responsesJSON + `
	      }
	    }
	  }
	}`
}

func TestIndex_ResponseSelection(t *testing.T) {
	tests := []struct {
		name           string
		responses      string
		wantSelectors  []string // expected emission order
		wantDefault    string   // selector of the row expected to be IsDefault
		wantHTTPStatus int
		wantOrigin     string
	}{
		{
			name:           "only 200",
			responses:      `{"200": {"description": "ok"}}`,
			wantSelectors:  []string{"200"},
			wantDefault:    "200",
			wantHTTPStatus: 200,
			wantOrigin:     "numeric",
		},
		{
			name:           "200 and 201",
			responses:      `{"201": {"description": "created"}, "200": {"description": "ok"}}`,
			wantSelectors:  []string{"200", "201"},
			wantDefault:    "200",
			wantHTTPStatus: 200,
			wantOrigin:     "numeric",
		},
		{
			name:           "only 2XX",
			responses:      `{"2XX": {"description": "any success"}}`,
			wantSelectors:  []string{"2XX"},
			wantDefault:    "2XX",
			wantHTTPStatus: 200,
			wantOrigin:     "2XX",
		},
		{
			name:           "only default",
			responses:      `{"default": {"description": "error"}}`,
			wantSelectors:  []string{"default"},
			wantDefault:    "default",
			wantHTTPStatus: 200,
			wantOrigin:     "default",
		},
		{
			name:           "only 404",
			responses:      `{"404": {"description": "not found"}}`,
			wantSelectors:  []string{"404"},
			wantDefault:    "404",
			wantHTTPStatus: 404,
			// no numeric 2xx, no "2XX", no "default" exist: the lowest
			// response of any kind is forced to be the default, and its
			// origin is overwritten to say so.
			wantOrigin: "fallback",
		},
		{
			name:           "no responses",
			responses:      `{}`,
			wantSelectors:  []string{""},
			wantDefault:    "",
			wantHTTPStatus: 200,
			wantOrigin:     "fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops, resp, _ := loadAndIndex(t, oasDoc(tt.responses))
			if len(ops) != 1 {
				t.Fatalf("operations = %d, want 1", len(ops))
			}
			rows := resp[0]

			gotSelectors := make([]string, len(rows))
			for i, r := range rows {
				gotSelectors[i] = r.Selector
			}
			if !slices.Equal(gotSelectors, tt.wantSelectors) {
				t.Errorf("selectors = %v, want %v", gotSelectors, tt.wantSelectors)
			}

			var def *specs.Response
			for _, r := range rows {
				if r.IsDefault {
					def = r
				}
			}
			if def == nil {
				t.Fatalf("no row marked IsDefault among %+v", rows)
			}
			if def.Selector != tt.wantDefault {
				t.Errorf("default selector = %q, want %q", def.Selector, tt.wantDefault)
			}
			if def.HTTPStatus != tt.wantHTTPStatus {
				t.Errorf("default http_status = %d, want %d", def.HTTPStatus, tt.wantHTTPStatus)
			}
			if def.StatusOrigin != tt.wantOrigin {
				t.Errorf("default status_origin = %q, want %q", def.StatusOrigin, tt.wantOrigin)
			}
		})
	}
}

func TestIndex_ResponseRef(t *testing.T) {
	tests := []struct {
		name string
		node string // the JSON object at responses.200
	}{
		{name: "pure $ref", node: `{"$ref": "#/components/responses/Ok"}`},
		{
			name: "$ref with a description sibling (siblings are ignored, still followed)",
			node: `{"$ref": "#/components/responses/Ok", "description": "overridden, ignored"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{
			  "openapi": "3.0.3",
			  "info": {"title": "T", "version": "1.0.0"},
			  "paths": {
			    "/x": {
			      "get": {"responses": {"200": ` + tt.node + `}}
			    }
			  },
			  "components": {
			    "responses": {
			      "Ok": {
			        "description": "ok",
			        "content": {
			          "application/json": {"schema": {"type": "object"}}
			        }
			      }
			    }
			  }
			}`

			ops, resp, _ := loadAndIndex(t, raw)
			if len(ops) != 1 {
				t.Fatalf("operations = %d, want 1", len(ops))
			}
			rows := resp[0]
			if len(rows) != 1 {
				t.Fatalf("responses = %d, want 1", len(rows))
			}
			r := rows[0]

			const wantMT = "application/json"
			if r.MediaType == nil || *r.MediaType != wantMT {
				t.Fatalf("media_type = %v, want %q", r.MediaType, wantMT)
			}
			const wantPtr = "#/components/responses/Ok/content/application~1json/schema"
			if r.SchemaPtr == nil || *r.SchemaPtr != wantPtr {
				t.Fatalf("schema_ptr = %v, want %q (resolved location, not the referring site)", r.SchemaPtr, wantPtr)
			}
		})
	}
}

func TestIndex_ResponseRefChained(t *testing.T) {
	// components/responses/A is itself a $ref to components/responses/B,
	// which carries the actual content. schema_ptr must anchor at B, the
	// LAST hop of the chain: A's own JSON is just {"$ref": "...B"} and has
	// no "content" key at all, so a pointer built from A would not actually
	// dereference to anything usable.
	raw := `{
	  "openapi": "3.0.3",
	  "info": {"title": "T", "version": "1.0.0"},
	  "paths": {
	    "/x": {
	      "get": {"responses": {"200": {"$ref": "#/components/responses/A"}}}
	    }
	  },
	  "components": {
	    "responses": {
	      "A": {"$ref": "#/components/responses/B"},
	      "B": {
	        "description": "ok",
	        "content": {
	          "application/json": {"schema": {"type": "object"}}
	        }
	      }
	    }
	  }
	}`

	ops, resp, _ := loadAndIndex(t, raw)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}
	rows := resp[0]
	if len(rows) != 1 {
		t.Fatalf("responses = %d, want 1", len(rows))
	}
	r := rows[0]

	const wantMT = "application/json"
	if r.MediaType == nil || *r.MediaType != wantMT {
		t.Fatalf("media_type = %v, want %q", r.MediaType, wantMT)
	}
	const wantPtr = "#/components/responses/B/content/application~1json/schema"
	if r.SchemaPtr == nil || *r.SchemaPtr != wantPtr {
		t.Fatalf("schema_ptr = %v, want %q (anchored at the LAST hop, B, not the first, A)", r.SchemaPtr, wantPtr)
	}
}

func TestIndex_ResponseRefUnresolved(t *testing.T) {
	raw := oasDoc(`{"200": {"$ref": "#/components/responses/Missing"}}`)
	ops, resp, report := loadAndIndex(t, raw)

	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}
	if ops[0].ParseError != nil {
		t.Errorf("ParseError = %q, want nil: an unresolved $ref is a warning, not a parse error", *ops[0].ParseError)
	}
	if len(report.Warnings) == 0 {
		t.Fatalf("report.Warnings is empty, want at least one for the unresolved $ref")
	}

	rows := resp[0]
	if len(rows) != 1 {
		t.Fatalf("responses = %d, want 1 (the row is still recorded, just without content)", len(rows))
	}
	r := rows[0]
	if r.Selector != "200" || r.HTTPStatus != 200 {
		t.Errorf("response = %+v, want selector 200 / http_status 200 still recorded", r)
	}
	if r.MediaType != nil || r.SchemaPtr != nil {
		t.Errorf("media_type/schema_ptr = %v/%v, want both nil", r.MediaType, r.SchemaPtr)
	}
}

func TestIndex_NoContentResponse(t *testing.T) {
	raw := oasDoc(`{"204": {"description": "no content"}}`)
	ops, resp, _ := loadAndIndex(t, raw)

	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}
	rows := resp[0]
	if len(rows) != 1 {
		t.Fatalf("responses = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.HTTPStatus != 204 {
		t.Errorf("http_status = %d, want 204", r.HTTPStatus)
	}
	if r.MediaType != nil {
		t.Errorf("media_type = %q, want nil", *r.MediaType)
	}
	if r.SchemaPtr != nil {
		t.Errorf("schema_ptr = %q, want nil", *r.SchemaPtr)
	}
}

func TestIndex_CanonicalPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "zero params", path: "/things", want: "/things"},
		{name: "one param", path: "/things/{id}", want: "/things/{}"},
		{name: "two params", path: "/things/{id}/sub/{subId}", want: "/things/{}/sub/{}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"openapi":"3.0.3","info":{"title":"T","version":"1.0.0"},"paths":{"` +
				tt.path + `":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
			ops, _, _ := loadAndIndex(t, raw)
			if len(ops) != 1 {
				t.Fatalf("operations = %d, want 1", len(ops))
			}
			if ops[0].CanonicalPath != tt.want {
				t.Errorf("CanonicalPath = %q, want %q", ops[0].CanonicalPath, tt.want)
			}
			// Index must never grow a second implementation of the
			// canonical-path rule (P2's override/conflict key) — it has to
			// agree with router.CanonicalPath by construction, not by luck.
			if got := router.CanonicalPath(tt.path); got != ops[0].CanonicalPath {
				t.Errorf("CanonicalPath diverges from router.CanonicalPath: %q vs %q", ops[0].CanonicalPath, got)
			}
		})
	}
}

func TestIndex_PointerEscaping(t *testing.T) {
	raw := `{"openapi":"3.0.3","info":{"title":"T","version":"1.0.0"},"paths":{"/api/v1/account":` +
		`{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	ops, _, _ := loadAndIndex(t, raw)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}
	const want = "#/paths/~1api~1v1~1account/get"
	if ops[0].Pointer != want {
		t.Errorf("Pointer = %q, want %q", ops[0].Pointer, want)
	}
}

func TestIndex_PathItemIgnoresNonOperationKeys(t *testing.T) {
	raw := `{
	  "openapi": "3.0.3",
	  "info": {"title": "T", "version": "1.0.0"},
	  "paths": {
	    "/x": {
	      "servers": [{"url": "https://elsewhere.example.com"}],
	      "parameters": [{"name": "id", "in": "query"}],
	      "summary": "a summary",
	      "description": "a description",
	      "get": {"responses": {"200": {"description": "ok"}}}
	    }
	  }
	}`
	ops, _, _ := loadAndIndex(t, raw)
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (servers/parameters/summary/description are not methods)", len(ops))
	}
	if ops[0].Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", ops[0].Method)
	}
}

func TestIndex_MalformedOperationNode(t *testing.T) {
	raw := `{"openapi":"3.0.3","info":{"title":"T","version":"1.0.0"},"paths":{"/x":{"get":"not an object"}}}`
	ops, resp, report := loadAndIndex(t, raw)

	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1 (a malformed operation is recorded, never dropped)", len(ops))
	}
	if ops[0].ParseError == nil {
		t.Fatalf("ParseError = nil, want set: the operation node is not a JSON object")
	}
	if report.Degraded != 1 {
		t.Errorf("report.Degraded = %d, want 1", report.Degraded)
	}
	if report.Operations != 1 {
		t.Errorf("report.Operations = %d, want 1", report.Operations)
	}

	rows := resp[0]
	if len(rows) != 1 || !rows[0].IsDefault || rows[0].HTTPStatus != 200 {
		t.Errorf("responses for malformed operation = %+v, want one synthesized 200/fallback row", rows)
	}
}

// TestSelectMediaType pins the exported picker's two rules directly:
// "application/json" wins outright when present, and otherwise the
// lexicographically first key wins — deterministic regardless of Go's
// randomized map iteration order, run enough times to make a non-
// deterministic implementation flaky rather than silently passing once.
func TestSelectMediaType(t *testing.T) {
	jsonAndOthers := map[string]any{
		"text/plain":       nil,
		"application/json": nil,
		"application/xml":  nil,
	}
	for range 20 {
		if got := specs.SelectMediaType(jsonAndOthers); got != "application/json" {
			t.Fatalf("SelectMediaType(with application/json present) = %q, want application/json", got)
		}
	}

	noJSON := map[string]any{
		"text/plain":      nil,
		"application/xml": nil,
	}
	for range 20 {
		if got := specs.SelectMediaType(noJSON); got != "application/xml" {
			t.Fatalf("SelectMediaType(no json) = %q, want application/xml (lexicographically first)", got)
		}
	}
}

// TestSelectMediaType_EmptyContentPanics pins the documented behavior: an
// empty content map is the CALLER's bug (the one production call site
// guards len(content)==0 before calling), and SelectMediaType is not
// expected to defend against it silently — it panics on keys[0] instead of
// returning "" and letting a caller mistake that for a real media type.
func TestSelectMediaType_EmptyContentPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("SelectMediaType(empty map) did not panic; the documented contract requires the caller to guard emptiness")
		}
	}()
	specs.SelectMediaType(map[string]any{})
}
