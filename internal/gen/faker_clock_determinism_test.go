// faker_clock_determinism_test.go is C9's guard: DESIGN §9's determinism
// contract says an ordinary (non-deadline) field derives from the SEED,
// never from the real clock — and "faker" is the one NEW recipe kind whose
// producer set (D6) includes a token, datetime.timestamp, that a wrong
// implementation could plausibly source from Env.Now instead of Env.Seed,
// since both are plumbed to the same Recipe.Value call. Two immediate
// requests would agree even under that bug (the clock barely moves between
// them); this test moves the injected clock explicitly, between two calls
// against the SAME Generator and the SAME seed, and pins both bodies
// against the same expected values.
//
// The fixture is this package's OWN inline test document (never the smoke
// corpus, a different fixture with different field names) and deliberately
// binds no "now" recipe and no deadline-shaped field name (isDeadlineField,
// names.go) — "now" legitimately follows the clock, so a fixture that
// exercised it would make this test red against a correct build.
package gen

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/recipes"
)

func clockDeterminismDoc() map[string]any {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/moments": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										// f1/f2: neutral names, neither
										// matches isDeadlineField (exp,
										// *_expires_at, expires_in,
										// not_after, *_valid_until).
										"f1": map[string]any{"type": "string"},
										"f2": map[string]any{"type": "string"},
									},
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

func TestFakerTimestampDerivesFromSeedNotClock(t *testing.T) {
	res := buildResolver(t, clockDeterminismDoc())

	// Exactly two recipes: the one token a wrong implementation would
	// plausibly source from the clock, and one non-clock recipe alongside
	// it — const, never "now" (that kind legitimately follows the clock,
	// so binding it would make this test fail against CORRECT code).
	set, err := recipes.Compile(map[string]recipes.Recipe{
		"f1": {Kind: recipes.KindFaker, Field: "datetime.timestamp"},
		"f2": {Kind: recipes.KindConst, Data: json.RawMessage(`"pinned-value"`)},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// now is the injected clock. Generator.newWalker calls opts.clock()()
	// fresh on every Body call (gen.go), never once at construction, so
	// mutating this variable between the two calls below moves the clock
	// under the SAME Generator, the SAME Options.Seed and the SAME
	// *recipes.Set — nothing else is free to differ between the two calls.
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	opts := Options{Seed: 909090, Now: func() time.Time { return now }}
	v := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1moments/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1moments/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/moments", Query: url.Values{}, Status: 200, Recipes: set}
	g := New(res, opts)

	body1, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (first, clock=%s): %v", now, err)
	}

	now = now.AddDate(10, 0, 0) // move the clock ten years forward between the two calls
	body2, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (second, clock=%s): %v", now, err)
	}

	// The pinned expected value: computed from the SAME producer D6's
	// table names for datetime.timestamp (ordinaryTimestamp, seed-derived,
	// never Env.Now), never captured from a run of the recipe path.
	seedList := SeedList(opts, req)
	want := map[string]any{
		"f1": ordinaryTimestamp(SeedScalar(seedList, "f1")),
		"f2": "pinned-value",
	}

	var got1, got2 map[string]any
	if err := json.Unmarshal(body1, &got1); err != nil {
		t.Fatalf("invalid JSON (first): %v: %s", err, body1)
	}
	if err := json.Unmarshal(body2, &got2); err != nil {
		t.Fatalf("invalid JSON (second): %v: %s", err, body2)
	}

	if !reflect.DeepEqual(got1, want) {
		t.Errorf("first body (clock 2020) = %v, want %v (body=%s)", got1, want, body1)
	}
	if !reflect.DeepEqual(got2, want) {
		t.Errorf("second body (clock moved to 2030) = %v, want %v (body=%s) — a datetime.timestamp producer reading Env.Now instead of Env.Seed would diverge here", got2, want, body2)
	}
	if !bytes.Equal(body1, body2) {
		t.Errorf("moving the injected clock between two calls with the same seed changed the body: %s vs %s", body1, body2)
	}
}
