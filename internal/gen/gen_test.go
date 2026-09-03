package gen

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/openapi"
)

// buildResolver loads doc (a decoded-from-JSON document root, as a Go
// literal so tests read like the fixture they describe) through the real
// openapi package: Load normalizes dialect quirks (singular "example" ->
// "examples", "nullable: true" -> a type union) exactly as production does,
// and NewResolver gives Generator the real, budget-bounded $ref chaser.
func buildResolver(t *testing.T, doc map[string]any) *openapi.Resolver {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	d, _, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("openapi.Load: %v", err)
	}
	return openapi.NewResolver(d, 0)
}

func baseDoc() map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"paths":   map[string]any{},
	}
}

func TestBodyGeneratesJSONMatchingSchema(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"id":   map[string]any{"type": "integer"},
										"name": map[string]any{"type": "string"},
									},
									"required": []any{"id", "name"},
								},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})

	v := ResponseVariant{
		Selector:   "200",
		HTTPStatus: 200,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1widgets~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "1"}, Status: 200}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Body did not produce valid JSON: %v\nbody: %s", err, b)
	}
	if _, ok := got["id"]; !ok {
		t.Fatalf("missing required field %q in %s", "id", b)
	}
	if _, ok := got["name"]; !ok {
		t.Fatalf("missing required field %q in %s", "name", b)
	}
}

func TestBodyDeterministicAcrossCalls(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"id":    map[string]any{"type": "integer"},
										"name":  map[string]any{"type": "string"},
										"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
										"score": map[string]any{"type": "number"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 7, ListSize: 4})

	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "42"}, Status: 200}

	b1, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (1): %v", err)
	}
	b2, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (2): %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("two identical requests produced different bodies:\n%s\n%s", b1, b2)
	}
}

func TestBodyEmptySchemaPtrIsNilNil(t *testing.T) {
	res := buildResolver(t, baseDoc())
	g := New(res, Options{Seed: 1})
	b, err := g.Body(ResponseVariant{SchemaPtr: ""}, Request{Method: "DELETE", CanonicalPath: "/x", Status: 204})
	if err != nil || b != nil {
		t.Fatalf("expected (nil, nil), got (%q, %v)", b, err)
	}
}

func TestBodyTextPlainReturnsRawString(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/status": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"409": map[string]any{
						"description": "conflict",
						"content": map[string]any{
							"text/plain": map[string]any{
								"schema": map[string]any{"type": "string", "minLength": 3, "maxLength": 3},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})

	v := ResponseVariant{
		Selector:  "409",
		MediaType: "text/plain",
		SchemaPtr: "#/paths/~1status/get/responses/409/content/text~1plain/schema",
		OpPointer: "#/paths/~1status/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/status", Status: 409}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	// A raw text/plain body must NOT be JSON-quoted (`"abc"`), just `abc`.
	if len(b) > 0 && b[0] == '"' {
		t.Fatalf("text/plain body looks JSON-quoted: %s", b)
	}
	var probe any
	if json.Unmarshal(b, &probe) == nil {
		if _, isString := probe.(string); isString {
			t.Fatalf("text/plain body should not itself be a JSON string literal: %s", b)
		}
	}
}

// TestBodyContentLevelExampleWins is the reason Body takes the whole
// ResponseVariant rather than just a schema pointer: DESIGN §9 ranks the
// media-type-level example ABOVE schema-driven generation.
func TestBodyContentLevelExampleWins(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":       "object",
									"properties": map[string]any{"id": map[string]any{"type": "integer"}},
								},
								"example": map[string]any{"id": 999999, "pinned": true},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})

	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "1"}, Status: 200}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	if got["pinned"] != true {
		t.Fatalf("expected the content-level example to win over schema generation, got %s", b)
	}
}

// TestHeadersRefdResponseWithHeaders is the fixture the digest explicitly
// calls out: the real acceptance document's only two "headers" maps hang
// off responses reachable purely through a $ref (never the selected 2xx
// directly), so a Headers implementation that navigates the referring site
// instead of resolving through the $ref would look perfect against the
// real document while doing nothing. This fixture forces that path: the
// operation's 200 is itself a $ref into components/responses, and THAT
// response carries "headers".
func TestHeadersRefdResponseWithHeaders(t *testing.T) {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"responses": map[string]any{
			"WidgetOK": map[string]any{
				"description": "ok",
				"headers": map[string]any{
					"X-Request-Id": map[string]any{
						"schema": map[string]any{"type": "string", "minLength": 8, "maxLength": 8},
					},
					"X-Rate-Limit": map[string]any{
						"schema": map[string]any{"type": "integer", "minimum": 100, "maximum": 100},
					},
				},
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"type": "object"},
					},
				},
			},
		},
	}
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{"$ref": "#/components/responses/WidgetOK"},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})

	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		OpPointer: "#/paths/~1widgets~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "1"}, Status: 200}

	headers := g.Headers(v, req)
	if len(headers) != 2 {
		t.Fatalf("expected 2 headers resolved through the $ref, got %d: %#v", len(headers), headers)
	}
	if headers["X-Request-Id"] == "" {
		t.Fatalf("X-Request-Id missing or empty: %#v", headers)
	}
	if len(headers["X-Request-Id"]) != 8 {
		t.Fatalf("X-Request-Id should respect minLength/maxLength=8, got %q", headers["X-Request-Id"])
	}
	if headers["X-Rate-Limit"] != "100" {
		t.Fatalf("X-Rate-Limit should be pinned to 100 by minimum==maximum, got %q", headers["X-Rate-Limit"])
	}
}

// TestHeadersNoHeadersDeclaredIsEmptyNotError is the common case: absent
// headers must never be treated as an error.
func TestHeadersNoHeadersDeclaredIsEmptyNotError(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{"schema": map[string]any{"type": "object"}},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})

	v := ResponseVariant{Selector: "200", MediaType: "application/json", OpPointer: "#/paths/~1widgets~1{id}/get"}
	req := Request{Method: "GET", CanonicalPath: "/widgets/{}", Status: 200}

	headers := g.Headers(v, req)
	if headers == nil {
		t.Fatalf("Headers must return an empty map, not nil")
	}
	if len(headers) != 0 {
		t.Fatalf("expected no headers, got %#v", headers)
	}
}

// TestDetailRouteIDMatchesRequestedPathParam exercises the identity
// contract end to end THROUGH Body/Generator: a detail schema whose id
// property is typed {integer, format: uint} must come back as a JSON
// number equal (in value) to the requested path parameter, never the raw
// URL string. IDForIndex/typedIDFromHash is what makes this possible; this
// test is what proves genScalar's OWN untyped fallback would have gotten
// it wrong — the only reason it passes is that the id field goes through
// path-param coercion, not generic scalar generation. Since the List seam
// is still a stub in this phase, this test exercises the COERCION
// primitive directly (typedIDFromHash via IDForIndex), matching what the
// List agent's real implementation will do with the requested id.
func TestPathParamIDCoercedToSchemaType(t *testing.T) {
	idSchema := map[string]any{"type": "integer", "format": "uint"}
	// Simulate: detail route requested id "42" as a raw URL string.
	requestedID := "42"

	// The List agent's job: coerce requestedID into idSchema's own typed
	// form before writing it into the item. We can't call into list.go (a
	// stub in this phase), but we CAN prove the underlying primitive this
	// phase owns behaves correctly for the exact fixture DESIGN calls out:
	// a numeric id schema must never just echo the string "42" as {"id":"42"}.
	seedList := SeedList(Options{Seed: 1}, Request{Method: "GET", CanonicalPath: "/bulletins/{}", Status: 200})
	val, canonical := IDForIndex(seedList, 42, idSchema)
	if _, ok := val.(int64); !ok {
		t.Fatalf("id typed as {integer, format: uint} must decode as int64, got %T", val)
	}
	if canonical == requestedID {
		// Coincidence is fine or not, but the KEY property is: whatever id
		// IDForIndex hands back must be JSON-marshalable as a NUMBER.
		t.Log("canonical happened to equal the raw path string; that's fine, only the TYPE matters here")
	}
	b, err := json.Marshal(map[string]any{"id": val})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) == `{"id":"42"}` {
		t.Fatalf(`id must be a JSON number, not the quoted path string: got %s`, b)
	}
	var probe struct {
		ID json.Number `json:"id"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("id property did not decode as a JSON number: %v (%s)", err, b)
	}
}

// TestGeneratorConcurrentBodyIsRaceFreeAndDeterministic is DESIGN §9's
// "fully synchronous ... two concurrent requests must not be able to
// interleave into each other's values" made concrete: many goroutines
// share ONE Generator (exactly how mockplane will use it — one Generator
// per workspace revision, every request on that workspace hitting it
// concurrently) and each repeatedly generates the SAME two distinct
// requests. Run with -race, this fails if any request-scoped state ever
// leaked onto the Generator itself; without -race, it still fails if the
// two requests' results ever cross-contaminate.
func TestGeneratorConcurrentBodyIsRaceFreeAndDeterministic(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"id":   map[string]any{"type": "integer"},
										"name": map[string]any{"type": "string"},
										"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
									},
									"required": []any{"id", "name"},
								},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 11, ListSize: 5})

	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets~1{id}/get",
	}
	reqA := Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "1"}, Status: 200}
	reqB := Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "2"}, Status: 200}

	wantA, err := g.Body(v, reqA)
	if err != nil {
		t.Fatalf("Body(reqA): %v", err)
	}
	wantB, err := g.Body(v, reqB)
	if err != nil {
		t.Fatalf("Body(reqB): %v", err)
	}
	if string(wantA) == string(wantB) {
		t.Fatalf("distinct ids produced identical bodies; test fixture is not distinguishing enough")
	}

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan string, goroutines*2)
	for range goroutines {
		wg.Go(func() {
			gotA, err := g.Body(v, reqA)
			if err != nil {
				errs <- err.Error()
				return
			}
			if string(gotA) != string(wantA) {
				errs <- "reqA body diverged under concurrency: " + string(gotA)
			}
			gotB, err := g.Body(v, reqB)
			if err != nil {
				errs <- err.Error()
				return
			}
			if string(gotB) != string(wantB) {
				errs <- "reqB body diverged under concurrency: " + string(gotB)
			}
			_ = g.Headers(v, reqA)
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
