package store_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestMigrateResourceIdentityPreservesPopulatedSchema(t *testing.T) {
	ctx := t.Context()
	path := t.TempDir() + "/upgrade.db"
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{
		"0001_init.sql", "0002_edit_version.sql", "0003_base_scope.sql", "0004_traffic_autoincrement.sql",
		"0005_custom_endpoints_stream.sql", "0006_assets.sql", "0007_fk_indexes.sql", "0008_custom_endpoints_operation.sql",
	} {
		body, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.W.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.W.ExecContext(ctx, `
        PRAGMA user_version = 8;
        INSERT INTO workspaces(id, slug, name, settings, created_at, updated_at) VALUES (1, 'old', 'old', '{}', 1, 2);
        INSERT INTO resources(id, workspace_id, route_family, name, entity_schema, seq) VALUES (40, 1, '/roots', 'roots', '{}', 7);
        INSERT INTO resources(id, workspace_id, route_family, name, parent_id, scope_params, entity_schema, wrapper, filter_map, write_form, seq, seed_count)
            VALUES (41, 1, '/roots/{}/children', 'children', 40, '["rootId"]', '{"type":"object"}', '{}', '{"name":"name"}', 'bare', 9, 2);
        INSERT INTO entities(id, resource_id, parent_entity_id, scope_key, entity_key, data, created_at, updated_at, base_scope_key)
            VALUES (50, 40, NULL, '', '7', '{"id":7}', 10, 11, 'tenant');
        INSERT INTO entities(id, resource_id, parent_entity_id, scope_key, entity_key, data, created_at, updated_at, base_scope_key)
            VALUES (51, 41, 50, '7', 'alice', '{"id":"alice","name":"😀"}', 12, 13, 'tenant');
        INSERT INTO op_overrides(id, workspace_id, method, path, resource_id, updated_at) VALUES (60, 1, 'GET', '/roots', 40, 14);
        INSERT INTO custom_endpoints(id, workspace_id, method, path, canonical_path, source_order, resource_id, created_at, updated_at)
            VALUES (70, 1, 'GET', '/children', '/children', 1, 41, 15, 16);
    `); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"SELECT * FROM resources ORDER BY id", "SELECT * FROM entities ORDER BY id",
		"SELECT * FROM op_overrides ORDER BY id", "SELECT * FROM custom_endpoints ORDER BY id",
		"SELECT name, sql FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL AND name NOT LIKE 'design_scenario%' ORDER BY name",
	}
	before := make([]string, len(queries))
	for i, query := range queries {
		before[i] = queryRows(t, db.R, query)
	}
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for i, query := range queries {
		if got := queryRows(t, db.R, query); got != before[i] {
			t.Errorf("%s changed:\nbefore %s\nafter %s", query, before[i], got)
		}
	}
	if got := queryRows(t, db.R, "PRAGMA foreign_key_check"); got != "" {
		t.Fatalf("foreign key violations: %s", got)
	}
	if _, err := db.W.ExecContext(ctx, "DELETE FROM resources WHERE id = 41"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// A deliberate replay must not lower sqlite_sequence after deletion.
	migrationSQL, err := os.ReadFile("migrations/0009_resource_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, string(migrationSQL)); return err }); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	res, err := db.W.ExecContext(ctx, "INSERT INTO resources(workspace_id, route_family, name, entity_schema) VALUES (1, '/new', 'new', '{}')")
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil || id <= 41 {
		t.Fatalf("new resource id=%d err=%v, must exceed retired id 41", id, err)
	}
	if got := queryRows(t, db.R, "PRAGMA foreign_key_check"); got != "" {
		t.Fatalf("foreign key violations: %s", got)
	}
	if _, err := db.W.ExecContext(ctx, "DELETE FROM resources"); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, string(migrationSQL)); return err }); err != nil {
		t.Fatal(err)
	}
	res, err = db.W.ExecContext(ctx, "INSERT INTO resources(workspace_id, route_family, name, entity_schema) VALUES (1, '/empty', 'empty', '{}')")
	if err != nil {
		t.Fatal(err)
	}
	next, err := res.LastInsertId()
	if err != nil || next <= id {
		t.Fatalf("new resource id=%d err=%v after replay on empty table, must exceed %d", next, err, id)
	}
}

func queryRows(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result string
	for rows.Next() {
		values := make([]sql.NullString, len(cols))
		targets := make([]any, len(cols))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if value.Valid {
				result += "[" + value.String + "]"
			} else {
				result += "[NULL]"
			}
		}
		result += "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
