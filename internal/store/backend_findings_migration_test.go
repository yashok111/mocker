package store

import (
	"path/filepath"
	"testing"
)

func TestBackendFindingsMigration22To23(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "findings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version <= 22 {
			if err = db.applyMigration(t.Context(), m); err != nil {
				t.Fatal(err)
			}
		}
	}
	migration21Version(t, db, 22)
	if err = db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// Was a literal 24, the head when B5.1 landed; every later migration
	// (25, 26, 27) turned it red without touching 22→23. Migrate goes to the
	// head, so the assertion is the head.
	migration21Version(t, db, latestMigration(t))
	migration21ForeignKeys(t, db)
	for _, name := range []string{"backend_finding_checks", "backend_finding_occurrences", "backend_finding_reviews", "backend_finding_events", "backend_finding_receipts"} {
		var n int
		if err = db.R.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal(name, n, err)
		}
	}
}
