package main

import (
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendblob"
	"github.com/yashok111/mocker/internal/store"
)

func TestBackendPurgePreviewConfirmationAndForeignHistory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "purge.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	const project = "10000000-0000-4000-8000-000000000001"
	const foreign = "10000000-0000-4000-8000-000000000002"
	const secret = "SYNTHETIC-ERASURE-WITNESS-NEVER-A-CREDENTIAL"
	const uniqueSecret = "UNSHARED-SYNTHETIC-ERASURE-WITNESS"
	const freedSecret = "HISTORICAL-FREELIST-SYNTHETIC-WITNESS"
	if _, err := db.W.Exec(`PRAGMA secure_delete=OFF`); err != nil {
		t.Fatal(err)
	}
	if err = db.Write(t.Context(), func(tx *sql.Tx) error {
		for _, id := range []string{project, foreign} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_projects VALUES(?,'Fixture',1,?,'now','now')`, id, id); err != nil {
				return err
			}
			if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, id, id, `{"schemaVersion":"1","summary":"`+secret+`"}`); err != nil {
				return err
			}
		}
		if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,document) VALUES(?,?,'node','unique','symbol',?,?)`, project, project, uniqueSecret, `{"id":"unique","kind":"symbol","name":"`+uniqueSecret+`"}`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_import_sessions VALUES('session',?,'aborted',1,?)`, project, `{"capture":"`+secret+`"}`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_import_records VALUES('session','node','key',?)`, `{"snippet":"`+secret+`"}`); err != nil {
			return err
		}
		for _, family := range []string{"proposal-create", "proposal-apply", "change-proposal-create", "change-proposal-apply", "change-proposal-restore", "change-proposal-rebase", "change-proposal-lifecycle", "saved-view-create", "saved-view-save", "api-pins", "artifact-pins"} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_command_receipts VALUES(?,?,?,?)`, family+":"+project+":resource", "receipt", "hash", `{"value":"`+uniqueSecret+`"}`); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_command_receipts VALUES(?,?,?,?)`, "proposal-apply:"+foreign+":"+project, "foreign", "hash", `{"keep":true}`); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO backend_command_receipts VALUES(?,?,?,?)`, "project:"+project, "old", "hash", `{"id":"`+project+`","name":"`+secret+`"}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`UPDATE backend_import_sessions SET document=? WHERE id='session'`, `{"capture":"`+strings.Repeat(freedSecret, 1000)+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`UPDATE backend_import_sessions SET document='{}' WHERE id='session'`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if err := runBackendStorage(t.Context(), []string{"preview-purge", "--db", path, "--project", project}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("preview disclosed stored content")
	}
	var preview struct {
		ConfirmationHash string `json:"confirmationHash"`
	}
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || len(preview.ConfirmationHash) != 64 {
		t.Fatalf("missing exact confirmation: %v", err)
	}
	out.Reset()
	if err := runBackendStorage(t.Context(), []string{"purge-project", "--db", path, "--project", project, "--confirmation", strings.Repeat("0", 64)}, &out, &stderr); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	// Equal row counts do not make a stale preview safe: content changes are
	// included in its hash and must require a new explicit confirmation.
	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`UPDATE backend_projects SET name='Changed' WHERE id=?`, project); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runBackendStorage(t.Context(), []string{"purge-project", "--db", path, "--project", project, "--confirmation", preview.ConfirmationHash}, &out, &stderr); err == nil {
		t.Fatal("changed preview accepted")
	}
	out.Reset()
	if err := runBackendStorage(t.Context(), []string{"preview-purge", "--db", path, "--project", project}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	args := []string{"purge-project", "--db", path, "--project", project, "--confirmation", preview.ConfirmationHash}
	if err := runBackendStorage(t.Context(), args, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	first := out.String()
	out.Reset()
	if err := runBackendStorage(t.Context(), args, &out, &stderr); err != nil || out.String() != first {
		t.Fatalf("purge receipt replay changed: %v", err)
	}
	if strings.Contains(out.String(), secret) {
		t.Fatal("receipt disclosed stored content")
	}
	databaseBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, []byte(freedSecret)) {
		t.Fatal("erased historical bytes remain in local database freelist")
	}
	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`SELECT count(*) FROM backend_projects WHERE id='` + project + `'`, `SELECT count(*) FROM backend_revisions WHERE project_id='` + project + `'`, `SELECT count(*) FROM backend_import_records`, `SELECT count(*) FROM backend_command_receipts WHERE scope='project:` + project + `'`} {
		var count int
		if err := db.R.QueryRow(q).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retained project rows: %d %v", count, err)
		}
	}
	var doc string
	if err := db.R.QueryRow(`SELECT document FROM backend_revisions_documents WHERE project_id=?`, foreign).Scan(&doc); err != nil || !strings.Contains(doc, secret) {
		t.Fatalf("foreign/shared history changed: %v", err)
	}
	var retained int
	if err := db.R.QueryRow(`SELECT count(*) FROM backend_command_receipts WHERE instr(response,?)>0`, uniqueSecret).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("sensitive replay receipts retained: %d %v", retained, err)
	}
	if err := db.R.QueryRow(`SELECT count(*) FROM backend_command_receipts WHERE scope=?`, "proposal-apply:"+foreign+":"+project).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("foreign replay receipt removed: %d %v", retained, err)
	}
	if err := db.R.QueryRow(`SELECT count(*) FROM backend_payload_blobs WHERE instr(CAST(payload AS TEXT),?)>0`, uniqueSecret).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("unshared sensitive payload retained: %d %v", retained, err)
	}
	if err := db.Read(t.Context(), func(tx *sql.Tx) error { return backendblob.Verify(t.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
}
