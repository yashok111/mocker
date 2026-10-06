package backendblob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func populatedFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	entries, err := os.ReadDir("../store/migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		version, e := strconv.Atoi(entry.Name()[:4])
		if e != nil {
			t.Fatal(e)
		}
		if version > 26 {
			continue
		}
		raw, e := os.ReadFile(filepath.Join("../store/migrations", entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	migration, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(t.Context(), migration); err != nil {
		migration.Rollback()
		t.Fatal(err)
	}
	if err = migration.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO backend_projects VALUES('p','project',1,'r','now','now')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = Exec(t.Context(), tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES('r','p','{"schemaVersion":"6"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = Exec(t.Context(), tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES('p','r','node','n',' {"id":"n","n":9007199254740993} ')`); err != nil {
		t.Fatal(err)
	}
	if err = Seal(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestRebuildInjectedSwitchFailureRollsBack(t *testing.T) {
	db := populatedFixture(t)
	// Inject a schema restoration write failure after all old tables have been
	// dropped and shadows renamed; the transaction must restore the old indexes.
	index := len(owners) - 1
	original := owners[index]
	defer func() { owners[index] = original }()
	owners[index].Objects = append(append([]Object(nil), original.Objects...), Object{Kind: "trigger", Name: "injected_failure", SQL: "SELECT * FROM injected_write_failure"})
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = Rebuild(t.Context(), tx, "p"); err == nil || !strings.Contains(err.Error(), "injected_write_failure") {
		t.Fatal("did not reach injected switch failure", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	owners[index] = original
	tx, err = db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = Verify(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = tx.QueryRow(`SELECT document FROM backend_graph_records_documents`).Scan(&raw); err != nil || raw != ` {"id":"n","n":9007199254740993} ` {
		t.Fatal(raw, err)
	}
}
func TestRebuildCancellationAndWrongDomain(t *testing.T) {
	db := populatedFixture(t)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = Rebuild(ctx, tx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	tx.Rollback()
	tx, err = db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = Exec(t.Context(), tx, `INSERT INTO backend_revision_sources(revision_id,payload_key) SELECT id,payload_key FROM backend_revisions`); err == nil {
		t.Fatal("cross-domain copy accepted")
	}
}

func TestRebuildDiskFullLeavesPriorIndexes(t *testing.T) {
	db := populatedFixture(t)
	var pages int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = Rebuild(t.Context(), tx, "p")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "full") {
		t.Fatalf("want SQLITE_FULL: %v", err)
	}
	tx.Rollback()
	tx, err = db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = Verify(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
}
