package scenarios_test

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/store"
)

// TestCreateFromCurrentState_allocatesDistinctEditVersion covers D4/D9's
// create-allocates rule: a freshly created scenario row does NOT carry the
// column's DEFAULT of 0 (the migration's own comment: "the DEFAULT is 0 and
// is never a live row's value"), and two scenarios created in the same
// workspace get two DIFFERENT versions — the property the whole per-
// workspace edit_seq exists to guarantee (D4's "no two LIVE rows of a
// workspace share an edit_version").
func TestCreateFromCurrentState_allocatesDistinctEditVersion(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	a, err := repo.CreateFromCurrentState(t.Context(), ws, "a")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	if a.EditVersion == 0 {
		t.Errorf("a.EditVersion = 0, want non-zero (the column DEFAULT must never be a live row's value)")
	}

	// A10 would refuse a second CreateFromCurrentState while a is active,
	// so deactivate before creating b — this test only cares that two
	// LIVE scenario rows never collide on edit_version.
	if _, err := repo.SetActive(t.Context(), ws, nil); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	b, err := repo.CreateFromCurrentState(t.Context(), ws, "b")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if b.EditVersion == 0 {
		t.Errorf("b.EditVersion = 0, want non-zero")
	}
	if a.EditVersion == b.EditVersion {
		t.Errorf("a.EditVersion == b.EditVersion == %d, want distinct versions for two live rows", a.EditVersion)
	}

	// List surfaces the same token the detail read does (D5's
	// scenarioSummaryView), not a separately-computed value.
	summaries, err := repo.List(t.Context(), ws)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[int64]int64{}
	for _, s := range summaries {
		byID[s.ID] = s.EditVersion
	}
	if byID[a.ID] != a.EditVersion {
		t.Errorf("List EditVersion for a = %d, want %d (detail read's own value)", byID[a.ID], a.EditVersion)
	}
	if byID[b.ID] != b.EditVersion {
		t.Errorf("List EditVersion for b = %d, want %d", byID[b.ID], b.EditVersion)
	}
}

// TestCloneFrom_allocatesItsOwnEditVersion pins D9's "a clone is a create":
// the cloned row must NOT carry the source row's edit_version — a caller
// holding a token read from the SOURCE must never be able to match the
// clone, which is exactly what copying the column instead of allocating a
// fresh one would produce.
func TestCloneFrom_allocatesItsOwnEditVersion(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	src, err := repo.CreateFromCurrentState(t.Context(), ws, "src")
	if err != nil {
		t.Fatalf("create src: %v", err)
	}

	// CloneFrom does not read the workspace's own layer at all (unlike
	// CreateFromCurrentState), so A10's active-scenario refusal does not
	// apply here — no need to deactivate src first (see CloneFrom's own
	// doc comment).
	clone, err := repo.CloneFrom(t.Context(), ws, src.ID, "clone")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if clone.EditVersion == 0 {
		t.Errorf("clone.EditVersion = 0, want non-zero")
	}
	if clone.EditVersion == src.EditVersion {
		t.Errorf("clone.EditVersion == src.EditVersion == %d, want a freshly allocated version distinct from the source's",
			src.EditVersion)
	}
}

// TestRenameExpecting_matchingVersionSucceedsAndAllocatesFresh is the
// straightforward compare-and-swap success path: expect equal to the row's
// current version proceeds, and the row is stamped with a NEW version
// (never expect+1 in place — D4/D9).
func TestRenameExpecting_matchingVersionSucceedsAndAllocatesFresh(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	expect := s.EditVersion

	renamed, err := repo.RenameExpecting(t.Context(), ws, s.ID, "two", &expect)
	if err != nil {
		t.Fatalf("RenameExpecting with matching version: %v", err)
	}
	if renamed.Name != "two" {
		t.Errorf("renamed.Name = %q, want two", renamed.Name)
	}
	if renamed.EditVersion == expect {
		t.Errorf("renamed.EditVersion = %d, want a FRESH version distinct from the expected %d (never expect+1 in place)",
			renamed.EditVersion, expect)
	}

	// A second call with the now-stale expect must fail — proves the row
	// really was re-stamped, not left at its old version under the hood.
	if _, err := repo.RenameExpecting(t.Context(), ws, s.ID, "three", &expect); !errors.Is(err, store.ErrEditConflict) {
		t.Errorf("RenameExpecting with the now-stale version: got %v, want store.ErrEditConflict", err)
	}
}

// TestRenameExpecting_staleVersionIsEditConflictCarryingCurrentRow is D7's
// "expected N, row present at M != N -> conflict" case, and pins the
// payload shape D8 requires: the conflict carries the row it lost to, not
// merely a boolean.
func TestRenameExpecting_staleVersionIsEditConflictCarryingCurrentRow(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stale := s.EditVersion - 1 // guaranteed not to equal the live version

	_, err = repo.RenameExpecting(t.Context(), ws, s.ID, "renamed-but-should-fail", &stale)
	if !errors.Is(err, store.ErrEditConflict) {
		t.Fatalf("RenameExpecting with a stale version: got %v, want store.ErrEditConflict", err)
	}
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("RenameExpecting error does not unwrap to *store.EditConflictError: %v", err)
	}
	if conflict.Gone {
		t.Errorf("conflict.Gone = true, want false (the row still exists)")
	}
	current, ok := conflict.Current.(*scenarios.Scenario)
	if !ok || current == nil {
		t.Fatalf("conflict.Current = %#v (%T), want a *scenarios.Scenario", conflict.Current, conflict.Current)
	}
	if current.Name != "one" {
		t.Errorf("conflict.Current.Name = %q, want the UNCHANGED name %q (the refused rename must not have taken effect)",
			current.Name, "one")
	}
	if current.EditVersion != s.EditVersion {
		t.Errorf("conflict.Current.EditVersion = %d, want the row's actual live version %d", current.EditVersion, s.EditVersion)
	}

	// The refused write must not have taken effect at all.
	got, gerr := repo.Get(t.Context(), ws, s.ID)
	if gerr != nil {
		t.Fatalf("Get after refused rename: %v", gerr)
	}
	if got.Name != "one" {
		t.Errorf("scenario name after refused rename = %q, want unchanged %q", got.Name, "one")
	}
}

// TestRenameExpecting_deletedRowIsEditConflictNotNotFound is D7/D8's
// deleted-row rule: an expectation was sent and the target row is gone ->
// ErrEditConflict (with Gone:true), never ErrNotFound. ErrNotFound stays
// reserved for the "no expectation at all" caller.
func TestRenameExpecting_deletedRowIsEditConflictNotNotFound(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "gone-soon")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	expect := s.EditVersion
	if err := repo.Delete(t.Context(), ws, s.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = repo.RenameExpecting(t.Context(), ws, s.ID, "too-late", &expect)
	if !errors.Is(err, store.ErrEditConflict) {
		t.Fatalf("RenameExpecting on a deleted row with an expectation: got %v, want store.ErrEditConflict", err)
	}
	if errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("RenameExpecting on a deleted row with an expectation must NOT answer ErrNotFound (D7's deleted-row rule)")
	}
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error does not unwrap to *store.EditConflictError: %v", err)
	}
	if !conflict.Gone {
		t.Errorf("conflict.Gone = false, want true (the row was deleted)")
	}
	if conflict.Current != nil {
		t.Errorf("conflict.Current = %#v, want nil when Gone is true", conflict.Current)
	}
}

// TestRenameExpecting_crossWorkspaceScenarioKeepsThe404 is the qualifier
// D7/D8 spend the most words on: an expectation being present must NOT turn
// a probe against somebody ELSE's scenario id into a {"gone": true}
// tombstone. Zero rows here means "not yours", and that is ErrNotFound —
// the target-row qualifier's whole point.
func TestRenameExpecting_crossWorkspaceScenarioKeepsThe404(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	wsA := insertWorkspace(t, db, "a", nil, domain.DefaultSettings())
	wsB := insertWorkspace(t, db, "b", nil, domain.DefaultSettings())

	sB, err := repo.CreateFromCurrentState(t.Context(), wsB, "b-scenario")
	if err != nil {
		t.Fatalf("create in workspace B: %v", err)
	}
	expect := sB.EditVersion

	_, err = repo.RenameExpecting(t.Context(), wsA, sB.ID, "stolen", &expect)
	if !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("RenameExpecting(A, B's scenario, B's own version): got %v, want ErrNotFound (not yours, keep the 404)", err)
	}
	if errors.Is(err, store.ErrEditConflict) {
		t.Errorf("RenameExpecting(A, B's scenario) must NOT answer store.ErrEditConflict — that would tombstone a row that exists and is not A's")
	}

	// B's row must be entirely untouched.
	gotB, gerr := repo.Get(t.Context(), wsB, sB.ID)
	if gerr != nil {
		t.Fatalf("Get workspace B's scenario after A's cross-workspace attempt: %v", gerr)
	}
	if gotB.Name != "b-scenario" || gotB.EditVersion != sB.EditVersion {
		t.Errorf("workspace B's scenario after A's failed cross-workspace RenameExpecting = %+v, want unchanged", gotB)
	}
}

// TestRenameExpecting_expectZeroOnLiveRowIsRefused pins D7's "0 is
// meaningful only for op_overrides — refused on the other three tables".
// A scenario row addressed by PUT .../scenarios/{sid} always already
// exists, so expect=0 can never legitimately match, and must answer a
// CONFLICT (not silently proceed, and not ErrNotFound either — an
// expectation was sent).
func TestRenameExpecting_expectZeroOnLiveRowIsRefused(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var zero int64

	_, err = repo.RenameExpecting(t.Context(), ws, s.ID, "should-fail", &zero)
	if !errors.Is(err, store.ErrEditConflict) {
		t.Fatalf("RenameExpecting(expect=0) on a live row: got %v, want store.ErrEditConflict", err)
	}
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) || conflict.Gone {
		t.Errorf("conflict = %+v, want Gone=false carrying the live row (the row exists, just not at version 0)", conflict)
	}

	got, gerr := repo.Get(t.Context(), ws, s.ID)
	if gerr != nil {
		t.Fatalf("Get after refused rename: %v", gerr)
	}
	if got.Name != "one" {
		t.Errorf("scenario name after refused expect=0 rename = %q, want unchanged %q", got.Name, "one")
	}
}

// TestRename_delegatesWithNoExpectationAndPerformsNoCheck pins the sibling
// contract (D8): Rename is a one-line delegation to RenameExpecting passing
// nil, so it keeps compiling and behaving exactly as it always did — in
// particular, it must NOT reject a caller for any version mismatch, because
// it makes no claim about one at all.
func TestRename_delegatesWithNoExpectationAndPerformsNoCheck(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	renamed, err := repo.Rename(t.Context(), ws, s.ID, "two")
	if err != nil {
		t.Fatalf("Rename (no expectation): %v", err)
	}
	if renamed.Name != "two" {
		t.Errorf("renamed.Name = %q, want two", renamed.Name)
	}
	// Still allocates a fresh version even though nothing was checked
	// (D4/D9: it changes `name`, a guarded field, regardless of whether a
	// check accompanied the write).
	if renamed.EditVersion == s.EditVersion {
		t.Errorf("renamed.EditVersion == original %d, want a freshly allocated version even on the unguarded path",
			s.EditVersion)
	}
}
