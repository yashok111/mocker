package specs

import (
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testspec"
)

// newDeriveTestDB opens a fresh, migrated SQLite file under t.TempDir() —
// duplicated from repo_test.go's own newTestDB rather than shared, because
// that helper lives in package specs_test (external) and this file is
// white-box (package specs), the only way to reach the unexported
// [newSpecResolver] hook clause 9 needs to instrument.
func newDeriveTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newDeriveTestRepo(t *testing.T) (*Repo, *store.DB) {
	t.Helper()
	db := newDeriveTestDB(t)
	cfg := &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		DataDir:        t.TempDir(),
		MaxBody:        10 << 20,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
	return NewRepo(db, cfg), db
}

// TestDeriveSuggestions_Clause8 is decisions.md §D13 clause 8: over
// [testspec.DerivationDoc], the derived route_family set is EXACTLY the two
// positive controls — not a superset (a predicate that suggests everything)
// and not a subset missing one of them (a predicate that suggests nothing
// useful). Every negative shape the fixture carries is asserted absent by
// name, so a future edit that starts suggesting one of them fails here
// rather than silently widening what an operator sees on the confirm
// screen.
func TestDeriveSuggestions_Clause8(t *testing.T) {
	doc, report, err := openapi.Load(testspec.DerivationDoc())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	ops, resp := Index(doc, res, report)

	got := deriveSuggestions(res, ops, resp)

	gotFamilies := make([]string, len(got))
	for i, s := range got {
		gotFamilies[i] = s.RouteFamily
	}
	want := []string{testspec.FamilyBareItems, testspec.FamilyWidgets}
	slices.Sort(want)
	if !slices.Equal(gotFamilies, want) {
		t.Fatalf("route families = %v, want exactly %v", gotFamilies, want)
	}

	// Every negative shape the fixture declares, named individually so a
	// future change that starts (wrongly) suggesting exactly one of them
	// fails on a specific line instead of a generic set-mismatch.
	mustNotAppear := []string{
		"/orgs/{}/departments", // D4.1: "/orgs" is not itself a derived family here, so a one-"{}" family cannot nest
		"/orphans",             // detail route, no matching collection GET
		"/lonely",              // collection GET, no matching detail route
		"/noiditems",           // item schema declares no "id" property
		"/ambiguous",           // wrapped 200 with TWO array-typed properties
		"/textdetail",          // detail 200 declared under a non-JSON media type
		"/scalarpage",          // 200 with no array-typed property at all
	}
	for _, fam := range mustNotAppear {
		if slices.Contains(gotFamilies, fam) {
			t.Errorf("route family %q was derived, want it excluded", fam)
		}
	}

	// The two positive controls' own shapes, checked in full — not just
	// that they were emitted — since clause 8 is about the SET, and a
	// wrong wrapper on a correctly-named family would pass a bare
	// membership check.
	byFamily := make(map[string]Suggestion, len(got))
	for _, s := range got {
		byFamily[s.RouteFamily] = s
	}

	widgets := byFamily[testspec.FamilyWidgets]
	if widgets.IDField != "id" {
		t.Errorf("widgets id_field = %q, want %q", widgets.IDField, "id")
	}
	if widgets.EntitySchema != "#/components/schemas/Widget" {
		t.Errorf("widgets entity_schema = %q, want %q", widgets.EntitySchema, "#/components/schemas/Widget")
	}
	if widgets.Wrapper.ArrayKey == nil || *widgets.Wrapper.ArrayKey != "items" {
		t.Errorf("widgets wrapper.arrayKey = %v, want \"items\"", widgets.Wrapper.ArrayKey)
	}
	if widgets.Wrapper.CountKey == nil || *widgets.Wrapper.CountKey != "total" {
		t.Errorf("widgets wrapper.countKey = %v, want \"total\"", widgets.Wrapper.CountKey)
	}
	// "total" is declared with NO "type" key at all (clause 49's own
	// fixture requirement) — SchemaType's structural fallback answers
	// "string" for a genuinely untyped leaf, which is what the generator
	// already serves that property as today; storing "" here (as
	// PrimaryIDType would) would flip the wire type the moment this
	// family is confirmed.
	if widgets.Wrapper.CountType != "string" {
		t.Errorf("widgets wrapper.countType = %q, want %q", widgets.Wrapper.CountType, "string")
	}
	if widgets.Wrapper.IDType != "integer" {
		t.Errorf("widgets wrapper.idType = %q, want %q", widgets.Wrapper.IDType, "integer")
	}

	bare := byFamily[testspec.FamilyBareItems]
	if bare.IDField != "id" {
		t.Errorf("bareitems id_field = %q, want %q", bare.IDField, "id")
	}
	if bare.Wrapper.ArrayKey != nil {
		t.Errorf("bareitems wrapper.arrayKey = %v, want nil (bare array)", bare.Wrapper.ArrayKey)
	}
	if bare.Wrapper.CountKey != nil {
		t.Errorf("bareitems wrapper.countKey = %v, want nil (bare array)", bare.Wrapper.CountKey)
	}
	if bare.Wrapper.CountType != "" {
		t.Errorf("bareitems wrapper.countType = %q, want \"\"", bare.Wrapper.CountType)
	}
	if bare.Wrapper.IDType != "string" {
		t.Errorf("bareitems wrapper.idType = %q, want %q", bare.Wrapper.IDType, "string")
	}
}

// TestDeriveSuggestions_Nested is D4.3's positive control, over its own
// document ([testspec.NestedDerivationDoc]) rather than [testspec.DerivationDoc]
// itself — extending that document in place would make its own negative
// control ("/orgs/{}/departments") derivable and turn both of its
// assertions red against a CORRECT implementation (D4.3's own warning).
//
// Asserts the derived set EXACTLY: the parent family and both of its
// children — proving "one parent, two children" — and nothing else, over a
// derivation whose parent-family check (D4.1) can only be exercised by a
// document that actually declares a derivable parent. This document's own
// chain is one level deep, so it exercises passes 0 and 1 of the bounded
// loop and says nothing about the passes above them; P3g's own deeper
// document is what reaches those.
func TestDeriveSuggestions_Nested(t *testing.T) {
	doc, report, err := openapi.Load(testspec.NestedDerivationDoc())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	ops, resp := Index(doc, res, report)

	got := deriveSuggestions(res, ops, resp)

	gotFamilies := make([]string, len(got))
	for i, s := range got {
		gotFamilies[i] = s.RouteFamily
	}
	want := []string{testspec.FamilyOrgDepartments, testspec.FamilyOrgUsers, testspec.FamilyOrgs}
	slices.Sort(want)
	if !slices.Equal(gotFamilies, want) {
		t.Fatalf("route families = %v, want exactly %v", gotFamilies, want)
	}

	// FamilyOrgUsers is the one whose outer parameter is spelled
	// differently on its two routes ("orgId" on the collection,
	// "organizationId" on the detail route) — D4.3's own requirement, not
	// a convenience. It must still derive, with the detail route's OWN id
	// field ("id"), proving the mismatch never confuses id reconciliation
	// with the outer scope parameter.
	byFamily := make(map[string]Suggestion, len(got))
	for _, s := range got {
		byFamily[s.RouteFamily] = s
	}
	users := byFamily[testspec.FamilyOrgUsers]
	if users.IDField != "id" {
		t.Errorf("orgUsers id_field = %q, want %q", users.IDField, "id")
	}
}

// TestDeriveSuggestions_DeepNesting is decisions.md §D13's P1-P3, over
// [testspec.DeepNestingDoc] rather than [testspec.NestedDerivationDoc] —
// extending that document would move an assertion this slice has no
// business moving (D4.3).
//
// Asserts the derived set EXACTLY: the four-family positive chain reaching
// [maxNestingDepth] (P1: depth 2 and depth 3 both derive when their whole
// chain derives), and every one of the three negative controls absent (P2:
// depth 4, one past the ceiling; P3: a chain broken at its top, and a chain
// broken above the immediate parent — the shape a single-level fixture
// cannot discriminate at all, since at depth 1 "parent is shape-legal" and
// "parent was derived" coincide).
func TestDeriveSuggestions_DeepNesting(t *testing.T) {
	doc, report, err := openapi.Load(testspec.DeepNestingDoc())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	ops, resp := Index(doc, res, report)

	got := deriveSuggestions(res, ops, resp)

	gotFamilies := make([]string, len(got))
	for i, s := range got {
		gotFamilies[i] = s.RouteFamily
	}
	want := []string{
		testspec.FamilyDeepOrgs,
		testspec.FamilyDeepTeams,
		testspec.FamilyDeepUsers,
		testspec.FamilyDeepBadges,
	}
	slices.Sort(want)
	if !slices.Equal(gotFamilies, want) {
		t.Fatalf("route families = %v, want exactly %v", gotFamilies, want)
	}

	// Named individually, per the negative shape it proves, rather than a
	// generic set-mismatch: each is a DIFFERENT way a shallow-checking
	// implementation can pass a one-level fixture and still be wrong here.
	mustNotAppear := []string{
		testspec.FamilyDeepBadgeHistory,  // P2: depth 4, one past maxNestingDepth
		testspec.FamilyDeepRegionCities,  // P3: chain broken at its TOP — parent "/regions" undeclared
		testspec.FamilyDeepRegionStreets, // P3: chain broken ABOVE the immediate parent — parent shape-legal but itself undeclared-chain
	}
	for _, fam := range mustNotAppear {
		if slices.Contains(gotFamilies, fam) {
			t.Errorf("route family %q was derived, want it excluded", fam)
		}
	}

	// The depth-2 family (users) is the one whose BOTH outer path
	// parameters are spelled differently between its collection route
	// ("orgId", "teamId") and its detail route ("organizationId", "team")
	// — D4.3's own requirement. It must still derive, with the detail
	// route's OWN id field ("id"), proving the double mismatch never
	// confuses id reconciliation with either outer scope parameter.
	byFamily := make(map[string]Suggestion, len(got))
	for _, s := range got {
		byFamily[s.RouteFamily] = s
	}
	users := byFamily[testspec.FamilyDeepUsers]
	if users.IDField != "id" {
		t.Errorf("deep users id_field = %q, want %q", users.IDField, "id")
	}
	badges := byFamily[testspec.FamilyDeepBadges]
	if badges.IDField != "id" {
		t.Errorf("deep badges id_field = %q, want %q", badges.IDField, "id")
	}
}

// TestDeriveSuggestions_Empty is the "and never-derived" half of R8's
// sentinel need: a document with no operations at all derives ZERO
// suggestions without panicking on an empty ops/resp pair — the shape
// [Repo.Import] must write the sentinel row for.
func TestDeriveSuggestions_Empty(t *testing.T) {
	doc, report, err := openapi.Load([]byte(`{"openapi":"3.0.3","info":{"title":"empty","version":"1.0.0"},"paths":{}}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	ops, resp := Index(doc, res, report)

	got := deriveSuggestions(res, ops, resp)
	if len(got) != 0 {
		t.Fatalf("deriveSuggestions over an empty document = %v, want none", got)
	}
}

// zeroSuggestionDoc derives NOTHING: its one route is a collection GET with
// no matching detail route, so [deriveSuggestions] emits an empty slice and
// [Repo.Import] writes the sentinel row instead. Clause 9 requires the
// backfill-runs-once property to hold for exactly this shape, since it is
// the one an "ON CONFLICT DO NOTHING re-derivation" row-count check could
// not tell apart from "never derived" at all.
const zeroSuggestionDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "zero", "version": "1.0.0"},
  "paths": {
    "/lonely": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {"application/json": {"schema": {"type": "array", "items": {"type": "object"}}}}
          }
        }
      }
    }
  }
}`

// TestRepo_EnsureSuggestions_BackfillRunsOnce is decisions.md §D13 clause 9:
// the first EnsureSuggestions call for a spec with NO resource_suggestions
// row at all derives (one resolver built); the second call for the SAME
// spec performs no derivation at all (resolver count stays at one) — a
// distinction a row-count assertion could not make, since ON CONFLICT DO
// NOTHING would leave the sentinel row present either way. Also asserts the
// property holds for a spec that derives ZERO suggestions, per the clause's
// own text.
func TestRepo_EnsureSuggestions_BackfillRunsOnce(t *testing.T) {
	r, db := newDeriveTestRepo(t)

	res, err := r.Import(t.Context(), ImportInput{Name: "zero", Source: "upload", Document: []byte(zeroSuggestionDoc)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	// Import (this slice) already wrote the sentinel in the same
	// transaction as the operations rows — simulate a spec that predates
	// P3a entirely, the one case EnsureSuggestions' backfill exists for,
	// by removing that record out from under it.
	if _, err := db.W.ExecContext(t.Context(), "DELETE FROM resource_suggestions WHERE spec_id = ?", specID); err != nil {
		t.Fatalf("delete resource_suggestions: %v", err)
	}

	var calls int
	orig := newSpecResolver
	newSpecResolver = func(d *openapi.Document, budget int) *openapi.Resolver {
		calls++
		return orig(d, budget)
	}
	t.Cleanup(func() { newSpecResolver = orig })

	got, err := r.EnsureSuggestions(t.Context(), specID)
	if err != nil {
		t.Fatalf("first EnsureSuggestions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("first EnsureSuggestions returned %d suggestions, want 0", len(got))
	}
	if calls != 1 {
		t.Fatalf("first EnsureSuggestions built %d resolvers, want exactly 1", calls)
	}

	var n int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resource_suggestions WHERE spec_id = ?", specID).Scan(&n); err != nil {
		t.Fatalf("count resource_suggestions: %v", err)
	}
	if n != 1 {
		t.Fatalf("resource_suggestions rows for spec %d = %d, want 1 (the sentinel)", specID, n)
	}

	got, err = r.EnsureSuggestions(t.Context(), specID)
	if err != nil {
		t.Fatalf("second EnsureSuggestions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("second EnsureSuggestions returned %d suggestions, want 0", len(got))
	}
	if calls != 1 {
		t.Fatalf("second EnsureSuggestions built %d resolvers total, want still 1 (no re-derivation)", calls)
	}
}

// TestRepo_Import_GenStaysOneAcrossDifferentDocuments is decisions.md §D13
// clause 31: importing a SECOND, DIFFERENT document (different hash — a
// re-import of identical bytes dedupes and derives nothing at all, proving
// nothing about this clause) that declares the SAME route families leaves
// the FIRST spec's resource_suggestions rows untouched, still at gen 1.
// [Suggestion] carries no gen or spec_id of its own precisely because each
// import's rows belong to its own freshly assigned spec_id — this test is
// what holds that apart, rather than trusting the type alone.
//
// P3f's own decisions.md §D9 P23 extends this test rather than restating
// it ("no path but this verb leaves a generation above 1 behind"): the
// SECOND import above and a THIRD spec's lazy backfill are the two other
// paths that write generation 1, and this adds [Repo.Rederive] as the ONE
// path that can write a generation above it — asserting SELECT DISTINCT
// gen is exactly [1] for every spec but the rederived one, and exactly
// [1, 2] for it. *Fails if any path other than this verb leaves a
// generation above 1 behind, or if the verb leaves none.*
func TestRepo_Import_GenStaysOneAcrossDifferentDocuments(t *testing.T) {
	r, db := newDeriveTestRepo(t)

	first, err := r.Import(t.Context(), ImportInput{Name: "v1", Source: "upload", Document: testspec.DerivationDoc()})
	if err != nil {
		t.Fatalf("import first: %v", err)
	}

	before := suggestionSnapshot(t, db, first.Spec.ID)
	if len(before) == 0 {
		t.Fatalf("first import derived no suggestions; test fixture assumption broken")
	}

	// A second document, byte-different from the fixture (an added,
	// irrelevant description field is enough to change the hash) but
	// declaring the exact same families — Import must treat it as a
	// genuinely new spec, not a re-import.
	second := append(slices.Clone(testspec.DerivationDoc()[:len(testspec.DerivationDoc())-1]), []byte(`,"x-second-import":true}`)...)
	secondRes, err := r.Import(t.Context(), ImportInput{Name: "v2", Source: "upload", Document: second})
	if err != nil {
		t.Fatalf("import second: %v", err)
	}
	if secondRes.Spec.ID == first.Spec.ID {
		t.Fatalf("second import reused spec id %d, want a fresh one", secondRes.Spec.ID)
	}

	after := suggestionSnapshot(t, db, first.Spec.ID)
	if !slices.Equal(before, after) {
		t.Fatalf("first spec's resource_suggestions changed after a second import: before %v, after %v", before, after)
	}

	var gen int
	if err := db.R.QueryRowContext(t.Context(),
		"SELECT DISTINCT gen FROM resource_suggestions WHERE spec_id = ?", first.Spec.ID).Scan(&gen); err != nil {
		t.Fatalf("read gen: %v", err)
	}
	if gen != 1 {
		t.Fatalf("first spec's resource_suggestions gen = %d, want 1", gen)
	}

	// P23: a THIRD spec, its resource_suggestions rows deleted right after
	// import to simulate one that predates P3a, then backfilled through
	// [Repo.EnsureSuggestions]'s lazy path rather than Import's own — the
	// other of the two production paths that write generation 1.
	third := append(slices.Clone(testspec.DerivationDoc()[:len(testspec.DerivationDoc())-1]), []byte(`,"x-third-import":true}`)...)
	thirdRes, err := r.Import(t.Context(), ImportInput{Name: "v3", Source: "upload", Document: third})
	if err != nil {
		t.Fatalf("import third: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), "DELETE FROM resource_suggestions WHERE spec_id = ?", thirdRes.Spec.ID); err != nil {
		t.Fatalf("clear third spec's suggestions: %v", err)
	}
	if _, err := r.EnsureSuggestions(t.Context(), thirdRes.Spec.ID); err != nil {
		t.Fatalf("backfill third spec: %v", err)
	}

	// target is a FOURTH spec, seeded NARROW by direct SQL right after
	// import — decisions.md §D9's own fixture rule: the tree holds one
	// deriveSuggestions, so an "old, narrower" generation can only be
	// produced this way — so a [Repo.Rederive] over it actually finds
	// something new and mints generation 2.
	targetDoc := append(slices.Clone(testspec.DerivationDoc()[:len(testspec.DerivationDoc())-1]), []byte(`,"x-target-import":true}`)...)
	target, err := r.Import(t.Context(), ImportInput{Name: "target", Source: "upload", Document: targetDoc})
	if err != nil {
		t.Fatalf("import target: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(),
		"DELETE FROM resource_suggestions WHERE spec_id = ? AND route_family = ?", target.Spec.ID, testspec.FamilyBareItems); err != nil {
		t.Fatalf("narrow target spec's generation 1: %v", err)
	}

	result, err := r.Rederive(t.Context(), target.Spec.ID)
	if err != nil {
		t.Fatalf("rederive target: %v", err)
	}
	if !result.Changed || result.Generation != 2 {
		t.Fatalf("rederive target = %+v, want Changed:true Generation:2", result)
	}

	for _, id := range []int64{first.Spec.ID, secondRes.Spec.ID, thirdRes.Spec.ID} {
		if gens := distinctGens(t, db, id); !slices.Equal(gens, []int{1}) {
			t.Errorf("spec %d gens = %v, want exactly [1]", id, gens)
		}
	}
	if gens := distinctGens(t, db, target.Spec.ID); !slices.Equal(gens, []int{1, 2}) {
		t.Errorf("target spec gens = %v, want exactly [1 2]", gens)
	}
}

// suggestionSnapshot reads specID's resource_suggestions rows as plain
// comparable strings ("route_family:id_field:entity_schema"), sorted, so
// TestRepo_Import_GenStaysOneAcrossDifferentDocuments can assert the WHOLE
// set is byte-for-byte untouched rather than merely unchanged in count.
func suggestionSnapshot(t *testing.T, db *store.DB, specID int64) []string {
	t.Helper()
	rows, err := db.R.QueryContext(t.Context(),
		"SELECT route_family, id_field, entity_schema FROM resource_suggestions WHERE spec_id = ? ORDER BY route_family", specID)
	if err != nil {
		t.Fatalf("query resource_suggestions: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var family, idField, schema string
		if err := rows.Scan(&family, &idField, &schema); err != nil {
			t.Fatalf("scan resource_suggestions row: %v", err)
		}
		out = append(out, family+":"+idField+":"+schema)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate resource_suggestions: %v", err)
	}
	return out
}
