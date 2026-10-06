package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendDiagramsDeferredHead(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "diagrams.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(ctx, slog.Default()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.R.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 25 {
		t.Fatalf("schema %d: %v", version, err)
	}
	if err = db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO backend_projects(id,name,version,current_revision_id,created_at,updated_at) VALUES('p','project',1,'r','','')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_revisions(id,project_id,document) VALUES('r','p','{}')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	insertHead := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO backend_diagrams(project_id,id,kind,version,target_json) VALUES('p','d','architecture',1,'{}')`)
		return err
	}
	if err = db.Write(ctx, func(tx *sql.Tx) error { return insertHead(ctx, tx) }); err == nil {
		t.Fatal("partial head committed without immutable version")
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if err := insertHead(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO backend_diagram_versions(project_id,diagram_id,version,content_hash,target_hash,document,author,created_at,provenance,provenance_hash) VALUES('p','d',1,'h','t','{}','a','','{}',?)`, strings.Repeat("a", 64))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.W.Exec(`UPDATE backend_diagrams SET target_json='{"changed":true}'`); err == nil {
		t.Fatal("immutable target changed")
	}
	if _, err = db.W.Exec(`UPDATE backend_diagrams SET version=3`); err == nil {
		t.Fatal("head skipped a version")
	}
	rows, err := db.R.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
