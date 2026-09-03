package gen

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAllOfNarrowsAndUnionsRequiredAndProperties is DESIGN §9's central
// allOf claim made concrete: required unions across branches, properties
// merge, and a bound redeclared by TWO branches narrows (keeps the more
// restrictive value) rather than letting the later branch silently widen
// the earlier one's constraint.
func TestAllOfNarrowsAndUnionsRequiredAndProperties(t *testing.T) {
	schema := map[string]any{
		"allOf": []any{
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "minimum": json.Number("1"), "maximum": json.Number("100")},
				},
				"required": []any{"id"},
			},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"id":   map[string]any{"type": "integer", "minimum": json.Number("50")},
				},
				"required": []any{"name"},
			},
		},
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
		t.Fatalf("branch 1's required property %q missing from merged result", "id")
	}
	if _, ok := obj["name"]; !ok {
		t.Fatalf("branch 2's required property %q missing from merged result", "name")
	}
	id, ok := obj["id"].(int64)
	if !ok {
		t.Fatalf("id should generate as an integer, got %T", obj["id"])
	}
	if id < 50 || id > 100 {
		t.Fatalf("id=%d violates the NARROWED bound [50,100] (minimum must be the MAX of the two branches' minimums, 50, not branch 1's looser 1)", id)
	}
}

// TestAllOfUnsatisfiableMinMax is DESIGN's own named example: a minimum
// from one branch that exceeds a maximum from another must be reported as
// ErrUnsatisfiable, never silently generate a number anyway.
func TestAllOfUnsatisfiableMinMax(t *testing.T) {
	schema := map[string]any{
		"allOf": []any{
			map[string]any{"type": "integer", "minimum": json.Number("10")},
			map[string]any{"type": "integer", "maximum": json.Number("5")},
		},
	}
	w := newTestWalker(Options{Seed: 1}, nil)
	_, err := w.walkSchema(schema, "", 0)
	if !isUnsatisfiable(err) {
		t.Fatalf("expected ErrUnsatisfiable, got %v", err)
	}
}

// TestAllOfRequiredAgainstClosedAdditionalPropertiesIsUnsatisfiable is the
// task's second named unsatisfiable case: additionalProperties:false in
// ANY branch closes the result, so a required property no branch ever
// declares under "properties" can never legally appear — a schema error,
// not a value to fabricate anyway.
func TestAllOfRequiredAgainstClosedAdditionalPropertiesIsUnsatisfiable(t *testing.T) {
	schema := map[string]any{
		"allOf": []any{
			map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"a": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
			map[string]any{
				"type":     "object",
				"required": []any{"b"}, // "b" is never declared under "properties" anywhere
			},
		},
	}
	w := newTestWalker(Options{Seed: 1}, nil)
	_, err := w.walkSchema(schema, "", 0)
	if !isUnsatisfiable(err) {
		t.Fatalf("expected ErrUnsatisfiable (required %q + additionalProperties:false closing the object without ever declaring %q), got %v", "b", "b", err)
	}
}

// TestAllOfBranchesResolveThroughRef proves branches are resolved lazily
// through the resolver: allOf routinely mixes a $ref to a shared base
// schema with an inline extension, and BOTH sides' required properties
// must land in the merged result.
func TestAllOfBranchesResolveThroughRef(t *testing.T) {
	res := &fakeResolver{byPointer: map[string]any{
		"#/Base": map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "integer"}},
			"required":   []any{"id"},
		},
	}}
	schema := map[string]any{
		"allOf": []any{
			map[string]any{"$ref": "#/Base"},
			map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"required":   []any{"name"},
			},
		},
	}
	w := newTestWalker(Options{Seed: 1}, res)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj := val.(map[string]any)
	if _, ok := obj["id"]; !ok {
		t.Fatalf("$ref'd branch's required property %q missing: %#v", "id", obj)
	}
	if _, ok := obj["name"]; !ok {
		t.Fatalf("inline branch's required property %q missing: %#v", "name", obj)
	}
}

// TestAllOfFlattensNestedAllOf covers a branch that is itself an allOf
// (allOf-of-allOf, a real pattern when specs layer several $ref'd mixins):
// mergeAllOf must flatten it rather than leaving a raw, unprocessed "allOf"
// key sitting inside the merged result.
func TestAllOfFlattensNestedAllOf(t *testing.T) {
	schema := map[string]any{
		"allOf": []any{
			map[string]any{
				"allOf": []any{
					map[string]any{
						"type":       "object",
						"properties": map[string]any{"a": map[string]any{"type": "string"}},
						"required":   []any{"a"},
					},
				},
			},
			map[string]any{
				"type":       "object",
				"properties": map[string]any{"b": map[string]any{"type": "string"}},
				"required":   []any{"b"},
			},
		},
	}
	w := newTestWalker(Options{Seed: 1}, nil)
	val, err := w.walkSchema(schema, "", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj := val.(map[string]any)
	if _, ok := obj["a"]; !ok {
		t.Fatalf("nested allOf branch's property %q missing: %#v", "a", obj)
	}
	if _, ok := obj["b"]; !ok {
		t.Fatalf("outer branch's property %q missing: %#v", "b", obj)
	}
}

// disjointPetSchema is a minimal, genuinely disjoint two-branch oneOf: each
// branch requires a field the other doesn't have and closes itself with
// additionalProperties:false, so a value can satisfy at most one of them.
func disjointPetSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"cat_name": map[string]any{"type": "string"}},
				"required":             []any{"cat_name"},
				"additionalProperties": false,
			},
			map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"dog_name": map[string]any{"type": "string"}},
				"required":             []any{"dog_name"},
				"additionalProperties": false,
			},
		},
	}
}

// --- P1b round-1 review, finding 1: mergeType's nullable-branch handling ---

// TestMergeTypeNullOnlyBranchIsExclusiveOfOtherType covers case 1 of the
// finding: a branch normalized to type EXACTLY ["null"] (normalizeNullable's
// own output for an OAS 3.0 schema with no other type) allOf'd with a plain
// non-nullable branch must NOT silently pass the other branch's full type
// through — a value cannot be both exclusively null and a string, so the
// only correct outcomes are ErrUnsatisfiable (nothing can be both) or "null"
// alone; the pre-fix code returned bare "string", dropping the null-only
// branch's constraint entirely.
func TestMergeTypeNullOnlyBranchIsExclusiveOfOtherType(t *testing.T) {
	got, err := mergeType([]any{"null"}, "string")
	if err == nil {
		t.Fatalf(`mergeType(["null"], "string") = %v, want ErrUnsatisfiable (an exclusively-null branch and a non-nullable string branch admit no common value)`, got)
	}
	if !isUnsatisfiable(err) {
		t.Fatalf("mergeType([\"null\"], \"string\"): got err %v, want ErrUnsatisfiable", err)
	}
}

// TestMergeTypeBothNullableDisjointTypesNarrowsToNull covers case 2: two
// branches each independently nullable (OAS 3.0 nullable:true normalizes to
// type:[T,"null"]) with DIFFERENT non-null types. The only value both
// branches actually admit is null itself — the pre-fix code hit the
// non-null intersection's own empty-result ErrUnsatisfiable before the
// aNull&&bNull check ever got a chance to save the merge.
func TestMergeTypeBothNullableDisjointTypesNarrowsToNull(t *testing.T) {
	got, err := mergeType([]any{"string", "null"}, []any{"integer", "null"})
	if err != nil {
		t.Fatalf(`mergeType(["string","null"], ["integer","null"]): unexpected error %v, want type "null"`, err)
	}
	if got != "null" {
		t.Fatalf(`mergeType(["string","null"], ["integer","null"]) = %v, want "null"`, got)
	}
}

// TestMergeTypeOverlappingNonNullIntersects and
// TestMergeTypeDisjointNonNullIsUnsatisfiable pin the ORDINARY (non-null)
// cases the fix must not have disturbed.
func TestMergeTypeOverlappingNonNullIntersects(t *testing.T) {
	got, err := mergeType([]any{"string", "integer"}, []any{"integer", "boolean"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "integer" {
		t.Fatalf(`mergeType(["string","integer"], ["integer","boolean"]) = %v, want "integer"`, got)
	}
}

func TestMergeTypeDisjointNonNullIsUnsatisfiable(t *testing.T) {
	if _, err := mergeType("string", "integer"); !isUnsatisfiable(err) {
		t.Fatalf(`mergeType("string", "integer"): got %v, want ErrUnsatisfiable`, err)
	}
}

// --- P1b round-1 review, finding 3: mergeAllOf is memoized per Generator ---

// refCountingResolver wraps fakeResolver, counting ResolveNode calls made
// for a SPECIFIC $ref pointer — used to observe whether mergeAllOf actually
// re-resolves an allOf's branches on a second call, or serves the memoized
// result instead.
type refCountingResolver struct {
	*fakeResolver
	calls map[string]int
}

func (r *refCountingResolver) ResolveNode(node any) (any, error) {
	if m, ok := node.(map[string]any); ok {
		if ref, ok := m["$ref"].(string); ok {
			r.calls[ref]++
		}
	}
	return r.fakeResolver.ResolveNode(node)
}

func TestMergeAllOfIsMemoizedAcrossWalkersSharingAGenerator(t *testing.T) {
	res := &refCountingResolver{
		fakeResolver: &fakeResolver{byPointer: map[string]any{
			"#/A": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []any{"a"}},
			"#/B": map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string"}}, "required": []any{"b"}},
		}},
		calls: map[string]int{},
	}
	schema := map[string]any{
		"allOf": []any{
			map[string]any{"$ref": "#/A"},
			map[string]any{"$ref": "#/B"},
		},
	}

	// mergeCache is shared across walkers the same way Generator.newWalker
	// shares one across every request — see gen.go.
	mc := &mergeCache{}

	w1 := newTestWalker(Options{Seed: 1}, res)
	w1.mergeCache = mc
	if _, err := w1.walkSchema(schema, "", 0); err != nil {
		t.Fatalf("first walk: %v", err)
	}
	if res.calls["#/A"] == 0 || res.calls["#/B"] == 0 {
		t.Fatalf("first walk should have resolved both allOf branches at least once, got calls=%v", res.calls)
	}

	res.calls = map[string]int{}
	w2 := newTestWalker(Options{Seed: 2}, res) // a DIFFERENT request/seed, same document
	w2.mergeCache = mc
	if _, err := w2.walkSchema(schema, "", 0); err != nil {
		t.Fatalf("second walk: %v", err)
	}
	if res.calls["#/A"] != 0 || res.calls["#/B"] != 0 {
		t.Fatalf("second walk over the IDENTICAL allOf schema re-resolved its branches (calls=%v), want 0 — mergeAllOf's result should have come from the memo (P1b round-1 review, finding 3)", res.calls)
	}
}

// --- P1b round-1 review, finding 8: allOf property-collision merge is work-bounded ---

// TestMergeAllOfSelfReferencingCollisionTerminatesQuickly is the DoS
// reproduction: a schema that references itself through TWO colliding
// property keys under allOf fans out multiplicatively with depth alone
// bounding it (2^maxMergeDepth mergeSchemaPair calls). Before the fix this
// hung indefinitely; the fix must make it return — either a body or
// ErrUnsatisfiable, either is fine — within a small, fixed time budget.
func TestMergeAllOfSelfReferencingCollisionTerminatesQuickly(t *testing.T) {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"schemas": map[string]any{
			"Big": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"p0": map[string]any{"$ref": "#/components/schemas/Big"},
					"p1": map[string]any{"$ref": "#/components/schemas/Big"},
				},
			},
		},
	}
	doc["paths"] = map[string]any{
		"/x": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"allOf": []any{
										map[string]any{"$ref": "#/components/schemas/Big"},
										map[string]any{"$ref": "#/components/schemas/Big"},
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
	g := New(res, Options{Seed: 1})
	v := ResponseVariant{
		Selector: "200", MediaType: "application/json",
		SchemaPtr: "#/paths/~1x/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1x/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/x", Status: 200}

	done := make(chan error, 1)
	go func() {
		_, err := g.Body(v, req)
		done <- err
	}()

	select {
	case <-done:
		// Either a generated body or ErrUnsatisfiable is an acceptable,
		// bounded outcome — what matters is that it returns at all. The
		// actual work is bounded (maxWalkNodes) and completes in low
		// single-digit seconds run in isolation; 30s leaves generous
		// headroom for `go test ./... -race` running every OTHER
		// package's test binary at the same time under -race's own
		// instrumentation overhead, without weakening what this test
		// actually checks (a regression here hangs indefinitely, not for
		// a few extra seconds).
	case <-time.After(30 * time.Second):
		t.Fatal("self-referencing allOf property collision did not terminate within 30s — mergeSchemaPair's work budget regressed (P1b round-1 review, finding 8)")
	}
}

// TestOneOfSatisfiesExactlyOneDisjointBranch is DESIGN's oneOf exclusivity
// requirement: the generated value must satisfy EXACTLY one of the two
// branches, and generation must be deterministic (same seed, same
// dataPath -> byte-identical body).
func TestOneOfSatisfiesExactlyOneDisjointBranch(t *testing.T) {
	schema := disjointPetSchema()

	w := newTestWalker(Options{Seed: 1}, nil)
	val, err := w.walkSchema(schema, "pet", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", val)
	}
	_, hasCat := obj["cat_name"]
	_, hasDog := obj["dog_name"]
	if hasCat == hasDog {
		t.Fatalf("expected EXACTLY one of the two disjoint oneOf branches, got cat=%v dog=%v: %#v", hasCat, hasDog, obj)
	}

	w2 := newTestWalker(Options{Seed: 1}, nil)
	val2, err := w2.walkSchema(schema, "pet", 0)
	if err != nil {
		t.Fatalf("walkSchema (2): %v", err)
	}
	b1, _ := json.Marshal(val)
	b2, _ := json.Marshal(val2)
	if string(b1) != string(b2) {
		t.Fatalf("oneOf branch selection is not deterministic across identical calls:\n%s\n%s", b1, b2)
	}
}

// TestOneOfBranchSelectionVariesByDataPath proves branch choice is
// actually hash-driven rather than always landing on branch 0 (a
// composeOneOf that ignored dataPath entirely would still pass the
// single-path exclusivity test above).
func TestOneOfBranchSelectionVariesByDataPath(t *testing.T) {
	schema := disjointPetSchema()
	w := newTestWalker(Options{Seed: 1}, nil)

	sawCat, sawDog := false, false
	for i := 0; i < 40 && (!sawCat || !sawDog); i++ {
		val, err := w.walkSchema(schema, itoaPath(i), 0)
		if err != nil {
			t.Fatalf("walkSchema: %v", err)
		}
		obj := val.(map[string]any)
		if _, ok := obj["cat_name"]; ok {
			sawCat = true
		}
		if _, ok := obj["dog_name"]; ok {
			sawDog = true
		}
	}
	if !sawCat || !sawDog {
		t.Fatalf("expected both oneOf branches to be reachable across different data paths, sawCat=%v sawDog=%v", sawCat, sawDog)
	}
}

// TestDiscriminatorPicksBranchViaMappingAndStampsProperty is the task's
// blocker-grade requirement made explicit: the discriminator property
// itself must always be set, to the value that selected the branch, and
// that branch is chosen through "mapping" (name -> $ref).
func TestDiscriminatorPicksBranchViaMappingAndStampsProperty(t *testing.T) {
	res := &fakeResolver{byPointer: map[string]any{
		"#/components/schemas/Cat": map[string]any{
			"type":       "object",
			"properties": map[string]any{"meow": map[string]any{"type": "boolean"}},
		},
		"#/components/schemas/Dog": map[string]any{
			"type":       "object",
			"properties": map[string]any{"bark": map[string]any{"type": "boolean"}},
		},
	}}
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"petType": map[string]any{"type": "string"}},
		"discriminator": map[string]any{
			"propertyName": "petType",
			"mapping": map[string]any{
				"cat": "#/components/schemas/Cat",
				"dog": "#/components/schemas/Dog",
			},
		},
		"oneOf": []any{
			map[string]any{"$ref": "#/components/schemas/Cat"},
			map[string]any{"$ref": "#/components/schemas/Dog"},
		},
	}
	w := newTestWalker(Options{Seed: 1}, res)
	val, err := w.walkSchema(schema, "pet", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", val)
	}

	petType, ok := obj["petType"].(string)
	if !ok || petType == "" {
		t.Fatalf("discriminator property %q must always be set to a non-empty value, got %#v", "petType", obj["petType"])
	}
	if petType != "cat" && petType != "dog" {
		t.Fatalf("petType must be one of the mapping's own keys (%q or %q), got %q", "cat", "dog", petType)
	}

	_, hasMeow := obj["meow"]
	_, hasBark := obj["bark"]
	switch petType {
	case "cat":
		if !hasMeow {
			t.Fatalf("petType=%q but the Cat branch's own field %q is missing: %#v", petType, "meow", obj)
		}
	case "dog":
		if !hasBark {
			t.Fatalf("petType=%q but the Dog branch's own field %q is missing: %#v", petType, "bark", obj)
		}
	}
}

// TestDiscriminatorFallsBackToSchemaNameWithoutMapping covers OAS's
// legal-but-lazy spec shape: a discriminator with a propertyName but no
// "mapping" at all, which DESIGN/the task say must fall back to the
// referenced schema's own component name ("Cat", from
// "#/components/schemas/Cat").
func TestDiscriminatorFallsBackToSchemaNameWithoutMapping(t *testing.T) {
	res := &fakeResolver{byPointer: map[string]any{
		"#/components/schemas/Cat": map[string]any{
			"type":       "object",
			"properties": map[string]any{"meow": map[string]any{"type": "boolean"}},
		},
	}}
	schema := map[string]any{
		"discriminator": map[string]any{"propertyName": "petType"}, // no mapping
		"oneOf": []any{
			map[string]any{"$ref": "#/components/schemas/Cat"},
		},
	}
	w := newTestWalker(Options{Seed: 1}, res)
	val, err := w.walkSchema(schema, "pet", 0)
	if err != nil {
		t.Fatalf("walkSchema: %v", err)
	}
	obj := val.(map[string]any)
	if obj["petType"] != "Cat" {
		t.Fatalf("expected discriminator fallback to the referenced schema's own component name %q, got %#v", "Cat", obj["petType"])
	}
}

// TestBodyAllOfComposedSchemaEndToEnd exercises allOf through the actual
// entry point (Generator.Body over a real *openapi.Resolver), not just
// walkSchema directly — allOf is the composition keyword measured on the
// real acceptance document's hot path (11 uses), so it must work through
// the full pipeline, $ref included, not only in isolation.
func TestBodyAllOfComposedSchemaEndToEnd(t *testing.T) {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"schemas": map[string]any{
			"Base": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "integer"}},
				"required":   []any{"id"},
			},
		},
	}
	doc["paths"] = map[string]any{
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"allOf": []any{
										map[string]any{"$ref": "#/components/schemas/Base"},
										map[string]any{
											"type":       "object",
											"properties": map[string]any{"name": map[string]any{"type": "string"}},
											"required":   []any{"name"},
										},
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
		t.Fatalf("Body did not produce valid JSON: %v\nbody: %s", err, b)
	}
	if _, ok := got["id"]; !ok {
		t.Fatalf("missing %q from the $ref'd allOf branch: %s", "id", b)
	}
	if _, ok := got["name"]; !ok {
		t.Fatalf("missing %q from the inline allOf branch: %s", "name", b)
	}
}
