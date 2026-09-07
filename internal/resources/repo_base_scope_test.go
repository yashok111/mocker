// repo_base_scope_test.go: P3h, the declared base-value set (D18/D14's
// P1-P4, P9-P11) — List/Get/Create/Delete disjoint across base values, one
// row set populated per declared base value, an empty declared set
// refusing, the cap enforced ACROSS the declared set, and the config fence
// catching a stale basePathValues. Split out of the former repo_test.go
// (package resources, not resources_test — see helpers_test.go's own
// comment for why).
package resources

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/testkit"
)

// TestList_Get_Create_Delete_DisjointAcrossBaseValues is P1/P2/P3 together:
// two requests differing only in a declared base value answer disjoint row
// sets (P1), a write into one base value is invisible in another (P2), and
// a keyed request (Get/Delete) addressed under one declared base value
// cannot reach a row that belongs to another (P3).
func TestList_Get_Create_Delete_DisjointAcrossBaseValues(t *testing.T) {
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{declaredBaseA, declaredBaseB},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	baseA, baseB := ScopeKey(declaredBaseA), ScopeKey(declaredBaseB)

	// P1: the two declared base values answer disjoint row sets, each of
	// the confirm's own listSize (3).
	entitiesA, err := repo.List(t.Context(), res.ID, baseA, "")
	if err != nil {
		t.Fatalf("List base A: %v", err)
	}
	entitiesB, err := repo.List(t.Context(), res.ID, baseB, "")
	if err != nil {
		t.Fatalf("List base B: %v", err)
	}
	if len(entitiesA) != 3 || len(entitiesB) != 3 {
		t.Fatalf("len(entitiesA)=%d len(entitiesB)=%d, want 3 and 3", len(entitiesA), len(entitiesB))
	}
	keysA := map[string]bool{}
	for _, e := range entitiesA {
		keysA[e.EntityKey] = true
	}
	for _, e := range entitiesB {
		if keysA[e.EntityKey] {
			t.Fatalf("entity_key %q present under BOTH base A and base B — the two collections must be disjoint", e.EntityKey)
		}
	}

	// P2: a POST into base A is invisible in base B, and B's collection is
	// byte-identical to what it was before.
	created, err := repo.Create(t.Context(), res.ID, baseA, "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "into-A"})
	if err != nil {
		t.Fatalf("Create into base A: %v", err)
	}
	entitiesBAfter, err := repo.List(t.Context(), res.ID, baseB, "")
	if err != nil {
		t.Fatalf("List base B after write: %v", err)
	}
	if len(entitiesBAfter) != len(entitiesB) {
		t.Fatalf("base B collection changed after a write into base A: len=%d, want unchanged %d", len(entitiesBAfter), len(entitiesB))
	}
	for i := range entitiesB {
		if entitiesBAfter[i].EntityKey != entitiesB[i].EntityKey || string(entitiesBAfter[i].Data) != string(entitiesB[i].Data) {
			t.Fatalf("base B row %d changed after a write into base A", i)
		}
	}
	entitiesAAfter, err := repo.List(t.Context(), res.ID, baseA, "")
	if err != nil {
		t.Fatalf("List base A after write: %v", err)
	}
	found := false
	for _, e := range entitiesAAfter {
		if e.EntityKey == created.EntityKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("the row created under base A does not appear in base A's own collection")
	}

	// P3: a keyed request (Get/Delete) addressed under one declared base
	// value cannot reach a row that belongs to another. entity_key is ONE
	// counter across the whole family (D6.2), so the same key never exists
	// under both base scopes at once — the defect this property observes is
	// a cross-value REACH, not a same-key collision.
	victimKey := entitiesA[0].EntityKey
	if _, ok, err := repo.Get(t.Context(), res.ID, baseB, "", victimKey); err != nil || ok {
		t.Fatalf("Get(baseB, key=%q) = ok=%v err=%v, want ok=false (the key belongs to base A)", victimKey, ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), res.ID, baseB, "", victimKey); err != nil || deleted {
		t.Fatalf("Delete(baseB, key=%q) = deleted=%v err=%v, want deleted=false", victimKey, deleted, err)
	}
	// The row survives byte-identical under its OWN base value.
	if _, ok, err := repo.Get(t.Context(), res.ID, baseA, "", victimKey); err != nil || !ok {
		t.Fatalf("Get(baseA, key=%q) after the refused cross-base delete = ok=%v err=%v, want ok=true", victimKey, ok, err)
	}
	// And the identical requests through base A's OWN url succeed.
	if _, ok, err := repo.Get(t.Context(), res.ID, baseA, "", victimKey); err != nil || !ok {
		t.Fatalf("Get(baseA, key=%q) = ok=%v err=%v, want ok=true", victimKey, ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), res.ID, baseA, "", victimKey); err != nil || !deleted {
		t.Fatalf("Delete(baseA, key=%q) = deleted=%v err=%v, want deleted=true", victimKey, deleted, err)
	}
}

// TestConfirm_PopulatesOneRowSetPerDeclaredBaseValue is P4: a confirm over
// THREE declared base values holds 3*listSize rows, listSize under each of
// the three base scope keys.
func TestConfirm_PopulatesOneRowSetPerDeclaredBaseValue(t *testing.T) {
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	values := []string{"1", "2", "3"}
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 4, BasePath: "/orgs/{orgId}", BasePathValues: values,
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 3*4 {
		t.Fatalf("total entity count = %d, want %d (3 declared values x listSize 4)", got, 3*4)
	}
	for _, v := range values {
		n, err := repo.List(t.Context(), res.ID, ScopeKey(v), "")
		if err != nil {
			t.Fatalf("List base %q: %v", v, err)
		}
		if len(n) != 4 {
			t.Errorf("base %q holds %d rows, want listSize 4 — a declared value with no rows fails this property", v, len(n))
		}
	}
}

// TestConfirm_EmptyDeclaredSetRefuses is P9: basePath carries a parameter
// and basePathValues is empty -> 409 base_scope_undeclared, and the confirm
// writes NOTHING — no resources row, no resource_decisions row, no entity.
func TestConfirm_EmptyDeclaredSetRefuses(t *testing.T) {
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: nil,
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrBaseScopeUndeclared) {
		t.Fatalf("Confirm with an empty declared set = %v, want ErrBaseScopeUndeclared", err)
	}
	if n := resourceRowCount(t, db, wsID, familyWidgets); n != 0 {
		t.Fatalf("a resources row was written despite the refusal: %d", n)
	}
	if state, ok := decisionState(t, db, wsID, familyWidgets); ok {
		t.Fatalf("a resource_decisions row was written despite the refusal: state=%q", state)
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("%d entity rows were written despite the refusal", total)
	}
}

// TestConfirm_PopulationExceedingCapAcrossDeclaredSetRefuses is P10: the
// cap check sees the DECLARED-SET factor, not just listSize^depth. Six
// declared values, a top-level family, listSize clamped to its own ceiling
// of 200 (clampSeedCount) -> 6*200=1200 > 1000, refused with no partial
// write — the top-level analogue of the worked example decisions.md D6.3
// gives for a depth-1 nested family, over this package's own default cap
// of 1000.
func TestConfirm_PopulationExceedingCapAcrossDeclaredSetRefuses(t *testing.T) {
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 200, BasePath: "/orgs/{orgId}", BasePathValues: []string{"1", "2", "3", "4", "5", "6"},
	})
	repo := newTestRepo(t, db, 64<<20, 64<<10) // generous byte caps: only the ROW cap must bite

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("Confirm over 1200 prepared rows = %v, want ErrEntityLimit", err)
	}
	if n := resourceRowCount(t, db, wsID, familyWidgets); n != 0 {
		t.Fatalf("a resources row was written despite the cap refusal: %d", n)
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("%d entity rows were written despite the cap refusal — the declared-set factor was not seen by the cap check", total)
	}
}

// TestFenceConfirmTx_CatchesStaleBasePathValues is P11: a settings edit
// that changes basePathValues, landing INSIDE the write transaction's own
// window (through confirmPreWriteHook — never before prepareConfirm's own
// read, which the pre-transaction fence already catches and proves
// nothing about the IN-TRANSACTION one), is caught with 409 stale_config —
// and this runs WHILE A SCENARIO IS ACTIVE, because D6.5 requires
// basePath/basePathValues to join the UNCONDITIONAL half of the fence,
// never the `!scenarioID.Valid` branch guarding seed/listSize: a fixture
// with no scenario active would pass even with the wrong placement.
func TestFenceConfirmTx_CatchesStaleBasePathValues(t *testing.T) {
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	settings := domain.Settings{Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7"}}
	wsID := insertWorkspace(t, db, "alpha", &specID, settings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	scenarioID := insertScenario(t, db, wsID, "s1", settings)
	activateScenario(t, db, wsID, scenarioID)

	confirmPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{
			Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7", "8"},
		})
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("Confirm with basePathValues changed inside the window = %v, want ErrStaleConfig", err)
	}
	if n := resourceRowCount(t, db, wsID, familyWidgets); n != 0 {
		t.Fatalf("a resources row was written despite the fence refusal: %d", n)
	}
}
