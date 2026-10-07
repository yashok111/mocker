package backendblob

import (
	"database/sql"
	"errors"
	"testing"
)

// addProject seals a second project with one graph record beside the
// populatedFixture's project p.
func addProject(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO backend_projects VALUES(?,?,1,?,'now','now')`, id, "project "+id, "r"+id); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = Exec(t.Context(), tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,'{"schemaVersion":"6"}')`, "r"+id, id); err != nil {
		t.Fatal(err)
	}
	if _, err = Exec(t.Context(), tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES(?,?,'node','n','{"id":"n"}')`, id, "r"+id); err != nil {
		t.Fatal(err)
	}
	if err = Seal(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func rebuildOne(t *testing.T, db *sql.DB, project string) error {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = Rebuild(t.Context(), tx, project); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func verifyAll(t *testing.T, db *sql.DB) error {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return Verify(t.Context(), tx)
}

// review 2026-10-06, F163: rebuild --project A copied project B's physical
// rows through unchanged, then shadow-compared and verified EVERY project.
// Derived damage in B therefore aborted A's repair (and the reverse), so with
// both damaged neither could ever be repaired. Each project's rebuild must
// repair its own rows and leave the other project's (damaged or missing)
// rows exactly as they were — for its own rebuild to fix.
func TestRebuildRepairsOneProjectWhileAnotherIsDamaged(t *testing.T) {
	for _, damage := range []struct{ name, sql string }{
		{"edited", `DROP TRIGGER backend_graph_records_blob_no_update; UPDATE backend_graph_records SET name='damaged'`},
		{"missing", `DROP TRIGGER backend_graph_records_blob_no_delete; DELETE FROM backend_graph_records`},
	} {
		t.Run(damage.name, func(t *testing.T) {
			db := populatedFixture(t)
			addProject(t, db, "q")
			if _, err := db.Exec(damage.sql); err != nil {
				t.Fatal(err)
			}
			if err := verifyAll(t, db); !errors.Is(err, ErrDerived) {
				t.Fatalf("damage not reported: %v", err)
			}
			if err := rebuildOne(t, db, "p"); err != nil {
				t.Fatalf("rebuild p blocked by q's damage: %v", err)
			}
			var pRows, qRows int
			if err := db.QueryRow(`SELECT count(*) FROM backend_graph_records WHERE project_id='p' AND name=''`).Scan(&pRows); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM backend_graph_records WHERE project_id='q' AND name=''`).Scan(&qRows); err != nil {
				t.Fatal(err)
			}
			if pRows != 1 || qRows != 0 {
				t.Fatalf("after rebuild p: p repaired=%d, q touched=%d", pRows, qRows)
			}
			if err := verifyAll(t, db); !errors.Is(err, ErrDerived) {
				t.Fatalf("q's damage hidden after p's rebuild: %v", err)
			}
			if err := rebuildOne(t, db, "q"); err != nil {
				t.Fatalf("rebuild q: %v", err)
			}
			if err := verifyAll(t, db); err != nil {
				t.Fatalf("both rebuilt: %v", err)
			}
		})
	}
}
