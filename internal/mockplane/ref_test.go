// ref_test.go holds properties 1-8 of the mocker-p3c-ref-recipe decision
// document's D11 acceptance section — everything whose behaviour LIVES in
// this package: [Plane.newRefResolver]/[Plane.resolveRef] (ref.go), the
// traffic mark they drive (traffic.go's markRefUnresolved), and the two
// call sites that hand them the request (respond.go's serveGenerated,
// preview.go's Preview). Property 9 (Validate) belongs to internal/recipes
// and is not here; property 10 is the unchanged 419-body golden and needs
// no new test.
//
// D11's own precondition rule is why every test below goes to the trouble
// it does: a recipe reaches the generator only through an ACTIVE
// op_overrides row (respond.go:481#OverrideActive), and this package's own
// real-repository harness (resource_integration_test.go) does not wire
// one — so every fixture here builds its own, via [fixtureRuntimeWithOverrides]
// or a hand-rolled *overrides.Row, rather than reusing that harness as-is.
// A body comparison also holds the recipe set the SAME SIZE on both sides
// of any "with ref" vs "without" comparison — gen.Body suppresses the
// whole-body example whenever the bound set is non-empty
// (gen.go:410-411#contentExample) — so this file never compares a
// ref-bound response against a response with NO override at all; where a
// baseline is needed (property 3's fallthrough), the baseline ALSO carries
// an active override, just one whose ref recipe is guaranteed to decline in
// the identical way, so both sides pay the identical "recipe present"
// example-suppression cost.
//
// D11's other rule — "a property is observed where its behaviour LIVES" —
// is why every test here drives the real [Plane.serveGenerated]/
// [Plane.Preview]/[Plane.ServeHTTP] entry points against fixtures built the
// same way runtime_test.go/resource_test.go/preview_test.go/traffic_test.go
// already build them (same package, same compilation unit), never a
// hand-rolled stand-in for [recipes.Ref] itself.
package mockplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

// --- fixtures -------------------------------------------------------------

// orderDoc is one ORDINARY operation (D8's own vocabulary: not a resource
// family — no "/order/{id}" detail route exists anywhere in this file, so
// [resourceBranch] never has a roster entry that could take it over): GET
// /order answers a single object carrying "subjectId", the field every test
// below binds a "ref" recipe to, plus one declared response HEADER
// ("X-Subject-Id", property 5's second consumer of the same request).
const orderDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "order", "version": "1.0.0"},
  "paths": {
    "/order": {
      "get": {
        "responses": {
          "200": {
            "description": "d",
            "headers": {"X-Subject-Id": {"schema": {"type": "integer"}}},
            "content": {"application/json": {
              "schema": {"type": "object", "properties": {"subjectId": {"type": "integer"}}}
            }}
          }
        }
      }
    }
  }
}`

// orderListDoc is orderDoc's array-shaped sibling — a bare top-level array
// of the same one-field object, for property 2's "positions of one array
// do not all carry one entity".
const orderListDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "order-list", "version": "1.0.0"},
  "paths": {
    "/orders": {
      "get": {
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"type": "object", "properties": {"subjectId": {"type": "integer"}}}}
        }}}}
      }
    }
  }
}`

// orderFreeformDoc is orderDoc's own-typed-non-scalar sibling: the same one
// operation (GET /order -> 200), but its body declares properties whose OWN
// declared type is the same kind resolveRef must refuse — "payload" is
// "object", "listing" is "array". [recipes.Coerce] (value.go:731-735 for
// "object", :718-729 for "array") ACCEPTS a value of the matching Go kind
// unconditionally, no scalar check anywhere in either case — so these are
// the one shape in this file where resolveRef's own non-scalar guard
// (ref.go:172-174, D10) is the ONLY thing standing between a stored
// object/array and the response body. orderDoc's own "subjectId" ("type":
// "integer") does not isolate this: Coerce's "integer" case already
// declines a map/array on its own, proving nothing about the switch.
//
// "extra" is a SECOND array-typed property, with none of DESIGN's
// canonical collection names (listPreferredArrayNames, gen/list.go) — with
// it, [gen.detectListShape] sees two ambiguous array candidates and
// declines the whole list-contract shape (gen/list.go:315-328), so
// "listing" is generated through the ORDINARY per-property walk
// (leafValue/recipeValue) like any other field, the path resolveRef
// actually runs on. Without "extra", "listing" would be the object's ONE
// array-typed property, and DESIGN's own list contract (§9, "объект с
// РОВНО одним array-свойством") would intercept it as the page's own
// array through listPageBody instead — a wholly different generation path
// that never calls recipeValue/resolveRef at all, so a ref recipe bound
// there would silently generate an ordinary page rather than testing
// anything about the resolver.
const orderFreeformDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "order-freeform", "version": "1.0.0"},
  "paths": {
    "/order": {
      "get": {
        "responses": {
          "200": {
            "description": "d",
            "content": {"application/json": {
              "schema": {"type": "object", "properties": {
                "payload": {"type": "object"},
                "listing": {"type": "array"},
                "extra": {"type": "array"}
              }}
            }}
          }
        }
      }
    }
  }
}`

func orderRoutes() []router.Route {
	return []router.Route{{OpRowID: 1, Method: http.MethodGet, Path: "/order", CanonicalPath: "/order", SourceOrder: 1}}
}

func orderVariants() map[int64][]gen.ResponseVariant {
	return map[int64][]gen.ResponseVariant{
		1: {{
			OpRowID:    1,
			Selector:   "200",
			HTTPStatus: 200,
			MediaType:  "application/json",
			SchemaPtr:  "#/paths/~1order/get/responses/200/content/application~1json/schema",
			OpPointer:  "#/paths/~1order/get",
		}},
	}
}

func orderListRoutes() []router.Route {
	return []router.Route{{OpRowID: 1, Method: http.MethodGet, Path: "/orders", CanonicalPath: "/orders", SourceOrder: 1}}
}

func orderListVariants() map[int64][]gen.ResponseVariant {
	return map[int64][]gen.ResponseVariant{
		1: {{
			OpRowID:    1,
			Selector:   "200",
			HTTPStatus: 200,
			MediaType:  "application/json",
			SchemaPtr:  "#/paths/~1orders/get/responses/200/content/application~1json/schema",
			OpPointer:  "#/paths/~1orders/get",
		}},
	}
}

// refRecipeData builds the three-key Data object D3 rides Recipe.Data with:
// no delimiter, family/property/policy each its own JSON key (D3's own
// refusal of a single delimited string — a family path may legally contain
// a dot or a "#").
func refRecipeData(t *testing.T, family, property, policy string) jsonx.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"family": family, "property": property, "policy": policy})
	if err != nil {
		t.Fatalf("marshal ref recipe data: %v", err)
	}
	return jsonx.RawMessage(b)
}

// refOverrideRow is one ACTIVE op_overrides row (D11's precondition) with a
// single "ref" recipe bound at pattern on path's 200 response.
func refOverrideRow(path, pattern string, data jsonx.RawMessage) *overrides.Row {
	return &overrides.Row{
		Method:     http.MethodGet,
		Path:       path,
		OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {
				Mode: "generated",
				Recipes: map[string]recipes.Recipe{
					pattern: {Kind: recipes.KindRef, Data: data},
				},
			},
		},
	}
}

// subjectsResource is the confirmed family a "ref" recipe addresses in this
// file's tests — RouteFamily is the only field [resolveRef] reads off it
// beyond ID (D6 step 2's verbatim roster-key match).
func subjectsResource(id int64) *resources.Resource {
	return &resources.Resource{ID: id, WorkspaceID: 1, RouteFamily: "/subjects"}
}

// refTestPlane wires a *Plane with store as its [EntityStore] and no
// [ResourceSource] of its own — every test here sets rt.resources by hand,
// mirroring resourceTestRuntime's own pattern (resource_test.go), because
// these properties are about the RESOLVER, not about roster derivation.
func refTestPlane(store EntityStore) *Plane {
	p := respondTestPlane()
	p.SetEntities(store)
	return p
}

// entityRow is one resources.Entity carrying data as its stored JSON
// object.
func entityRow(id int64, key, data string) resources.Entity {
	return resources.Entity{ID: id, EntityKey: key, Data: jsonx.RawMessage(data)}
}

// testStoreError is a store failure distinct from [resources.ErrResourceGone]
// — property 3's fourth decline reason ("the store returned any other
// error", D6/D15).
type testStoreError struct{}

func (*testStoreError) Error() string { return "injected store failure" }

var errInjectedStoreFailure = &testStoreError{}

// --- property 1: a ref resolves to a value the family's own rows hold -----

func TestRefResolver_ResolvesToStoredFamilyValue(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", "generate")),
	}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}

	stored := []resources.Entity{
		entityRow(1, "1", `{"id":7,"name":"a"}`),
		entityRow(2, "2", `{"id":8,"name":"b"}`),
		entityRow(3, "3", `{"id":9,"name":"c"}`),
	}
	store := &fakeEntityStore{listFn: func(_ context.Context, resourceID int64, _, _ resources.ScopeKey) ([]resources.Entity, error) {
		if resourceID != 42 {
			t.Fatalf("List called with resourceID %d, want 42 (the confirmed /subjects resource)", resourceID)
		}
		return stored, nil
	}}
	p := refTestPlane(store)
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	got, ok := decodeAny(t, rec.Body.Bytes()).(map[string]any)
	if !ok {
		t.Fatalf("body did not decode into an object: %s", rec.Body)
	}
	subjectID, ok := got["subjectId"].(json.Number)
	if !ok {
		t.Fatalf("subjectId = %v (%T), want a JSON number", got["subjectId"], got["subjectId"])
	}
	switch subjectID.String() {
	case "7", "8", "9":
		// one of the ids the /subjects family really stores — the property.
	default:
		t.Fatalf("subjectId = %s, want one of the /subjects family's real ids (7, 8, 9), not a plausible-but-unrelated integer", subjectID.String())
	}
}

// --- property 2: deterministic from the seed, and varies by data path -----

func TestRefResolver_DeterministicSameRequestSameValue(t *testing.T) {
	settings := domain.Settings{Seed: 5, ListSize: 3}
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", "generate")),
	}
	stored := []resources.Entity{
		entityRow(1, "1", `{"id":11}`),
		entityRow(2, "2", `{"id":22}`),
		entityRow(3, "3", `{"id":33}`),
	}
	run := func() any {
		rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
		rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}
		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			return stored, nil
		}}
		p := refTestPlane(store)
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
		obj, ok := decodeAny(t, rec.Body.Bytes()).(map[string]any)
		if !ok {
			t.Fatalf("body did not decode into an object: %s", rec.Body)
		}
		return obj["subjectId"]
	}

	first, second := run(), run()
	if first != second {
		t.Fatalf("subjectId across two identical requests = %v then %v, want the same value (same seed, same spec, same request)", first, second)
	}
}

// TestRefResolver_VariesByDataPath is D11 property 2's other half: bound at
// every position of one array ("[*].subjectId"), the resolver must not pick
// the SAME entity for every element — Env.Seed is per DATA PATH (D4/D6),
// and a list of many orders referencing one entity every time is exactly
// the failure "Seed is load-bearing" (D4) exists to prevent.
func TestRefResolver_VariesByDataPath(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 20}
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/orders"): refOverrideRow("/orders", "[*].subjectId", refRecipeData(t, "/subjects", "id", "generate")),
	}
	rt := fixtureRuntimeWithOverrides(t, orderListDoc, orderListRoutes(), orderListVariants(), settings, rows)
	rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}

	stored := make([]resources.Entity, 10)
	for i := range stored {
		b, err := json.Marshal(map[string]int{"id": 100 + i})
		if err != nil {
			t.Fatalf("marshal entity fixture: %v", err)
		}
		stored[i] = entityRow(int64(i+1), "", string(b))
	}
	store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		return stored, nil
	}}
	p := refTestPlane(store)
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/orders", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/orders"), resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	items, ok := decodeAny(t, rec.Body.Bytes()).([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("body did not decode into a non-empty array: %s", rec.Body)
	}
	seen := map[string]bool{}
	for _, it := range items {
		obj, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("array element did not decode into an object: %v", it)
		}
		n, ok := obj["subjectId"].(json.Number)
		if !ok {
			t.Fatalf("subjectId = %v (%T), want a JSON number", obj["subjectId"], obj["subjectId"])
		}
		seen[n.String()] = true
	}
	if len(seen) < 2 {
		t.Fatalf("every element's subjectId resolved to the SAME entity (%v) across %d items, want at least two distinct entities picked across array positions", seen, len(items))
	}
}

// --- property 3: an unresolved ref never fails the response ---------------

// TestRefResolver_UnresolvedFallsThroughOrEmitsNull covers every decline
// reason D6/D15 name — a nil resolver (no [EntityStore] wired), a family
// absent from the roster, a family with no rows, a store error, the picked
// row not carrying the bound property, and a scalar value that fails to
// coerce to the schema's declared type — under each of the two shipped
// policies. The non-scalar decline (ref.go:172-174, D10's amplification
// guard) is deliberately NOT one of these cases: this file's only schema
// property ("subjectId") declares "type": "integer", so an object or array
// stored there already fails [recipes.Coerce]'s own type check and would
// decline identically with that switch deleted — proving nothing about the
// switch itself. See
// TestRefResolver_NonScalarPropertyNeverSplicesRawValue below, which binds
// an UNTYPED property for exactly that reason. Under "generate" the response must
// carry EXACTLY the value the ordinary (non-ref) generated chain would
// have produced for this schema node — proven against a baseline built
// from the SAME active override (so both sides pay the identical
// example-suppression cost the file header describes) whose ref is
// guaranteed to decline the same way. Under "set-null" the field must be
// JSON null.
func TestRefResolver_UnresolvedFallsThroughOrEmitsNull(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}

	baselineRows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", "generate")),
	}
	baselineRT := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, baselineRows)
	baselineRT.resources = map[string]*resources.Resource{} // family never confirmed -> decline
	baselineP := refTestPlane(&fakeEntityStore{})
	baselineRec := httptest.NewRecorder()
	baselineP.serveGenerated(baselineRec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil), respondTestWorkspace(), baselineRT, mustMatch(t, baselineRT, http.MethodGet, "/order"), resources.ScopeKey(""))
	baselineObj, ok := decodeAny(t, baselineRec.Body.Bytes()).(map[string]any)
	if !ok {
		t.Fatalf("baseline body did not decode into an object: %s", baselineRec.Body)
	}
	baselineValue := baselineObj["subjectId"]

	cases := []struct {
		name       string
		noStore    bool   // D6 step 1: p.entities never wired at all
		noRoster   bool   // D6 step 2: family not confirmed in this workspace
		emptyRows  bool   // D6 step 3: List succeeds with zero rows
		listErr    error  // D6 step 3: List returns an error
		entityData string // overrides the stored row's JSON; "" keeps the default {"id":7}
	}{
		{name: "nil resolver (no entity store wired)", noStore: true},
		{name: "family not in roster", noRoster: true},
		{name: "family holds no rows", emptyRows: true},
		{name: "store returns an error", listErr: errInjectedStoreFailure},
		// D6 step 5 (ref.go:166-168): the picked row decodes fine but does
		// not carry the bound property at all.
		{name: "entity does not carry the bound property", entityData: `{"other":1}`},
		// D6 step 6 (ref.go:176): the bound property is a scalar but fails
		// to coerce to the schema's declared type ("subjectId" is
		// "integer" — see orderDoc) — a non-numeric string.
		{name: "bound property fails to coerce to the declared type", entityData: `{"id":"not-a-number"}`},
	}

	for _, tc := range cases {
		for _, policy := range []string{"generate", "set-null"} {
			t.Run(tc.name+"/"+policy, func(t *testing.T) {
				rows := map[string]*overrides.Row{
					overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", policy)),
				}
				rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
				if tc.noRoster {
					rt.resources = map[string]*resources.Resource{}
				} else {
					rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}
				}

				var store EntityStore
				if !tc.noStore {
					store = &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
						if tc.listErr != nil {
							return nil, tc.listErr
						}
						if tc.emptyRows {
							return nil, nil
						}
						data := tc.entityData
						if data == "" {
							data = `{"id":7}`
						}
						return []resources.Entity{entityRow(1, "1", data)}, nil
					}}
				}
				p := refTestPlane(store)
				req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
				rec := httptest.NewRecorder()
				p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))

				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200 — an unresolved ref must never fail the response; body=%s", rec.Code, rec.Body)
				}
				obj, ok := decodeAny(t, rec.Body.Bytes()).(map[string]any)
				if !ok {
					t.Fatalf("body did not decode into an object: %s", rec.Body)
				}
				got := obj["subjectId"]
				if policy == "set-null" {
					if got != nil {
						t.Fatalf("subjectId = %v, want JSON null under policy set-null", got)
					}
					return
				}
				if got != baselineValue {
					t.Fatalf("subjectId = %v, want the SAME value the ordinary generated chain produces (%v) — a declined ref must fall through, not substitute its own placeholder", got, baselineValue)
				}
			})
		}
	}
}

// TestRefResolver_NonScalarPropertyNeverSplicesRawValue is D10's own
// amplification guard (ref.go:172-174), proven in the one shape that
// actually isolates it: [orderFreeformDoc] declares "payload" as "type":
// "object" and "listing" as "type": "array" — the SAME kind the stored
// value itself is — so [recipes.Coerce]'s own "object"/"array" cases would
// otherwise accept the raw map/slice unconditionally (value.go:718-735) and
// pass it straight through as the resolved value, exactly D10's own
// example, "200 copy bindings turned an 807KB body into 161MB". orderDoc's
// "subjectId" ("type": "integer") does not isolate this — Coerce's
// "integer" case already declines a map/array on its own, so that
// mismatched-type combination proves nothing about the switch itself.
// Under "set-null" a resolved ref is indistinguishable on the wire from a
// declined one (both emit JSON null) except for what the field actually
// holds — an object/array that made it through the switch would show up as
// that object/array, not null.
func TestRefResolver_NonScalarPropertyNeverSplicesRawValue(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}

	cases := []struct {
		name       string
		property   string // the schema property bound, and its declared type's own matching JSON kind
		entityData string
	}{
		{name: "bound property is a JSON object, declared type object", property: "payload", entityData: `{"payload":{"nested":"a splice would carry this string into the response body"}}`},
		{name: "bound property is a JSON array, declared type array", property: "listing", entityData: `{"listing":[1,2,3]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The recipe's Data.property names the STORED ENTITY's own key
			// to read (parseRef, value.go) — independent of tc.property,
			// which is the SCHEMA path the recipe is bound at. Both name
			// the same field here on purpose, matching entityData's own
			// key, so the resolver actually reaches step 6 with the
			// non-scalar value rather than declining earlier, at step 5,
			// on an "id" key neither fixture entity carries.
			rows := map[string]*overrides.Row{
				overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", tc.property, refRecipeData(t, "/subjects", tc.property, "set-null")),
			}
			rt := fixtureRuntimeWithOverrides(t, orderFreeformDoc, orderRoutes(), orderVariants(), settings, rows)
			rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}
			store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
				return []resources.Entity{entityRow(1, "1", tc.entityData)}, nil
			}}
			p := refTestPlane(store)
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — an unresolved ref must never fail the response; body=%s", rec.Code, rec.Body)
			}
			obj, ok := decodeAny(t, rec.Body.Bytes()).(map[string]any)
			if !ok {
				t.Fatalf("body did not decode into an object: %s", rec.Body)
			}
			if got := obj[tc.property]; got != nil {
				t.Fatalf("%s = %v, want JSON null — a non-scalar stored value must decline (D10), never splice its raw object/array into the body", tc.property, got)
			}
		})
	}
}

// --- property 4: the traffic mark, and the join rule -----------------------

// TestServeHTTP_RefUnresolved_TrafficMark drives the real [Plane.ServeHTTP]
// (never [Plane.serveGenerated] directly): markRefUnresolved is a no-op
// unless [attachTrafficMatch] ran, and only [Plane.captureTraffic] —
// reached from ServeHTTP, not from a direct serveGenerated call — does
// that.
func TestServeHTTP_RefUnresolved_TrafficMark(t *testing.T) {
	cases := []struct {
		name      string
		policy    string
		withPause bool
		wantNotes string
	}{
		{name: "generate alone marks ref_unresolved", policy: "generate", wantNotes: noteRefUnresolved},
		{name: "set-null marks nothing", policy: "set-null", wantNotes: ""},
		{name: "generate joins an existing pause_refused note rather than replacing it", policy: "generate", withPause: true, wantNotes: notePauseRefused + "," + noteRefUnresolved},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeTrafficSink{}
			ws := widgetsWorkspace(7, domain.DefaultSettings())
			src := &fakeRuntimeSource{normalized: []byte(orderDoc), routes: orderRoutes(), variants: orderVariants()}
			p := trafficPlane(t, 1<<20, src, sink, ws)
			p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
				overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", tc.policy)),
			}})
			p.SetResources(previewFakeResourceSource{res: subjectsResource(42)})
			// The default fakeEntityStore (listFn nil -> List returns
			// nil, nil): an EMPTY family, one of D6's own decline
			// reasons — every case here needs the ref to decline, never
			// resolve, so the mark (or its absence, under set-null) is
			// what each assertion below is actually about.
			p.SetEntities(&fakeEntityStore{})

			if tc.withPause {
				store := livestate.NewStore(0, nil)
				if err := store.Set(ws.ID, livestate.Directive{
					Target: livestate.Target{Method: http.MethodGet, Path: "/order"}, Action: livestate.ActionPause,
				}); err != nil {
					t.Fatalf("store.Set: %v", err)
				}
				for i := range livestate.MaxPausedPerWorkspace {
					if eff := store.Apply(ws.ID, http.MethodGet, "/order"); !eff.Pause {
						t.Fatalf("park %d: Apply.Pause = false, want true", i)
					}
				}
				p.SetLiveState(store)
			}

			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}

			events := sink.all()
			if len(events) != 1 {
				t.Fatalf("events = %d, want 1", len(events))
			}
			if events[0].Notes != tc.wantNotes {
				t.Errorf("Notes = %q, want %q", events[0].Notes, tc.wantNotes)
			}
		})
	}
}

// --- property 5: one memoized read per family per REQUEST -----------------

// TestRefResolver_MemoizesOncePerRequestAcrossBodyAndHeaders binds a "ref"
// recipe twice on ONE operation — once in the body ("subjectId"), once in
// a declared response header ("header.X-Subject-Id") — both addressing the
// SAME family, so a memo scoped to Body alone would read twice for one
// response. The SECOND request must read again: the memo lives on the
// per-request closure (D4), never across requests.
func TestRefResolver_MemoizesOncePerRequestAcrossBodyAndHeaders(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): {
			Method: http.MethodGet, Path: "/order", OverrideOn: true,
			Responses: map[string]overrides.Variant{"200": {Mode: "generated", Recipes: map[string]recipes.Recipe{
				"subjectId":           {Kind: recipes.KindRef, Data: refRecipeData(t, "/subjects", "id", "generate")},
				"header.X-Subject-Id": {Kind: recipes.KindRef, Data: refRecipeData(t, "/subjects", "id", "generate")},
			}}},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}

	var calls int
	store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		calls++
		return []resources.Entity{entityRow(1, "1", `{"id":7}`)}, nil
	}}
	p := refTestPlane(store)

	req1 := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	p.serveGenerated(httptest.NewRecorder(), req1, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
	if calls != 1 {
		t.Fatalf("List calls after one request (body + header both bind the same family) = %d, want 1 — one memo, shared by Body and Headers", calls)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	p.serveGenerated(httptest.NewRecorder(), req2, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
	if calls != 2 {
		t.Fatalf("List calls after a second request = %d, want 2 — a later request reads again, no memo survives across requests", calls)
	}
}

// --- property 6: a ref never crosses a workspace boundary ------------------

// TestRefResolver_NeverCrossesWorkspaceBoundary drives the SAME store
// (which would answer List(ctx, 42) with the SAME rows either way) behind
// two different rosters: one that confirms /subjects, one that does not.
// [EntityStore.List] is not itself workspace-scoped (D6) — the roster is
// structurally the only thing standing between a ref and another
// workspace's rows, and this proves it holds even when the store itself
// would happily answer.
func TestRefResolver_NeverCrossesWorkspaceBoundary(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", "generate")),
	}
	stored := []resources.Entity{entityRow(1, "1", `{"id":777}`)}
	store := &fakeEntityStore{listFn: func(_ context.Context, resourceID int64, _, _ resources.ScopeKey) ([]resources.Entity, error) {
		if resourceID != 42 {
			t.Fatalf("List called with resourceID %d, want 42 — a ref must reach the store only through THIS request's own roster", resourceID)
		}
		return stored, nil
	}}

	// Workspace A: /subjects IS in this request's roster.
	rtA := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	rtA.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}
	pA := refTestPlane(store)
	recA := httptest.NewRecorder()
	pA.serveGenerated(recA, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil), respondTestWorkspace(), rtA, mustMatch(t, rtA, http.MethodGet, "/order"), resources.ScopeKey(""))
	objA, ok := decodeAny(t, recA.Body.Bytes()).(map[string]any)
	if !ok {
		t.Fatalf("workspace A body did not decode into an object: %s", recA.Body)
	}
	gotA, ok := objA["subjectId"].(json.Number)
	if !ok || gotA.String() != "777" {
		t.Fatalf("workspace A (family confirmed in its own roster) subjectId = %v, want 777", objA["subjectId"])
	}

	// Workspace B: /subjects is NOT in this request's roster, even though
	// the SAME store would answer List(ctx, 42) with the SAME rows if ever
	// asked directly (it is never asked here — resolveRef declines at the
	// roster lookup, step 2, before any store call).
	rtB := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	rtB.resources = map[string]*resources.Resource{}
	pB := refTestPlane(store)
	recB := httptest.NewRecorder()
	pB.serveGenerated(recB, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil), respondTestWorkspace(), rtB, mustMatch(t, rtB, http.MethodGet, "/order"), resources.ScopeKey(""))
	if recB.Code != http.StatusOK {
		t.Fatalf("workspace B status = %d, want 200; body=%s", recB.Code, recB.Body)
	}
	objB, ok := decodeAny(t, recB.Body.Bytes()).(map[string]any)
	if !ok {
		t.Fatalf("workspace B body did not decode into an object: %s", recB.Body)
	}
	if n, ok := objB["subjectId"].(json.Number); ok && n.String() == "777" {
		t.Fatalf("workspace B (no /subjects in its own roster) subjectId = %s, want NOT 777 — the family must be unreachable without a roster entry of its own", n.String())
	}
}

// --- property 7 (mockplane half): Preview resolves nothing, reads nothing -

// TestPreview_RefResolvesNothingAndReadsNothing is D5's own rule made
// observable: [Plane.assembleResponse] RECEIVES the resolver, [Plane.Preview]
// passes nil rather than building a live one — so a draft binding a "ref"
// recipe over a family that genuinely IS confirmed in this workspace's
// roster (poisonStore would happily answer it) must still behave exactly
// like a decline, and the store must never be touched at all.
func TestPreview_RefResolvesNothingAndReadsNothing(t *testing.T) {
	src := &fakeRuntimeSource{normalized: []byte(orderDoc), routes: orderRoutes(), variants: orderVariants()}
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())
	p.SetResources(previewFakeResourceSource{res: subjectsResource(42)})
	poison := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		t.Fatalf("EntityStore.List called during Preview — D5: Preview passes a nil resolver so a draft never touches real entity rows")
		return nil, nil
	}}
	p.SetEntities(poison)

	ws := widgetsWorkspace(1, domain.DefaultSettings())
	draft := previewDraft(t, true, map[string]overrides.Variant{
		"200": {Mode: "generated", Recipes: map[string]recipes.Recipe{
			"subjectId": {Kind: recipes.KindRef, Data: refRecipeData(t, "/subjects", "id", "set-null")},
		}},
	})
	result, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
		OpKey: overrides.OpKey(http.MethodGet, "/order"),
		Draft: draft,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if result.Refused != nil {
		t.Fatalf("Preview refused: %+v, want it to serve — /order is not resource-served (confirmed family is /subjects, unrelated)", *result.Refused)
	}
	obj, ok := decodeAny(t, result.Body).(map[string]any)
	if !ok {
		t.Fatalf("preview body did not decode into an object: %s", result.Body)
	}
	if got := obj["subjectId"]; got != nil {
		t.Fatalf("subjectId = %v, want JSON null — a nil Ref is treated exactly like a live resolver's decline (D5), and this recipe's policy is set-null", got)
	}
}

// --- property 8: a confirmed resource serves PreBuilt unchanged -----------

// TestResourceBranch_ServesPreBuiltUnchangedDespiteActiveRefOverride is D8
// made observable the way its own doc comment says to word it: "unchanged"
// is not "unchanged since before this slice" (there is no golden at an
// earlier revision to compare against) but "the stored bytes, verbatim,
// even with an ACTIVE override on the SAME operation binding a ref at the
// SAME field the response actually carries" — [resourceBranch] never calls
// gen.Body at all (D8), so the override must be provably inert.
func TestResourceBranch_ServesPreBuiltUnchangedDespiteActiveRefOverride(t *testing.T) {
	settings := domain.DefaultSettings()
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, settings, res)
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/items"): {
			Method: http.MethodGet, Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{"200": {Mode: "generated", Recipes: map[string]recipes.Recipe{
				"id": {Kind: recipes.KindRef, Data: refRecipeData(t, "/items", "id", "generate")},
			}}},
		},
	}
	rt.overrides = rows
	rt.recipeSets = buildRecipeSets(runtimeTestLogger(), "test", rows)

	entities := []resources.Entity{
		{Data: jsonx.RawMessage(`{"id":1,"name":"a"}`)},
		{Data: jsonx.RawMessage(`{"id":2,"name":"b"}`)},
	}
	store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		return entities, nil
	}}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/items"), resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	want := `[{"id":1,"name":"a"},{"id":2,"name":"b"}]`
	got := decodeAny(t, rec.Body.Bytes())
	wantDecoded := decodeAny(t, []byte(want))
	if fmt.Sprint(got) != fmt.Sprint(wantDecoded) {
		t.Errorf("body = %s, want the stored bytes verbatim (%s) — a confirmed resource never calls gen.Body (D8), so an active override binding a ref on its own operation must be inert", rec.Body.String(), want)
	}
}

// TestRefResolver_ResolvesWithinTheServingRequestsBaseScope is P3h's P13
// (D10): a ref resolves within the SERVING request's own base scope — a
// generated body served under base value "7" carries an id belonging to
// "7"'s own rows of the referenced family, and the same operation under
// "8" carries one of "8"'s. The route scope stays empty regardless (P3c's
// carve-out, unmoved) — this property is only about the base axis, which is
// workspace-wide (D3.2.2): the value that scopes the served family is the
// SAME value that scopes the referenced one.
func TestRefResolver_ResolvesWithinTheServingRequestsBaseScope(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 1, BasePath: "/tenants/{tenantId}", BasePathValues: []string{"7", "8"}}
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): refOverrideRow("/order", "subjectId", refRecipeData(t, "/subjects", "id", "generate")),
	}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}

	rowsByBase := map[resources.ScopeKey][]resources.Entity{
		"7": {entityRow(1, "", `{"id":701}`)},
		"8": {entityRow(2, "", `{"id":801}`)},
	}
	store := &fakeEntityStore{listFn: func(_ context.Context, resourceID int64, base, _ resources.ScopeKey) ([]resources.Entity, error) {
		if resourceID != 42 {
			t.Fatalf("List called with resourceID %d, want 42", resourceID)
		}
		return rowsByBase[base], nil
	}}
	p := refTestPlane(store)

	get := func(t *testing.T, path string) json.Number {
		t.Helper()
		m := mustMatch(t, rt, http.MethodGet, path)
		values, ok := router.BaseValues(settings.BasePath, NormalizeSegments(path))
		if !ok {
			t.Fatalf("router.BaseValues(%q, %q) = _, false", settings.BasePath, path)
		}
		base := resources.EncodeScope(values)
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, base)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200; body=%s", path, rec.Code, rec.Body)
		}
		obj, ok := decodeAny(t, rec.Body.Bytes()).(map[string]any)
		if !ok {
			t.Fatalf("GET %s: body did not decode into an object: %s", path, rec.Body)
		}
		n, ok := obj["subjectId"].(json.Number)
		if !ok {
			t.Fatalf("GET %s: subjectId = %v (%T), want a JSON number", path, obj["subjectId"], obj["subjectId"])
		}
		return n
	}

	if got := get(t, "/tenants/7/order"); got.String() != "701" {
		t.Errorf("subjectId under base 7 = %s, want 701 (base 7's own row)", got.String())
	}
	if got := get(t, "/tenants/8/order"); got.String() != "801" {
		t.Errorf("subjectId under base 8 = %s, want 801 (base 8's own row)", got.String())
	}
}
