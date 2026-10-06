package store

import (
	"path/filepath"
	"testing"
	"uuid"
)

func TestInstallationIdentityUpgrade(t *testing.T) {
	for _, state := range []string{"missing", "existing", "empty"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store27.db")
			db, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			migrations, err := loadMigrations()
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range migrations {
				if m.version <= 27 {
					if err := db.applyMigration(t.Context(), m); err != nil {
						t.Fatal(err)
					}
				}
			}
			var original string
			if err := db.R.QueryRowContext(t.Context(), "SELECT installation_id FROM backend_installation_identity").Scan(&original); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "missing":
				_, err = db.W.ExecContext(t.Context(), "DROP TABLE backend_installation_identity")
			case "empty":
				_, err = db.W.ExecContext(t.Context(), "DROP TRIGGER backend_installation_identity_delete; DELETE FROM backend_installation_identity")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.W.ExecContext(t.Context(), `INSERT INTO backend_command_receipts VALUES('upgrade-proof','key','hash',' { "n": 9007199254740993 } ')`); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			var id string
			if err := db.R.QueryRowContext(t.Context(), "SELECT installation_id FROM backend_installation_identity WHERE singleton=1").Scan(&id); err != nil {
				t.Fatal(err)
			}
			if _, err := uuid.Parse(id); err != nil {
				t.Fatal(err)
			}
			if state == "existing" && id != original {
				t.Fatal("changed existing identity")
			}
			var count int
			if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM backend_installation_identity").Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			for _, query := range []string{"UPDATE backend_installation_identity SET installation_id=installation_id", "DELETE FROM backend_installation_identity"} {
				if _, err := db.W.ExecContext(t.Context(), query); err == nil {
					t.Fatalf("immutable guard missing: %s", query)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			var reopened, receipt string
			if err := db.R.QueryRowContext(t.Context(), "SELECT installation_id FROM backend_installation_identity").Scan(&reopened); err != nil || reopened != id {
				t.Fatal(reopened, err)
			}
			if err := db.R.QueryRowContext(t.Context(), "SELECT response FROM backend_command_receipts WHERE scope='upgrade-proof'").Scan(&receipt); err != nil || receipt != ` { "n": 9007199254740993 } ` {
				t.Fatal(receipt, err)
			}
		})
	}
}
