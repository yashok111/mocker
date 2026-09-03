package gen

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/recipes"
)

// fakeResolver is a minimal stand-in for *openapi.Resolver, letting these
// tests exercise walker/walkSchema directly against hand-built schema maps
// without constructing a full openapi.Document. It implements exactly the
// resolver interface schema.go declares.
type fakeResolver struct {
	// byPointer answers Resolve("#/...") calls; used only by tests that
	// need $ref-following (the self-reference termination test).
	byPointer map[string]any
}

func (f *fakeResolver) Resolve(pointer string) (any, error) {
	if f.byPointer == nil {
		return nil, errNotFound(pointer)
	}
	v, ok := f.byPointer[pointer]
	if !ok {
		return nil, errNotFound(pointer)
	}
	return f.resolveRefChain(v)
}

func (f *fakeResolver) ResolveNode(node any) (any, error) {
	return f.resolveRefChain(node)
}

// resolveRefChain follows {"$ref": "#/..."} chains through byPointer, the
// same iterative (non-recursive-call) shape the real resolver uses.
func (f *fakeResolver) resolveRefChain(node any) (any, error) {
	for {
		m, ok := node.(map[string]any)
		if !ok {
			return node, nil
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return node, nil
		}
		next, ok := f.byPointer[ref]
		if !ok {
			return nil, errNotFound(ref)
		}
		node = next
	}
}

type notFoundError string

func (e notFoundError) Error() string  { return "not found: " + string(e) }
func errNotFound(pointer string) error { return notFoundError(pointer) }

func newTestWalker(opts Options, res resolver) *walker {
	if res == nil {
		res = &fakeResolver{}
	}
	now := opts.clock()()
	return &walker{
		res:      res,
		opts:     opts,
		req:      Request{Method: "GET", CanonicalPath: "/x", Status: 200},
		seedList: SeedList(opts, Request{Method: "GET", CanonicalPath: "/x", Status: 200}),
		now:      now,
		budget:   &walkBudget{remaining: opts.effMaxBytes()},
	}
}

// TestSchemaType exercises the newly-exported reader directly, including
// the three structural fallbacks (properties/items/additionalProperties)
// that fire only when "type" itself is absent, and the final untyped-leaf
// default of "string" — the exact case [CountValue]'s own doc comment
// warns must NOT be confused with countType "" (an unresolved node, which
// SchemaType is never even called on).
func TestSchemaType(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"declared type wins", map[string]any{"type": "integer"}, "integer"},
		{"union skips null", map[string]any{"type": []any{"null", "string"}}, "string"},
		{"properties implies object", map[string]any{"properties": map[string]any{}}, "object"},
		{"items implies array", map[string]any{"items": map[string]any{}}, "array"},
		{"map-shaped additionalProperties implies object", map[string]any{"additionalProperties": map[string]any{}}, "object"},
		{"bool additionalProperties does not imply object", map[string]any{"additionalProperties": true}, "string"},
		{"truly untyped leaf falls back to string", map[string]any{}, "string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SchemaType(tc.schema); got != tc.want {
				t.Errorf("SchemaType(%v) = %q, want %q", tc.schema, got, tc.want)
			}
		})
	}
}

func TestWalkObjectRequiredAndOptional(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "integer"},
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"id"},
	}
	w := newTestWalker(Options{Seed: 1}, nil)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", val)
	}
	if _, ok := obj["id"]; !ok {
		t.Fatalf("required property %q missing", "id")
	}
	if _, ok := obj["name"]; !ok {
		t.Fatalf("optional property %q should be populated in normal (non-boundary) generation", "name")
	}
}

func TestWalkObjectOmitsWriteOnlyEvenIfRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"password": map[string]any{"type": "string", "writeOnly": true},
			"email":    map[string]any{"type": "string"},
		},
		"required": []any{"password", "email"}, // spec bug: writeOnly + required together
	}
	w := newTestWalker(Options{Seed: 1}, nil)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj := val.(map[string]any)
	if _, present := obj["password"]; present {
		t.Fatalf("writeOnly property must be omitted from a response body, even when required")
	}
	if _, present := obj["email"]; !present {
		t.Fatalf("non-writeOnly required property must still be present")
	}
}

func TestWalkArrayLengthFromListSize(t *testing.T) {
	schema := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
	w := newTestWalker(Options{Seed: 1, ListSize: 7}, nil)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	arr, ok := val.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", val)
	}
	if len(arr) != 7 {
		t.Fatalf("expected length 7 (Options.ListSize), got %d", len(arr))
	}
}

func TestWalkArrayRespectsMinMaxItems(t *testing.T) {
	schema := map[string]any{
		"type":     "array",
		"items":    map[string]any{"type": "string"},
		"minItems": json.Number("2"),
		"maxItems": json.Number("3"),
	}
	w := newTestWalker(Options{Seed: 1, ListSize: 20}, nil) // ListSize would overshoot maxItems
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	arr := val.([]any)
	if len(arr) < 2 || len(arr) > 3 {
		t.Fatalf("expected length within [2,3], got %d", len(arr))
	}
}

func TestDeterminismSameRequestSameBody(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    map[string]any{"type": "integer"},
			"name":  map[string]any{"type": "string"},
			"score": map[string]any{"type": "number"},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []any{"id", "name"},
	}
	opts := Options{Seed: 99, ListSize: 3}
	w1 := newTestWalker(opts, nil)
	w2 := newTestWalker(opts, nil)

	v1, err := w1.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema (1): %v", err)
	}
	v2, err := w2.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema (2): %v", err)
	}

	b1, _ := json.Marshal(v1)
	b2, _ := json.Marshal(v2)
	if string(b1) != string(b2) {
		t.Fatalf("two identical requests produced different bodies:\n%s\n%s", b1, b2)
	}
}

func TestNullableForcedOnlyWhenTypeExactlyNull(t *testing.T) {
	// type exactly ["null"] -> always null
	forced := map[string]any{"type": []any{"null"}}
	w := newTestWalker(Options{Seed: 1, NullRate: 0}, nil)
	v, err := w.walkSchema(forced, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	if v != nil {
		t.Fatalf("type exactly [\"null\"] must always generate null, got %v", v)
	}

	// type ["string","null"] with NullRate 0 -> never null (not forced)
	union := map[string]any{"type": []any{"string", "null"}}
	v2, err := w.walkSchema(union, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	if v2 == nil {
		t.Fatalf(`type ["string","null"] with NullRate 0 must not be forced null`)
	}
}

func TestNullableDefaultNullForcesNull(t *testing.T) {
	schema := map[string]any{"type": "string", "default": nil}
	w := newTestWalker(Options{Seed: 1}, nil)
	v, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	if v != nil {
		t.Fatalf(`schema with "default": null must generate null, got %v`, v)
	}
}

// TestRecipeBeatsDefaultNull is round-1 finding #1, pinned directly: a
// schema shaped exactly like TestNullableDefaultNullForcesNull's own
// ("type":"string","default":null — a very ordinary nullable-field spec)
// must still let a bound const recipe win once one IS bound. Before the
// fix, walkSchema forced null unconditionally on isDefaultNull, before
// leafValue (and therefore recipeValue) ever ran — so the recipe below
// would have been silently ignored and this would have observed nil.
func TestRecipeBeatsDefaultNull(t *testing.T) {
	schema := map[string]any{"type": "string", "default": nil}
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"": {Kind: recipes.KindConst, Data: json.RawMessage(`"from-recipe"`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	w := newTestWalker(Options{Seed: 1}, nil)
	w.req.Recipes = set

	v, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	if v != "from-recipe" {
		t.Fatalf(`a const recipe bound to a "default": null field must win, got %v`, v)
	}
}

func TestNullRateAllOrNothing(t *testing.T) {
	schema := map[string]any{"type": []any{"string", "null"}}

	// NullRate 0: never null, across many different dataPaths (proxy for
	// many different fields/requests).
	w0 := newTestWalker(Options{Seed: 1, NullRate: 0}, nil)
	for i := range 50 {
		v, err := w0.walkSchema(schema, itoaPath(i), 0)
		if err != nil {
			t.Fatalf("walkSchema: %v", err)
		}
		if v == nil {
			t.Fatalf("NullRate 0 produced null at path %d", i)
		}
	}

	// NullRate 1: always null.
	w1 := newTestWalker(Options{Seed: 1, NullRate: 1}, nil)
	for i := range 50 {
		v, err := w1.walkSchema(schema, itoaPath(i), 0)
		if err != nil {
			t.Fatalf("walkSchema: %v", err)
		}
		if v != nil {
			t.Fatalf("NullRate 1 produced non-null (%v) at path %d", v, i)
		}
	}
}

func itoaPath(i int) string {
	return "field" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

func TestUnsatisfiableMinMaxIsError(t *testing.T) {
	schema := map[string]any{
		"type":    "integer",
		"minimum": json.Number("10"),
		"maximum": json.Number("5"),
	}
	w := newTestWalker(Options{Seed: 1}, nil)
	_, err := w.walkSchema(schema, "", 0)
	if err == nil {
		t.Fatalf("expected ErrUnsatisfiable, got nil")
	}
	if !isUnsatisfiable(err) {
		t.Fatalf("expected ErrUnsatisfiable, got %v", err)
	}
}

func isUnsatisfiable(err error) bool {
	for err != nil {
		if errors.Is(err, ErrUnsatisfiable) {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestMaxDepthOmitsOptionalKeepsRequiredMinimal(t *testing.T) {
	// A shallow, NON-recursive schema, but with an artificially tiny
	// MaxDepth so the object's own properties sit past the ceiling.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "integer", "minimum": json.Number("5")},
			"optional": map[string]any{"type": "string"},
		},
		"required": []any{"id"},
	}
	w := newTestWalker(Options{Seed: 1, MaxDepth: 1}, nil)
	// depth starts at 1: childDepth (2) > MaxDepth (1) immediately.
	val, err := w.walkSchema(schema, "", 1)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj := val.(map[string]any)
	if _, present := obj["optional"]; present {
		t.Fatalf("optional property must be omitted at the depth ceiling")
	}
	id, present := obj["id"]
	if !present {
		t.Fatalf("required property must still be present at the depth ceiling")
	}
	if n, ok := id.(int64); !ok || n < 5 {
		t.Fatalf("required property at the depth ceiling must be a legal (schema-satisfying) minimal value, got %v", id)
	}
}

// TestRequiredObjectReachedViaArrayAtDepthCeilingKeepsOwnRequiredFields is
// the P1b round-1 review's finding 5: a REQUIRED object-typed schema node
// reached through an array item's own walkSchema ceiling check (as opposed
// to walkObject's per-property ceiling branch, which already handled this
// correctly) used to collapse to bare {} — satisfying its own TYPE but not
// its own "required" list, failing schema validation outright for exactly
// the shape a self-referencing or deeply nested corpus produces (Position
// several hops below an array of arrays, in the task's own real-corpus
// reproduction).
func TestRequiredObjectReachedViaArrayAtDepthCeilingKeepsOwnRequiredFields(t *testing.T) {
	itemSchema := map[string]any{
		"type":     "object",
		"required": []any{"code", "name"},
		"properties": map[string]any{
			"code": map[string]any{"type": "string"},
			"name": map[string]any{"type": "string"},
		},
	}
	schema := map[string]any{
		"type":  "array",
		"items": itemSchema,
	}
	// depth=1, MaxDepth=1: walkArray itself runs at depth 1 (<=1, fine),
	// but each ITEM is walked at depth+1=2, which exceeds MaxDepth(1) —
	// walkSchema's OWN ceiling check fires for the item, never reaching
	// walkObject's per-property handling at all.
	w := newTestWalker(Options{Seed: 1, MaxDepth: 1, ListSize: 2}, nil)
	val, err := w.walkSchema(schema, "", 1)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	arr, ok := val.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("expected a non-empty array, got %#v", val)
	}
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item %d: expected object, got %T", i, item)
		}
		if _, present := obj["code"]; !present {
			t.Fatalf("item %d: required property %q missing at the depth ceiling: %#v", i, "code", obj)
		}
		if _, present := obj["name"]; !present {
			t.Fatalf("item %d: required property %q missing at the depth ceiling: %#v", i, "name", obj)
		}
	}
}

// TestCompositionAtDepthCeilingUnwrapsToBranchType is finding 5's sibling
// defect, found verifying it against the real corpus: a oneOf/allOf node
// caught AT the depth/node ceiling has no "type"/"properties"/"items" of
// its OWN, so SchemaType's untyped-leaf default ("string") used to apply —
// generating a plain string filler for a field whose schema is really a
// oneOf of objects, failing validation against every branch at once.
func TestCompositionAtDepthCeilingUnwrapsToBranchType(t *testing.T) {
	branchA := map[string]any{
		"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	}
	branchB := map[string]any{
		"type": "object", "required": []any{"b"},
		"properties": map[string]any{"b": map[string]any{"type": "string"}},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item": map[string]any{"oneOf": []any{branchA, branchB}},
		},
		"required": []any{"item"},
	}
	w := newTestWalker(Options{Seed: 1, MaxDepth: 1}, nil)
	val, err := w.walkSchema(schema, "", 1)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj := val.(map[string]any)
	item, ok := obj["item"].(map[string]any)
	if !ok {
		t.Fatalf(`"item" at the ceiling = %#v (%T), want an object (one of the oneOf branches), not a string fallback`, obj["item"], obj["item"])
	}
	_, hasA := item["a"]
	_, hasB := item["b"]
	if !hasA && !hasB {
		t.Fatalf(`"item" at the ceiling = %#v, want at least one branch's own required property present`, item)
	}
}

// TestSelfReferencingArrayTerminates is the required stress test: a schema
// that references itself through an array of itself must return, not hang.
// It uses its own timeout (not test-runner patience) so a regression that
// reintroduces unbounded recursion fails fast and loud instead of wedging
// the whole `go test ./...` run.
func TestSelfReferencingArrayTerminates(t *testing.T) {
	res := &fakeResolver{byPointer: map[string]any{}}
	nodeSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"children": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/Node"},
			},
		},
	}
	res.byPointer["#/Node"] = nodeSchema

	// A small byte budget keeps the test itself fast (see the walkArray
	// pre-check in schema.go: array iteration bails out BEFORE recursing
	// once the shared budget is exhausted, not only after).
	opts := Options{Seed: 1, ListSize: 5, MaxDepth: 6, MaxBytes: 4000}
	w := newTestWalker(opts, res)

	done := make(chan struct {
		val any
		err error
	}, 1)
	go func() {
		v, err := w.walkNode(map[string]any{"$ref": "#/Node"}, "", 0)
		done <- struct {
			val any
			err error
		}{v, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("walkNode on self-referencing schema returned an error: %v", r.err)
		}
		b, err := json.Marshal(r.val)
		if err != nil {
			t.Fatalf("result is not valid JSON: %v", err)
		}
		if len(b) == 0 {
			t.Fatalf("expected a non-empty result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("self-referencing schema did not terminate within 5s — recursion/budget guard regressed")
	}
}

func TestEmptySchemaPointerReturnsNoBodyNoError(t *testing.T) {
	g := New(nil, Options{})
	// Body checks SchemaPtr == "" before ever touching the resolver, so a
	// nil *openapi.Resolver is safe here.
	b, err := g.Body(ResponseVariant{SchemaPtr: ""}, Request{Method: "GET", CanonicalPath: "/x", Status: 204})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil body, got %q", b)
	}
}

// --- P1b round-1 review, finding 9: arrayLength hard-caps a hostile minItems ---

func TestArrayLengthCapsHostileMinItems(t *testing.T) {
	schema := map[string]any{
		"type": "array", "items": map[string]any{"type": "integer"},
		"minItems": json.Number("5000000"),
	}
	w := newTestWalker(Options{Seed: 1, ListSize: 20}, nil)
	n := w.arrayLength(schema, "")
	if n > maxGenArrayLength {
		t.Fatalf("arrayLength(minItems=5000000) = %d, want capped at maxGenArrayLength (%d)", n, maxGenArrayLength)
	}
}

// TestWalkArrayHostileMinItemsDoesNotOverAllocate is the end-to-end
// reproduction: a 500,000,000-minItems array must not make walkArray
// reserve hundreds of millions of slice slots (an OOM in the making) or
// generate anywhere near that many items — it terminates well short of its
// own minItems instead, letting the byte/node budgets cut it down like any
// other array (a deliberate trade-off: see arrayLength's own doc comment).
func TestWalkArrayHostileMinItemsDoesNotOverAllocate(t *testing.T) {
	schema := map[string]any{
		"type": "array", "items": map[string]any{"type": "integer"},
		"minItems": json.Number("500000000"),
	}
	w := newTestWalker(Options{Seed: 1, ListSize: 20, MaxBytes: 4096}, nil)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	arr, ok := val.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", val)
	}
	if len(arr) > maxGenArrayLength {
		t.Fatalf("array with a hostile minItems=500000000 generated %d items, want capped well under maxGenArrayLength (%d)", len(arr), maxGenArrayLength)
	}
}

// --- P1b round-1 review, findings 10/11: string length bounds are clamped ---

func TestStringLengthBoundsFloorsNegativeMinLength(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("-5")}
	lo, hi, err := stringLengthBounds(schema)
	if err != nil {
		t.Fatalf("stringLengthBounds: %v", err)
	}
	if lo < 0 {
		t.Fatalf("stringLengthBounds(minLength=-5) floored to lo=%d, want >= 0", lo)
	}
	if hi < lo {
		t.Fatalf("stringLengthBounds(minLength=-5): hi=%d < lo=%d", hi, lo)
	}
}

// TestGenStringNegativeMinLengthDoesNotPanic reproduces finding 11 directly:
// before the floor, seed%22 landing under 5 made genString's computed
// length negative, and make([]byte, negative) panics. Sweeping seeds
// catches the case deterministically rather than relying on luck.
func TestGenStringNegativeMinLengthDoesNotPanic(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("-5")}
	for seed := range int64(30) {
		w := newTestWalker(Options{Seed: seed}, nil)
		if _, err := w.walkSchema(schema, "", 0); err != nil {
			t.Fatalf("seed %d: walkSchema: %v", seed, err)
		}
	}
}

func TestStringLengthBoundsCapsHostileMinLength(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("10000000")}
	lo, hi, err := stringLengthBounds(schema)
	if err != nil {
		t.Fatalf("stringLengthBounds: %v", err)
	}
	if lo > maxGenStringLength || hi > maxGenStringLength {
		t.Fatalf("stringLengthBounds(minLength=1e7): lo=%d hi=%d, want both capped at maxGenStringLength (%d)", lo, hi, maxGenStringLength)
	}
}

// TestWalkSchemaHostileMinLengthDoesNotBlowBudget is the end-to-end
// reproduction: a single field with a many-megabyte minLength must not
// generate anywhere near that much data regardless of Options.MaxBytes.
func TestWalkSchemaHostileMinLengthDoesNotBlowBudget(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("10000000")}
	w := newTestWalker(Options{Seed: 1, MaxBytes: 4096}, nil)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("expected string, got %T", val)
	}
	if len(s) > maxGenStringLength {
		t.Fatalf("string with minLength=1e7 generated %d bytes, want capped at maxGenStringLength (%d)", len(s), maxGenStringLength)
	}
}

// --- P1b round-1 review, finding 12: integer span never overflows to zero ---

func TestIntegerSpanFullRangeDoesNotOverflowToZero(t *testing.T) {
	span := integerSpan(math.MinInt64, math.MaxInt64)
	if span == 0 {
		t.Fatalf("integerSpan(MinInt64, MaxInt64) = 0, want nonzero (a zero span means seed%%span divides by zero)")
	}
}

func TestIntegerSpanOrdinaryRangeIsExact(t *testing.T) {
	if got := integerSpan(5, 10); got != 6 {
		t.Fatalf("integerSpan(5, 10) = %d, want 6 (values 5..10 inclusive)", got)
	}
	if got := integerSpan(-3, 3); got != 7 {
		t.Fatalf("integerSpan(-3, 3) = %d, want 7", got)
	}
}

// TestGenIntegerHostileFullRangeBoundsDoesNotPanic reproduces finding 12
// end-to-end: minimum pushed to exactly math.MinInt64 and exclusiveMaximum
// to one past math.MaxInt64 (a float64 value that IS exactly
// representable, though its int64 conversion overflows) used to divide by
// a span that silently wrapped to zero.
func TestGenIntegerHostileFullRangeBoundsDoesNotPanic(t *testing.T) {
	schema := map[string]any{
		"type":             "integer",
		"minimum":          json.Number("-9223372036854775808"),
		"exclusiveMaximum": json.Number("9223372036854775808"),
	}
	for seed := range int64(10) {
		w := newTestWalker(Options{Seed: seed}, nil)
		if _, err := w.walkSchema(schema, "", 0); err != nil {
			t.Fatalf("seed %d: walkSchema: %v", seed, err)
		}
	}
}

// TestGeneratedIntegerHostileFullRangeBoundsDoesNotPanic is the same
// reproduction against values.go's generatedInteger, which shares the
// identical span computation via a "uint"-formatted field (or an
// id/count-shaped field name) rather than schema.go's bare genInteger.
func TestGeneratedIntegerHostileFullRangeBoundsDoesNotPanic(t *testing.T) {
	schema := map[string]any{
		"type":             "integer",
		"format":           "uint",
		"minimum":          json.Number("-9223372036854775808"),
		"exclusiveMaximum": json.Number("9223372036854775808"),
	}
	for seed := range int64(10) {
		w := newTestWalker(Options{Seed: seed}, nil)
		if _, ok := generatedInteger(w, schema, "count", "uint", fieldKindCount); !ok {
			t.Fatalf("seed %d: generatedInteger declined for a format=uint field, want it to handle the bounds (even if degraded)", seed)
		}
	}
}
