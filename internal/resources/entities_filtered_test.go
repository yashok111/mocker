// Tests for [Repo.ListFiltered] (D12 of mocker-a4-mcp-reach) — the
// structured, paginated, scope-filtered read D4's admin route serves. This
// file owns the fixture D4 names as the one every clause of its own Shape
// is read against: a confirmed NESTED family with at least two live parent
// rows (so at least two distinct scope_key values carry rows) and more than
// nine rows in one of those scopes — which needs a non-default listSize of
// at least 10, and is why [confirmDeepChain]'s own six call sites (all
// listSize=2) cannot be reused unchanged for the cursor property below.
package resources

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/testspec"
)

// scopePtr is [ListFiltered]'s own "pin this axis" spelling — a nil
// *ScopeKey means "any", so a test that wants to pin a filter, including to
// the empty scope, needs the address of a value rather than the bare
// ScopeKey [Repo.List] takes.
func scopePtr(s ScopeKey) *ScopeKey { return &s }

// confirmDeepTeamsLarge confirms FamilyDeepOrgs at listSize=2 (giving two
// live parent rows, so two distinct team scopes) and then, ONLY after that
// confirm, raises the workspace's listSize to teamsListSize before
// confirming FamilyDeepTeams — the same "raise listSize between two
// confirms" technique
// TestResetData_Reseed_NestedGroupAtomicity_ChildOverCaps (nested_test.go)
// already uses, because SeedCount is frozen per-family at ITS OWN confirm
// time (reset.go's own comment), not at the workspace's current setting.
// teamsListSize must be >= 10 for the cursor property below to have a
// ninth and a tenth row to compare; the caller states it explicitly rather
// than this helper hard-coding it, so a future property that wants a
// different size does not have to fork the helper.
func confirmDeepTeamsLarge(t *testing.T, teamsListSize int) (repo *Repo, org, teams *Resource, wsID int64) {
	t.Helper()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID = insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo = newTestRepo(t, db, 64<<20, 64<<10)

	var err error
	org, err = repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs)
	if err != nil {
		t.Fatalf("confirm %q: %v", testspec.FamilyDeepOrgs, err)
	}
	mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: teamsListSize})
	teams, err = repo.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams)
	if err != nil {
		t.Fatalf("confirm %q: %v", testspec.FamilyDeepTeams, err)
	}
	return repo, org, teams, wsID
}

// TestListFiltered_WildcardScope_ReturnsEveryScope is the fixture's own
// sanity check: a nil scope filter (base pinned to the empty base scope
// every unparameterised basePath computes) returns every row of the
// family, across both parent scopes — proving the fixture actually built
// two distinct scopes with rows in each before the narrower tests below
// rely on it.
func TestListFiltered_WildcardScope_ReturnsEveryScope(t *testing.T) {
	t.Parallel()
	repo, org, teams, _ := confirmDeepTeamsLarge(t, 10)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("orgs = %d, want 2 (two live parent rows)", len(orgEntities))
	}

	all, err := repo.ListFiltered(t.Context(), teams.ID, nil, nil, 0, 1000)
	if err != nil {
		t.Fatalf("ListFiltered wildcard base+scope: %v", err)
	}
	if len(all) != 20 {
		t.Fatalf("ListFiltered wildcard = %d rows, want 20 (2 org scopes * 10 teams)", len(all))
	}
	for _, e := range all {
		if e.BaseScopeKey != "" {
			t.Fatalf("entity %q has BaseScopeKey %q, want \"\" (unparameterised basePath)", e.EntityKey, e.BaseScopeKey)
		}
	}
}

// TestListFiltered_ScopeFilter_ExcludesOtherScope is D4's own Fails-if:
// "a request with scopeKey=X returns a row belonging to the fixture's
// OTHER scope" must not happen — pinning scope to one org's tuple must
// return exactly that org's ten team rows, none of the other org's ten.
func TestListFiltered_ScopeFilter_ExcludesOtherScope(t *testing.T) {
	t.Parallel()
	repo, org, teams, _ := confirmDeepTeamsLarge(t, 10)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	scopeA := EncodeScope([]string{orgEntities[0].EntityKey})
	scopeB := EncodeScope([]string{orgEntities[1].EntityKey})

	rowsA, err := repo.ListFiltered(t.Context(), teams.ID, nil, scopePtr(scopeA), 0, 1000)
	if err != nil {
		t.Fatalf("ListFiltered scope A: %v", err)
	}
	if len(rowsA) != 10 {
		t.Fatalf("scope A rows = %d, want 10", len(rowsA))
	}
	for _, e := range rowsA {
		if e.ScopeKey != string(scopeA) {
			t.Fatalf("scope-A-filtered row has ScopeKey %q, want %q — a row from the other scope leaked through", e.ScopeKey, scopeA)
		}
	}

	rowsB, err := repo.ListFiltered(t.Context(), teams.ID, nil, scopePtr(scopeB), 0, 1000)
	if err != nil {
		t.Fatalf("ListFiltered scope B: %v", err)
	}
	if len(rowsB) != 10 {
		t.Fatalf("scope B rows = %d, want 10", len(rowsB))
	}
	seenA := map[string]bool{}
	for _, e := range rowsA {
		seenA[e.EntityKey] = true
	}
	for _, e := range rowsB {
		if seenA[e.EntityKey] {
			t.Fatalf("entity_key %q present in BOTH scope-A and scope-B results — scope filter did not exclude the other scope", e.EntityKey)
		}
	}
}

// TestListFiltered_Cursor_PastNinthRow_OrdersByID is D4's cursor
// Fails-if, read against exactly the row count the Shape calls for: a page
// requested with `after` set past the ninth row must not omit or reorder a
// row with a higher numeric entityKey. Scope A's ten rows mint entity_key
// "1".."10" (one family-wide counter, this org's scope populated in one
// pass) — the ninth row's key is the TEXT string "9", the tenth's is "10",
// and "10" < "9" under SQLite's BINARY collation. A cursor built from a CAST
// of entity_key would still return the row (same query, ordered
// differently) — this test's OWN ordering assertion is what a CAST-based
// cursor fails: it would place "10" before rows with keys that sort higher
// as text but lower as a CAST integer, corrupting the ORDER BY this method
// promises just as much as omitting rows would.
func TestListFiltered_Cursor_PastNinthRow_OrdersByID(t *testing.T) {
	t.Parallel()
	repo, org, teams, _ := confirmDeepTeamsLarge(t, 10)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	scopeA := EncodeScope([]string{orgEntities[0].EntityKey})

	full, err := repo.ListFiltered(t.Context(), teams.ID, nil, scopePtr(scopeA), 0, 1000)
	if err != nil {
		t.Fatalf("ListFiltered full page: %v", err)
	}
	if len(full) != 10 {
		t.Fatalf("scope A rows = %d, want 10", len(full))
	}
	// full is ordered by id ASC (ListFiltered's own contract). The ninth
	// row (index 8) is the fixture's own entity_key "9": a family-wide
	// counter minting scope A's ten rows in one population pass gives
	// consecutive ids "1".."10" in that exact order.
	ninth := full[8]
	tenth := full[9]
	if ninth.EntityKey != "9" || tenth.EntityKey != "10" {
		t.Fatalf("fixture precondition broken: rows 9,10 = %q,%q, want \"9\",\"10\" (needed for the TEXT-collation property this test checks)", ninth.EntityKey, tenth.EntityKey)
	}

	page, err := repo.ListFiltered(t.Context(), teams.ID, nil, scopePtr(scopeA), ninth.ID, 1000)
	if err != nil {
		t.Fatalf("ListFiltered after cursor: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page after the ninth row = %d rows, want exactly 1 (the tenth) — omission or reordering past the id cursor", len(page))
	}
	if page[0].ID != tenth.ID || page[0].EntityKey != tenth.EntityKey {
		t.Fatalf("row after cursor = id %d entityKey %q, want id %d entityKey %q", page[0].ID, page[0].EntityKey, tenth.ID, tenth.EntityKey)
	}

	// A cursor at 0 (never a live id) returns the whole page again, from
	// the start — afterID is never optional.
	fromStart, err := repo.ListFiltered(t.Context(), teams.ID, nil, scopePtr(scopeA), 0, 1000)
	if err != nil {
		t.Fatalf("ListFiltered afterID=0: %v", err)
	}
	if len(fromStart) != 10 {
		t.Fatalf("ListFiltered afterID=0 = %d rows, want 10 (the whole scope, from the start)", len(fromStart))
	}
}

// TestListFiltered_Limit_BoundsPageSize is the companion to the cursor
// test above: limit bounds how many rows come back, in the same SQL the
// cursor predicate lives in (D12's own Fails-if names both as one code
// path). The clamp-to-a-ceiling CONVENTION itself belongs to D4's admin
// handler and its own test, over a population above 500 — this is only
// the repository's own contract: whatever limit it is given, it returns no
// more than that many rows.
func TestListFiltered_Limit_BoundsPageSize(t *testing.T) {
	t.Parallel()
	repo, org, teams, _ := confirmDeepTeamsLarge(t, 10)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	scopeA := EncodeScope([]string{orgEntities[0].EntityKey})

	page, err := repo.ListFiltered(t.Context(), teams.ID, nil, scopePtr(scopeA), 0, 3)
	if err != nil {
		t.Fatalf("ListFiltered limit=3: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("ListFiltered limit=3 = %d rows, want 3", len(page))
	}
	wantKeys := []string{"1", "2", "3"}
	for i, e := range page {
		if e.EntityKey != wantKeys[i] {
			t.Fatalf("row %d entityKey = %q, want %q (id ASC order, unaffected by the limit)", i, e.EntityKey, wantKeys[i])
		}
	}
}

// TestListFiltered_ResourceGone_ErrResourceGone is the ErrResourceGone
// contract [Repo.List] and [Repo.Get] already carry, extended to
// [Repo.ListFiltered]: a resource id that no longer names a live resources
// row answers ErrResourceGone, never a bare empty page — R37's distinction
// between "declined out from under a parked request" and "legitimately
// empty" applies here exactly as it does on the two existing methods.
func TestListFiltered_ResourceGone_ErrResourceGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "acme"); err != nil {
		t.Fatalf("decline: %v", err)
	}

	if _, err := repo.ListFiltered(t.Context(), res.ID, nil, nil, 0, 100); !errors.Is(err, ErrResourceGone) {
		t.Fatalf("ListFiltered over a declined resource = %v, want ErrResourceGone", err)
	}
}

// TestListFiltered_BaseFilter_ExcludesOtherBase and its wildcard sibling
// below exercise the OTHER axis ListFiltered's WHERE conditionally adds:
// base_scope_key. The nested-family fixture above never varies this axis
// (an unparameterised basePath, D3.1's empty tuple on every row), so this
// reuses [TestList_Get_Create_Delete_DisjointAcrossBaseValues]'s own
// fixture — a non-nested family confirmed under a parameterised basePath
// with two declared values — the same one D12's sibling decision (D4)
// points to for this shape.
func TestListFiltered_BaseFilter_ExcludesOtherBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{declaredBaseA, declaredBaseB},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	baseA, baseB := ScopeKey(declaredBaseA), ScopeKey(declaredBaseB)

	rowsA, err := repo.ListFiltered(t.Context(), res.ID, scopePtr(baseA), nil, 0, 100)
	if err != nil {
		t.Fatalf("ListFiltered base A: %v", err)
	}
	if len(rowsA) != 3 {
		t.Fatalf("base A rows = %d, want 3", len(rowsA))
	}
	for _, e := range rowsA {
		if e.BaseScopeKey != string(baseA) {
			t.Fatalf("base-A-filtered row has BaseScopeKey %q, want %q — a row from the other base leaked through", e.BaseScopeKey, baseA)
		}
	}

	rowsB, err := repo.ListFiltered(t.Context(), res.ID, scopePtr(baseB), nil, 0, 100)
	if err != nil {
		t.Fatalf("ListFiltered base B: %v", err)
	}
	if len(rowsB) != 3 {
		t.Fatalf("base B rows = %d, want 3", len(rowsB))
	}
	seenA := map[string]bool{}
	for _, e := range rowsA {
		seenA[e.EntityKey] = true
	}
	for _, e := range rowsB {
		if seenA[e.EntityKey] {
			t.Fatalf("entity_key %q present in BOTH base-A and base-B results — base filter did not exclude the other base", e.EntityKey)
		}
	}

	wildcard, err := repo.ListFiltered(t.Context(), res.ID, nil, nil, 0, 100)
	if err != nil {
		t.Fatalf("ListFiltered wildcard base: %v", err)
	}
	if len(wildcard) != 6 {
		t.Fatalf("ListFiltered wildcard base = %d rows, want 6 (both declared bases)", len(wildcard))
	}
}
