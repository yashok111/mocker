package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// MigrateThrough is Migrate stopped at version: it applies, in order, every
// embedded migration above the file's user_version and at or below version,
// through the same applyMigration hooks (21 and 27 included) the server uses.
//
// It exists because the per-migration tests in this package used to call
// Migrate and assert the head they were written against (24, 25), and then
// wrote raw SQL against the inline `document` columns of that schema. Store27
// (0027_backend_payload_blobs, B6.3) rebuilt every immutable owner table into
// a payload_key projection, so "migrate to head" stopped meaning "migrate to
// the schema this test asserts" and thirteen of them went red at once. A test
// that guards the DDL of migration N pins the schema it exercises here; the
// 26→27 rebuild is guarded by the TestBlob* tests on its own.
func (db *DB) MigrateThrough(ctx context.Context, version int) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	current, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if current > version {
		return fmt.Errorf("schema version %d is already past %d", current, version)
	}
	for _, m := range migrations {
		if m.version <= current || m.version > version {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %04d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// latestMigration is the head this binary embeds — what Migrate reaches. A
// test that migrates to the head asserts this rather than a literal, which
// every new migration silently made stale.
func latestMigration(t testing.TB) int {
	t.Helper()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return ms[len(ms)-1].version
}

// lastInlinePayloadSchema is the last store schema whose immutable owner
// tables keep their JSON in inline `document` columns; 0027 moved them behind
// backend_payload_blobs.
const lastInlinePayloadSchema = 26

// OpenInlinePayloadSchema is a fresh database migrated through
// lastInlinePayloadSchema, for the store_test storage tests that pin the
// B4.1/B4.2 ownership and immutability triggers with raw positional SQL over
// inline `document` columns. They used testkit.NewDB, which migrates to the
// head, and every one of them went red when 0027 replaced those columns with
// payload keys (backend_revisions has no `document` at Store27, and a JSON
// literal in its payload_key slot fails the blob foreign key before the
// trigger under test is reached).
func OpenInlinePayloadSchema(t testing.TB) *DB {
	t.Helper()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "inline.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateThrough(t.Context(), lastInlinePayloadSchema); err != nil {
		t.Fatal(err)
	}
	return db
}
