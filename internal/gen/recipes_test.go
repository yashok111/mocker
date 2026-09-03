package gen

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/recipes"
)

// --- byte-identity: HARD RULE 6, at unit scale --------------------------
//
// golden_p1b_test.go is the real, document-wide guard; these four fixtures
// exist to pin the SAME invariant at a scale small enough to read in a
// failure message, and were captured from the tree BEFORE this phase
// touched internal/gen at all (the same technique, just by hand instead of
// a stored file) — a hardcoded literal, not merely "two calls agree with
// each other" (which a change that shifted every body the SAME way would
// still pass, exactly the failure mode HARD RULE 6 warns about).

func recipeObjectDoc() map[string]any {
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
										"id":     map[string]any{"type": "integer", "format": "uint"},
										"name":   map[string]any{"type": "string"},
										"status": map[string]any{"type": "string"},
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
	return doc
}

func recipeNestedListDoc() map[string]any {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/groups": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"groups": map[string]any{
											"type": "array",
											"items": map[string]any{
												"type": "object",
												"properties": map[string]any{
													"id": map[string]any{"type": "integer"},
													"members": map[string]any{
														"type":  "array",
														"items": map[string]any{"type": "string"},
													},
												},
												"required": []any{"id", "members"},
											},
										},
									},
									"required": []any{"groups"},
								},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

func recipeSelfRefDoc() map[string]any {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"schemas": map[string]any{
			"Node": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"children": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/Node"},
					},
				},
				"required": []any{"name"},
			},
		},
	}
	doc["paths"] = map[string]any{
		"/nodes/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/Node"},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

// TestByteIdentityNoRecipesUnchanged is the "recipes.go/gen.go/schema.go/
// list.go changed nothing when Request.Recipes is nil" proof, for a
// handful of representative shapes: a plain object, a wrapped list, a
// nested (list-of-objects-with-their-own-array) shape, and a
// self-referencing schema. Each expected literal was captured from this
// package BEFORE this phase's implementation existed; a diff here means a
// no-recipes request no longer reproduces P1b's own output.
func TestByteIdentityNoRecipesUnchanged(t *testing.T) {
	cases := []struct {
		name string
		doc  map[string]any
		v    ResponseVariant
		req  Request
		opts Options
		want string
	}{
		{
			name: "object",
			doc:  recipeObjectDoc(),
			v: ResponseVariant{
				Selector: "200", HTTPStatus: 200, MediaType: "application/json",
				SchemaPtr: "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
				OpPointer: "#/paths/~1widgets~1{id}/get",
			},
			req:  Request{Method: "GET", CanonicalPath: "/widgets/{}", PathParams: map[string]string{"id": "1"}, Query: url.Values{}, Status: 200},
			opts: Options{Seed: 12345, ListSize: 10, NullRate: 0, MaxBytes: 4 << 20},
			want: `{"id":618406,"name":"Blair Walker","status":"inactive"}`,
		},
		{
			name: "list",
			doc:  widgetsDoc(),
			v:    widgetsListVariant(),
			req:  Request{Method: "GET", CanonicalPath: "/widgets", Query: url.Values{"limit": {"3"}, "offset": {"0"}}, Status: 200},
			opts: Options{Seed: 12345, ListSize: 10, NullRate: 0, MaxBytes: 4 << 20},
			want: `{"items":[{"id":553977626127,"name":"Drew Garcia","status":"processing"},{"id":553977625692,"name":"Casey Johnson","status":"inactive"},{"id":553977626997,"name":"Emerson Lee","status":"archived"}],"limit":3,"offset":0,"total":10}`,
		},
		{
			// A single array-typed property ("groups") makes this ALSO a
			// detected list route (list.go's detectListShape) — its own
			// ListSize (3, not the other cases' 10) is what governs both
			// how many groups come back AND, via Options.ListSize feeding
			// arrayLength's own base, how long each group's nested
			// "members" array is. Captured with exactly this ListSize.
			name: "nested list",
			doc:  recipeNestedListDoc(),
			v: ResponseVariant{
				Selector: "200", HTTPStatus: 200, MediaType: "application/json",
				SchemaPtr: "#/paths/~1groups/get/responses/200/content/application~1json/schema",
				OpPointer: "#/paths/~1groups/get",
			},
			req:  Request{Method: "GET", CanonicalPath: "/groups", Query: url.Values{}, Status: 200},
			opts: Options{Seed: 12345, ListSize: 3, NullRate: 0, MaxBytes: 4 << 20},
			want: `{"groups":[{"id":1738039233,"members":["h8qnxgmzpgyz","a1d26ldaat","b2w1r6spf6cxz"]},{"id":1738038798,"members":["hc63d","211i6ll2mplay","b6ch72"]},{"id":1738038363,"members":["1si","a","vm45"]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := buildResolver(t, tc.doc)
			g1 := New(res, tc.opts)
			g2 := New(res, tc.opts)

			b1, err := g1.Body(tc.v, tc.req)
			if err != nil {
				t.Fatalf("g1.Body: %v", err)
			}
			b2, err := g2.Body(tc.v, tc.req)
			if err != nil {
				t.Fatalf("g2.Body: %v", err)
			}
			if string(b1) != string(b2) {
				t.Fatalf("two independently constructed Generators diverged:\n  g1=%s\n  g2=%s", b1, b2)
			}
			if string(b1) != tc.want {
				t.Fatalf("output changed from the pre-P1c capture:\n  want=%s\n  got =%s", tc.want, b1)
			}
		})
	}
}

// The self-referencing fixture gets its own test: a tighter MaxBytes/
// MaxDepth (matching how it was originally captured) than the table
// above's shared Options.
func TestByteIdentityNoRecipesUnchangedSelfReferencing(t *testing.T) {
	res := buildResolver(t, recipeSelfRefDoc())
	opts := Options{Seed: 12345, ListSize: 3, NullRate: 0, MaxBytes: 4096, MaxDepth: 4}
	g1 := New(res, opts)
	g2 := New(res, opts)

	v := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1nodes~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1nodes~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/nodes/{}", PathParams: map[string]string{"id": "1"}, Query: url.Values{}, Status: 200}

	const want = `{"children":[{"children":[{"children":[{"name":""},{"name":""},{"name":""}],"name":"Hayden Moore"},{"children":[{"name":""},{"name":""},{"name":""}],"name":"Blair Walker"},{"children":[{"name":""},{"name":""},{"name":""}],"name":"Sage Martinez"}],"name":"Emerson Johnson"},{"children":[{"children":[{"name":""},{"name":""},{"name":""}],"name":"Jordan Wilson"},{"children":[{"name":""},{"name":""},{"name":""}],"name":"Quinn Martin"},{"children":[{"name":""},{"name":""},{"name":""}],"name":"Reese Walker"}],"name":"Quinn Martin"},{"children":[{"children":[{"name":""},{"name":""},{"name":""}],"name":"Drew Jackson"},{"children":[{"name":""},{"name":""},{"name":""}],"name":"Sage Smith"},{"children":[{"name":""},{"name":""},{"name":""}],"name":"Blair Thomas"}],"name":"Rowan Smith"}],"name":"Quinn Smith"}`

	b1, err := g1.Body(v, req)
	if err != nil {
		t.Fatalf("g1.Body: %v", err)
	}
	b2, err := g2.Body(v, req)
	if err != nil {
		t.Fatalf("g2.Body: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("two independently constructed Generators diverged:\n  g1=%s\n  g2=%s", b1, b2)
	}
	if string(b1) != want {
		t.Fatalf("output changed from the pre-P1c capture:\n  want=%s\n  got =%s", want, b1)
	}
}

// --- priority: a recipe outranks example/const/default -------------------

// TestRecipePriorityBeatsExampleConstDefault proves BOTH seams at once: the
// gated contentExample short-circuit (gen.go's Body) and recipeValue's own
// position as leafValue's first check. Without the gate, the media-type
// example would win before the walk (and recipeValue) ever ran at all;
// without recipeValue being first in the chain, const/default would win
// even once the walk does run.
func TestRecipePriorityBeatsExampleConstDefault(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/greeting": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"greeting": map[string]any{
											"type":    "string",
											"const":   "from-const",
											"default": "from-default",
										},
									},
								},
								"example": map[string]any{"greeting": "from-media-example"},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)

	set, err := recipes.Compile(map[string]recipes.Recipe{
		"greeting": {Kind: recipes.KindConst, Data: json.RawMessage(`"from-recipe"`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	g := New(res, Options{Seed: 1})
	v := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1greeting/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1greeting/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/greeting", Query: url.Values{}, Status: 200, Recipes: set}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	if got["greeting"] != "from-recipe" {
		t.Fatalf("expected the recipe to win over media-type example/const/default, got %v (body=%s)", got["greeting"], b)
	}
}

// --- precedence: exact index beats wildcard --------------------------------

func TestRecipePrecedenceExactIndexBeatsWildcard(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"items[*].status": {Kind: recipes.KindConst, Data: json.RawMessage(`"wildcard"`)},
		"items[0].status": {Kind: recipes.KindConst, Data: json.RawMessage(`"exact-zero"`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	g := New(res, Options{Seed: 1, ListSize: 20})
	v := widgetsListVariant()
	req := Request{Method: "GET", CanonicalPath: "/widgets", Status: 200, Query: url.Values{"limit": {"3"}}, Recipes: set}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var page map[string]any
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	items, _ := page["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	for i, it := range items {
		obj, _ := it.(map[string]any)
		want := "wildcard"
		if i == 0 {
			want = "exact-zero"
		}
		if obj["status"] != want {
			t.Fatalf("item[%d].status = %v, want %q", i, obj["status"], want)
		}
	}
}

// --- copy: a sibling of THIS object, not the first item's -----------------

func TestRecipeCopyPerListItemNotFirstItems(t *testing.T) {
	res := buildResolver(t, recipeListDoc())
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"items[*].echoedId": {Kind: recipes.KindCopy, Field: "$.id"},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	g := New(res, Options{Seed: 3, ListSize: 20})
	v := recipeListVariant()
	req := Request{Method: "GET", CanonicalPath: "/ritems", Status: 200, Query: url.Values{"limit": {"5"}}, Recipes: set}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var page map[string]any
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	items, _ := page["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}

	seen := map[float64]bool{}
	for i, it := range items {
		obj, _ := it.(map[string]any)
		id, idOK := obj["id"].(float64)
		echoed, echoedOK := obj["echoedId"].(float64)
		if !idOK || !echoedOK {
			t.Fatalf("item[%d]: id/echoedId not numeric: %#v", i, obj)
		}
		if id != echoed {
			t.Fatalf("item[%d]: echoedId (%v) != this item's OWN id (%v) — copy must read the sibling of the SAME object, not some other item's", i, echoed, id)
		}
		seen[echoed] = true
	}
	if len(seen) < 2 {
		t.Fatalf("every item's copied echoedId collapsed onto %d distinct value(s) — looks like every row copied the FIRST item's id instead of its own", len(seen))
	}
}

// --- coercion refusal: an incompatible recipe leaves generation alone -----

func TestRecipeJWTCoercionRefusalOnIntegerField(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/scores/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"score": map[string]any{"type": "integer"},
									},
									"required": []any{"score"},
								},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"score": {Kind: recipes.KindJWT},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	g := New(res, Options{
		Seed: 1,
		Auth: domain.AuthSettings{JWTTTLSec: 3600, Alg: "HS256", SigningKey: "test-signing-key"},
	})
	v := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1scores~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1scores~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/scores/{}", PathParams: map[string]string{"id": "1"}, Query: url.Values{}, Status: 200, Recipes: set}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	score, ok := got["score"].(float64)
	if !ok {
		t.Fatalf("a jwt recipe bound to an integer field must decline (a JWS cannot honestly be an integer): got %T %v", got["score"], got["score"])
	}
	if score < 0 {
		t.Fatalf("score should still be schema-conformant generated content, got %v", score)
	}
}

// --- listSize ---------------------------------------------------------

// recipeNestedArrayDoc puts the array TWO levels below the response root
// (object -> profile -> tags), specifically so detectListShape's own "a
// single array-typed property is a list" rule (list.go) does NOT fire on
// the top-level object — these tests are pinning schema.go's arrayLength,
// the plain nested-array path, not list.go's page-level sizing (which gets
// its own dedicated test below, TestRecipeListSizeBeatenByExplicitLimit).
func recipeNestedArrayDoc(maxItems any) map[string]any {
	doc := baseDoc()
	tags := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	if maxItems != nil {
		tags["maxItems"] = maxItems
	}
	doc["paths"] = map[string]any{
		"/tagged/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"profile": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"tags": tags,
											},
											"required": []any{"tags"},
										},
									},
									"required": []any{"profile"},
								},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

func recipeNestedArrayVariant() ResponseVariant {
	return ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1tagged~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1tagged~1{id}/get",
	}
}

func recipeNestedArrayReq() Request {
	return Request{Method: "GET", CanonicalPath: "/tagged/{}", PathParams: map[string]string{"id": "1"}, Query: url.Values{}, Status: 200}
}

func TestRecipeListSizePinnedExact(t *testing.T) {
	res := buildResolver(t, recipeNestedArrayDoc(nil))
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"profile.tags": {Kind: recipes.KindListSize, Data: json.RawMessage(`4`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	g := New(res, Options{Seed: 1, ListSize: 20, MaxBytes: 4 << 20})
	req := recipeNestedArrayReq()
	req.Recipes = set

	b, err := g.Body(recipeNestedArrayVariant(), req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	profile, _ := got["profile"].(map[string]any)
	tags, _ := profile["tags"].([]any)
	if len(tags) != 4 {
		t.Fatalf("listSize=4 should pin the array at exactly 4 (Options.ListSize=20 must be overridden), got %d: %s", len(tags), b)
	}
}

func TestRecipeListSizeRangeFromSeed(t *testing.T) {
	res := buildResolver(t, recipeNestedArrayDoc(nil))
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"profile.tags": {Kind: recipes.KindListSize, Data: json.RawMessage(`[2,6]`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	g := New(res, Options{Seed: 1, ListSize: 20, MaxBytes: 4 << 20})
	req := recipeNestedArrayReq()
	req.Recipes = set

	tagsLen := func() int {
		b, err := g.Body(recipeNestedArrayVariant(), req)
		if err != nil {
			t.Fatalf("Body: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("invalid JSON: %v: %s", err, b)
		}
		profile, _ := got["profile"].(map[string]any)
		tags, _ := profile["tags"].([]any)
		return len(tags)
	}

	n1 := tagsLen()
	if n1 < 2 || n1 > 6 {
		t.Fatalf("listSize=[2,6] produced a length outside the range: %d", n1)
	}
	n2 := tagsLen()
	if n1 != n2 {
		t.Fatalf("a ranged listSize recipe must pick reproducibly from the seed: got %d then %d for the identical request", n1, n2)
	}
}

func TestRecipeListSizeClampedByMaxItems(t *testing.T) {
	res := buildResolver(t, recipeNestedArrayDoc(json.Number("10")))
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"profile.tags": {Kind: recipes.KindListSize, Data: json.RawMessage(`50`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	g := New(res, Options{Seed: 1, ListSize: 3, MaxBytes: 4 << 20})
	req := recipeNestedArrayReq()
	req.Recipes = set

	b, err := g.Body(recipeNestedArrayVariant(), req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}
	profile, _ := got["profile"].(map[string]any)
	tags, _ := profile["tags"].([]any)
	if len(tags) != 10 {
		t.Fatalf("a listSize recipe pinning 50 on a maxItems:10 array must clamp to 10 (never override the schema's own bound), got %d: %s", len(tags), b)
	}
}

// recipeListDoc mirrors widgetsDoc's list/detail shape but with an extra
// "echoedId" item property (for the copy test) — kept separate from
// widgetsDoc so list_test.go's own fixture stays untouched.
func recipeListDoc() map[string]any {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/ritems": map[string]any{
			"get": map[string]any{
				"parameters": []any{
					map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}},
					map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer"}},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"items": map[string]any{
											"type": "array",
											"items": map[string]any{
												"type": "object",
												"properties": map[string]any{
													"id":       map[string]any{"type": "integer", "format": "uint"},
													"name":     map[string]any{"type": "string"},
													"status":   map[string]any{"type": "string"},
													"echoedId": map[string]any{"type": "integer"},
												},
												"required": []any{"id", "name"},
											},
										},
										"total":  map[string]any{"type": "integer"},
										"limit":  map[string]any{"type": "integer"},
										"offset": map[string]any{"type": "integer"},
									},
									"required": []any{"items", "total", "limit", "offset"},
								},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

func recipeListVariant() ResponseVariant {
	return ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1ritems/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1ritems/get",
	}
}

// TestRecipeListSizeBeatenByExplicitLimit is list.go's own half of listSize:
// a recipe pinned to the array's own path becomes pageLimit's FALLBACK
// default (replacing Options.ListSize) when the client asks for nothing,
// but an explicit ?limit= is checked first and always wins — "the client
// asked, the client wins" (task digest). Checked on the wrapper's own
// echoed "limit" field, exactly like list_test.go's own
// TestListBodyPaginationParamPriority, so this does not conflate "which
// page size was requested" with "how many rows exist" (total's own,
// unrelated ceiling on item count — unchanged by this recipe, see
// listSizeOrDefault's own doc).
func TestRecipeListSizeBeatenByExplicitLimit(t *testing.T) {
	res := buildResolver(t, recipeListDoc())
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"items": {Kind: recipes.KindListSize, Data: json.RawMessage(`5`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	g := New(res, Options{Seed: 1, ListSize: 20})
	v := recipeListVariant()

	page := func(query url.Values) map[string]any {
		req := Request{Method: "GET", CanonicalPath: "/ritems", Status: 200, Query: query, Recipes: set}
		b, err := g.Body(v, req)
		if err != nil {
			t.Fatalf("Body: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("invalid JSON: %v: %s", err, b)
		}
		return out
	}

	noQuery := page(url.Values{})
	if lim, _ := noQuery["limit"].(float64); lim != 5 {
		t.Fatalf("with no explicit query, pageLimit's fallback should be the recipe's pinned 5 (not Options.ListSize=20), got %v", noQuery["limit"])
	}

	explicit := page(url.Values{"limit": {"7"}})
	if lim, _ := explicit["limit"].(float64); lim != 7 {
		t.Fatalf("an explicit ?limit=7 must beat the listSize recipe's pinned 5, got %v", explicit["limit"])
	}
}

// --- race: concurrent requests through one shared Generator with recipes --

// TestRecipeGeneratorConcurrentRaceFreeAndDeterministic is
// TestGeneratorConcurrentBodyIsRaceFreeAndDeterministic's (gen_test.go)
// counterpart WITH recipes bound: const (recipeValue's own lookup path),
// copy (the post-pass), and listSize (arrayLength/listSizeOrDefault) all
// exercised concurrently over one shared Generator/*recipes.Set, run under
// -race. Anything request-scoped that leaked onto the Generator (or onto
// the Set, or onto a walker shared across goroutines) shows up here as
// either a race or a diverged body.
func TestRecipeGeneratorConcurrentRaceFreeAndDeterministic(t *testing.T) {
	res := buildResolver(t, recipeListDoc())
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"items[*].status":   {Kind: recipes.KindConst, Data: json.RawMessage(`"active"`)},
		"items[*].echoedId": {Kind: recipes.KindCopy, Field: "$.id"},
		"items":             {Kind: recipes.KindListSize, Data: json.RawMessage(`[3,6]`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	g := New(res, Options{Seed: 21, ListSize: 20})
	v := recipeListVariant()
	req := Request{Method: "GET", CanonicalPath: "/ritems", Status: 200, Query: url.Values{}, Recipes: set}

	want, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	for range goroutines {
		wg.Go(func() {
			got, err := g.Body(v, req)
			if err != nil {
				errs <- err.Error()
				return
			}
			if string(got) != string(want) {
				errs <- "body diverged under concurrency: " + string(got)
			}
			_ = g.Headers(v, req)
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// --- copy post-pass: bounded by MaxBytes, not multiplied by binding count -

// TestRecipeCopyPostPassCappedByMaxBytes is round-1 finding #6: an operator
// who binds MANY copy recipes to the same large sibling must never get a
// body larger than the generator's own ceiling — the post-pass runs on the
// FINISHED, already-MaxBytes-bounded tree, and nothing downstream ever
// re-checked its result. This exercises applyRecipePostPass directly
// (bypassing the schema walk entirely) so the fixture can pin an exact,
// tight MaxBytes without needing a document large enough to hit it through
// generation.
func TestRecipeCopyPostPassCappedByMaxBytes(t *testing.T) {
	const nCopies = 50

	big := make([]any, 200)
	for i := range big {
		big[i] = map[string]any{"a": "0123456789abcdef0123456789abcdef"}
	}
	val := map[string]any{"big": big}
	bindings := map[string]recipes.Recipe{}
	for i := range nCopies {
		name := fmt.Sprintf("copy%d", i)
		val[name] = nil
		bindings[name] = recipes.Recipe{Kind: recipes.KindCopy, Field: "$.big"}
	}
	set, err := recipes.Compile(bindings)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// A tight ceiling well under what nCopies un-budgeted clones of `big`
	// would produce (nCopies * len(big) is already an order of magnitude
	// over this), but comfortably over `val`'s own pre-transform size —
	// proving the CAP is what declined the transform, not merely a floor
	// too low for anything to fit.
	const maxBytes = 16 * 1024
	pristine, err := json.Marshal(val)
	if err != nil {
		t.Fatalf("marshal pristine: %v", err)
	}
	if int64(len(pristine)) > maxBytes {
		t.Fatalf("fixture bug: pristine body (%d bytes) already exceeds the test's own MaxBytes (%d)", len(pristine), maxBytes)
	}

	w := newTestWalker(Options{Seed: 1, MaxBytes: maxBytes}, nil)
	w.req.Recipes = set

	out := w.applyRecipePostPass(val)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal post-pass result: %v", err)
	}
	if int64(len(b)) > maxBytes {
		t.Fatalf("post-pass result is %d bytes, over MaxBytes (%d): %d copy recipes must never multiply the body past the generator's own ceiling", len(b), maxBytes, nCopies)
	}
}

// TestDeepCloneValueChargesPostPassBudget proves deepCloneValue's own
// recursion spends from the SAME postPassBudget applyDeferred's does (round-
// 1 finding #6's other half): a clone that would run past the shared budget
// declines outright (ok=false) rather than running the clone to completion
// uncounted.
func TestDeepCloneValueChargesPostPassBudget(t *testing.T) {
	budget := &postPassBudget{nodes: maxPostPassNodes - 2}
	v := map[string]any{"a": 1.0, "b": 2.0, "c": 3.0} // 1 (map) + 3 (scalars) = 4 nodes, budget has 2 left
	if _, ok := deepCloneValue(v, budget); ok {
		t.Fatalf("clone should have declined once the shared postPassBudget ran out mid-clone")
	}
}

// TestDeepCloneValueWithinBudgetIsAnIndependentCopy is the budget-charging
// change's own non-regression: comfortably within budget, a clone must
// still be a real, alias-free copy — mutating the source must never be
// observable through the clone.
func TestDeepCloneValueWithinBudgetIsAnIndependentCopy(t *testing.T) {
	budget := &postPassBudget{}
	v := map[string]any{"a": 1.0, "nested": []any{1.0, 2.0}}

	got, ok := deepCloneValue(v, budget)
	if !ok {
		t.Fatalf("clone should succeed comfortably within budget")
	}

	v["a"] = 999.0
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("clone of a map[string]any came back as %T", got)
	}
	if m["a"] != 1.0 {
		t.Fatalf("clone aliased the source: mutating the source changed the clone's %v", m["a"])
	}
}
