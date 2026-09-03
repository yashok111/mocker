package customep_test

import (
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(), mirroring
// internal/overrides/repo_test.go's harness.
func newTestDB(t *testing.T) *store.DB {
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

// insertWorkspace writes a minimal workspaces row directly, exactly as
// internal/overrides/repo_test.go does — this package owns no workspace
// logic and must not import internal/workspaces.
func insertWorkspace(t *testing.T, db *store.DB, slug string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, revision, settings, created_at, updated_at)
		VALUES (?, ?, 1, '{}', unixepoch(), unixepoch())`,
		slug, slug,
	)
	if err != nil {
		t.Fatalf("insert workspace %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

func workspaceRevision(t *testing.T, db *store.DB, id int64) int64 {
	t.Helper()
	var rev int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT revision FROM workspaces WHERE id = ?", id).Scan(&rev); err != nil {
		t.Fatalf("read revision for workspace %d: %v", id, err)
	}
	return rev
}

func TestRepo_Create_insertThenReadBack(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	in := &customep.Row{
		Method:       "post",
		Path:         "/widgets/{id}",
		OverrideOn:   true,
		ActiveStatus: 201,
		Responses: map[string]overrides.Variant{
			"201": {Mode: "generated"},
		},
	}
	stored, err := repo.Create(t.Context(), wsID, in)
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if stored.ID == 0 {
		t.Error("stored.ID is zero, want an assigned id")
	}
	if stored.Method != http.MethodPost {
		t.Errorf("Method = %q, want upper-cased POST", stored.Method)
	}
	if stored.CanonicalPath != "/widgets/{}" {
		t.Errorf("CanonicalPath = %q, want /widgets/{}", stored.CanonicalPath)
	}
	if stored.WorkspaceID != wsID {
		t.Errorf("WorkspaceID = %d, want %d", stored.WorkspaceID, wsID)
	}

	got, err := repo.Get(t.Context(), wsID, stored.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got.Path != "/widgets/{id}" {
		t.Errorf("Get().Path = %q, want /widgets/{id}", got.Path)
	}
	if got.ActiveStatus != 201 {
		t.Errorf("Get().ActiveStatus = %d, want 201", got.ActiveStatus)
	}
	if _, ok := got.Responses["201"]; !ok {
		t.Errorf("Get().Responses missing 201, got %v", got.Responses)
	}
}

func TestRepo_Create_defaultActiveStatus(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	stored, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/z"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if stored.ActiveStatus != 200 {
		t.Errorf("ActiveStatus = %d, want the column's default 200", stored.ActiveStatus)
	}
}

// TestRepo_Create_defaultsOverrideOn is a REGRESSION test, and the defect it
// pins shipped: Row.OverrideOn is a plain bool, both callers created rows
// without mentioning it, Create wrote override_on = 0, and runtime.go leaves
// such a row out of the route table — so an endpoint an operator had just
// created answered 404, with every unit test green because none of them served
// a request. The default belongs to the repo (the column is DEFAULT 1 and
// internal/overrides/repo.go builds new rows the same way), which is why this
// asserts on Create's own result rather than on any handler.
func TestRepo_Create_defaultsOverrideOn(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	stored, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/z"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if !stored.OverrideOn {
		t.Error("OverrideOn = false on a freshly created endpoint; a row created switched off never enters the route table, so the endpoint answers 404")
	}

	reread, err := repo.Get(t.Context(), wsID, stored.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if !reread.OverrideOn {
		t.Error("OverrideOn = false after a read back from the database; the default must be persisted, not synthesised by Create")
	}
}

// TestRepo_ForWorkspace_orderedBySourceOrderThenID covers the task's
// "list ordering by source_order then id" case: Create assigns source_order
// as max+1, so three Creates on the same workspace come back in creation
// order, and ForWorkspace must not silently reorder them by, say, path.
func TestRepo_ForWorkspace_orderedBySourceOrderThenID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	paths := []string{"/z-first", "/a-second", "/m-third"}
	wantIDs := make([]int64, 0, len(paths))
	for _, p := range paths {
		stored, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: p})
		if err != nil {
			t.Fatalf("Create(%s): %v", p, err)
		}
		wantIDs = append(wantIDs, stored.ID)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != len(paths) {
		t.Fatalf("ForWorkspace() = %d rows, want %d", len(all), len(paths))
	}
	for i, row := range all {
		if row.ID != wantIDs[i] {
			t.Errorf("row %d: ID = %d, want %d (creation order via source_order)", i, row.ID, wantIDs[i])
		}
		if row.SourceOrder != int64(i) {
			t.Errorf("row %d: SourceOrder = %d, want %d", i, row.SourceOrder, i)
		}
	}
}

func TestRepo_Create_bumpsRevisionByExactlyOne(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	before := workspaceRevision(t, db, wsID)
	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"}); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	after := workspaceRevision(t, db, wsID)
	if after != before+1 {
		t.Errorf("revision after Create = %d, want %d", after, before+1)
	}
}

// TestRepo_Delete_bumpsRevision covers the task's explicit reverse-bug case:
// a deleted endpoint that keeps serving until some unrelated edit bumps the
// revision is the same bug as a missed bump on Create.
func TestRepo_Delete_bumpsRevision(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	stored, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	before := workspaceRevision(t, db, wsID)

	if err := repo.Delete(t.Context(), wsID, stored.ID); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	after := workspaceRevision(t, db, wsID)
	if after != before+1 {
		t.Errorf("revision after Delete = %d, want %d", after, before+1)
	}

	if _, err := repo.Get(t.Context(), wsID, stored.ID); !errors.Is(err, customep.ErrNotFound) {
		t.Errorf("Get() after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestRepo_Delete_missingIsErrNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	before := workspaceRevision(t, db, wsID)
	err := repo.Delete(t.Context(), wsID, 999)
	if !errors.Is(err, customep.ErrNotFound) {
		t.Fatalf("Delete(missing) err = %v, want ErrNotFound", err)
	}
	if got := workspaceRevision(t, db, wsID); got != before {
		t.Errorf("revision after a no-op Delete = %d, want unchanged %d", got, before)
	}
}

func TestRepo_Get_missing(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	if _, err := repo.Get(t.Context(), wsID, 42); !errors.Is(err, customep.ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}
}

func TestRepo_Create_workspaceNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)

	_, err := repo.Create(t.Context(), 9999, &customep.Row{Method: "GET", Path: "/a"})
	if !errors.Is(err, customep.ErrWorkspaceNotFound) {
		t.Errorf("Create() err = %v, want ErrWorkspaceNotFound", err)
	}
}

func TestRepo_Delete_workspaceNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)

	err := repo.Delete(t.Context(), 9999, 1)
	if !errors.Is(err, customep.ErrWorkspaceNotFound) {
		t.Errorf("Delete() err = %v, want ErrWorkspaceNotFound", err)
	}
}

// TestRepo_Create_conflict covers ErrConflict on a second endpoint sharing
// (method, canonical_path) — including the case the literal Path text
// differs ("/a/{id}" vs "/a/{name}") but canonicalizes to the same "/a/{}",
// which is exactly the collision DESIGN §8 calls a real conflict between two
// custom endpoints.
func TestRepo_Create_conflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a/{id}"}); err != nil {
		t.Fatalf("first Create(): %v", err)
	}

	tests := []struct {
		name string
		row  *customep.Row
	}{
		{"identical literal path", &customep.Row{Method: "GET", Path: "/a/{id}"}},
		{"different param name, same canonical path", &customep.Row{Method: "GET", Path: "/a/{name}"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.Create(t.Context(), wsID, tc.row)
			if !errors.Is(err, customep.ErrConflict) {
				t.Errorf("Create() err = %v, want ErrConflict", err)
			}
		})
	}
}

// TestRepo_Create_sameCanonicalPathDifferentMethodAllowed proves the
// UNIQUE index is scoped to (workspace, method, canonical_path), not just
// (workspace, canonical_path): GET and POST on the same path are two
// independent endpoints, never a clash.
func TestRepo_Create_sameCanonicalPathDifferentMethodAllowed(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a/{id}"}); err != nil {
		t.Fatalf("GET Create(): %v", err)
	}
	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "POST", Path: "/a/{id}"}); err != nil {
		t.Errorf("POST Create() on the same canonical path as an existing GET: %v, want no error", err)
	}
}

// TestRepo_ConcurrentCreates drives several goroutines through Create at
// once so -race has real concurrent writer-pool traffic to check, and
// proves no source_order is lost or duplicated: n concurrent Creates on
// distinct paths must land on n distinct, contiguous source_order values.
func TestRepo_ConcurrentCreates(t *testing.T) {
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repo.Create(t.Context(), wsID, &customep.Row{
				Method: "GET",
				Path:   "/concurrent/" + string(rune('a'+i)),
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create() [%d]: %v", i, err)
		}
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != n {
		t.Fatalf("ForWorkspace() = %d rows, want %d", len(all), n)
	}

	seen := make(map[int64]bool, n)
	for _, row := range all {
		if seen[row.SourceOrder] {
			t.Errorf("source_order %d used by more than one row", row.SourceOrder)
		}
		seen[row.SourceOrder] = true
		if row.SourceOrder < 0 || row.SourceOrder >= n {
			t.Errorf("source_order %d out of the expected [0,%d) range", row.SourceOrder, n)
		}
	}
}

// TestRepo_Update_changesFieldsAndRecomputesCanonicalPath is the happy
// path: Update rewrites Method, Path, the two booleans, ActiveStatus and
// Responses, and CanonicalPath comes back derived from the NEW Path, never
// the one the row was created with.
func TestRepo_Update_changesFieldsAndRecomputesCanonicalPath(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{
		Method: "GET", Path: "/before", ActiveStatus: 200,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	updated, err := repo.Update(t.Context(), wsID, created.ID, func(cur *customep.Row) error {
		cur.Method = "post"
		cur.Path = "/after/{id}"
		cur.OverrideOn = false
		cur.RouteOff = true
		cur.ActiveStatus = 201
		cur.Responses = map[string]overrides.Variant{"201": {Mode: "generated"}}
		return nil
	})
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("Update().ID = %d, want unchanged %d", updated.ID, created.ID)
	}
	if updated.Method != http.MethodPost {
		t.Errorf("Method = %q, want upper-cased POST", updated.Method)
	}
	if updated.Path != "/after/{id}" {
		t.Errorf("Path = %q, want /after/{id}", updated.Path)
	}
	if updated.CanonicalPath != "/after/{}" {
		t.Errorf("CanonicalPath = %q, want /after/{} (recomputed from the new Path)", updated.CanonicalPath)
	}
	if updated.OverrideOn {
		t.Error("OverrideOn = true, want false as set by mutate")
	}
	if !updated.RouteOff {
		t.Error("RouteOff = false, want true as set by mutate")
	}
	if updated.ActiveStatus != 201 {
		t.Errorf("ActiveStatus = %d, want 201", updated.ActiveStatus)
	}
	if _, ok := updated.Responses["201"]; !ok {
		t.Errorf("Responses missing 201, got %v", updated.Responses)
	}

	reread, err := repo.Get(t.Context(), wsID, created.ID)
	if err != nil {
		t.Fatalf("Get() after Update: %v", err)
	}
	if reread.Path != "/after/{id}" {
		t.Errorf("Get().Path after Update = %q, want /after/{id}", reread.Path)
	}
}

// TestRepo_Update_bumpsRevisionByExactlyOne exists because nothing else
// catches its absence: revert the bump and every other assertion in this
// file stays green while routeCache, keyed (workspace_id, revision), keeps
// serving the pre-edit endpoint.
func TestRepo_Update_bumpsRevisionByExactlyOne(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	before := workspaceRevision(t, db, wsID)

	if _, err := repo.Update(t.Context(), wsID, created.ID, func(cur *customep.Row) error {
		cur.ActiveStatus = 204
		return nil
	}); err != nil {
		t.Fatalf("Update(): %v", err)
	}
	after := workspaceRevision(t, db, wsID)
	if after != before+1 {
		t.Errorf("revision after Update = %d, want %d (STRICTLY one more than before)", after, before+1)
	}
}

// TestRepo_Update_preservesIDAcrossPathChange is D4's item 3 directly: the
// row id must survive an edit that changes Path, because GET /endpoints
// already handed that id to a UI that holds onto it. upsertTx would INSERT
// a fresh row under a new id here instead — this is the test that would
// catch Update() being implemented on top of upsertTx by mistake.
func TestRepo_Update_preservesIDAcrossPathChange(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/old-path"})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	updated, err := repo.Update(t.Context(), wsID, created.ID, func(cur *customep.Row) error {
		cur.Path = "/brand-new-path"
		return nil
	})
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("Update().ID = %d, want %d (unchanged across a Path edit)", updated.ID, created.ID)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ForWorkspace() = %d rows, want exactly 1 (an id-unstable Update would leave 2: old and new)", len(all))
	}
}

// TestRepo_Update_preservesFieldsNotTouchedByMutate covers the "not a
// partial merge, but not a blank slate either" contract: fields mutate
// never assigns (here, ActiveStatus and Responses) come back exactly as
// Create left them, because Update reads the current row and hands it to
// mutate rather than starting from a zero Row.
func TestRepo_Update_preservesFieldsNotTouchedByMutate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{
		Method: "GET", Path: "/x", ActiveStatus: 201,
		Responses: map[string]overrides.Variant{"201": {Mode: "generated"}},
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	updated, err := repo.Update(t.Context(), wsID, created.ID, func(cur *customep.Row) error {
		cur.RouteOff = true // touches one field, leaves ActiveStatus/Responses alone
		return nil
	})
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if updated.ActiveStatus != 201 {
		t.Errorf("ActiveStatus = %d, want the untouched 201", updated.ActiveStatus)
	}
	if _, ok := updated.Responses["201"]; !ok {
		t.Errorf("Responses lost the untouched 201 entry, got %v", updated.Responses)
	}
}

// TestRepo_Update_missingIsErrNotFound mirrors TestRepo_Delete_missingIsErrNotFound.
func TestRepo_Update_missingIsErrNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	before := workspaceRevision(t, db, wsID)
	_, err := repo.Update(t.Context(), wsID, 999, func(cur *customep.Row) error { return nil })
	if !errors.Is(err, customep.ErrNotFound) {
		t.Fatalf("Update(missing) err = %v, want ErrNotFound", err)
	}
	if got := workspaceRevision(t, db, wsID); got != before {
		t.Errorf("revision after a failed Update = %d, want unchanged %d", got, before)
	}
}

// TestRepo_Update_workspaceNotFound mirrors TestRepo_Delete_workspaceNotFound.
func TestRepo_Update_workspaceNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)

	_, err := repo.Update(t.Context(), 9999, 1, func(cur *customep.Row) error { return nil })
	if !errors.Is(err, customep.ErrWorkspaceNotFound) {
		t.Errorf("Update() err = %v, want ErrWorkspaceNotFound", err)
	}
}

// TestRepo_Update_conflictNaturalKey is D4 item 2's first half: editing a
// second endpoint onto the SAME (method, path) an existing row already
// holds violates the natural-key unique index (0001_init.sql:210) and must
// answer ErrConflict, not a raw driver error or an id-unstable overwrite.
func TestRepo_Update_conflictNaturalKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/taken"}); err != nil {
		t.Fatalf("first Create(): %v", err)
	}
	mover, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/mover"})
	if err != nil {
		t.Fatalf("second Create(): %v", err)
	}

	_, err = repo.Update(t.Context(), wsID, mover.ID, func(cur *customep.Row) error {
		cur.Path = "/taken" // same method, identical literal path as the first row
		return nil
	})
	if !errors.Is(err, customep.ErrConflict) {
		t.Fatalf("Update() onto an existing (method, path) err = %v, want ErrConflict", err)
	}
}

// TestRepo_Update_conflictCanonicalKey is D4 item 2's second half: editing a
// second endpoint onto a DIFFERENT literal path that canonicalizes to the
// same value as an existing row's — "/a/{id}" vs "/a/{name}" — violates the
// canonical-key unique index (0001_init.sql:211) even though the natural
// key never collides.
func TestRepo_Update_conflictCanonicalKey(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a/{id}"}); err != nil {
		t.Fatalf("first Create(): %v", err)
	}
	mover, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/mover"})
	if err != nil {
		t.Fatalf("second Create(): %v", err)
	}

	_, err = repo.Update(t.Context(), wsID, mover.ID, func(cur *customep.Row) error {
		cur.Path = "/a/{name}" // different literal path, same canonical "/a/{}"
		return nil
	})
	if !errors.Is(err, customep.ErrConflict) {
		t.Fatalf("Update() onto a colliding canonical path err = %v, want ErrConflict", err)
	}
}

// --- §G obs 16: the two restore-order hazards, driven directly at
// ReplaceAllTx because neither is reachable through the admin API --------

// insertDecoyEndpoint creates one row on its OWN, unrelated workspace and
// returns that workspace's id. §G obs 16 requires this of BOTH fixtures
// below: custom_endpoints.id is a bare INTEGER PRIMARY KEY with no
// AUTOINCREMENT (0001_init.sql:191), so SQLite assigns the next rowid as
// max(rowid)+1 across the WHOLE TABLE — not per workspace. A workspace-
// scoped DELETE that empties the table entirely lets the very next INSERT
// restart at rowid 1, handing a freshly re-created row back the SAME id a
// deleted one had by pure accident of an empty table, regardless of whether
// the restore is actually id-stable. This decoy row keeps a higher rowid
// alive throughout the delete-and-reinsert below, so a re-created row
// landing on a NEW id is something the test can actually observe — without
// it, both hazards this observation exists to catch would pass vacuously
// against the very implementation they are meant to fail. Called AFTER the
// fixture's own live row, so it truly holds the higher rowid.
func insertDecoyEndpoint(t *testing.T, db *store.DB, repo *customep.Repo) {
	t.Helper()
	decoyWsID := insertWorkspace(t, db, "decoy-"+t.Name())
	if _, err := repo.Create(t.Context(), decoyWsID, &customep.Row{Method: "GET", Path: "/decoy"}); err != nil {
		t.Fatalf("Create() decoy endpoint on workspace %d: %v", decoyWsID, err)
	}
}

// replaceAllTx opens the one db.Write ReplaceAllTx is meant to run inside —
// internal/checkpoints' job in the real system, stood in for here so this
// package's own tests can drive the tx-scoped function directly, exactly as
// §G obs 16 asks for.
func replaceAllTx(t *testing.T, db *store.DB, workspaceID int64, rows []*customep.Row) error {
	t.Helper()
	return db.Write(t.Context(), func(tx *sql.Tx) error {
		return customep.ReplaceAllTx(t.Context(), tx, workspaceID, rows, time.Now().UTC())
	})
}

// TestReplaceAllTx_canonicalPathCollision_deleteBeforeUpsertSucceeds is §G
// obs 16(a). custom_endpoints carries a SECOND unique index the restore
// does NOT key on — UNIQUE (workspace_id, method, canonical_path),
// 0001_init.sql:211 — while ReplaceAllTx's delete pass keys on (method,
// path), 0001_init.sql:210. A snapshot row "GET /a/{id}" and a live row
// "GET /a/{x}" are two DIFFERENT (method, path) natural keys that both
// canonicalize to "/a/{}": an upsert-before-delete order would try to write
// the snapshot row while the live one still occupies that canonical_path
// and abort the whole transaction on the UNIQUE violation (C1). Deleting
// first clears it, so this restore must succeed.
func TestReplaceAllTx_canonicalPathCollision_deleteBeforeUpsertSucceeds(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "primary")

	if _, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a/{x}"}); err != nil {
		t.Fatalf("Create() live row: %v", err)
	}
	insertDecoyEndpoint(t, db, repo)

	snapshot := []*customep.Row{
		{Method: "GET", Path: "/a/{id}", OverrideOn: true, ActiveStatus: 200},
	}
	if err := replaceAllTx(t, db, wsID, snapshot); err != nil {
		t.Fatalf("ReplaceAllTx() = %v, want success — delete-before-upsert must clear the live "+
			"row's canonical_path before the snapshot row is written; upsert-before-delete would "+
			"abort here on UNIQUE (workspace_id, method, canonical_path)", err)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ForWorkspace() = %d rows, want exactly 1 (the live row replaced by the snapshot row)", len(all))
	}
	if all[0].Path != "/a/{id}" {
		t.Errorf("restored row Path = %q, want /a/{id}", all[0].Path)
	}
	if all[0].CanonicalPath != "/a/{}" {
		t.Errorf("restored row CanonicalPath = %q, want /a/{} (C4 duty 6: derived via router.CanonicalPath)", all[0].CanonicalPath)
	}
}

// TestReplaceAllTx_methodCasing_idIsUnchanged is §G obs 16(b): the
// method-casing hazard C4 duty 1 exists to close, described there as "the
// only non-vacuous check C4 duty 1 has anywhere in the run" — unreachable
// through the admin API, because Repo.Create already normalizes Method
// before it ever reaches storage (repo.go:116), so only a direct call to
// ReplaceAllTx can drive a snapshot row whose Method reads lower-case
// against a live upper-case row of the same operation.
func TestReplaceAllTx_methodCasing_idIsUnchanged(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "primary")

	live, err := repo.Create(t.Context(), wsID, &customep.Row{Method: "GET", Path: "/a"})
	if err != nil {
		t.Fatalf("Create() live row: %v", err)
	}
	insertDecoyEndpoint(t, db, repo) // see its doc comment: not decoration, see §G obs 16

	snapshot := []*customep.Row{
		{Method: "get", Path: "/a", OverrideOn: true, ActiveStatus: 200},
	}
	if err := replaceAllTx(t, db, wsID, snapshot); err != nil {
		t.Fatalf("ReplaceAllTx(): %v", err)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ForWorkspace() = %d rows, want exactly 1", len(all))
	}
	if all[0].ID != live.ID {
		t.Errorf("restored row ID = %d, want %d (unchanged) — a restore that computes its delete "+
			"set BEFORE upper-casing Method treats \"get /a\" as a different key from the live "+
			"\"GET /a\", deletes the live row, and the following upsert re-creates it under a NEW id",
			all[0].ID, live.ID)
	}
	if all[0].Method != http.MethodGet {
		t.Errorf("restored row Method = %q, want upper-cased GET", all[0].Method)
	}
}
