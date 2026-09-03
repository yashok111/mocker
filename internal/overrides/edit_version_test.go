package overrides_test

// Tests for A3's per-row compare-and-swap over internal/overrides: the
// five-case rule (D7) as implemented by PutExpecting/PutManyExpecting, the
// allocator's never-reused guarantee as it interacts with ReplaceAllTx's
// restore path (D9), and the delegating verbs' "no expectation" behaviour.

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

func i64p(v int64) *int64 { return &v }

func noopMutate(*overrides.Row) error { return nil }

// TestPutExpecting_nilIsUnchecked covers Put's own delegation and any other
// caller that passes nil: no check runs, and the write still allocates a
// fresh edit_version (D9's criterion is about the fields written, not about
// whether a check happened).
func TestPutExpecting_nilIsUnchecked(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "nil-expect")
	key := overrides.OpKey("GET", "/a")

	first, _, err := repo.PutExpecting(t.Context(), wsID, key, nil, func(row *overrides.Row) error {
		row.RouteOff = true
		return nil
	})
	if err != nil {
		t.Fatalf("PutExpecting(nil): %v", err)
	}
	if first.EditVersion == 0 {
		t.Error("EditVersion = 0 after an insert, want a nonzero allocated value")
	}

	// A second nil-expectation write proceeds even though a real token
	// exists and is not the one supplied (there is none supplied at all).
	second, _, err := repo.PutExpecting(t.Context(), wsID, key, nil, func(row *overrides.Row) error {
		row.RouteOff = false
		return nil
	})
	if err != nil {
		t.Fatalf("PutExpecting(nil) second call: %v", err)
	}
	if second.EditVersion == first.EditVersion {
		t.Errorf("EditVersion = %d, want a fresh value distinct from %d", second.EditVersion, first.EditVersion)
	}
}

// TestPutExpecting_zeroAgainstNoRow covers "expected 0, no row present:
// proceed, INSERT at a freshly allocated version" (D7).
func TestPutExpecting_zeroAgainstNoRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "zero-no-row")
	key := overrides.OpKey("GET", "/b")

	stored, _, err := repo.PutExpecting(t.Context(), wsID, key, i64p(0), noopMutate)
	if err != nil {
		t.Fatalf("PutExpecting(expect=0, no row): %v", err)
	}
	if stored.EditVersion == 0 {
		t.Error("EditVersion = 0, want a freshly allocated nonzero value")
	}
}

// TestPutExpecting_zeroAgainstLiveRow covers "expected 0, row present:
// conflict" — the row EXISTS and expect=0 means "I expect none of it",
// which this table refuses rather than ignores (D7).
func TestPutExpecting_zeroAgainstLiveRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "zero-live-row")
	key := overrides.OpKey("GET", "/c")

	created, _, err := repo.Put(t.Context(), wsID, key, noopMutate)
	if err != nil {
		t.Fatalf("seed Put(): %v", err)
	}

	_, _, err = repo.PutExpecting(t.Context(), wsID, key, i64p(0), noopMutate)
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("PutExpecting(expect=0, live row) error = %v, want *store.EditConflictError", err)
	}
	if conflict.Gone {
		t.Error("Gone = true, want false: the row exists")
	}
	current, ok := conflict.Current.(overrides.Row)
	if !ok {
		t.Fatalf("Current = %T, want overrides.Row", conflict.Current)
	}
	if current.EditVersion != created.EditVersion {
		t.Errorf("Current.EditVersion = %d, want %d", current.EditVersion, created.EditVersion)
	}
	if !errors.Is(err, store.ErrEditConflict) {
		t.Error("errors.Is(err, store.ErrEditConflict) = false, want true")
	}
}

// TestPutExpecting_matchingVersionAllocatesFresh covers "expected N, row
// present at N: proceed, write at a FRESHLY ALLOCATED version (never N+1 in
// place)" — the freshly-allocated half is the property an in-place +1 would
// also satisfy by coincidence on a single write, so this asserts against the
// allocator directly (D7).
func TestPutExpecting_matchingVersionAllocatesFresh(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "matching-version")
	key := overrides.OpKey("GET", "/d")

	created, _, err := repo.Put(t.Context(), wsID, key, noopMutate)
	if err != nil {
		t.Fatalf("seed Put(): %v", err)
	}

	updated, _, err := repo.PutExpecting(t.Context(), wsID, key, i64p(created.EditVersion), func(row *overrides.Row) error {
		row.RouteOff = true
		return nil
	})
	if err != nil {
		t.Fatalf("PutExpecting(expect=matching): %v", err)
	}
	if updated.EditVersion == created.EditVersion {
		t.Errorf("EditVersion = %d, want a fresh value distinct from %d", updated.EditVersion, created.EditVersion)
	}
	if !updated.RouteOff {
		t.Error("RouteOff = false, want true: the write must have actually applied")
	}
}

// TestPutExpecting_mismatchedVersionConflicts covers "expected N, row
// present at M != N: conflict" (D7).
func TestPutExpecting_mismatchedVersionConflicts(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "mismatched-version")
	key := overrides.OpKey("GET", "/e")

	created, _, err := repo.Put(t.Context(), wsID, key, noopMutate)
	if err != nil {
		t.Fatalf("seed Put(): %v", err)
	}

	_, _, err = repo.PutExpecting(t.Context(), wsID, key, i64p(created.EditVersion+999), noopMutate)
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("PutExpecting(expect=stale) error = %v, want *store.EditConflictError", err)
	}
	if conflict.Gone {
		t.Error("Gone = true, want false: the row exists, just at a different version")
	}
	current, ok := conflict.Current.(overrides.Row)
	if !ok {
		t.Fatalf("Current = %T, want overrides.Row", conflict.Current)
	}
	if current.EditVersion != created.EditVersion {
		t.Errorf("Current.EditVersion = %d, want %d (unchanged by the refused write)", current.EditVersion, created.EditVersion)
	}
}

// TestPutExpecting_expectedVersionAgainstDeletedRow covers "expected N, no
// row present: conflict, not not-found" — D7's target-row qualifier: the row
// this caller named was deleted under it, which is a lost update rather than
// a missing resource, and overrides.Repo has no old 404 here for this to
// override (Put has always seeded a blank row on absence).
func TestPutExpecting_expectedVersionAgainstDeletedRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "deleted-row")
	key := overrides.OpKey("GET", "/f")

	created, _, err := repo.Put(t.Context(), wsID, key, noopMutate)
	if err != nil {
		t.Fatalf("seed Put(): %v", err)
	}
	if _, _, err := repo.Delete(t.Context(), wsID, key); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	_, _, err = repo.PutExpecting(t.Context(), wsID, key, i64p(created.EditVersion), noopMutate)
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("PutExpecting(expect=N, deleted row) error = %v, want *store.EditConflictError", err)
	}
	if !conflict.Gone {
		t.Error("Gone = false, want true: the row was deleted out from under the caller")
	}
	if errors.Is(err, overrides.ErrNotFound) {
		t.Error("errors.Is(err, overrides.ErrNotFound) = true, want false: D7 overrides not-found with a conflict here")
	}
}

// TestPutExpecting_workspaceNotFoundBeatsTheCheck covers D7's qualifier from
// the other direction: a missing WORKSPACE is not "the target row", so it
// keeps answering ErrWorkspaceNotFound even when an expectation is present
// and the row is absent for exactly that reason.
func TestPutExpecting_workspaceNotFoundBeatsTheCheck(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)

	_, _, err := repo.PutExpecting(t.Context(), 999999, overrides.OpKey("GET", "/g"), i64p(1), noopMutate)
	if !errors.Is(err, overrides.ErrWorkspaceNotFound) {
		t.Errorf("err = %v, want ErrWorkspaceNotFound", err)
	}
	var conflict *store.EditConflictError
	if errors.As(err, &conflict) {
		t.Error("errors.As found an EditConflictError, want none: a missing workspace is not the target row")
	}
}

// TestReplaceAllTx_restoreOverAnExistingRowAllocatesFreshVersion covers D9's
// dominant restore-path fix: ReplaceAllTx's upsert is INSERT ... ON CONFLICT
// DO UPDATE, and a column the SET list does not name survives untouched — so
// a caller holding a pre-restore token must NOT still match the row after a
// restore replaces its content, even though the row's PRIMARY KEY and
// natural key are unchanged.
func TestReplaceAllTx_restoreOverAnExistingRowAllocatesFreshVersion(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "restore-fresh-version")
	key := overrides.OpKey("GET", "/h")

	created, _, err := repo.Put(t.Context(), wsID, key, func(row *overrides.Row) error {
		row.RouteOff = false
		return nil
	})
	if err != nil {
		t.Fatalf("seed Put(): %v", err)
	}
	preRestoreVersion := created.EditVersion

	// Restore the SAME (workspace, method, path) row through ReplaceAllTx,
	// as internal/checkpoints' rollback would, with content that DOES
	// change (RouteOff flips) -- the dominant restore case.
	snapshot := []*overrides.Row{{
		WorkspaceID: wsID,
		Method:      "GET",
		Path:        "/h",
		OverrideOn:  true,
		RouteOff:    true,
		Responses:   map[string]overrides.Variant{},
	}}
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return overrides.ReplaceAllTx(t.Context(), tx, wsID, snapshot, time.Now().UTC())
	}); err != nil {
		t.Fatalf("ReplaceAllTx(): %v", err)
	}

	restored, err := repo.Get(t.Context(), wsID, key)
	if err != nil {
		t.Fatalf("Get() after restore: %v", err)
	}
	if restored.EditVersion == preRestoreVersion {
		t.Errorf("EditVersion = %d after restore, want a fresh value distinct from the pre-restore %d",
			restored.EditVersion, preRestoreVersion)
	}
	if !restored.RouteOff {
		t.Error("RouteOff = false after restore, want true: the restore's own content must have applied")
	}

	// The stale, pre-restore token must now be refused as a conflict, not
	// silently accepted.
	_, _, err = repo.PutExpecting(t.Context(), wsID, key, i64p(preRestoreVersion), noopMutate)
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("PutExpecting(pre-restore token) error = %v, want *store.EditConflictError", err)
	}
}

// TestPutManyExpecting_nilIsUnchecked mirrors PutMany's own delegation: nil
// means no check, and every written row still allocates a fresh version,
// returned keyed by opKey.
func TestPutManyExpecting_nilIsUnchecked(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "putmany-nil")

	rows := []*overrides.Row{
		{Method: "GET", Path: "/x", OverrideOn: true, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/y", OverrideOn: true, Responses: map[string]overrides.Variant{}},
	}
	versions, _, err := repo.PutManyExpecting(t.Context(), wsID, nil, func(map[string]*overrides.Row) ([]*overrides.Row, error) {
		return rows, nil
	})
	if err != nil {
		t.Fatalf("PutManyExpecting(nil): %v", err)
	}
	for _, key := range []string{overrides.OpKey("GET", "/x"), overrides.OpKey("GET", "/y")} {
		if versions[key] == 0 {
			t.Errorf("versions[%q] = 0, want a freshly allocated nonzero value", key)
		}
	}
}

// TestPutManyExpecting_perKeyFiveCaseCheck exercises the map-valued check
// (D8/D12) against a workspace holding one live row: a matching key
// proceeds, a stale key is collected into the conflict's Current map keyed
// by opKey (nil for a key whose row is entirely absent), and the whole call
// is refused on ANY single mismatch — not just the mismatching key's own
// write.
func TestPutManyExpecting_perKeyFiveCaseCheck(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "putmany-five-case")
	liveKey := overrides.OpKey("GET", "/live")
	absentKey := overrides.OpKey("GET", "/absent")

	live, _, err := repo.Put(t.Context(), wsID, liveKey, noopMutate)
	if err != nil {
		t.Fatalf("seed Put(): %v", err)
	}

	merge := func(map[string]*overrides.Row) ([]*overrides.Row, error) {
		return []*overrides.Row{{Method: "GET", Path: "/live", OverrideOn: true, RouteOff: true, Responses: map[string]overrides.Variant{}}}, nil
	}

	// One key matches, one key names a row that does not exist at all:
	// the whole call is refused, and the conflict's Current names ONLY the
	// mismatching key, with a nil value for the absent one.
	expect := map[string]int64{
		liveKey:   live.EditVersion,
		absentKey: 7,
	}
	_, _, err = repo.PutManyExpecting(t.Context(), wsID, expect, merge)
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("PutManyExpecting(mixed) error = %v, want *store.EditConflictError", err)
	}
	stale, ok := conflict.Current.(map[string]*int64)
	if !ok {
		t.Fatalf("Current = %T, want map[string]*int64", conflict.Current)
	}
	if len(stale) != 1 {
		t.Fatalf("stale has %d entries, want 1 (only the mismatching key): %+v", len(stale), stale)
	}
	v, present := stale[absentKey]
	if !present {
		t.Fatalf("stale[%q] missing, want an entry for the absent row's key", absentKey)
	}
	if v != nil {
		t.Errorf("stale[%q] = %v, want nil (GONE)", absentKey, *v)
	}
	if _, present := stale[liveKey]; present {
		t.Errorf("stale contains %q, want only the mismatching key present", liveKey)
	}

	// The live row must be UNCHANGED: the whole call was refused, not
	// partially applied.
	untouched, err := repo.Get(t.Context(), wsID, liveKey)
	if err != nil {
		t.Fatalf("Get() after refused PutManyExpecting: %v", err)
	}
	if untouched.RouteOff {
		t.Error("RouteOff = true, want false: the refused call must not have written anything")
	}
	if untouched.EditVersion != live.EditVersion {
		t.Errorf("EditVersion = %d, want unchanged %d", untouched.EditVersion, live.EditVersion)
	}

	// Now with both expectations correct, the call proceeds.
	expect[absentKey] = 0
	versions, _, err := repo.PutManyExpecting(t.Context(), wsID, expect, merge)
	if err != nil {
		t.Fatalf("PutManyExpecting(correct expectations): %v", err)
	}
	if versions[liveKey] == live.EditVersion {
		t.Errorf("versions[%q] = %d, want a fresh value distinct from %d", liveKey, versions[liveKey], live.EditVersion)
	}
}
