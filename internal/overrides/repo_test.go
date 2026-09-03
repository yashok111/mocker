package overrides_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/store"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(), mirroring
// internal/workspaces/repo_test.go's harness.
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

// insertWorkspace writes a minimal workspaces row directly. The overrides
// package owns no workspace logic (and must not import internal/workspaces
// — HARD RULE 5), so tests build the FK target by hand, exactly as
// internal/workspaces/repo_test.go builds its FK-target users rows.
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

// insertOperation writes a minimal specs row and a minimal operations row
// hanging off it, returning the operations row's id. Row.OperationID is a
// FOREIGN KEY REFERENCES operations(id) with foreign_keys=ON, so a fixture
// that wants a non-nil OperationID needs a real row to point at — the
// column comment's "may be nil or stale" covers going stale via the
// schema's own ON DELETE SET NULL, not pointing at an id that never
// existed.
func insertOperation(t *testing.T, db *store.DB, method, path string) int64 {
	t.Helper()
	specRes, err := db.W.ExecContext(t.Context(), `
		INSERT INTO specs (name, format, source, hash, raw, normalized, created_at)
		VALUES ('fixture', 'oas31', 'upload', ?, '{}', '{}', unixepoch())`,
		method+" "+path+"-"+t.Name(),
	)
	if err != nil {
		t.Fatalf("insert fixture spec: %v", err)
	}
	specID, err := specRes.LastInsertId()
	if err != nil {
		t.Fatalf("spec id: %v", err)
	}

	opRes, err := db.W.ExecContext(t.Context(), `
		INSERT INTO operations (spec_id, method, path, canonical_path, source_order, pointer)
		VALUES (?, ?, ?, ?, 0, '#/paths/fixture')`,
		specID, method, path, path,
	)
	if err != nil {
		t.Fatalf("insert fixture operation: %v", err)
	}
	opID, err := opRes.LastInsertId()
	if err != nil {
		t.Fatalf("operation id: %v", err)
	}
	return opID
}

func workspaceRevision(t *testing.T, db *store.DB, id int64) int64 {
	t.Helper()
	var rev int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT revision FROM workspaces WHERE id = ?", id).Scan(&rev); err != nil {
		t.Fatalf("read revision for workspace %d: %v", id, err)
	}
	return rev
}

// fullVariant builds a Variant with every field populated, including the
// ones this slice does not implement (When, SchemaPatch) — the fixture the
// round-trip-fidelity test needs per point 2 of the task.
func fullVariant() overrides.Variant {
	return overrides.Variant{
		Mode: "pinned",
		When: []overrides.Condition{
			{In: "query", Name: "verbose", Op: "equals", Value: "true"},
			{In: "header", Name: "X-Debug", Op: "exists"},
		},
		Body:         raw(`{"id":1,"name":"pinned"}`),
		BodyEncoding: "",
		MediaType:    "application/json",
		Headers:      map[string]string{"X-Extra": "1", "X-Other": "2"},
		SchemaPatch:  raw(`{"add":{"/properties/extra":{"type":"string"}}}`),
		Recipes: map[string]recipes.Recipe{
			"user.id":      {Kind: recipes.KindIdentity, Field: "id"},
			"items[*].sku": {Kind: recipes.KindEnum, Data: raw(`["a","b","c"]`)},
			"token":        {Kind: recipes.KindJWT, TTLSec: 3600, Claims: raw(`{"scope":"read"}`)},
		},
	}
}

func fullRow(workspaceID, opID int64, method, path string) *overrides.Row {
	status := 409
	listSize := &overrides.ListSize{Min: 2, Max: 5}
	delay := 150
	validateReq := true
	return &overrides.Row{
		WorkspaceID:  workspaceID,
		Method:       method,
		Path:         path,
		OperationID:  &opID,
		OverrideOn:   true,
		RouteOff:     false,
		ActiveStatus: &status,
		Responses: map[string]overrides.Variant{
			"200": fullVariant(),
			"409": {Mode: "generated"},
		},
		ListSize:      listSize,
		DelayMs:       &delay,
		FailDirective: raw(`{"status":503,"mode":"next_n","n":2}`),
		ValidateReq:   &validateReq,
	}
}

// TestRepo_Put_insertThenReadBack covers "insert then read back with every
// field populated" from the task's test list.
func TestRepo_Put_insertThenReadBack(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")
	opID := insertOperation(t, db, "POST", "/orders/{id}")

	want := fullRow(wsID, opID, "POST", "/orders/{id}")
	key := overrides.OpKey("POST", "/orders/{id}")

	stored, rev, err := repo.Put(t.Context(), wsID, key, func(row *overrides.Row) error {
		*row = *want
		return nil
	})
	if err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if rev != 2 {
		t.Errorf("revision after first Put = %d, want 2 (workspace starts at 1)", rev)
	}
	if stored.ID == 0 {
		t.Error("stored.ID is zero, want assigned id")
	}

	got, err := repo.Get(t.Context(), wsID, key)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	assertRowEqualIgnoringIDAndTime(t, got, want)
}

// TestRepo_Put_updateOneFieldRoundTripsTheRest covers "update one field and
// prove the others round-trip" AND requirement 2's byte-preserved fidelity
// for the fields this slice never interprets: When, SchemaPatch,
// BodyEncoding and FailDirective.
func TestRepo_Put_updateOneFieldRoundTripsTheRest(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")
	opID := insertOperation(t, db, "GET", "/widgets/{id}")

	original := fullRow(wsID, opID, "GET", "/widgets/{id}")
	key := overrides.OpKey("GET", "/widgets/{id}")

	_, _, err := repo.Put(t.Context(), wsID, key, func(row *overrides.Row) error {
		*row = *original
		return nil
	})
	if err != nil {
		t.Fatalf("initial Put(): %v", err)
	}

	// Touch exactly one field: flip RouteOff. Everything else — including
	// the four fields this slice only preserves — must survive unchanged.
	_, _, err = repo.Put(t.Context(), wsID, key, func(row *overrides.Row) error {
		row.RouteOff = true
		return nil
	})
	if err != nil {
		t.Fatalf("second Put(): %v", err)
	}

	got, err := repo.Get(t.Context(), wsID, key)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if !got.RouteOff {
		t.Error("RouteOff = false, want true after the targeted edit")
	}

	// The preserved-only fields, byte for byte.
	v := got.Responses["200"]
	wantV := original.Responses["200"]
	if string(v.SchemaPatch) != string(wantV.SchemaPatch) {
		t.Errorf("SchemaPatch = %s, want %s", v.SchemaPatch, wantV.SchemaPatch)
	}
	if len(v.When) != len(wantV.When) {
		t.Fatalf("When has %d entries, want %d", len(v.When), len(wantV.When))
	}
	for i := range v.When {
		if v.When[i] != wantV.When[i] {
			t.Errorf("When[%d] = %+v, want %+v", i, v.When[i], wantV.When[i])
		}
	}
	if v.BodyEncoding != wantV.BodyEncoding {
		t.Errorf("BodyEncoding = %q, want %q", v.BodyEncoding, wantV.BodyEncoding)
	}
	if string(got.FailDirective) != string(original.FailDirective) {
		t.Errorf("FailDirective = %s, want %s (byte-preserved)", got.FailDirective, original.FailDirective)
	}

	// And everything else that was never touched either.
	if got.OperationID == nil || *got.OperationID != *original.OperationID {
		t.Errorf("OperationID = %v, want %v", got.OperationID, original.OperationID)
	}
	if got.ListSize == nil || *got.ListSize != *original.ListSize {
		t.Errorf("ListSize = %v, want %v", got.ListSize, original.ListSize)
	}
	if got.DelayMs == nil || *got.DelayMs != *original.DelayMs {
		t.Errorf("DelayMs = %v, want %v", got.DelayMs, original.DelayMs)
	}
	if got.ValidateReq == nil || *got.ValidateReq != *original.ValidateReq {
		t.Errorf("ValidateReq = %v, want %v", got.ValidateReq, original.ValidateReq)
	}
}

// TestRepo_Put_revisionBumpedByExactlyOne covers "revision incremented by
// exactly one per Put ... (NOT per row)": Put touches many nested fields in
// one call, and the bump must still be 1.
func TestRepo_Put_revisionBumpedByExactlyOne(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	before := workspaceRevision(t, db, wsID)

	_, rev, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/a"), func(row *overrides.Row) error {
		row.RouteOff = true
		row.OverrideOn = true
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}, "404": {Mode: "pinned"}}
		return nil
	})
	if err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if rev != before+1 {
		t.Errorf("revision = %d, want %d", rev, before+1)
	}
	if got := workspaceRevision(t, db, wsID); got != rev {
		t.Errorf("persisted revision = %d, want %d", got, rev)
	}
}

// TestRepo_PutMany_oneRevisionBumpForManyRows covers "revision incremented
// by exactly one per ... PutMany (NOT per row)".
func TestRepo_PutMany_oneRevisionBumpForManyRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	before := workspaceRevision(t, db, wsID)

	rows := []*overrides.Row{
		{Method: "GET", Path: "/a", OverrideOn: true, Responses: map[string]overrides.Variant{"200": {Mode: "generated"}}},
		{Method: "GET", Path: "/b", OverrideOn: true, Responses: map[string]overrides.Variant{"200": {Mode: "generated"}}},
		{Method: "POST", Path: "/c", OverrideOn: true, Responses: map[string]overrides.Variant{"201": {Mode: "generated"}}},
	}
	rev, err := repo.PutMany(t.Context(), wsID, rows)
	if err != nil {
		t.Fatalf("PutMany(): %v", err)
	}
	if rev != before+1 {
		t.Errorf("revision = %d, want %d (one bump for %d rows)", rev, before+1, len(rows))
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != len(rows) {
		t.Fatalf("ForWorkspace() = %d rows, want %d", len(all), len(rows))
	}
	for _, r := range rows {
		key := overrides.OpKey(r.Method, r.Path)
		if _, ok := all[key]; !ok {
			t.Errorf("ForWorkspace() missing row for key %q", key)
		}
	}
}

// TestRepo_PutMany_ReplacesRowWholesale pins PutMany's OWN contract, in
// contrast to Put's read-mutate-write, as documented on PutMany itself
// (round-1 finding #2): PutMany writes each Row exactly as given, with no
// read of whatever is already stored for that (workspace, method, path)
// first. A caller that builds a bare Row for an operation that already has
// a Put-created override silently wipes every other column that row
// carried. Today's one PutMany caller (admin/preset_handlers.go's
// handleApplyAuthPreset) avoids this by reading ForWorkspace and reusing
// the existing Row as its own starting point — see
// TestHandler_authPresetLifecycle's "merges in, does not replace the row"
// subtest for that guarantee at the level it actually matters. This test
// exists so PutMany's OWN behavior stays pinned and documented even if a
// future caller is added that forgets to read-merge first.
func TestRepo_PutMany_ReplacesRowWholesale(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	delay := 250
	status := 503
	validate := true
	if _, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("POST", "/login"), func(row *overrides.Row) error {
		row.DelayMs = &delay
		row.ActiveStatus = &status
		row.ValidateReq = &validate
		row.FailDirective = json.RawMessage(`{"kind":"timeout"}`)
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	// A bare Row for the SAME (method, path) — only what a caller applying a
	// fresh set of recipe bindings without reading first would set.
	bare := &overrides.Row{
		Method:     "POST",
		Path:       "/login",
		OverrideOn: true,
		Responses:  map[string]overrides.Variant{"200": {Mode: "generated"}},
	}
	if _, err := repo.PutMany(t.Context(), wsID, []*overrides.Row{bare}); err != nil {
		t.Fatalf("PutMany(): %v", err)
	}

	got, err := repo.Get(t.Context(), wsID, overrides.OpKey("POST", "/login"))
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got.DelayMs != nil {
		t.Errorf("DelayMs = %v, want nil (PutMany with a bare Row replaces wholesale)", *got.DelayMs)
	}
	if got.ActiveStatus != nil {
		t.Errorf("ActiveStatus = %v, want nil", *got.ActiveStatus)
	}
	if got.ValidateReq != nil {
		t.Errorf("ValidateReq = %v, want nil", *got.ValidateReq)
	}
	if len(got.FailDirective) != 0 {
		t.Errorf("FailDirective = %s, want empty", got.FailDirective)
	}
}

// TestRepo_ForWorkspace_isOneQuery is a light structural guard for point 5:
// it does not measure query counts (no query-counting harness exists in
// this repo), but it does prove ForWorkspace returns every row of the
// target workspace and none of another's in a single call.
func TestRepo_ForWorkspace_isOneQuery(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsA := insertWorkspace(t, db, "workspace-a")
	wsB := insertWorkspace(t, db, "workspace-b")

	if _, _, err := repo.Put(t.Context(), wsA, overrides.OpKey("GET", "/a1"), func(row *overrides.Row) error { return nil }); err != nil {
		t.Fatalf("Put(a1): %v", err)
	}
	if _, _, err := repo.Put(t.Context(), wsA, overrides.OpKey("GET", "/a2"), func(row *overrides.Row) error { return nil }); err != nil {
		t.Fatalf("Put(a2): %v", err)
	}
	if _, _, err := repo.Put(t.Context(), wsB, overrides.OpKey("GET", "/b1"), func(row *overrides.Row) error { return nil }); err != nil {
		t.Fatalf("Put(b1): %v", err)
	}

	got, err := repo.ForWorkspace(t.Context(), wsA)
	if err != nil {
		t.Fatalf("ForWorkspace(a): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ForWorkspace(a) = %d rows, want 2", len(got))
	}
	for key, row := range got {
		if row.WorkspaceID != wsA {
			t.Errorf("row %q has WorkspaceID %d, want %d", key, row.WorkspaceID, wsA)
		}
	}
}

// TestRepo_Delete covers "delete of a missing row is a no-op that still
// bumps nothing it should not", alongside the ordinary delete-an-existing-row
// path.
func TestRepo_Delete(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")
	key := overrides.OpKey("GET", "/gone")

	t.Run("missing row is a no-op", func(t *testing.T) {
		before := workspaceRevision(t, db, wsID)
		rev, _, err := repo.Delete(t.Context(), wsID, key)
		if err != nil {
			t.Fatalf("Delete() of missing row: %v", err)
		}
		if rev != before {
			t.Errorf("revision = %d, want unchanged %d", rev, before)
		}
	})

	t.Run("existing row is removed and bumps once", func(t *testing.T) {
		if _, _, err := repo.Put(t.Context(), wsID, key, func(row *overrides.Row) error { return nil }); err != nil {
			t.Fatalf("Put(): %v", err)
		}
		before := workspaceRevision(t, db, wsID)

		rev, _, err := repo.Delete(t.Context(), wsID, key)
		if err != nil {
			t.Fatalf("Delete(): %v", err)
		}
		if rev != before+1 {
			t.Errorf("revision = %d, want %d", rev, before+1)
		}
		if _, err := repo.Get(t.Context(), wsID, key); !errors.Is(err, overrides.ErrNotFound) {
			t.Errorf("Get() after Delete() error = %v, want ErrNotFound", err)
		}

		// A second delete of the now-missing row is again a no-op.
		rev2, _, err := repo.Delete(t.Context(), wsID, key)
		if err != nil {
			t.Fatalf("second Delete(): %v", err)
		}
		if rev2 != rev {
			t.Errorf("revision after second Delete = %d, want unchanged %d", rev2, rev)
		}
	})
}

// TestRepo_Get_missing proves Get reports ErrNotFound rather than a bare
// sql.ErrNoRows or a panic.
func TestRepo_Get_missing(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	_, err := repo.Get(t.Context(), wsID, overrides.OpKey("GET", "/nope"))
	if !errors.Is(err, overrides.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// TestRepo_Put_workspaceNotFound covers "a write to a workspace that does
// not exist must fail cleanly, not create an orphan row".
func TestRepo_Put_workspaceNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)

	const missingWorkspace = 999999
	_, _, err := repo.Put(t.Context(), missingWorkspace, overrides.OpKey("GET", "/x"), func(row *overrides.Row) error { return nil })
	if !errors.Is(err, overrides.ErrWorkspaceNotFound) {
		t.Fatalf("Put() error = %v, want ErrWorkspaceNotFound", err)
	}

	var count int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM op_overrides").Scan(&count); err != nil {
		t.Fatalf("count op_overrides: %v", err)
	}
	if count != 0 {
		t.Errorf("op_overrides has %d rows, want 0 (rejected Put must not create an orphan row)", count)
	}
}

// TestRepo_PutMany_workspaceNotFound is PutMany's twin of the same
// guarantee.
func TestRepo_PutMany_workspaceNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)

	const missingWorkspace = 999999
	rows := []*overrides.Row{
		{Method: "GET", Path: "/a", Responses: map[string]overrides.Variant{}},
	}
	_, err := repo.PutMany(t.Context(), missingWorkspace, rows)
	if !errors.Is(err, overrides.ErrWorkspaceNotFound) {
		t.Fatalf("PutMany() error = %v, want ErrWorkspaceNotFound", err)
	}
}

// TestRepo_ConcurrentPuts drives several goroutines through Put at once so
// -race has real concurrent writer-pool traffic to check, and proves every
// Put's bump landed (final revision = start + n, none lost to a race).
func TestRepo_ConcurrentPuts(t *testing.T) {
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")
	before := workspaceRevision(t, db, wsID)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct paths: n independent rows written concurrently,
			// which is the realistic case (several admin tabs editing
			// different operations at once) and still forces every Put
			// through the single writer connection at the same time.
			key := overrides.OpKey("GET", "/concurrent/"+string(rune('a'+i)))
			_, _, err := repo.Put(t.Context(), wsID, key, func(row *overrides.Row) error {
				row.RouteOff = true
				return nil
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Put() [%d]: %v", i, err)
		}
	}

	got := workspaceRevision(t, db, wsID)
	if got != before+n {
		t.Errorf("final revision = %d, want %d (%d concurrent Puts, each bumping by exactly 1)", got, before+n, n)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != n {
		t.Fatalf("ForWorkspace() = %d rows, want %d", len(all), n)
	}
}

// TestRepo_scan_malformedResponsesIsAnErrorNotAPanic covers the last item
// in the task's test list directly against the decode path: a row inserted
// by hand (bypassing Put's validation entirely, standing in for data that
// reached the table some other way) with a responses blob no valid Put
// could ever have produced must come back as an error from Get, never a
// panic.
func TestRepo_scan_malformedResponsesIsAnErrorNotAPanic(t *testing.T) {
	tests := []struct {
		name      string
		responses string
	}{
		{name: "not JSON at all", responses: `not json`},
		{name: "JSON array instead of object", responses: `["200"]`},
		{name: "status key not 3 digits", responses: `{"20":{"mode":"generated"}}`},
		{name: "unknown mode", responses: `{"200":{"mode":"teleport"}}`},
		{name: "recipe with unknown kind", responses: `{"200":{"mode":"generated","recipes":{"id":{"kind":"bogus"}}}}`},
		{name: "recipe missing required field", responses: `{"200":{"mode":"generated","recipes":{"id":{"kind":"const"}}}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			repo := overrides.NewRepo(db)
			wsID := insertWorkspace(t, db, "alex")

			_, err := db.W.ExecContext(t.Context(), `
				INSERT INTO op_overrides (workspace_id, method, path, override_on, responses, updated_at)
				VALUES (?, 'GET', '/hand-inserted', 1, ?, unixepoch())`,
				wsID, tt.responses,
			)
			if err != nil {
				t.Fatalf("hand-insert malformed row: %v", err)
			}

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Get() panicked on malformed responses %q: %v", tt.responses, r)
					}
				}()
				_, err := repo.Get(t.Context(), wsID, overrides.OpKey("GET", "/hand-inserted"))
				if err == nil {
					t.Fatalf("Get() with malformed responses %q returned no error", tt.responses)
				}
			}()

			// ForWorkspace walks the same decode path and must be equally
			// panic-free — this is the one the mock plane actually calls
			// on the unauthenticated request path.
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("ForWorkspace() panicked on malformed responses %q: %v", tt.responses, r)
					}
				}()
				if _, err := repo.ForWorkspace(t.Context(), wsID); err == nil {
					t.Fatalf("ForWorkspace() with malformed responses %q returned no error", tt.responses)
				}
			}()
		})
	}
}

// assertRowEqualIgnoringIDAndTime compares two rows field by field, skipping
// ID (assigned by the DB) and UpdatedAt (stamped by Put, not caller-supplied).
func assertRowEqualIgnoringIDAndTime(t *testing.T, got, want *overrides.Row) {
	t.Helper()
	if got.WorkspaceID != want.WorkspaceID {
		t.Errorf("WorkspaceID = %d, want %d", got.WorkspaceID, want.WorkspaceID)
	}
	if got.Method != want.Method {
		t.Errorf("Method = %q, want %q", got.Method, want.Method)
	}
	if got.Path != want.Path {
		t.Errorf("Path = %q, want %q", got.Path, want.Path)
	}
	if (got.OperationID == nil) != (want.OperationID == nil) || (got.OperationID != nil && *got.OperationID != *want.OperationID) {
		t.Errorf("OperationID = %v, want %v", got.OperationID, want.OperationID)
	}
	if got.OverrideOn != want.OverrideOn {
		t.Errorf("OverrideOn = %v, want %v", got.OverrideOn, want.OverrideOn)
	}
	if got.RouteOff != want.RouteOff {
		t.Errorf("RouteOff = %v, want %v", got.RouteOff, want.RouteOff)
	}
	if (got.ActiveStatus == nil) != (want.ActiveStatus == nil) || (got.ActiveStatus != nil && *got.ActiveStatus != *want.ActiveStatus) {
		t.Errorf("ActiveStatus = %v, want %v", got.ActiveStatus, want.ActiveStatus)
	}
	if (got.ListSize == nil) != (want.ListSize == nil) || (got.ListSize != nil && *got.ListSize != *want.ListSize) {
		t.Errorf("ListSize = %v, want %v", got.ListSize, want.ListSize)
	}
	if (got.DelayMs == nil) != (want.DelayMs == nil) || (got.DelayMs != nil && *got.DelayMs != *want.DelayMs) {
		t.Errorf("DelayMs = %v, want %v", got.DelayMs, want.DelayMs)
	}
	if string(got.FailDirective) != string(want.FailDirective) {
		t.Errorf("FailDirective = %s, want %s", got.FailDirective, want.FailDirective)
	}
	if (got.ValidateReq == nil) != (want.ValidateReq == nil) || (got.ValidateReq != nil && *got.ValidateReq != *want.ValidateReq) {
		t.Errorf("ValidateReq = %v, want %v", got.ValidateReq, want.ValidateReq)
	}

	if len(got.Responses) != len(want.Responses) {
		t.Fatalf("Responses has %d entries, want %d", len(got.Responses), len(want.Responses))
	}
	for status, wv := range want.Responses {
		gv, ok := got.Responses[status]
		if !ok {
			t.Fatalf("Responses missing status %q", status)
		}
		assertVariantEqual(t, status, gv, wv)
	}
}

func assertVariantEqual(t *testing.T, status string, got, want overrides.Variant) {
	t.Helper()
	if got.Mode != want.Mode {
		t.Errorf("[%s] Mode = %q, want %q", status, got.Mode, want.Mode)
	}
	if string(got.Body) != string(want.Body) {
		t.Errorf("[%s] Body = %s, want %s", status, got.Body, want.Body)
	}
	if got.BodyEncoding != want.BodyEncoding {
		t.Errorf("[%s] BodyEncoding = %q, want %q", status, got.BodyEncoding, want.BodyEncoding)
	}
	if got.MediaType != want.MediaType {
		t.Errorf("[%s] MediaType = %q, want %q", status, got.MediaType, want.MediaType)
	}
	if len(got.Headers) != len(want.Headers) {
		t.Errorf("[%s] Headers has %d entries, want %d", status, len(got.Headers), len(want.Headers))
	}
	for k, v := range want.Headers {
		if got.Headers[k] != v {
			t.Errorf("[%s] Headers[%q] = %q, want %q", status, k, got.Headers[k], v)
		}
	}
	if string(got.SchemaPatch) != string(want.SchemaPatch) {
		t.Errorf("[%s] SchemaPatch = %s, want %s", status, got.SchemaPatch, want.SchemaPatch)
	}
	if len(got.When) != len(want.When) {
		t.Fatalf("[%s] When has %d entries, want %d", status, len(got.When), len(want.When))
	}
	for i := range want.When {
		if got.When[i] != want.When[i] {
			t.Errorf("[%s] When[%d] = %+v, want %+v", status, i, got.When[i], want.When[i])
		}
	}
	if len(got.Recipes) != len(want.Recipes) {
		t.Fatalf("[%s] Recipes has %d entries, want %d", status, len(got.Recipes), len(want.Recipes))
	}
	for pattern, wr := range want.Recipes {
		gr, ok := got.Recipes[pattern]
		if !ok {
			t.Fatalf("[%s] Recipes missing pattern %q", status, pattern)
		}
		if gr.Kind != wr.Kind || string(gr.Data) != string(wr.Data) || gr.Field != wr.Field ||
			gr.Offset != wr.Offset || gr.Format != wr.Format || string(gr.Claims) != string(wr.Claims) || gr.TTLSec != wr.TTLSec {
			t.Errorf("[%s] Recipes[%q] = %+v, want %+v", status, pattern, gr, wr)
		}
	}
}

// --- ReplaceAllTx (C4/C1): the restore primitive internal/checkpoints
// calls, one write per table, inside its own caller-owned transaction ------

// replaceAllTx opens the one db.Write ReplaceAllTx is meant to run inside —
// internal/checkpoints' job in the real system, stood in for here so this
// package's own tests can drive the tx-scoped function directly.
func replaceAllTx(t *testing.T, db *store.DB, workspaceID int64, rows []*overrides.Row) error {
	t.Helper()
	return db.Write(t.Context(), func(tx *sql.Tx) error {
		return overrides.ReplaceAllTx(t.Context(), tx, workspaceID, rows, time.Now().UTC())
	})
}

// TestReplaceAllTx_deleteThenUpsert_roundTrip proves the three things a
// restore has to do to a table in one call: a row absent from the snapshot
// is DELETED (C1), a row present on both sides is UPSERTED in place (its
// stored data changes to match the snapshot), and a row new to the
// snapshot is INSERTED — all inside one transaction, with NO revision bump
// (that is the caller's job, exactly once, for the whole multi-table
// restore — C12).
func TestReplaceAllTx_deleteThenUpsert_roundTrip(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	// kept: present on both sides, but with DIFFERENT data live vs.
	// snapshot — proves the upsert half actually overwrites rather than
	// leaving the live row untouched because a key already matched.
	if _, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/kept"), func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("Put(/kept): %v", err)
	}
	// dropped: present live, absent from the snapshot — must be deleted.
	if _, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/dropped"), func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("Put(/dropped): %v", err)
	}

	before := workspaceRevision(t, db, wsID)

	pinnedStatus := 409
	snapshot := []*overrides.Row{
		{
			Method: "get", Path: "/kept", OverrideOn: true,
			ActiveStatus: &pinnedStatus,
			Responses:    map[string]overrides.Variant{"409": {Mode: "pinned", Body: raw(`{"e":1}`)}},
		},
		{Method: "POST", Path: "/added", OverrideOn: true, Responses: map[string]overrides.Variant{"201": {Mode: "generated"}}},
	}
	if err := replaceAllTx(t, db, wsID, snapshot); err != nil {
		t.Fatalf("ReplaceAllTx(): %v", err)
	}

	if got := workspaceRevision(t, db, wsID); got != before {
		t.Errorf("revision after ReplaceAllTx = %d, want unchanged %d — the caller bumps once for the whole restore, not this function", got, before)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ForWorkspace() = %d rows, want 2 (/kept upserted, /added inserted, /dropped deleted)", len(all))
	}

	kept, ok := all[overrides.OpKey("GET", "/kept")]
	if !ok {
		t.Fatal("ForWorkspace() missing GET /kept")
	}
	if kept.ActiveStatus == nil || *kept.ActiveStatus != pinnedStatus {
		t.Errorf("kept.ActiveStatus = %v, want %d — the upsert must overwrite the live row's data, not leave it alone because the key already existed", kept.ActiveStatus, pinnedStatus)
	}
	if _, ok := kept.Responses["409"]; !ok {
		t.Errorf("kept.Responses missing 409, got %v — the snapshot's responses must replace the live row's", kept.Responses)
	}

	if _, ok := all[overrides.OpKey("POST", "/added")]; !ok {
		t.Error("ForWorkspace() missing POST /added (the snapshot's new row)")
	}
	if _, ok := all[overrides.OpKey("GET", "/dropped")]; ok {
		t.Error("ForWorkspace() still has GET /dropped — a row absent from the snapshot must be deleted (C1)")
	}
}

// TestReplaceAllTx_emptyRowsWipesTable proves the empty-snapshot edge case
// (reset-overrides restoring to a workspace with no overrides at all, C9):
// an empty rows slice keeps NOTHING, so every existing row is absent from
// it by definition and gets deleted.
func TestReplaceAllTx_emptyRowsWipesTable(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	if _, _, err := repo.Put(t.Context(), wsID, overrides.OpKey("GET", "/a"), func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	if err := replaceAllTx(t, db, wsID, nil); err != nil {
		t.Fatalf("ReplaceAllTx(nil): %v", err)
	}

	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace(): %v", err)
	}
	if len(all) != 0 {
		t.Errorf("ForWorkspace() = %d rows after an empty-snapshot restore, want 0", len(all))
	}
}

// TestReplaceAllTx_setsWorkspaceID proves duty 2: a row decoded from a
// checkpoint/scenario BLOB carries no WorkspaceID (bundle.OverrideEntry
// never stores one, C3), so ReplaceAllTx must set it itself rather than
// trust — or require — whatever the caller left on the Go value.
func TestReplaceAllTx_setsWorkspaceID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := overrides.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	// Deliberately zero, as a BLOB-decoded EndpointEntry conversion would
	// leave it — WorkspaceID is not one of EndpointEntry's carried fields.
	snapshot := []*overrides.Row{
		{Method: "GET", Path: "/a", OverrideOn: true, Responses: map[string]overrides.Variant{"200": {Mode: "generated"}}},
	}
	if err := replaceAllTx(t, db, wsID, snapshot); err != nil {
		t.Fatalf("ReplaceAllTx(): %v", err)
	}

	got, err := repo.Get(t.Context(), wsID, overrides.OpKey("GET", "/a"))
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got.WorkspaceID != wsID {
		t.Errorf("WorkspaceID = %d, want %d", got.WorkspaceID, wsID)
	}
}
