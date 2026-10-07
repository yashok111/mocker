package store

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendReplayMigration24To25(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name >= "0025" {
			break
		}
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		raw, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.W.ExecContext(t.Context(), string(raw)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err = db.W.ExecContext(t.Context(), `PRAGMA user_version=24`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.W.ExecContext(t.Context(), `INSERT INTO users(id,name,created_at) VALUES(987,'migration-proof',1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	version, err := db.SchemaVersion(t.Context())
	// Was a literal 25 (the head when B5.3 landed); Migrate goes to the head.
	if err != nil || version != latestMigration(t) {
		t.Fatalf("version %d: %v", version, err)
	}
	var name string
	if err = db.R.QueryRowContext(t.Context(), `SELECT name FROM users WHERE id=987`).Scan(&name); err != nil || name != "migration-proof" {
		t.Fatal("existing data changed", err)
	}
	for _, table := range []string{"profiles", "packages", "runs", "receipts", "steps", "evidence", "target_leases", "revocations"} {
		var n int
		err = db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, fmt.Sprint("backend_replay_", table)).Scan(&n)
		if err != nil || n != 1 {
			t.Fatalf("%s: %v", table, err)
		}
	}
	if err = db.Migrate(t.Context(), nil); err != nil {
		t.Fatal("repeat migration", err)
	}
}
