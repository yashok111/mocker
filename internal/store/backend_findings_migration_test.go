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
	migration21Version(t, db, 23)
	migration21ForeignKeys(t, db)
	for _, name := range []string{"backend_finding_checks", "backend_finding_occurrences", "backend_finding_reviews", "backend_finding_events", "backend_finding_receipts"} {
		var n int
		if err = db.R.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal(name, n, err)
		}
	}
}
