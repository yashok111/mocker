// faker_test.go is C5's guard: D6 publishes twelve faker tokens
// (recipes.FakerTokens) and this package's faker.go wires each one to a
// producer (fakerProducers) that already exists elsewhere in this package.
// A test that only checked "the field is non-empty" would pass against a
// completely DISCONNECTED registry — every "faker" recipe declines,
// ordinary schema generation fills the field with its own perfectly
// non-empty string, and nothing would ever notice the wiring was never
// made. This file pins each token's OWN expected value instead, computed
// from the underlying producer (never from the registry entry under test,
// and never captured from a run of the recipe path — either would just
// re-validate whatever the code already does, including "declines").
//
// package gen, not internal/recipes: reaching Recipe.Value with a real
// Faker from internal/recipes would mean fabricating a stub producer and
// asserting the stub produces a value, which proves nothing about THIS
// package's registry — the one D6's seam actually joins to the published
// list.
package gen

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"testing"

	"github.com/yashok111/mocker/internal/recipes"
)

// fakerTokenCase pairs one published token with a NEUTRALLY named field
// (f1, f2, ...) and the expression D6's own table names as that token's
// expected value. Neutral names matter on their own: a token bound to a
// field literally called "email" would pass even with the registry
// disconnected, because classifyFieldName(names.go) would recognise the
// NAME and generatedString's own fieldKindEmail arm would produce a
// plausible email regardless of whether any recipe ever ran.
var fakerTokenCases = []struct {
	field string
	token string
	want  func(seed uint64) any
}{
	// person.fullName passes an EMPTY name to genPersonName — the one
	// producer among the twelve whose OWN arguments differ from
	// generatedString's call site (values.go): an empty name is what makes
	// it TOTAL under D6(2)'s "person.fullName stays" clause.
	{"f1", "person.fullName", func(seed uint64) any { return genPersonName(seed, "") }},
	{"f2", "internet.email", func(seed uint64) any { return genEmail(seed) }},
	{"f3", "phone.number", func(seed uint64) any { return genPhone(seed) }},
	{"f4", "datetime.timestamp", func(seed uint64) any { return ordinaryTimestamp(seed) }},
	{"f5", "datetime.date", func(seed uint64) any { return ordinaryDate(seed) }},
	{"f6", "lorem.title", func(seed uint64) any { return genWords(seed, 2, 4, true) }},
	{"f7", "lorem.description", func(seed uint64) any { return genWords(seed, 6, 14, false) }},
	{"f8", "status.value", func(seed uint64) any { return statusCorpus[seed%uint64(len(statusCorpus))] }},
	{"f9", "code.value", func(seed uint64) any { return genCode(seed) }},
	{"f10", "color.hex", func(seed uint64) any { return genColor(seed) }},
	{"f11", "slug.value", func(seed uint64) any { return genSlug(seed) }},
	{"f12", "string.uuid", func(seed uint64) any { return idString(seed, "uuid") }},
}

// fakerTestDoc is a single NON-LIST operation whose 200 schema declares
// twelve string properties, one per fakerTokenCases entry — plain
// {"type":"string"}, no format, so nothing but the bound recipe could be
// the source of a field's value.
func fakerTestDoc() map[string]any {
	props := make(map[string]any, len(fakerTokenCases))
	for _, c := range fakerTokenCases {
		props[c.field] = map[string]any{"type": "string"}
	}
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/fakers": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":       "object",
									"properties": props,
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

func TestFakerTokensProduceOwnPinnedValues(t *testing.T) {
	res := buildResolver(t, fakerTestDoc())

	bindings := make(map[string]recipes.Recipe, len(fakerTokenCases))
	for _, c := range fakerTokenCases {
		bindings[c.field] = recipes.Recipe{Kind: recipes.KindFaker, Field: c.token}
	}
	set, err := recipes.Compile(bindings)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	opts := Options{Seed: 424242}
	v := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1fakers/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1fakers/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/fakers", Query: url.Values{}, Status: 200, Recipes: set}
	g := New(res, opts)

	boundRaw, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (bound): %v", err)
	}
	var bound map[string]any
	if err := json.Unmarshal(boundRaw, &bound); err != nil {
		t.Fatalf("invalid JSON (bound): %v: %s", err, boundRaw)
	}

	// The seed is SeedScalar over the RAW walk path, exactly as
	// w.recipeEnv builds Env.Seed (recipes.go) — never the prefixed
	// lookup path, which only matters inside a list item and is a
	// different concern entirely (recipePrefix never reaches Env.Seed).
	seedList := SeedList(opts, req)
	for _, c := range fakerTokenCases {
		seed := SeedScalar(seedList, c.field)
		want := c.want(seed)
		got := bound[c.field]
		if got != want {
			t.Errorf("token %q bound to field %q: got %v, want %v (body=%s)", c.token, c.field, got, want, boundRaw)
		}
	}

	// The unbound baseline: same schema, same seed, no *recipes.Set at
	// all. This is the ONE assertion a disconnected registry cannot pass —
	// if every faker recipe silently declined, ordinary generation would
	// fill every field exactly as it does here, and bound would equal
	// unbound field-for-field.
	unboundReq := req
	unboundReq.Recipes = nil
	unboundRaw, err := g.Body(v, unboundReq)
	if err != nil {
		t.Fatalf("Body (unbound): %v", err)
	}
	var unbound map[string]any
	if err := json.Unmarshal(unboundRaw, &unbound); err != nil {
		t.Fatalf("invalid JSON (unbound): %v: %s", err, unboundRaw)
	}
	changed := false
	for _, c := range fakerTokenCases {
		if bound[c.field] != unbound[c.field] {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("binding all twelve faker recipes produced the SAME body as binding none — the registry looks disconnected: bound=%s unbound=%s", boundRaw, unboundRaw)
	}

	// Set equality, both directions, between the published vocabulary
	// (recipes.FakerTokens) and this package's own registry
	// (fakerProducers) — a token published with no producer has nothing to
	// register; a producer registered under no published token can never
	// be reached by an operator, since Validate rejects any "faker" recipe
	// whose field isn't in the published list before Compile ever sees it.
	published := recipes.FakerTokens()
	publishedSet := make(map[string]struct{}, len(published))
	for _, tok := range published {
		publishedSet[tok] = struct{}{}
	}
	registryTokens := make([]string, 0, len(fakerProducers))
	for tok := range fakerProducers {
		registryTokens = append(registryTokens, tok)
	}
	sort.Strings(registryTokens)
	registrySet := make(map[string]struct{}, len(registryTokens))
	for _, tok := range registryTokens {
		registrySet[tok] = struct{}{}
	}

	for tok := range publishedSet {
		if _, ok := registrySet[tok]; !ok {
			t.Errorf("recipes.FakerTokens() publishes %q, which has no producer in fakerProducers", tok)
		}
	}
	for tok := range registrySet {
		if _, ok := publishedSet[tok]; !ok {
			t.Errorf("fakerProducers has a producer for %q, which recipes.FakerTokens() does not publish", tok)
		}
	}
	sortedPublished := append([]string(nil), published...)
	sort.Strings(sortedPublished)
	if fmt.Sprint(sortedPublished) != fmt.Sprint(registryTokens) {
		t.Errorf("published token set and registry token set differ:\n  published (%d): %v\n  registry  (%d): %v",
			len(sortedPublished), sortedPublished, len(registryTokens), registryTokens)
	}
}
