// repo_decision_test.go: the decision row and its guards (D13 clauses 12,
// 13, 30, 37, 39, 43, 48) — confirm/decline lifecycle, an unknown family, a
// rederive dropping a family, decline's concurrency guard, confirm's caps
// on GENERATED population, the config fence (via the hook and via
// fenceConfirmTx directly), id-type agreement between Create and Confirm,
// and confirm's atomicity under an injected entity-insert failure. Split
// out of the former repo_test.go (package resources, not resources_test —
// see helpers_test.go's own comment for why).
package resources

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/testspec"
)

func TestDecisionLifecycle(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// Declining an UNCONFIRMED suggestion writes state='declined' and it
	// stays declined across a reload (a second read of the row).
	if err := repo.Decline(t.Context(), wsID, familyUsers, ""); err != nil {
		t.Fatalf("Decline (unconfirmed, no slug needed): %v", err)
	}
	if state, ok := decisionState(t, db, wsID, familyUsers); !ok || state != "declined" {
		t.Fatalf("decision state = %q, ok=%v, want declined", state, ok)
	}
	if state, ok := decisionState(t, db, wsID, familyUsers); !ok || state != "declined" {
		t.Fatalf("decision state on reload = %q, ok=%v, want still declined", state, ok)
	}

	// Confirming writes the decision row too.
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if state, ok := decisionState(t, db, wsID, familyWidgets); !ok || state != "confirmed" {
		t.Fatalf("decision state = %q, ok=%v, want confirmed", state, ok)
	}

	// Declining a CONFIRMED resource without a slug changes nothing.
	if err := repo.Decline(t.Context(), wsID, familyWidgets, ""); !errors.Is(err, ErrConfirmSlugRequired) {
		t.Fatalf("Decline without slug = %v, want ErrConfirmSlugRequired", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 1 {
		t.Fatalf("resource row was removed despite the refused decline")
	}

	// With the WRONG slug, same refusal, same no-op.
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "not-alpha"); !errors.Is(err, ErrConfirmSlugMismatch) {
		t.Fatalf("Decline with wrong slug = %v, want ErrConfirmSlugMismatch", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 1 {
		t.Fatalf("resource row was removed despite the mismatched slug")
	}

	// Declining a confirmed one WITH the right slug deletes the resource
	// and its entities (ON DELETE CASCADE).
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "alpha"); err != nil {
		t.Fatalf("Decline with correct slug: %v", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("resource row still present after a correctly-slugged decline")
	}
	if entityCount(t, db, res.ID) != 0 {
		t.Fatalf("entities still present after their resource was declined")
	}
	if state, ok := decisionState(t, db, wsID, familyWidgets); !ok || state != "declined" {
		t.Fatalf("decision state = %q, ok=%v, want declined", state, ok)
	}
}

func TestDecline_UnknownFamily(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// No spec attached, no resources row: neither a suggestion nor a
	// resource can name this family.
	if err := repo.Decline(t.Context(), wsID, "/nonexistent", ""); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Decline unknown family = %v, want ErrUnknownFamily", err)
	}
}

func TestConfirm_UnknownFamily(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, "/nonexistent"); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Confirm unknown family = %v, want ErrUnknownFamily", err)
	}
}

// TestConfirm_And_ResetData_AfterRederiveDropsFamily is P3f's own decisions.md
// §D9 P4: [findSuggestion] owns no query of its own — it reads through
// specs.EnsureSuggestions and so inherits [internal/specs]' single "newest
// generation only" predicate (§D3.2, §D13.3) with no second, driftable
// implementation here. Once a rederive's newest generation no longer names
// familyWidgets, a Confirm of it is refused exactly like an unknown family
// (§D6.1), and an already-confirmed familyWidgets is classified `stranded`
// by the next reseed (§D6.2) — the same closed reason
// TestResetData_Reseed_OneStrandedOneRepopulated already produces by
// REBINDING the workspace to a narrower spec; this test produces the
// identical outcome by minting a narrower NEWEST GENERATION over the SAME
// spec instead, which is the one path this slice adds.
//
// The narrowing itself is direct SQL, not a second rederive: decisions.md
// §D9's fixture rule ("no production seam is added to make derivation
// configurable") applies here exactly as it does in internal/specs' own
// tests — [internal/specs.Repo.Rederive] itself is exercised by that
// package, not re-derived a second way from this one.
func TestConfirm_And_ResetData_AfterRederiveDropsFamily(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// familyWidgets is confirmed BEFORE the drop, so the reseed half below
	// has an already-confirmed family to classify; familyUsers is left
	// unconfirmed, so the confirm half below has a family to refuse —
	// D6.1 and D6.2 are two different consequences of the same drop and
	// each needs its own family to observe cleanly.
	widgets, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets before drop: %v", err)
	}

	// Narrow the spec by MINTING GENERATION 2 without the two families and
	// LEAVING generation 1 standing — the state a real
	// [internal/specs.Repo.Rederive] call leaves behind, which adds a
	// generation and never deletes the superseded one.
	//
	// Copying rather than DELETING is load-bearing for what this test is
	// FOR, and the run that shipped this slice measured the difference:
	// the first version narrowed by deleting generation 1's two rows, and
	// under P4's own mutation — a second, gen-blind SELECT inside
	// [findSuggestion], the §D13.3 divergence the property forbids — the
	// test stayed GREEN. With the rows physically gone, a read WITHOUT the
	// newest-generation predicate and a read WITH it both return nothing,
	// so the two implementations are indistinguishable and every assertion
	// below holds against either. Leaving the stale generation in place is
	// exactly what a gen-blind SELECT trips over: it resolves familyWidgets
	// from the superseded rows, the Confirm below succeeds instead of
	// refusing, and the test goes red.
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO resource_suggestions
			(spec_id, gen, route_family, name, id_field, parent_family, entity_schema, wrapper, confidence)
		SELECT spec_id, 2, route_family, name, id_field, parent_family, entity_schema, wrapper, confidence
		  FROM resource_suggestions
		 WHERE spec_id = ? AND gen = 1 AND route_family NOT IN (?, ?)`,
		specID, familyWidgets, familyUsers); err != nil {
		t.Fatalf("mint generation 2 without %s and %s: %v", familyWidgets, familyUsers, err)
	}

	// D6.1: a confirm of the dropped, never-confirmed family now fails
	// exactly like an unknown one.
	if _, err := repo.Confirm(t.Context(), wsID, familyUsers); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Confirm dropped family = %v, want ErrUnknownFamily", err)
	}

	// D6.2: reset-data's reseed classifies the already-confirmed family
	// `stranded` — the existing closed reason, never a fifth one.
	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false — the only confirmed family was skipped")
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != familyWidgets || out.Skipped[0].Reason != skipReasonStranded {
		t.Fatalf("Skipped = %+v, want [{%s stranded}]", out.Skipped, familyWidgets)
	}
	if got := entityCount(t, db, widgets.ID); got != 2 {
		t.Errorf("entity count = %d, want 2 (unchanged — the row was left standing)", got)
	}
}

func TestConfirm_AlreadyConfirmed(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("first Confirm: %v", err)
	}
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrAlreadyConfirmed) {
		t.Fatalf("second Confirm = %v, want ErrAlreadyConfirmed", err)
	}
}

// TestDecline_ConcurrencyGuard is D13 clause 43: one goroutine confirming
// and one declining without a slug, racing on the SAME family, must leave
// either a confirmed resource with ALL its entities or no resource at all —
// never a resource whose entities are gone. Both outcomes are legal
// (whichever transaction the single writer connection serializes first);
// only a torn state is a failure.
func TestDecline_ConcurrencyGuard(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 4})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = repo.Confirm(t.Context(), wsID, familyWidgets)
	}()
	go func() {
		defer wg.Done()
		_ = repo.Decline(t.Context(), wsID, familyWidgets, "") // no slug: refused if a resource exists by then
	}()
	wg.Wait()

	n := resourceRowCount(t, db, wsID, familyWidgets)
	switch n {
	case 0:
		// Declined (or never confirmed): nothing to check further.
	case 1:
		var resourceID int64
		if err := db.R.QueryRowContext(t.Context(), "SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", wsID, familyWidgets).Scan(&resourceID); err != nil {
			t.Fatalf("read resource id: %v", err)
		}
		if got := entityCount(t, db, resourceID); got != 4 {
			t.Fatalf("torn state: resource exists with %d entities, want the full seed_count 4", got)
		}
	default:
		t.Fatalf("resource row count = %d, want 0 or 1", n)
	}
}

// TestConfirm_CapsHoldOnGeneratedPopulation is the gap the acceptance
// report named: every OTHER Confirm test in this file uses a generous
// 4<<20-byte budget, so none of them ever drives generation over a cap.
// This one dials maxResponseBytes down to a few hundred bytes and clamps
// ListSize to its own ceiling (200 — clampSeedCount's own max, comfortably
// below defaultMaxEntityRows' 1000, which is why the ROW cap is not
// Confirm-reachable at all through seed_count alone and this test targets
// the byte-total cap instead) so [Repo.checkBatchCaps] — called from
// [Repo.prepareConfirm] at repo.go's own "if err := r.checkBatchCaps(bodies)"
// line, BEFORE the write transaction ever opens — is what refuses, not a
// hand-called checkBatchCaps in isolation the way TestCheckBatchCaps
// exercises it.
func TestConfirm_CapsHoldOnGeneratedPopulation(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 200})
	// entityByteCap = maxResponseBytes/2 = 200 bytes total — 200 generated
	// widget bodies comfortably exceed that, well before the (unreachable
	// here) row cap of 1000 could ever matter.
	repo := newTestRepo(t, db, 400, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("Confirm over the byte-total cap = %v, want ErrEntityLimit", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("a resources row survived a refused confirm")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entities survived a refused confirm: %d rows", total)
	}
	// checkBatchCaps refuses BEFORE the write transaction (which is what
	// writes the decision row) even opens — a refused confirm must leave
	// no decision row behind either, unlike a refusal reached INSIDE the
	// transaction (D13 clause 18 territory).
	if state, ok := decisionState(t, db, wsID, familyWidgets); ok {
		t.Fatalf("decision state = %q, want no decision row written for a confirm refused before its transaction opened", state)
	}

	// A retry with room to spare succeeds cleanly, proving the refusal
	// above really was the byte cap and not a permanent defect in the
	// fixture or the workspace.
	repo2 := newTestRepo(t, db, 4<<20, 64<<10)
	if _, err := repo2.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("retry Confirm with a generous cap: %v", err)
	}
}

// TestConfirm_StaleConfigViaHook closes the gap TestFenceConfirmTx_*
// above leaves: those two call fenceConfirmTx directly through the
// runFenceTx helper, which cannot tell a Confirm that still calls it at its
// real call site (repo.go's write transaction, right after fenceConfirmTx's
// own line) from one that silently stopped. [confirmPreWriteHook] fires in
// the exact window D4/R36 describes — after prepareConfirm's generation
// half returns, before the write transaction opens — so mutating settings
// from inside it races Confirm's OWN fence, not a bespoke stand-in for it.
func TestConfirm_StaleConfigViaHook(t *testing.T) {
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	confirmPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{Seed: 99, ListSize: 3})
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("Confirm racing a settings change = %v, want ErrStaleConfig", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("a resources row survived a stale-config confirm")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entities survived a stale-config confirm: %d rows", total)
	}

	// A retry with the hook cleared, against the now-settled settings,
	// succeeds — the race really was the cause, not a permanent defect.
	confirmPreWriteHook = confirmPreWriteHookNoop
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("retry Confirm after settings settled: %v", err)
	}
}

// TestCreateAndConfirm_AgreeOnIDType is D13 clause 48, through the REAL
// Repo end to end — never a fake store, which is all the two existing
// citations (mockplane's TestResourceServePost_BodyIsStoreOutputVerbatim
// and this package's own TestConfirm_NonIDFieldWorks_IntegerShape) ever
// exercised: a POST-created row and a Confirm-generated row, for the SAME
// family, must decode "id" as the SAME JSON type. Run once for /widgets (a
// bare-array 200) and once for /gadgets (a wrapped 200) — clause 48's own
// "wrapper carries the types and nothing else".
func TestCreateAndConfirm_AgreeOnIDType(t *testing.T) {
	t.Parallel()
	// The third row is the STRING-id half, and it is why this table has
	// three rather than two: the first version ran a bare array and a
	// wrapped 200 and BOTH declared id as {type: integer}, so clause 48's
	// own second sentence — "in a family whose id is a string, both decode
	// as a string" — was held by nothing but gen.CoerceIDValue's pure unit
	// test, which the acceptance walk had already ruled insufficient alone.
	// An idType that coerced every id to a number would have passed both
	// integer families.
	//
	// It comes from internal/testspec rather than from this file's own
	// inline document because that is where D13 clause 8 puts the
	// derivation fixture, and /bareitems there already declares exactly the
	// shape this needs: a bare array whose item id is {type: string}.
	for _, tc := range []struct {
		family string
		doc    []byte
		want   string
	}{
		{familyWidgets, []byte(fixtureDoc), "number"},
		{familyGadgets, []byte(fixtureDoc), "number"},
		{testspec.FamilyBareItems, testspec.DerivationDoc(), "string"},
	} {
		family := tc.family
		t.Run(family, func(t *testing.T) {
			t.Parallel()
			db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
			specID := importSpecDoc(t, db, tc.doc)
			wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
			repo := newTestRepo(t, db, 4<<20, 64<<10)

			res, err := repo.Confirm(t.Context(), wsID, family)
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			entities, err := repo.List(t.Context(), res.ID, "", "")
			if err != nil || len(entities) == 0 {
				t.Fatalf("List = %v, %v, want at least one confirm-generated entity", entities, err)
			}
			var confirmGenerated map[string]any
			if err := jsonx.Unmarshal(entities[0].Data, &confirmGenerated); err != nil {
				t.Fatalf("decode confirm-generated entity: %v", err)
			}
			confirmIDType := jsonIDType(confirmGenerated["id"])

			created, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "posted"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var postCreated map[string]any
			if err := jsonx.Unmarshal(created.Data, &postCreated); err != nil {
				t.Fatalf("decode POST-created entity: %v", err)
			}
			postIDType := jsonIDType(postCreated["id"])

			if confirmIDType != postIDType {
				t.Fatalf("%s: confirm-generated id decodes as %s, POST-created id decodes as %s, want the same JSON type", family, confirmIDType, postIDType)
			}
			// Agreement alone is not the clause: two sides that both got
			// it wrong agree perfectly. The type must be the one the SPEC
			// declares, and the expectation is written into the table
			// rather than read back off the resource row — a row whose
			// idType was itself derived wrong would otherwise agree with
			// the bodies it produced.
			if confirmIDType != tc.want {
				t.Fatalf("%s: id decodes as %s, want %s (declared idType %q)", family, confirmIDType, tc.want, res.Wrapper.IDType)
			}
		})
	}
}

func TestConfirm_AtomicOnEntityInsertFailure(t *testing.T) {
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 5})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// Fail INSIDE the write transaction, at entity 3 of 5 — after the
	// resources row and two entities were inserted in this same
	// transaction, so a non-atomic implementation would leave a partial
	// resource behind.
	confirmEntityHook = func(i, n int) error {
		if i == 2 {
			return fmt.Errorf("injected failure at entity %d of %d", i, n)
		}
		return nil
	}
	t.Cleanup(func() { confirmEntityHook = confirmEntityHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err == nil {
		t.Fatalf("Confirm succeeded despite the injected mid-transaction failure")
	}

	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("a resources row survived a failed confirm")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entities survived a failed confirm: %d rows", total)
	}

	// A retry with the hook cleared succeeds cleanly — proving the failure
	// really was injected inside the transaction, not a permanent defect.
	confirmEntityHook = confirmEntityHookNoop
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("retry Confirm: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 5 {
		t.Fatalf("entity count after retry = %d, want 5", got)
	}
}

func TestFenceConfirmTx_CatchesStaleSeedAndListSize(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{Seed: 1, ListSize: 5})
	before := mustReadCore(t, db, wsID)

	tests := []struct {
		name     string
		mutate   func()
		wantSame bool
	}{
		{"unchanged", func() {}, true},
		{"seed changed", func() {
			mustSetSettings(t, db, wsID, domain.Settings{Seed: 2, ListSize: 5})
		}, false},
		{"listSize changed", func() {
			mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 6})
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 5})
			tt.mutate()
			err := runFenceTx(t, db, wsID, before, 1, 5)
			same := err == nil
			if same != tt.wantSame {
				t.Fatalf("fenceConfirmTx same=%v err=%v, want same=%v", same, err, tt.wantSame)
			}
		})
	}
}

func TestFenceConfirmTx_IncarnationVsBareRevisionBump(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{Seed: 1, ListSize: 5})
	before := mustReadCore(t, db, wsID)

	// A bare revision bump — a second family's confirm, or an anonymous
	// POST {prefix}/state — must NOT refuse.
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return store.BumpRevisionTx(t.Context(), tx, wsID, time.Now())
	}); err != nil {
		t.Fatalf("bump revision: %v", err)
	}
	if err := runFenceTx(t, db, wsID, before, 1, 5); err != nil {
		t.Fatalf("fenceConfirmTx after a bare revision bump = %v, want nil", err)
	}

	// A workspace deleted and re-created with the SAME id (created_at
	// differs) refuses with ErrStaleConfig.
	if _, err := db.W.ExecContext(t.Context(), "DELETE FROM workspaces WHERE id = ?", wsID); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (id, slug, name, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, '{}', ?, ?)`,
		wsID, "alpha", "alpha", time.Now().Unix()+1, time.Now().Unix()+1); err != nil {
		t.Fatalf("re-create workspace: %v", err)
	}
	if err := runFenceTx(t, db, wsID, before, 1, 5); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("fenceConfirmTx after workspace re-creation = %v, want ErrStaleConfig", err)
	}
}
