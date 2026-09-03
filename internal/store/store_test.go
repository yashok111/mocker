package store_test

import (
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

// TestMigrate_reachesSchemaVersion5 asserts a fresh database ends up on the
// version 0005_custom_endpoints_stream.sql declares (P6b D2; 4 after P6a's
// 0004, 3 through P3h's 0003).
func TestMigrate_reachesSchemaVersion5(t *testing.T) {
	db := newTestDB(t)

	v, err := db.SchemaVersion(t.Context())
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v != 8 {
		t.Fatalf("schema version = %d, want 8", v)
	}
}

// TestMigrate_trafficIdsAreNeverReissued is 0004's own observation (P6a
// D20): with a bare INTEGER PRIMARY KEY, deleting the HIGHEST traffic rows
// let SQLite hand their ids out again on the next INSERT, and a stream or
// poll client holding a cursor past them went permanently deaf. After the
// rebuild the next id continues above the highest ever used, deleted or not.
func TestMigrate_trafficIdsAreNeverReissued(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()

	var ddl string
	if err := db.R.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'traffic'`).Scan(&ddl); err != nil {
		t.Fatalf("read traffic DDL: %v", err)
	}
	if !strings.Contains(ddl, "AUTOINCREMENT") {
		t.Fatalf("traffic DDL after migration carries no AUTOINCREMENT: %s", ddl)
	}
	var idx int
	if err := db.R.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'traffic_ws' AND tbl_name = 'traffic'`).Scan(&idx); err != nil {
		t.Fatalf("read traffic_ws index: %v", err)
	}
	if idx != 1 {
		t.Fatalf("traffic_ws index rows = %d, want 1 — the rebuild must recreate the index the read paths walk", idx)
	}

	if _, err := db.W.ExecContext(ctx, `INSERT INTO workspaces (slug, name, revision, settings, created_at, updated_at) VALUES ('w', 'w', 1, '{}', 0, 0)`); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	insert := func() int64 {
		res, err := db.W.ExecContext(ctx, `INSERT INTO traffic (workspace_id, ts, method, path, status, duration_ms) VALUES (1, 0, 'GET', '/x', 200, 1)`)
		if err != nil {
			t.Fatalf("insert traffic: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last insert id: %v", err)
		}
		return id
	}
	first, second, third := insert(), insert(), insert()
	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("ids = %d, %d, %d, want 1, 2, 3", first, second, third)
	}
	if _, err := db.W.ExecContext(ctx, `DELETE FROM traffic`); err != nil {
		t.Fatalf("clear traffic: %v", err)
	}
	if got := insert(); got != 4 {
		t.Fatalf("id after clearing the table = %d, want 4 — a reissued id is exactly the hole 0004 closes", got)
	}
}

// TestApplyBaseScopeMigrationTwice_failsLoudly is P18's own observation: the
// migration is loud on a second run and silent on a first. 0003's ALTER TABLE
// is deliberately its first statement -- SQLite has no ADD COLUMN IF NOT
// EXISTS, so re-running it must fail with "duplicate column name" rather than
// silently rewriting base_scope_key the way an INSERT ... SELECT rebuild
// would (D5.2). A fresh, already-migrated database has already run 0003
// through db.Migrate; this test re-applies its raw SQL by hand to reproduce
// exactly the crash-window repair scenario the migration's own header
// describes, and asserts the second run does not succeed silently.
func TestApplyBaseScopeMigrationTwice_failsLoudly(t *testing.T) {
	ctx := t.Context()
	db := newTestDB(t) // already at version 3 -- 0003 has run once already

	migrationSQL, err := os.ReadFile("migrations/0003_base_scope.sql")
	if err != nil {
		t.Fatalf("read 0003_base_scope.sql: %v", err)
	}
	if _, err := db.W.ExecContext(ctx, string(migrationSQL)); err == nil {
		t.Fatal("re-applying 0003_base_scope.sql over an already-migrated database succeeded; want a loud failure (duplicate column name)")
	}
}

// TestMigrate_backfillsEmptyBaseScopeAndServes reproduces the upgrade path
// D5.3 describes: entity rows created under schema version 2 alone, before
// 0003 ever ran, must come out of the migration with base_scope_key = ”
// (the empty base tuple every unparameterised-basePath request already
// computes) and must still be readable by an ordinary SELECT the way
// internal/resources' read path issues one -- the migration must not have
// silently changed what a row means to code that has not learned about the
// new column yet. This stops at PRAGMA user_version = 2, inserts a resource
// and entity rows by hand exactly as they would have existed under P3g, then
// finishes the migration and inspects the back-filled column.
func TestMigrate_backfillsEmptyBaseScopeAndServes(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	// Apply 0001 and 0002 by hand and stop there, mirroring a database
	// upgraded from before 0003 existed.
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_edit_version.sql"} {
		sqlBytes, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.W.ExecContext(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := db.W.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
		t.Fatalf("set user_version=2: %v", err)
	}

	wsID := insertLegacyWorkspace(t, db, "ws-basescope")
	resID := insertLegacyResource(t, db, wsID, "/items")
	entIDs := []int64{
		insertLegacyEntity(t, db, resID, "", "1", `{"id":"1"}`),
		insertLegacyEntity(t, db, resID, "", "2", `{"id":"2"}`),
	}

	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate to 0003: %v", err)
	}

	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v != 8 {
		t.Fatalf("schema version = %d, want 8", v)
	}

	for _, id := range entIDs {
		var baseScopeKey, data string
		if err := db.R.QueryRowContext(ctx,
			`SELECT base_scope_key, data FROM entities WHERE id = ?`, id,
		).Scan(&baseScopeKey, &data); err != nil {
			t.Fatalf("read entity %d after migration: %v", id, err)
		}
		if baseScopeKey != "" {
			t.Errorf("entity %d: base_scope_key = %q, want the empty base tuple (D5.3)", id, baseScopeKey)
		}
		if data == "" {
			t.Errorf("entity %d: data lost across migration", id)
		}
	}

	// "Still serve" is checked the way the new read path (D5.1) will query
	// them: by (resource_id, base_scope_key, ...), which is exactly the
	// predicate an unparameterised-basePath request computes.
	var count int
	if err := db.R.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entities WHERE resource_id = ? AND base_scope_key = ''`, resID,
	).Scan(&count); err != nil {
		t.Fatalf("count entities by base scope: %v", err)
	}
	if count != len(entIDs) {
		t.Errorf("entities readable under the empty base scope = %d, want %d", count, len(entIDs))
	}
}

// TestMigrate_entitiesListIndexCoversBaseScope asserts the NAMED index was
// dropped and recreated with base_scope_key as its second column -- the
// order D5.2 specifies, base value first, ancestor scope second -- and that
// entities_parent (parent_entity_id stays NULL, unmoved by this slice) was
// left untouched.
func TestMigrate_entitiesListIndexCoversBaseScope(t *testing.T) {
	db := newTestDB(t)

	cols := indexColumns(t, db, "entities_list")
	want := []string{"resource_id", "base_scope_key", "scope_key", "id"}
	if len(cols) != len(want) {
		t.Fatalf("entities_list columns = %v, want %v", cols, want)
	}
	for i, c := range cols {
		if c != want[i] {
			t.Errorf("entities_list column %d = %q, want %q (full: %v)", i, c, want[i], cols)
		}
	}

	parentCols := indexColumns(t, db, "entities_parent")
	if len(parentCols) != 1 || parentCols[0] != "parent_entity_id" {
		t.Errorf("entities_parent columns = %v, want [parent_entity_id] -- untouched by 0003", parentCols)
	}
}

// indexColumns reads an index's columns, in order, via SQLite's own
// introspection pragma -- the only way to observe what CREATE INDEX actually
// produced short of parsing sqlite_master's SQL text by hand.
func indexColumns(t *testing.T, db *store.DB, indexName string) []string {
	t.Helper()
	rows, err := db.R.QueryContext(t.Context(), `PRAGMA index_info(`+indexName+`)`)
	if err != nil {
		t.Fatalf("PRAGMA index_info(%s): %v", indexName, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatalf("scan index_info row for %s: %v", indexName, err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_info(%s): %v", indexName, err)
	}
	return out
}

// insertLegacyResource inserts a resource row with the shape P3g-era code
// writes, entity_schema included (NOT NULL, no default).
func insertLegacyResource(t *testing.T, db *store.DB, workspaceID int64, routeFamily string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO resources (workspace_id, route_family, name, entity_schema)
		VALUES (?, ?, ?, '{}')`, workspaceID, routeFamily, routeFamily)
	if err != nil {
		t.Fatalf("insert legacy resource %s: %v", routeFamily, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("legacy resource id: %v", err)
	}
	return id
}

// insertLegacyEntity inserts an entity row exactly as pre-0003 code would
// have -- no base_scope_key in the INSERT at all, so the column's DEFAULT is
// what decides its value, the same as for a real pre-migration row.
func insertLegacyEntity(t *testing.T, db *store.DB, resourceID int64, scopeKey, entityKey, data string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO entities (resource_id, scope_key, entity_key, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 0)`, resourceID, scopeKey, entityKey, data)
	if err != nil {
		t.Fatalf("insert legacy entity %s/%s: %v", scopeKey, entityKey, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("legacy entity id: %v", err)
	}
	return id
}
