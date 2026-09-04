package bundle_test

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// goldenPath is relative to this package's own directory — `go test` sets
// the working directory to the package under test, the same convention
// internal/gen/golden_p1b_test.go documents for its own testdata file.
const goldenPath = "testdata/golden_bundle.json"

// goldenBundle is the ONE fixed Bundle the byte-stability golden (A19)
// pins against. It is built to exercise every stability concern Encode has
// to get right in a single document, not spread thin across separate
// fixtures:
//
//   - two OverrideEntry rows, one OverrideOn=true and one OverrideOn=false
//     — so A2's "present in the snapshot AND switched off" distinction is
//     pinned on the actual wire bytes, not just asserted by a separate
//     round-trip test;
//   - the OverrideOn=true row's pinned Body, and the workspace's own
//     NotFoundBody, are both written with keys in NON-alphabetical order —
//     a passing comparison against the golden file PROVES canonicalize()
//     actually ran, rather than the fixture merely happening to already be
//     sorted;
//   - a HAND-BUILT domain.Settings rather than [domain.DefaultSettings]:
//     that constructor mints a random SigningKey via crypto/rand
//     (domain/settings.go), which would make this fixture's bytes change
//     on every single test run.
func goldenBundle() bundle.Bundle {
	settings := domain.Settings{
		Seed:     42,
		BasePath: "/api/v1",
		ListSize: 5,
		NullRate: 0.1,
		Identity: domain.Identity{
			ID:    1,
			Name:  "Test Testov",
			Email: "test@example.com",
			Roles: []string{"user"},
			Org:   &domain.Org{ID: 1, Name: "Test Org", Type: "school"},
		},
		Auth: domain.AuthSettings{
			JWTTTLSec:     3600,
			Alg:           "HS256",
			SigningKey:    "fixed-signing-key-for-the-golden-fixture-only",
			RequireHeader: false,
		},
		CORS:             domain.CORSSettings{Mode: domain.CORSReflect, Credentials: true},
		ValidateRequests: false,
		DelayMs:          0,
		NotFoundBody:     jsonx.RawMessage(`{"z":true,"a":"first"}`),
	}

	active := 404
	entries := []bundle.OverrideEntry{
		{
			Method:       "GET",
			Path:         "/widgets/{id}",
			OverrideOn:   true,
			ActiveStatus: &active,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "pinned",
					Body: jsonx.RawMessage(`{"z":1,"a":2,"m":{"y":9,"b":8}}`),
				},
			},
		},
		{
			// A2: membership (this key IS in the snapshot) and the flag
			// (OverrideOn=false) are independent. This row means "under
			// this scenario, GET /quizzes answers exactly as the spec
			// document says" — a THIRD state neither the workspace layer
			// nor an absent key can express on its own. If the golden
			// byte form ever collapsed this row into an absence, that
			// distinction would have silently broken.
			Method:     "GET",
			Path:       "/quizzes",
			OverrideOn: false,
			Responses:  map[string]overrides.Variant{},
		},
	}

	spec := bundle.SpecRef{Hash: "a1b2c3d4", Name: "platform"}
	return bundle.New("quiz-editor-cases", settings, spec, entries)
}

// TestEncode_golden is the A19 byte-stability pin: HARD RULE-shaped, in the
// spirit of internal/gen/golden_p1b_test.go, except this compares bytes
// directly rather than hashes — a Bundle is small enough, and this way a
// failing diff shows exactly which key moved instead of just "something
// changed". Re-run with MOCKER_REGENERATE_GOLDEN=1 ONLY when a change to
// this package's own encoding is deliberate; a diff any other time means
// the wire format moved out from under whatever already-stored scenario
// snapshots exist.
func TestEncode_golden(t *testing.T) {
	got, err := bundle.Encode(goldenBundle())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if os.Getenv("MOCKER_REGENERATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Skip("regenerated " + goldenPath + " — rerun without MOCKER_REGENERATE_GOLDEN=1 to verify it")
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with MOCKER_REGENERATE_GOLDEN=1 to create it)", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("Encode output does not match %s.\n\ngot:  %s\n\nwant: %s", goldenPath, got, want)
	}
}

// TestEncode_golden_alsoRoundTrips proves the golden fixture itself is not
// just byte-stable but actually DECODES back to an equivalent document —
// a golden that pins bytes nobody can read back would be worse than none.
func TestEncode_golden_roundTrips(t *testing.T) {
	data, err := bundle.Encode(goldenBundle())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := bundle.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Workspace.Name != "quiz-editor-cases" {
		t.Errorf("workspace name = %q", decoded.Workspace.Name)
	}
	if len(decoded.Overrides) != 2 {
		t.Fatalf("overrides count = %d, want 2", len(decoded.Overrides))
	}
}

// TestBundle_resourcesEndpointsEntities_presentAndEmpty pins that
// endpoints/resources/decisions encode as the literal "[]" and entities as
// the literal "null" — PRESENT, never an omitted key — for a Bundle built
// via [bundle.New] with nothing assigned on top. The SHAPE half of this
// test's original purpose is exactly as true as it always was: all four
// fields are populated slots on the wire, never omitted, whether or not
// anything actually fills them.
//
// What is NOT true anymore is the reason an earlier version of this comment
// gave: it framed the emptiness as something only a FUTURE version bump
// would ever lift. That framing died twice, under the same v3 format both
// times — first for Endpoints (C2 of P2c's gate), then for Resources and
// Decisions (P3b), each filled by internal/checkpoints assigning them
// directly on the Bundle New returns. New itself still leaves all three
// empty, for the narrower reason its own doc comment gives. Only Entities
// is still genuinely a phase away (P3d), and it is the one field Validate
// still refuses a value in.
func TestBundle_resourcesEndpointsEntities_presentAndEmpty(t *testing.T) {
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)
	data, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"overrides":[]`, `"endpoints":[]`, `"resources":[]`, `"decisions":[]`, `"entities":null`} {
		if !strings.Contains(s, want) {
			t.Errorf("Encode output missing %q; got %s", want, s)
		}
	}
}

// TestDecode_rejectsBasePathDisagreement is A14: the top-level basePath
// and workspace.settings.basePath must agree, and Decode is where a
// disagreeing document (hand-edited, or written by a build that got the
// invariant wrong) is caught — this bypasses [bundle.New] deliberately
// (New always keeps the two in sync by construction) to prove Decode
// itself, not just New, enforces it.
func TestDecode_rejectsBasePathDisagreement(t *testing.T) {
	raw := []byte(`{
		"mockerBundle": 5,
		"workspace": {"name": "x", "settings": {"basePath": "/a"}},
		"basePath": "/b",
		"spec": {"hash": "", "name": "", "inline": null},
		"overrides": [], "endpoints": [], "resources": [], "entities": null
	}`)
	_, err := bundle.Decode(raw)
	if !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Decode: got %v, want an error wrapping bundle.ErrInvalid", err)
	}
}

// TestDecode_rejectsUnknownVersion is A16's other half, re-aimed by P7a and
// again by A18: this build reads 5..6, so the unknown version a document must
// be refused for is one ABOVE the current — a future v7 — rather than the v6
// this slice now writes. The re-aim is mechanical and the comment records that
// it has happened twice, because a "future version" fixture goes stale on
// every bump by construction.
func TestDecode_rejectsUnknownVersion(t *testing.T) {
	raw := []byte(`{
		"mockerBundle": 7,
		"workspace": {"name": "x", "settings": {}},
		"basePath": "", "spec": {"hash":"","name":"","inline":null},
		"overrides": [], "endpoints": [], "resources": [], "entities": null
	}`)
	_, err := bundle.Decode(raw)
	if !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Decode: got %v, want an error wrapping bundle.ErrInvalid", err)
	}
}

// TestValidate_acceptsResourcesAndStillRejectsNonNullEntities is P3b's
// half of §0's original cut, and the two halves now point in OPPOSITE
// directions — which is the whole reason this test exists under a name that
// says so.
//
// `resources` is ACCEPTED: internal/checkpoints fills it with a workspace's
// live resource rows, and a format-level refusal would make the capture
// impossible. `entities` is still REFUSED, and that refusal is one this
// slice positively wants: P3b captures resource CONFIGURATION only, its
// restore is UPSERT-only precisely BECAUSE it has no entity rows to put
// back, and this is what stops a later capture site from quietly starting
// to carry them.
//
// Both halves go through [bundle.Decode], not [bundle.Validate] on a
// hand-built value: a document is how either one actually arrives from
// storage, and decoding proves the wire shape and the rule together.
//
// This test used to also cover Endpoints, under the name
// TestValidate_rejectsNonEmptyEndpointsResourcesOrNonNullEntities. C2 of
// P2c's gate removed that half: Validate no longer rejects a non-empty
// Endpoints array at all, only SHAPE-checks each entry (see
// TestValidate_acceptsAndShapeChecksEndpoints below). That assertion did
// not disappear, it MOVED, to the narrower scenario-specific rule in
// internal/scenarios' scanScenario. The `resources` half of the name did
// disappear, in P3b, and NOTHING replaced it anywhere: no writer in this
// build can put a resource into a SCENARIO snapshot, so a guard there would
// have defended a population of zero (R21).
func TestValidate_acceptsResourcesAndStillRejectsNonNullEntities(t *testing.T) {
	const withResources = `{
		"mockerBundle": 5,
		"workspace": {"name": "ws", "settings": {"basePath": ""}},
		"basePath": "",
		"spec": {"hash": "", "name": "", "inline": null},
		"overrides": [], "endpoints": [],
		"resources": [{"routeFamily": "/widgets", "name": "widgets", "idField": "id",
			"idStrategy": "seq", "scopeParams": [], "entitySchema": "#/components/schemas/Widget",
			"wrapper": null, "filterMap": {}, "writeForm": "bare", "seq": 3, "seedCount": 3,
			"parentFamily": null}],
		"decisions": [{"routeFamily": "/widgets", "state": "confirmed"}],
		"entities": null
	}`
	b, err := bundle.Decode([]byte(withResources))
	if err != nil {
		t.Fatalf("Decode(document carrying resources): %v, want nil", err)
	}
	if len(b.Resources) != 1 || b.Resources[0].RouteFamily != "/widgets" {
		t.Fatalf("decoded resources = %+v, want the one /widgets entry", b.Resources)
	}
	if b.Resources[0].WriteForm == nil || *b.Resources[0].WriteForm != "bare" {
		t.Fatalf("decoded writeForm = %v, want \"bare\"", b.Resources[0].WriteForm)
	}
	if len(b.Decisions) != 1 || b.Decisions[0].State != "confirmed" {
		t.Fatalf("decoded decisions = %+v, want one confirmed /widgets row", b.Decisions)
	}

	const withEntities = `{
		"mockerBundle": 5,
		"workspace": {"name": "ws", "settings": {"basePath": ""}},
		"basePath": "",
		"spec": {"hash": "", "name": "", "inline": null},
		"overrides": [], "endpoints": [], "resources": [], "decisions": [],
		"entities": [{"resourceId": 1, "entityKey": "1", "data": {}}]
	}`
	if _, err := bundle.Decode([]byte(withEntities)); !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Decode(document carrying entities): got %v, want an error wrapping bundle.ErrInvalid", err)
	}
}

// TestEncode_resourcesAndDecisionsAreOrderIndependent is D10 clause 28: two
// Bundles differing ONLY in the order of their Resources and Decisions must
// encode to the same bytes.
//
// It is the same property [TestEncode_golden] pins for Overrides one step
// earlier in the pipeline, and it needs its own test for the reason the
// Endpoints pass already documents: neither array passes through
// [bundle.New], so nothing sorts them before canonicalize does. The two
// input slices are built as separate literals rather than one slice sorted
// two ways, so a canonicalize that sorted the CALLER's backing array in
// place (Encode promises it never mutates its argument) would still be
// caught by the second Encode seeing an already-sorted input.
func TestEncode_resourcesAndDecisionsAreOrderIndependent(t *testing.T) {
	widgets := bundle.ResourceEntry{
		RouteFamily: "/widgets", Name: "widgets", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Widget",
		Wrapper: jsonx.RawMessage(`{"z":1,"a":2}`), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 4, SeedCount: 4,
	}
	quizzes := bundle.ResourceEntry{
		RouteFamily: "/quizzes", Name: "quizzes", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Quiz",
		FilterMap: jsonx.RawMessage(`{}`), Seq: 2, SeedCount: 2,
	}

	first := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)
	first.Resources = []bundle.ResourceEntry{widgets, quizzes}
	first.Decisions = []bundle.DecisionEntry{
		{RouteFamily: "/widgets", State: "confirmed"},
		{RouteFamily: "/quizzes", State: "declined"},
	}

	second := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)
	second.Workspace.Settings = first.Workspace.Settings // DefaultSettings mints a random signing key
	second.Resources = []bundle.ResourceEntry{quizzes, widgets}
	second.Decisions = []bundle.DecisionEntry{
		{RouteFamily: "/quizzes", State: "declined"},
		{RouteFamily: "/widgets", State: "confirmed"},
	}

	a, err := bundle.Encode(first)
	if err != nil {
		t.Fatalf("Encode(first): %v", err)
	}
	b, err := bundle.Encode(second)
	if err != nil {
		t.Fatalf("Encode(second): %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("Encode is order-dependent:\n a: %s\n b: %s", a, b)
	}
	// The caller's own slices must be untouched: canonicalize returns a
	// SHALLOW copy of the Bundle, so a sort without an allocation would
	// reorder these behind Encode's back.
	if first.Resources[0].RouteFamily != "/widgets" || first.Decisions[0].RouteFamily != "/widgets" {
		t.Fatalf("Encode reordered the caller's slices: %+v / %+v", first.Resources, first.Decisions)
	}
}

// TestDecode_documentWithNoDecisionsKey was D10 clause 31 of P3b: a snapshot
// with `"resources":[]` present and no `decisions` key at all still decodes.
// Until P6b it also pinned mockerBundle at 3 on the ground that "a bump
// orphans every stored snapshot"; P6b took that bump on the owner's own
// statement that no deployment existed, and the literal has followed the
// floor ever since — 4 at P6b, 5 at A18, which is where minVersion now sits.
// The VERSION is incidental here and always was: what this test guards is the
// tolerance for a missing `decisions` key, and [TestDecode_refusesV3] pins the
// other side of P6b's D12.
func TestDecode_documentWithNoDecisionsKey(t *testing.T) {
	const stored = `{"mockerBundle":5,"workspace":{"name":"ws-a","settings":{"basePath":"/api"}},` +
		`"basePath":"/api","spec":{"hash":"a1b2","name":"platform","inline":null},` +
		`"overrides":[],"endpoints":[],"resources":[],"entities":null}`

	b, err := bundle.Decode([]byte(stored))
	if err != nil {
		t.Fatalf("Decode(a document without a decisions key): %v", err)
	}
	if b.MockerBundle != 5 {
		t.Fatalf("mockerBundle = %d, want 5", b.MockerBundle)
	}
	if b.Decisions != nil {
		t.Fatalf("decisions = %+v, want nil: the key is absent from the stored bytes", b.Decisions)
	}
	if b.Resources == nil || len(b.Resources) != 0 {
		t.Fatalf("resources = %+v, want the empty array the stored bytes carry", b.Resources)
	}
}

// TestValidate_acceptsAndShapeChecksEndpoints is the Endpoints-side sibling
// of TestValidate_rejectsNonEmptyResourcesOrNonNullEntities right above:
// where that test pins what is STILL rejected, this one pins what C2/C3
// changed. A structurally valid Endpoints entry now passes Validate
// outright; a structurally invalid one is still refused, but for its own
// shape rather than for merely being present — reusing
// [overrides.ValidateResponses] (A13's second half, one struct over from
// TestValidate_reusesOverridesValidateResponses's Overrides-side proof
// below).
func TestValidate_acceptsAndShapeChecksEndpoints(t *testing.T) {
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)

	b.Endpoints = []bundle.EndpointEntry{{
		Method:    "GET",
		Path:      "/custom",
		Responses: map[string]overrides.Variant{"200": {Mode: "generated"}},
	}}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("Validate(well-formed endpoint): got %v, want nil", err)
	}

	b.Endpoints = []bundle.EndpointEntry{{
		Method:    "GET",
		Path:      "/custom",
		Responses: map[string]overrides.Variant{"200": {Mode: "not-a-real-mode"}},
	}}
	err := bundle.Validate(b)
	if !errors.Is(err, overrides.ErrInvalidRow) {
		t.Fatalf("Validate(bad endpoint mode): got %v, want an error wrapping overrides.ErrInvalidRow", err)
	}
	if !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Validate(bad endpoint mode): got %v, want an error ALSO wrapping bundle.ErrInvalid", err)
	}
}

// TestOverrideOnFalse_roundTripsPresentAndSwitchedOff is the test the task
// calls out by name: A2's whole point is that a scenario row can MASK a
// workspace edit by being present with OverrideOn=false, and that is only
// possible if encode/decode never treats "false" as "may as well be
// absent". This constructs a Row exactly the way overrides.Repo would
// scan one back, converts it through [bundle.NewOverrideEntry] (the same
// path internal/scenarios' CreateFromCurrentState uses), and checks the
// full Encode/Decode round trip.
func TestOverrideOnFalse_roundTripsPresentAndSwitchedOff(t *testing.T) {
	row := &overrides.Row{
		Method:     http.MethodDelete,
		Path:       "/quizzes/{id}",
		OverrideOn: false,
		Responses:  map[string]overrides.Variant{},
	}
	entries := []bundle.OverrideEntry{bundle.NewOverrideEntry(row)}
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, entries)

	data, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(string(data), `"overrideOn":false`) {
		t.Fatalf("Encode output does not carry overrideOn:false at all — got %s", data)
	}

	decoded, err := bundle.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded.Overrides) != 1 {
		t.Fatalf("overrides count = %d, want 1 (the row must survive, not be dropped)", len(decoded.Overrides))
	}
	got := decoded.Overrides[0]
	if got.Method != http.MethodDelete || got.Path != "/quizzes/{id}" {
		t.Errorf("entry identity = %s %s, want DELETE /quizzes/{id}", got.Method, got.Path)
	}
	if got.OverrideOn {
		t.Errorf("OverrideOn = true after round trip, want false")
	}
}

// TestDecode_neverConsultsSpecHash is A15's assigned cover: this package
// has no database access at all, so there is nothing it COULD compare
// Spec.Hash against — but the property worth pinning is that Decode
// succeeds and returns the hash verbatim regardless of what it contains,
// including a value that plainly does not correspond to any real spec.
// Every live observation in this run uses a single spec that never
// changes, so this test is the property's only cover (A15's own text).
func TestDecode_neverConsultsSpecHash(t *testing.T) {
	for _, hash := range []string{"", "not-a-real-sha256", strings.Repeat("f", 64)} {
		b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{Hash: hash, Name: "whatever"}, nil)
		data, err := bundle.Encode(b)
		if err != nil {
			t.Fatalf("Encode(hash=%q): %v", hash, err)
		}
		decoded, err := bundle.Decode(data)
		if err != nil {
			t.Fatalf("Decode(hash=%q): %v — a hash value alone must never fail decode", hash, err)
		}
		if decoded.Spec.Hash != hash {
			t.Errorf("Spec.Hash = %q, want %q", decoded.Spec.Hash, hash)
		}
	}
}

// TestValidate_reusesOverridesValidateResponses is A13's second half: this
// package must not grow a second notion of what a valid override is. An
// unknown Variant.Mode is rejected by [overrides.ValidateVariant]
// specifically, so seeing that exact sentinel come back through
// bundle.Validate proves the SAME function ran, not a lookalike.
func TestValidate_reusesOverridesValidateResponses(t *testing.T) {
	entries := []bundle.OverrideEntry{{
		Method:     "GET",
		Path:       "/x",
		OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {Mode: "not-a-real-mode"},
		},
	}}
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, entries)
	err := bundle.Validate(b)
	if !errors.Is(err, overrides.ErrInvalidRow) {
		t.Fatalf("Validate: got %v, want an error wrapping overrides.ErrInvalidRow", err)
	}
	if !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Validate: got %v, want an error ALSO wrapping bundle.ErrInvalid", err)
	}
}

// TestEncode_canonicalizesBodyButNotFailDirectiveKeyOrder checks the one
// deliberate asymmetry in A19's canonicalisation pass: a pinned response
// Body has its keys SORTED (decode/encode through [canonicalizeRaw]), but
// FailDirective's key order is left exactly as given — matching
// overrides/repo.go's own upsertTx rule that the SAME column's key order
// must not depend on which code path wrote it. (Whitespace is a separate
// property Go's encoding/json compacts for every RawMessage field
// regardless — see the field comment on OverrideEntry.FailDirective — so
// this checks key ORDER, the property this package actually controls.)
func TestEncode_canonicalizesBodyButNotFailDirectiveKeyOrder(t *testing.T) {
	entries := []bundle.OverrideEntry{{
		Method:        "GET",
		Path:          "/x",
		OverrideOn:    true,
		FailDirective: jsonx.RawMessage(`{"z":1,"a":2}`), // "z" deliberately before "a"
		Responses: map[string]overrides.Variant{
			"200": {Mode: "pinned", Body: jsonx.RawMessage(`{"z":1,"a":2}`)},
		},
	}}
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, entries)
	data, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"body":{"a":2,"z":1}`) {
		t.Errorf("Body was not canonicalised (keys sorted); got %s", s)
	}
	if !strings.Contains(s, `"failDirective":{"z":1,"a":2}`) {
		t.Errorf("FailDirective's key order was sorted, want it left exactly as given; got %s", s)
	}
}

// TestEncode_canonicalizesEndpointVariantBody is §G obs 18(a), Endpoints'
// own sibling of TestEncode_canonicalizesBodyButNotFailDirectiveKeyOrder
// above. Mirroring that test's shape is the ONLY thing that actually
// proves canonicalize() walks Bundle.Endpoints too, not just
// Bundle.Overrides: encoding one in-memory bundle twice would prove
// NOTHING, since jsonx's own backend is encoding/json, which key-sorts any
// map unconditionally whether or not canonicalizeEndpointEntry ever ran —
// the only real proof is a RawMessage body whose keys arrive deliberately
// UNSORTED, checked against what actually comes out the other side.
func TestEncode_canonicalizesEndpointVariantBody(t *testing.T) {
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)
	b.Endpoints = []bundle.EndpointEntry{{
		Method: "GET",
		Path:   "/custom",
		Responses: map[string]overrides.Variant{
			"200": {Mode: "pinned", Body: jsonx.RawMessage(`{"z":1,"a":2}`)}, // "z" deliberately before "a"
		},
	}}

	data, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"body":{"a":2,"z":1}`) {
		t.Errorf("endpoint variant Body was not canonicalised (keys sorted); got %s", s)
	}
}

// TestEncode_sortsEndpointsByMethodAndPath is §G obs 18(b): canonicalize
// sorts Bundle.Endpoints by (Method, Path) ITSELF, unlike Overrides, which
// arrives already sorted because New sorts it before canonicalize ever
// runs (see TestNew_sortsEntriesDeterministically below). Endpoints never
// passes through New at all — New's signature carries no endpoints
// parameter (C2/C3) — internal/checkpoints assigns b.Endpoints directly on
// the value New returns, in whatever order customep.ForWorkspace's `ORDER
// BY source_order, id` happened to hand back, which is a DB ordering, not
// a (Method, Path) one. Supplying two entries in REVERSE (Method, Path)
// order and asserting the output lists them sorted is the only shape that
// actually proves canonicalize sorts them, rather than the fixture merely
// happening to already be in order.
func TestEncode_sortsEndpointsByMethodAndPath(t *testing.T) {
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)
	// Deliberately reverse (Method, Path) order: POST /z before GET /a.
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "POST", Path: "/z", Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/a", Responses: map[string]overrides.Variant{}},
	}

	data, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Index-compare the raw bytes first — this is what actually pins the
	// WIRE order, as opposed to Decode's own re-reading of it below.
	s := string(data)
	idxA := strings.Index(s, `"path":"/a"`)
	idxZ := strings.Index(s, `"path":"/z"`)
	if idxA == -1 || idxZ == -1 || idxA > idxZ {
		t.Fatalf("encoded endpoints not sorted by (method, path) in the raw bytes: %s", s)
	}

	decoded, err := bundle.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded.Endpoints) != 2 {
		t.Fatalf("endpoints count = %d, want 2", len(decoded.Endpoints))
	}
	if decoded.Endpoints[0].Method != http.MethodGet || decoded.Endpoints[0].Path != "/a" {
		t.Errorf("endpoints[0] = %s %s, want GET /a (sorted first)", decoded.Endpoints[0].Method, decoded.Endpoints[0].Path)
	}
	if decoded.Endpoints[1].Method != http.MethodPost || decoded.Endpoints[1].Path != "/z" {
		t.Errorf("endpoints[1] = %s %s, want POST /z (sorted second)", decoded.Endpoints[1].Method, decoded.Endpoints[1].Path)
	}
}

// TestNew_sortsEntriesDeterministically proves the snapshot's byte form
// does not depend on the order entries were handed to New in — the actual
// caller (internal/scenarios) builds this slice by ranging over a Go map
// ([overrides.Repo.ForWorkspace]'s return value), whose iteration order is
// randomised per run.
func TestNew_sortsEntriesDeterministically(t *testing.T) {
	forward := []bundle.OverrideEntry{
		{Method: "GET", Path: "/b", OverrideOn: true, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/a", OverrideOn: true, Responses: map[string]overrides.Variant{}},
	}
	backward := []bundle.OverrideEntry{forward[1], forward[0]}

	// domain.DefaultSettings mints a random signing key, so a second call
	// would make the two encodings differ for a reason that has nothing to
	// do with sort order — reuse ONE settings value for both.
	settings := domain.DefaultSettings()
	a, err := bundle.Encode(bundle.New("ws", settings, bundle.SpecRef{}, forward))
	if err != nil {
		t.Fatalf("Encode(forward): %v", err)
	}
	b, err := bundle.Encode(bundle.New("ws", settings, bundle.SpecRef{}, backward))
	if err != nil {
		t.Fatalf("Encode(backward): %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("New did not sort entries: forward-order and backward-order encodings differ:\n%s\n\nvs\n\n%s", a, b)
	}
}

// TestDecode_refusesV3 is P6b D12's own observation: a version-3 document —
// every checkpoint and scenario written before 2026-09-02 — is REFUSED, not
// read leniently. The owner chose this over "read 3 and 4, write 4" on the
// stated ground that no deployment exists; the consequence (a pre-P6b
// database's snapshots become undecodable) is recorded in CARVE-OUTS.md,
// and this test is what keeps a later slice from quietly re-widening the
// reader without a decision.
func TestDecode_refusesV3(t *testing.T) {
	const stored = `{"mockerBundle":3,"workspace":{"name":"ws-a","settings":{"basePath":"/api"}},` +
		`"basePath":"/api","spec":{"hash":"a1b2","name":"platform","inline":null},` +
		`"overrides":[],"endpoints":[],"resources":[],"entities":null}`
	if _, err := bundle.Decode([]byte(stored)); !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Decode(v3): got %v, want an error wrapping bundle.ErrInvalid", err)
	}
}
