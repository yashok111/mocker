package store_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(), mirroring
// the harness every other package's repo_test.go already uses.
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

// A fresh database reaching the newest migration's version is asserted by
// TestMigrate_reachesSchemaVersion3 in store_test.go (P3h added
// 0003_base_scope.sql; this file's own concern is the 0002 back-fill below,
// which does not need to pin the head version by name).

// TestMigrate_backfillLeavesNoLiveCollision reproduces the upgrade path D4
// describes: rows created under 0001 alone, before 0002 ever ran, must come
// out of the back-fill with distinct edit_version values within their
// workspace and edit_seq left at the highest value handed out. A fresh,
// already-migrated workspace can never exercise this: its rows are created
// after 0002 exists, so every one of them already allocates. This test
// stops at PRAGMA user_version = 1, inserts legacy rows across all four
// tables and two workspaces, then finishes the migration and inspects the
// back-fill's own output.
func TestMigrate_backfillLeavesNoLiveCollision(t *testing.T) {
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

	// Apply 0001 by hand and stop there, mirroring what a database upgraded
	// from before 0002 existed would look like.
	initSQL, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read 0001_init.sql: %v", err)
	}
	if _, err := db.W.ExecContext(ctx, string(initSQL)); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	if _, err := db.W.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set user_version=1: %v", err)
	}

	// Two workspaces, each with rows in every one of the four tables the
	// migration back-fills, all created pre-0002 and therefore sitting at
	// whatever the column DEFAULT will be once 0002 adds it.
	ws1 := insertLegacyWorkspace(t, db, "ws-one")
	ws2 := insertLegacyWorkspace(t, db, "ws-two")

	insertLegacyOverride(t, db, ws1, "GET", "/a")
	insertLegacyOverride(t, db, ws1, "GET", "/b")
	insertLegacyOverride(t, db, ws2, "GET", "/a")

	insertLegacyEndpoint(t, db, ws1, "POST", "/c")
	insertLegacyEndpoint(t, db, ws2, "POST", "/c")
	insertLegacyEndpoint(t, db, ws2, "POST", "/d")

	insertLegacyScenario(t, db, ws1, "snap-1")
	insertLegacyScenario(t, db, ws2, "snap-1")
	insertLegacyScenario(t, db, ws2, "snap-2")

	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}

	// db.Migrate applies every migration past the stopped-at version 1, which
	// since P3h includes 0003_base_scope.sql too -- 0003 does not touch any
	// of the four tables this test backfills, so its only visible effect
	// here is the head version this assertion now reaches.
	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v != 8 {
		t.Fatalf("schema version = %d, want 8", v)
	}

	for _, ws := range []int64{ws1, ws2} {
		versions := liveEditVersions(t, db, ws)
		seen := make(map[int64]bool, len(versions))
		for _, ver := range versions {
			if ver == 0 {
				t.Errorf("workspace %d: back-filled row carries the DEFAULT (0), which the design says a live row must never hold", ws)
			}
			if seen[ver] {
				t.Errorf("workspace %d: edit_version %d shared by two live rows after back-fill", ws, ver)
			}
			seen[ver] = true
		}

		var editSeq int64
		if err := db.R.QueryRowContext(ctx, `SELECT edit_seq FROM workspaces WHERE id = ?`, ws).Scan(&editSeq); err != nil {
			t.Fatalf("read edit_seq for workspace %d: %v", ws, err)
		}
		if int(editSeq) != len(versions) {
			t.Errorf("workspace %d: edit_seq = %d, want %d (highest value handed out)", ws, editSeq, len(versions))
		}
		var maxVersion int64
		for _, ver := range versions {
			if ver > maxVersion {
				maxVersion = ver
			}
		}
		if editSeq != maxVersion {
			t.Errorf("workspace %d: edit_seq = %d, want the max handed-out version %d", ws, editSeq, maxVersion)
		}
	}

	// The very next allocation must continue the sequence, not restart it.
	preAllocMax := int64(0)
	for _, ver := range liveEditVersions(t, db, ws1) {
		if ver > preAllocMax {
			preAllocMax = ver
		}
	}
	next := allocate(t, db, ws1)
	if next <= preAllocMax {
		t.Errorf("first post-migration allocation for workspace %d = %d, want strictly greater than pre-existing max %d", ws1, next, preAllocMax)
	}
}

func insertLegacyWorkspace(t *testing.T, db *store.DB, slug string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, settings, created_at, updated_at)
		VALUES (?, ?, '{}', 0, 0)`, slug, slug)
	if err != nil {
		t.Fatalf("insert legacy workspace %s: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("legacy workspace id: %v", err)
	}
	return id
}

func insertLegacyOverride(t *testing.T, db *store.DB, workspaceID int64, method, path string) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO op_overrides (workspace_id, method, path, updated_at)
		VALUES (?, ?, ?, 0)`, workspaceID, method, path); err != nil {
		t.Fatalf("insert legacy override: %v", err)
	}
}

func insertLegacyEndpoint(t *testing.T, db *store.DB, workspaceID int64, method, path string) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO custom_endpoints (workspace_id, method, path, canonical_path, source_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 0, 0)`, workspaceID, method, path, path); err != nil {
		t.Fatalf("insert legacy endpoint: %v", err)
	}
}

func insertLegacyScenario(t *testing.T, db *store.DB, workspaceID int64, name string) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO scenarios (workspace_id, name, snapshot, created_at)
		VALUES (?, ?, x'00', 0)`, workspaceID, name); err != nil {
		t.Fatalf("insert legacy scenario: %v", err)
	}
}

// liveEditVersions returns every edit_version across the four tables for one
// workspace, workspaces' own row included.
func liveEditVersions(t *testing.T, db *store.DB, workspaceID int64) []int64 {
	t.Helper()
	var out []int64
	rows, err := db.R.QueryContext(t.Context(), `
		SELECT edit_version FROM op_overrides WHERE workspace_id = ?
		UNION ALL
		SELECT edit_version FROM custom_endpoints WHERE workspace_id = ?
		UNION ALL
		SELECT edit_version FROM workspaces WHERE id = ?
		UNION ALL
		SELECT edit_version FROM scenarios WHERE workspace_id = ?`,
		workspaceID, workspaceID, workspaceID, workspaceID)
	if err != nil {
		t.Fatalf("query edit_version rows: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan edit_version: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate edit_version rows: %v", err)
	}
	return out
}

func allocate(t *testing.T, db *store.DB, workspaceID int64) int64 {
	t.Helper()
	var next int64
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		v, err := store.AllocateEditVersion(t.Context(), tx, workspaceID)
		if err != nil {
			return err
		}
		next = v
		return nil
	})
	if err != nil {
		t.Fatalf("allocate edit_version for workspace %d: %v", workspaceID, err)
	}
	return next
}
