// orphaned_families_test.go is [Repo.OrphanedFamilies]'s own acceptance
// (mocker-p4a-triage decisions.md D5, D8 P23/P27): the ONE implementation
// of "a confirmed family the bound spec's newest suggestion generation does
// not name", set-wise by signature, and the nil-specID short circuit that
// touches no database at all.
package resources

import (
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/store"
)

// TestOrphanedFamilies_namesFamiliesTheNewestGenerationDoesNotSuggest is the
// behavioural half of P3/P23: a family the bound spec still suggests reads
// false, a family it does not — because it was never suggested at all, or
// because a re-bind moved the spec out from under it — reads true.
func TestOrphanedFamilies_namesFamiliesTheNewestGenerationDoesNotSuggest(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specA := importFixtureSpec(t, db)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	orphaned, err := repo.OrphanedFamilies(t.Context(), &specA, []string{familyWidgets, familyUsers, "/never-suggested"})
	if err != nil {
		t.Fatalf("OrphanedFamilies: %v", err)
	}
	if orphaned[familyWidgets] {
		t.Errorf("widgets orphaned = true, want false — specA suggests it")
	}
	if orphaned[familyUsers] {
		t.Errorf("users orphaned = true, want false — specA suggests it")
	}
	if !orphaned["/never-suggested"] {
		t.Errorf("/never-suggested orphaned = false, want true — no generation ever named it")
	}
}

// TestOrphanedFamilies_nilSpecID_answersEmptyMapWithoutTouchingDB is D8 P27:
// the nil-specID short circuit answers before [SpecSource.EnsureSuggestions]
// is ever called — proven by handing OrphanedFamilies a repo built over a
// CLOSED *store.DB, so any query at all would surface as an error rather
// than silently returning zero rows.
func TestOrphanedFamilies_nilSpecID_answersEmptyMapWithoutTouchingDB(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	orphaned, err := repo.OrphanedFamilies(t.Context(), nil, []string{familyWidgets, familyUsers})
	if err != nil {
		t.Fatalf("OrphanedFamilies(nil specID) over a closed db: %v", err)
	}
	if len(orphaned) != 0 {
		t.Fatalf("OrphanedFamilies(nil specID) = %v, want an empty map", orphaned)
	}
}

// TestOrphanedFamilies_emptyFamilies_answersEmptyMap is the degenerate
// input case beside P27's degenerate specID case: an empty roster answers
// an empty map without erroring, regardless of what the spec suggests.
func TestOrphanedFamilies_emptyFamilies_answersEmptyMap(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specA := importFixtureSpec(t, db)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	orphaned, err := repo.OrphanedFamilies(t.Context(), &specA, nil)
	if err != nil {
		t.Fatalf("OrphanedFamilies: %v", err)
	}
	if len(orphaned) != 0 {
		t.Fatalf("OrphanedFamilies(no families) = %v, want an empty map", orphaned)
	}
}

// TestResetData_Reseed_AgreesWithOrphanedFamiliesAfterRederive is D8 P5's
// own mutation clause, from this package's side: reset-data's `stranded`
// classification and [Repo.OrphanedFamilies] must name the SAME family
// after a rederive mints a narrower generation over the SAME spec — no
// re-bind at all — because both read through the identical predicate
// (D5/R1). A divergent second implementation of the predicate (a raw
// SELECT against resource_suggestions with no gen filter, the shape D5
// forbids) would pass every OTHER test in this file while failing this one,
// because it is the only place that puts the reseed loop's own answer next
// to a second reader's.
func TestResetData_Reseed_AgreesWithOrphanedFamiliesAfterRederive(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specA := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specA, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// Mint a narrower generation over the SAME spec (rederive's own shape,
	// P3f) — no rebindSpec call, so workspaces.spec_id never moves. A
	// second gen-1 row naming only /users, /gadgets, /notes strands
	// /widgets without a re-bind, exactly D3.1's "both leave a stored row
	// the current spec no longer answers for" pairing.
	insertNarrowerGeneration(t, db, specA)

	orphaned, err := repo.OrphanedFamilies(t.Context(), &specA, []string{familyWidgets})
	if err != nil {
		t.Fatalf("OrphanedFamilies: %v", err)
	}
	if !orphaned[familyWidgets] {
		t.Fatalf("OrphanedFamilies reports widgets NOT orphaned after rederive, want orphaned")
	}

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != familyWidgets || out.Skipped[0].Reason != skipReasonStranded {
		t.Fatalf("Skipped = %+v, want [{%s stranded}] — reset-data's own OrphanedFamilies-backed loop must agree with the call above", out.Skipped, familyWidgets)
	}
}

// insertNarrowerGeneration writes resource_suggestions gen 2 over specID,
// naming every family fixtureDoc declares EXCEPT /widgets — the hand-built
// equivalent of a real [specs.Repo.Rederive] call whose new generation
// drops one family, without pulling in internal/specs' own derivation
// pipeline for one test.
func insertNarrowerGeneration(t *testing.T, db *store.DB, specID int64) {
	t.Helper()
	for _, family := range []string{familyUsers, familyGadgets, "/notes"} {
		if _, err := db.W.ExecContext(t.Context(), `
			INSERT INTO resource_suggestions (spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
			VALUES (?, 2, ?, ?, 'id', '/dummy', '{}', 1.0)`,
			specID, family, family); err != nil {
			t.Fatalf("insert generation-2 suggestion row for %q: %v", family, err)
		}
	}
}
