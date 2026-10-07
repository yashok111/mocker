package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendblob"
)

func blobBaseline(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version <= 26 {
			if err = db.applyMigration(t.Context(), m); err != nil {
				t.Fatal(err)
			}
		}
	}
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		for _, q := range []string{`INSERT INTO backend_projects VALUES('p','P',1,'r','now','now')`, `INSERT INTO backend_revisions VALUES('r','p','{ "schemaVersion":"6", "semanticHash":"unchanged", "n":9007199254740993 }')`, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES('p','r','node','n',' {"id":"n","n":9007199254740993} ')`, `INSERT INTO backend_command_receipts VALUES('scope','key','hash',' { "receipt": 9007199254740993 } ')`} {
			if _, err := tx.ExecContext(t.Context(), q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}
func TestBlobMigrationBytes(t *testing.T) {
	db := blobBaseline(t)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.W.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 27 {
		t.Fatalf("version %d %v", version, err)
	}
	for _, q := range []struct{ sql, want string }{{`SELECT document FROM backend_graph_records_documents`, ` {"id":"n","n":9007199254740993} `}, {`SELECT response FROM backend_command_receipts`, ` { "receipt": 9007199254740993 } `}, {`SELECT document FROM backend_revisions_documents`, `{ "schemaVersion":"6", "semanticHash":"unchanged", "n":9007199254740993 }`}} {
		var got string
		if err := db.R.QueryRow(q.sql).Scan(&got); err != nil || got != q.want {
			t.Fatalf("byte witness %q %v", got, err)
		}
	}
	var foreign int
	if err := db.W.QueryRow("PRAGMA foreign_keys").Scan(&foreign); err != nil || foreign != 1 {
		t.Fatal(foreign, err)
	}
}
func TestBlobMigrationRollback(t *testing.T) {
	db := blobBaseline(t)
	// Fail after several owners have already been copied, using an invalid
	// historical graph owner reference; migration must not leave any new schema.
	if _, err := db.W.Exec(`PRAGMA foreign_keys=OFF; INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES('p','missing','node','bad','{}'); PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}
	err := db.Migrate(t.Context(), nil)
	if err == nil {
		t.Fatal("invalid old ownership accepted")
	}
	var v, fk, n int
	if err = db.W.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if err = db.W.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if err = db.W.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name LIKE 'backend_payload_%' OR name LIKE '%_blob_new'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if v != 26 || fk != 1 || n != 0 {
		t.Fatal(fmt.Sprint(v, fk, n))
	}
	var raw string
	if err = db.W.QueryRow("SELECT document FROM backend_graph_records WHERE id='bad'").Scan(&raw); err != nil || strings.TrimSpace(raw) != "{}" {
		t.Fatal(raw, err)
	}
}

func TestBlobOfflineRebuildAndCanonicalLoss(t *testing.T) {
	db := blobBaseline(t)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// Isolated corruption fixture: remove guards and damage only query metadata.
	if _, err := db.W.Exec(`DROP TRIGGER backend_graph_records_blob_no_update; UPDATE backend_graph_records SET name='damaged'; DROP INDEX backend_graph_subject`); err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := BackendStorage(t.Context(), path, "", false); err == nil || !strings.Contains(err.Error(), "derived") {
		t.Fatalf("verify: %v", err)
	}
	for range 2 {
		if err := BackendStorage(t.Context(), path, "p", true); err != nil {
			t.Fatal(err)
		}
	}
	if err := BackendStorage(t.Context(), path, "", false); err != nil {
		t.Fatal(err)
	}
	again, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = again.W.Exec(`DROP TRIGGER backend_payload_members_delete; DELETE FROM backend_payload_members WHERE owner='backend_graph_records'`); err != nil {
		t.Fatal(err)
	}
	again.Close()
	if err := BackendStorage(t.Context(), path, "p", true); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("canonical loss repaired: %v", err)
	}
}
func TestBlobOfflineBusyDoesNotMutate(t *testing.T) {
	db := blobBaseline(t)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := BackendStorage(t.Context(), db.Path(), "p", true); err == nil {
		t.Fatal("maintenance acquired application connection while idle")
	}
	tx, err := db.R.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM backend_graph_records").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err = BackendStorage(t.Context(), db.Path(), "p", true); err == nil {
		t.Fatal("maintenance acquired busy database")
	}
	if err = tx.QueryRow("SELECT count(*) FROM backend_graph_records").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestBlobHundredRevisionsDeduplicateLogicalMembership(t *testing.T) {
	db := blobBaseline(t)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for i := range 99 {
		id := fmt.Sprintf("copy-%03d", i)
		err := db.Write(t.Context(), func(tx *sql.Tx) error {
			if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_revisions(id,project_id,payload_key) SELECT ?,'p',payload_key FROM backend_revisions WHERE id='r'`, id); err != nil {
				return err
			}
			_, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,from_id,to_id,subject_id,payload_key) SELECT project_id,?,record_type,id,kind,name,parent_id,from_id,to_id,subject_id,payload_key FROM backend_graph_records WHERE revision_id='r'`, id)
			return err
		})
		if err != nil {
			t.Fatal(i, err)
		}
	}
	var logical, physical int
	if err := db.R.QueryRow(`SELECT count(*) FROM backend_graph_records`).Scan(&logical); err != nil {
		t.Fatal(err)
	}
	if err := db.R.QueryRow(`SELECT count(*) FROM backend_payload_blobs WHERE owner='backend_graph_records/document'`).Scan(&physical); err != nil {
		t.Fatal(err)
	}
	if logical != 100 || physical != 1 {
		t.Fatalf("logical=%d physical=%d", logical, physical)
	}
	if err := db.Read(t.Context(), func(tx *sql.Tx) error { return backendblob.Verify(t.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
}
func TestBlobRegistryCoversStore26JSONOwners(t *testing.T) {
	db := blobBaseline(t)
	known := map[string]map[string]bool{}
	for _, o := range backendblob.Registry() {
		known[o.Table] = map[string]bool{}
		for _, c := range o.Columns {
			if c.Payload {
				known[o.Table][c.Name] = true
			}
		}
	}
	for _, e := range backendblob.Exclusions() {
		if e.Reason == "" {
			t.Fatal("silent exclusion")
		}
		known[e.Table] = map[string]bool{}
		for _, c := range e.Columns {
			known[e.Table][c] = true
		}
	}
	rows, err := db.R.Query(`SELECT name,sql FROM sqlite_schema WHERE type='table' AND name LIKE 'backend_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	fields := strings.Fields("document provenance commands origin_pin items_json body document_json authorization_json input_json terminal_report_json request_json response receipt result_json target_json diagnostic manifest preview details ready_reference")
	for rows.Next() {
		var table, ddl string
		if err = rows.Scan(&table, &ddl); err != nil {
			t.Fatal(err)
		}
		cols, err := db.R.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var cid, nn, pk int
			var name, typ string
			var def sql.NullString
			if err = cols.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
				t.Fatal(err)
			}
			for _, f := range fields {
				if f == name && !known[table][name] {
					t.Fatalf("unregistered %s.%s", table, name)
				}
			}
		}
		if err = cols.Err(); err != nil {
			t.Fatal(err)
		}
		cols.Close()
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBlobRecoveryReviewRegressions(t *testing.T) {
	for _, fault := range []string{"whole-manifest-loss", "parent-projection-loss", "read-view-loss", "replay-order"} {
		t.Run(fault, func(t *testing.T) {
			db := blobBaseline(t)
			if _, err := db.W.Exec(`INSERT INTO backend_revision_sources VALUES('r',' {"legacy":9007199254740993} '); INSERT INTO backend_replay_runs(id,project_id,target_id,status,input_hash,input_json,version,author,created_at,updated_at) VALUES('run','p','target','queued','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}',1,'author','now','now'); INSERT INTO backend_replay_steps VALUES('run','z','request-z','order','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',x'7b7d'); INSERT INTO backend_replay_steps VALUES('run','a','request-a','order','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',x'7b7d');`); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "whole-manifest-loss":
				if _, err := db.W.Exec(`DROP TRIGGER backend_payload_members_delete; DROP TRIGGER backend_payload_manifests_delete; DELETE FROM backend_payload_members WHERE owner='backend_graph_records'; DELETE FROM backend_payload_manifests WHERE owner='backend_graph_records'`); err != nil {
					t.Fatal(err)
				}
			case "parent-projection-loss":
				if _, err := db.W.Exec(`PRAGMA foreign_keys=OFF; DROP TRIGGER backend_revisions_blob_no_delete; DELETE FROM backend_revisions; PRAGMA foreign_keys=ON;`); err != nil {
					t.Fatal(err)
				}
			case "read-view-loss":
				if _, err := db.W.Exec(`DROP VIEW backend_graph_records_documents`); err != nil {
					t.Fatal(err)
				}
			}
			path := db.Path()
			db.Close()
			err := BackendStorage(t.Context(), path, "", false)
			if fault == "whole-manifest-loss" {
				if err == nil || !strings.Contains(err.Error(), "canonical") {
					t.Fatalf("missing canonical accepted: %v", err)
				}
				if err = BackendStorage(t.Context(), path, "p", true); err == nil {
					t.Fatal("loss repaired by dropping history")
				}
				return
			}
			if fault != "replay-order" && (err == nil || !strings.Contains(err.Error(), "derived")) {
				t.Fatalf("derived fault: %v", err)
			}
			for range 2 {
				if err = BackendStorage(t.Context(), path, "p", true); err != nil {
					t.Fatal(err)
				}
			}
			again, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			var steps string
			if err = again.R.QueryRow(`SELECT group_concat(step_id,',') FROM (SELECT step_id FROM backend_replay_steps_documents ORDER BY rowid)`).Scan(&steps); err != nil || steps != "z,a" {
				t.Fatalf("order %q %v", steps, err)
			}
			var raw string
			if err = again.R.QueryRow(`SELECT document FROM backend_revision_sources_documents`).Scan(&raw); err != nil || raw != ` {"legacy":9007199254740993} ` {
				t.Fatal(raw, err)
			}
		})
	}
}

func TestBlobOwnershipMappingsRebuild(t *testing.T) {
	db := blobBaseline(t)
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO backend_observation_sets VALUES('p','set',1,'name','context',2,1)`,
			`INSERT INTO backend_observation_versions VALUES('p','set',1,'hash','{"version":1}')`,
			`INSERT INTO backend_observation_blobs VALUES('hash',' {"n":9007199254740993} ')`,
			`INSERT INTO backend_observation_members VALUES('p','set','record','hash',1)`,
			`INSERT INTO backend_analysis_inputs VALUES('p','input','{}',2)`,
			`INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,created_at,updated_at) VALUES('job','p','input','diagnostics','completed',1,'now','now')`,
			`INSERT INTO backend_analysis_manifests VALUES('p','job',1,0,'hash','{}',2)`,
			`INSERT INTO backend_finding_checks VALUES('p','job',1,'fingerprint','original-scope','present')`,
		} {
			if _, e := tx.Exec(q); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err = db.W.Exec(`DROP TRIGGER backend_observation_members_delete; DROP TRIGGER backend_observation_members_blob_no_delete; DELETE FROM backend_observation_members; DROP TRIGGER backend_finding_checks_update; DROP TRIGGER backend_finding_checks_blob_no_update; UPDATE backend_finding_checks SET scope_key='wrong'; DROP INDEX backend_payload_members_identity;`); err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	db.Close()
	if err = BackendStorage(t.Context(), path, "", false); err == nil {
		t.Fatal("mapping loss not detected")
	}
	if err = BackendStorage(t.Context(), path, "p", true); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var hash, scope string
	if err = reopened.R.QueryRow(`SELECT hash FROM backend_observation_members WHERE record_id='record'`).Scan(&hash); err != nil || hash != "hash" {
		t.Fatal(hash, err)
	}
	if err = reopened.R.QueryRow(`SELECT scope_key FROM backend_finding_checks`).Scan(&scope); err != nil || scope != "original-scope" {
		t.Fatal(scope, err)
	}
}

func TestBlobMigrationDiskFullAndOriginalSettings(t *testing.T) {
	for _, mode := range []string{"disk-full", "foreign-keys-off"} {
		t.Run(mode, func(t *testing.T) {
			db := blobBaseline(t)
			if mode == "disk-full" {
				var pages int64
				if err := db.W.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
					t.Fatal(err)
				}
				if _, err := db.W.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.W.Exec("PRAGMA foreign_keys=OFF"); err != nil {
					t.Fatal(err)
				}
			}
			err := db.Migrate(t.Context(), nil)
			var version, foreign int
			db.W.QueryRow("PRAGMA user_version").Scan(&version)
			db.W.QueryRow("PRAGMA foreign_keys").Scan(&foreign)
			if mode == "disk-full" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "full") || version != 26 || foreign != 1 {
					t.Fatalf("diskfull rollback: v%d fk%d %v", version, foreign, err)
				}
			} else if err != nil || version != 27 || foreign != 0 {
				t.Fatalf("original settings: v%d fk%d %v", version, foreign, err)
			}
		})
	}
}

func TestBlobMigrationFailureAfterPhysicalSwitchRollsBack(t *testing.T) {
	db := blobBaseline(t)
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var m migration
	for _, candidate := range ms {
		if candidate.version == 27 {
			m = candidate
		}
	}
	// A conflicting view causes failure after original tables have been dropped
	// and renamed replacements installed, not merely during the initial copy.
	m.sql += ` CREATE VIEW backend_graph_records_documents AS SELECT 1;`
	if err = db.applyMigration(t.Context(), m); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("did not reach switch failure: %v", err)
	}
	var version, foreign, count int
	db.W.QueryRow("PRAGMA user_version").Scan(&version)
	db.W.QueryRow("PRAGMA foreign_keys").Scan(&foreign)
	if err = db.R.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name LIKE 'backend_payload_%' OR name='backend_graph_records_documents'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if version != 26 || foreign != 1 || count != 0 {
		t.Fatal(version, foreign, count)
	}
	var raw string
	if err = db.R.QueryRow("SELECT document FROM backend_graph_records").Scan(&raw); err != nil || raw != ` {"id":"n","n":9007199254740993} ` {
		t.Fatal(raw, err)
	}
	if err = db.R.QueryRow("SELECT response FROM backend_command_receipts").Scan(&raw); err != nil || raw != ` { "receipt": 9007199254740993 } ` {
		t.Fatal(raw, err)
	}
}
