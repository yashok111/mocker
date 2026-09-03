package customep_test

// Tests for A3's per-row compare-and-swap on custom_endpoints: UpdateExpecting's
// five-case check (D7), the GONE-vs-NOT-YOURS split on the absent-row branch
// (D7/D8's "target row" qualifier), Create's allocation, and ReplaceAllTx's
// per-row re-allocation on restore (D9's "dominant restore path" warning).

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/store"
)

// TestRepo_Create_allocatesDistinctEditVersion pins D4/D9's "creates
// allocate too": two rows created in the same workspace must never share an
// edit_version, and a freshly created row must never sit at the column's
// DEFAULT of 0 (D4: "0 is never a live row's value").
func TestRepo_Create_allocatesDistinctEditVersion(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	a, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(a): %v", err)
	}
	b, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/b"})
	if err != nil {
		t.Fatalf("Create(b): %v", err)
	}

	if a.EditVersion == 0 {
		t.Error("Create(a).EditVersion = 0, want a nonzero allocated value")
	}
	if b.EditVersion == 0 {
		t.Error("Create(b).EditVersion = 0, want a nonzero allocated value")
	}
	if a.EditVersion == b.EditVersion {
		t.Errorf("two live rows of the same workspace share edit_version %d", a.EditVersion)
	}
}

// TestRepo_UpdateExpecting_nilExpectationSkipsCheck proves Update's
// delegation still behaves exactly as it always has: no expectation, no
// check, edit runs through regardless of the row's current edit_version.
func TestRepo_UpdateExpecting_nilExpectationSkipsCheck(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	updated, err := repo.UpdateExpecting(t.Context(), wsID, created.ID, nil, func(cur *customep.Row) error {
		cur.RouteOff = true
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateExpecting(nil): %v", err)
	}
	if !updated.RouteOff {
		t.Error("RouteOff not applied under a nil expectation")
	}
}

// TestRepo_UpdateExpecting_matchProceedsAndAllocatesFresh is the middle
// case of the five: expect == current.EditVersion proceeds, and the write
// stamps a FRESH version, never expect+1 in place (D4/D9).
func TestRepo_UpdateExpecting_matchProceedsAndAllocatesFresh(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	expect := created.EditVersion
	updated, err := repo.UpdateExpecting(t.Context(), wsID, created.ID, &expect, func(cur *customep.Row) error {
		cur.RouteOff = true
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateExpecting(match): %v", err)
	}
	if updated.EditVersion == created.EditVersion {
		t.Errorf("EditVersion after a matched update = %d, want a NEW value distinct from %d (never expect+1 in place)",
			updated.EditVersion, created.EditVersion)
	}
	if updated.EditVersion == 0 {
		t.Error("EditVersion after a matched update = 0, want a nonzero allocated value")
	}
}

// TestRepo_UpdateExpecting_mismatchIsEditConflictWithCurrent is D7's
// "expected N, row present at M != N" case: the write is refused, and the
// conflict carries the row it lost to so the handler can build a wire
// payload from it.
func TestRepo_UpdateExpecting_mismatchIsEditConflictWithCurrent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	stale := created.EditVersion + 999
	_, err = repo.UpdateExpecting(t.Context(), wsID, created.ID, &stale, func(cur *customep.Row) error {
		cur.RouteOff = true
		return nil
	})
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("UpdateExpecting(mismatch) err = %v, want *store.EditConflictError", err)
	}
	if conflict.Gone {
		t.Error("Gone = true on a version mismatch against a row that still exists, want false")
	}
	current, ok := conflict.Current.(customep.Row)
	if !ok {
		t.Fatalf("conflict.Current = %T, want customep.Row", conflict.Current)
	}
	if current.ID != created.ID || current.EditVersion != created.EditVersion {
		t.Errorf("conflict.Current = %+v, want the row as it stands now (id %d, version %d)",
			current, created.ID, created.EditVersion)
	}

	// The write must not have happened.
	reread, err := repo.Get(t.Context(), wsID, created.ID)
	if err != nil {
		t.Fatalf("Get() after refused update: %v", err)
	}
	if reread.RouteOff {
		t.Error("RouteOff = true after a refused update, the mutate closure must not have applied")
	}
}

// TestRepo_UpdateExpecting_zeroIsRefusedOnALiveRow pins D7's rule that is
// "the rule most likely to be silently unimplemented": unlike op_overrides,
// 0 is never a legal expectation on custom_endpoints because a row always
// exists by the time its edit route is reachable. Expecting 0 against a
// live row must conflict, not proceed as a stealth INSERT-style bypass.
func TestRepo_UpdateExpecting_zeroIsRefusedOnALiveRow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	zero := int64(0)
	_, err = repo.UpdateExpecting(t.Context(), wsID, created.ID, &zero, func(cur *customep.Row) error {
		return nil
	})
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("UpdateExpecting(expect=0, live row) err = %v, want *store.EditConflictError", err)
	}
	if conflict.Gone {
		t.Error("Gone = true, want false: the row exists, it is only the expectation that is wrong")
	}
}

// TestRepo_UpdateExpecting_deletedRowIsEditConflictGone is D7's overridden
// 404: with an expectation sent against a row that has been deleted, the
// answer is ErrEditConflict{Gone:true}, not the plain ErrNotFound Update
// alone would give (proven by TestRepo_UpdateExpecting_nilNoExpectation_missingIsErrNotFound below).
func TestRepo_UpdateExpecting_deletedRowIsEditConflictGone(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := repo.Delete(t.Context(), wsID, created.ID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	expect := created.EditVersion
	_, err = repo.UpdateExpecting(t.Context(), wsID, created.ID, &expect, func(cur *customep.Row) error {
		return nil
	})
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("UpdateExpecting(deleted row) err = %v, want *store.EditConflictError", err)
	}
	if !conflict.Gone {
		t.Error("Gone = false, want true: the target row no longer exists anywhere")
	}
	if conflict.Current != nil {
		t.Errorf("Current = %v, want nil when Gone is true", conflict.Current)
	}
}

// TestRepo_UpdateExpecting_nilNoExpectation_missingIsErrNotFound proves the
// "no expectation" half of D7's overriding rule still stands: without an
// expectation, a deleted row answers the plain ErrNotFound Update always
// gave, not the new EditConflictError.
func TestRepo_UpdateExpecting_nilNoExpectation_missingIsErrNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := repo.Delete(t.Context(), wsID, created.ID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}

	_, err = repo.UpdateExpecting(t.Context(), wsID, created.ID, nil, func(cur *customep.Row) error {
		return nil
	})
	if !errors.Is(err, customep.ErrNotFound) {
		t.Errorf("UpdateExpecting(nil, deleted row) err = %v, want ErrNotFound", err)
	}
	var conflict *store.EditConflictError
	if errors.As(err, &conflict) {
		t.Error("got *store.EditConflictError with no expectation sent, want the plain ErrNotFound")
	}
}

// TestRepo_UpdateExpecting_crossWorkspaceIdKeepsErrNotFound is D7/D8's
// "target row" qualifier, driven directly at this package rather than
// assumed from the scenarios package's identical rule: an id that belongs
// to a DIFFERENT workspace must answer the plain 404 (ErrNotFound), never
// the {"gone": true} tombstone that would falsely tell the caller its own
// endpoint was deleted and the retry is a create.
func TestRepo_UpdateExpecting_crossWorkspaceIdKeepsErrNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	ownerWsID := insertWorkspace(t, db, "owner")
	otherWsID := insertWorkspace(t, db, "other")

	created, err := repo.Create(t.Context(), ownerWsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	expect := created.EditVersion
	_, err = repo.UpdateExpecting(t.Context(), otherWsID, created.ID, &expect, func(cur *customep.Row) error {
		return nil
	})
	if !errors.Is(err, customep.ErrNotFound) {
		t.Errorf("UpdateExpecting() across workspaces err = %v, want ErrNotFound (the caller's row was never touched, it just isn't this one)", err)
	}
	var conflict *store.EditConflictError
	if errors.As(err, &conflict) {
		t.Error("got *store.EditConflictError for a row belonging to a DIFFERENT workspace, want the plain ErrNotFound — " +
			"a Gone:true tombstone here would falsely tell the caller its own endpoint was deleted")
	}

	// And the row under its actual owner is untouched.
	reread, err := repo.Get(t.Context(), ownerWsID, created.ID)
	if err != nil {
		t.Fatalf("Get() under owning workspace: %v", err)
	}
	if reread.EditVersion != created.EditVersion {
		t.Errorf("EditVersion = %d after a cross-workspace UpdateExpecting call, want unchanged %d",
			reread.EditVersion, created.EditVersion)
	}
}

// TestReplaceAllTx_reallocatesEditVersionOnRestore is D9's "dominant
// restore path" regression: a live row also present in the snapshot is
// UPDATEd in place through upsertTx's ON CONFLICT clause, which does not
// reset the token to anything unless this package explicitly allocates one.
// Without that allocation, a caller holding the pre-rollback token would be
// accepted against a row whose content the restore just replaced.
func TestReplaceAllTx_reallocatesEditVersionOnRestore(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	live, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	preRestoreVersion := live.EditVersion

	snapshot := []*customep.Row{
		{Method: "GET", Path: "/a", OverrideOn: true, ActiveStatus: 204},
	}
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return customep.ReplaceAllTx(t.Context(), tx, wsID, snapshot, time.Now().UTC())
	}); err != nil {
		t.Fatalf("ReplaceAllTx(): %v", err)
	}

	restored, err := repo.Get(t.Context(), wsID, live.ID)
	if err != nil {
		t.Fatalf("Get() after restore: %v", err)
	}
	if restored.EditVersion == 0 {
		t.Error("EditVersion after restore = 0, want a freshly allocated nonzero value")
	}
	if restored.EditVersion == preRestoreVersion {
		t.Errorf("EditVersion after restore = %d, unchanged from the pre-restore value %d — "+
			"a caller holding the OLD token would be wrongly accepted against the restored content",
			restored.EditVersion, preRestoreVersion)
	}

	// The pre-restore token must now be refused, proving the restore's
	// re-allocation actually protects a caller who read before it ran.
	stale := preRestoreVersion
	_, err = repo.UpdateExpecting(t.Context(), wsID, live.ID, &stale, func(cur *customep.Row) error {
		return nil
	})
	var conflict *store.EditConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("UpdateExpecting(pre-restore token) err = %v, want *store.EditConflictError", err)
	}
}

// TestReplaceAllTx_insertedRowsGetDistinctEditVersions covers the other half
// of D9: a row absent live and present in the snapshot is a fresh INSERT,
// and it must land on a version distinct from every other live row of the
// workspace, not the column DEFAULT of 0.
func TestReplaceAllTx_insertedRowsGetDistinctEditVersions(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	live, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	snapshot := []*customep.Row{
		{Method: "GET", Path: "/a", OverrideOn: true, ActiveStatus: 200},
		{Method: "GET", Path: "/b", OverrideOn: true, ActiveStatus: 200},
	}
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return customep.ReplaceAllTx(t.Context(), tx, wsID, snapshot, time.Now().UTC())
	}); err != nil {
		t.Fatalf("ReplaceAllTx(): %v", err)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ForWorkspace() = %d rows, want 2", len(all))
	}
	if all[0].EditVersion == 0 || all[1].EditVersion == 0 {
		t.Errorf("restored rows carry a zero EditVersion: %+v", all)
	}
	if all[0].EditVersion == all[1].EditVersion {
		t.Errorf("two rows restored in the same call share edit_version %d", all[0].EditVersion)
	}
	_ = live
}
