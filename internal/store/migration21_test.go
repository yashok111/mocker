package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigration21PreservesHistoryAndForeignKeys(t *testing.T) {
	db := migration21Fixture(t)
	before := migration21History(t, db)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	migration21Version(t, db, 23)
	if after := migration21History(t, db); !slices.Equal(before, after) {
		t.Fatalf("history bytes changed\nbefore: %v\nafter: %v", before, after)
	}
	migration21ForeignKeys(t, db)
	// Legacy receipt replay reads the acknowledged payload without re-encoding it.
	var receipt []byte
	if err := db.R.QueryRowContext(t.Context(), "SELECT response FROM backend_command_receipts WHERE scope='change:proposal' AND key='old-key'").Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receipt, []byte("{ \"accepted\": true, \"escaped\": \"\\u0061\" }\n")) {
		t.Fatalf("receipt changed: %q", receipt)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	migration21Version(t, reopened, 23)
	if after := migration21History(t, reopened); !slices.Equal(before, after) {
		t.Fatal("reopen changed historical bytes")
	}
	migration21ForeignKeys(t, reopened)
}

func TestMigration21RollbackAndConnectionState(t *testing.T) {
	for _, stage := range []string{"after copy", "after drop", "before version"} {
		t.Run(stage, func(t *testing.T) {
			db := migration21Fixture(t)
			before := migration21History(t, db)
			m := migration21Definition(t)
			switch stage {
			case "after copy":
				m.sql = strings.Replace(m.sql, "DROP TRIGGER backend_change_command_matches_batch;", "SELECT * FROM injected_missing_table;\nDROP TRIGGER backend_change_command_matches_batch;", 1)
			case "after drop":
				m.sql = strings.Replace(m.sql, "ALTER TABLE backend_change_proposals_v21 RENAME", "SELECT * FROM injected_missing_table;\nALTER TABLE backend_change_proposals_v21 RENAME", 1)
			case "before version":
				m.sql += "\nSELECT * FROM injected_missing_table;"
			}
			if err := db.applyMigration(t.Context(), m); err == nil || !strings.Contains(err.Error(), "injected_missing_table") {
				t.Fatalf("injected failure: %v", err)
			}
			migration21Version(t, db, 20)
			migration21ForeignKeys(t, db)
			if after := migration21History(t, db); !slices.Equal(before, after) {
				t.Fatal("rollback changed history bytes")
			}
			var replacementCount int
			if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name LIKE '%_v21' OR name LIKE 'backend_analysis_%'").Scan(&replacementCount); err != nil {
				t.Fatal(err)
			}
			if replacementCount != 0 {
				t.Fatalf("rollback leaked %d schema objects", replacementCount)
			}
			path := db.Path()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			migration21Version(t, reopened, 20)
			migration21ForeignKeys(t, reopened)
			if after := migration21History(t, reopened); !slices.Equal(before, after) {
				t.Fatal("reopen after failure changed bytes")
			}
		})
	}
}

func TestMigration21AnalysisStorageGuards(t *testing.T) {
	db := migration21Fixture(t)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	migration21Version(t, db, 23)
	exec := func(q string) {
		t.Helper()
		if _, err := db.W.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	reject := func(q string) {
		t.Helper()
		if _, err := db.W.ExecContext(t.Context(), q); err == nil {
			t.Fatalf("invalid write accepted: %s", q)
		}
	}
	exec(`INSERT INTO backend_analysis_inputs(project_id,input_hash,document,input_bytes) VALUES ('p','input','{}',2)`)
	exec(`INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,created_at,updated_at,reserved_output_bytes,reserved_terminal_bytes) VALUES ('job','p','input','impact','queued',1,'now','now',100,10)`)
	exec(`INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,created_at,updated_at) VALUES ('future','p','input','test-gap','queued',1,'now','now')`)
	reject(`INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,created_at,updated_at) VALUES ('foreign','other','input','diff','queued',1,'now','now')`)
	reject(`UPDATE backend_analysis_jobs SET result_version=1 WHERE id='job'`)
	reject(`UPDATE backend_analysis_jobs SET reserved_output_bytes=9 WHERE id='job'`)
	reject(`UPDATE backend_analysis_jobs SET progress_states=-1 WHERE id='job'`)
	exec(`INSERT INTO backend_analysis_manifests(project_id,job_id,result_version,high_water_sequence,result_hash,document,manifest_bytes) VALUES ('p','job',1,0,'partial','{}',2)`)
	exec(`UPDATE backend_analysis_jobs SET result_version=1 WHERE id='job'`)
	exec(`INSERT INTO backend_analysis_chunks(project_id,job_id,sequence,section,content_hash,items_json,record_count,chunk_bytes) VALUES ('p','job',1,'changes','hash','[]',0,2)`)
	exec(`INSERT INTO backend_analysis_receipts(project_id,action,key,job_id,request_hash,response,response_bytes) VALUES ('p','start','key','job','request','{}',2)`)
	for _, q := range []string{
		`INSERT INTO backend_analysis_chunks(project_id,job_id,sequence,section,content_hash,items_json,record_count,chunk_bytes) VALUES ('p','job',0,'changes','hash','[]',0,2)`,
		`INSERT INTO backend_analysis_manifests(project_id,job_id,result_version,high_water_sequence,result_hash,document,manifest_bytes) VALUES ('p','job',0,0,'hash','{}',2)`,
		`INSERT INTO backend_analysis_manifests(project_id,job_id,result_version,high_water_sequence,result_hash,document,manifest_bytes) VALUES ('other','job',2,0,'hash','{}',2)`,
		`UPDATE backend_analysis_jobs SET result_bytes=-1 WHERE id='job'`,
		`UPDATE backend_change_proposals SET ready_reference='invalid' WHERE id='proposal'`,
	} {
		reject(q)
	}

	for _, column := range []string{"progress_states", "progress_dependency_visits", "progress_findings", "progress_records", "result_bytes", "reserved_output_bytes", "reserved_terminal_bytes"} {
		reject("UPDATE backend_analysis_jobs SET " + column + "=-1 WHERE id='job'")
	}
	for _, q := range []string{
		`INSERT INTO backend_analysis_inputs VALUES ('p','negative','{}',-1)`,
		`INSERT INTO backend_analysis_chunks VALUES ('p','job',2,'findings','hash','[]',-1,2)`,
		`INSERT INTO backend_analysis_chunks VALUES ('p','job',2,'findings','hash','[]',0,-1)`,
		`INSERT INTO backend_analysis_chunks VALUES ('other','job',2,'findings','hash','[]',0,2)`,
		`INSERT INTO backend_analysis_manifests VALUES ('p','job',2,-1,'hash','{}',2)`,
		`INSERT INTO backend_analysis_manifests VALUES ('p','job',2,1,'hash','{}',-1)`,
		`INSERT INTO backend_analysis_receipts VALUES ('p','start','negative','job','request','{}',-1)`,
		`INSERT INTO backend_analysis_receipts VALUES ('other','start','foreign','job','request','{}',2)`,
		`INSERT INTO backend_analysis_inputs VALUES ('p','input','{"changed":true}',16)`,
		`INSERT INTO backend_analysis_chunks VALUES ('p','job',1,'changes','different','[]',0,2)`,
		`INSERT INTO backend_analysis_manifests VALUES ('p','job',1,0,'different','{}',2)`,
		`INSERT INTO backend_analysis_receipts VALUES ('p','start','key','job','different','{}',2)`,
	} {
		reject(q)
	}
	for _, table := range []string{"backend_analysis_inputs", "backend_analysis_chunks", "backend_analysis_manifests", "backend_analysis_receipts"} {
		for _, q := range []string{"UPDATE " + table + " SET project_id=project_id", "DELETE FROM " + table} {
			if _, err := db.W.ExecContext(t.Context(), q); err == nil || !strings.Contains(err.Error(), "immutable") {
				t.Fatalf("immutable guard %s: %v", q, err)
			}
		}
	}
	for _, table := range []string{"backend_change_proposal_revisions", "backend_change_proposal_events", "backend_change_proposal_identities", "backend_change_proposal_batches", "backend_change_proposal_commands"} {
		reject("UPDATE " + table + " SET document='{}'")
		reject("DELETE FROM " + table)
	}
	reject(`INSERT INTO backend_change_proposal_commands VALUES ('proposal','wrong','draft',1,'{}')`)
	exec(`UPDATE backend_change_proposals SET status='ready',ready_reference='{}' WHERE id='proposal'`)
	for _, status := range []string{"implemented", "archived"} {
		exec("UPDATE backend_change_proposals SET status='" + status + "' WHERE id='proposal'")
	}
	migration21ForeignKeys(t, db)
}

func migration21Fixture(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version <= 20 {
			if err := db.applyMigration(t.Context(), m); err != nil {
				t.Fatal(err)
			}
		}
	}
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		queries := []string{
			`INSERT INTO backend_projects VALUES ('p','Project',1,'source6','created','updated')`,
			`INSERT INTO backend_projects VALUES ('other','Other',1,'other-source','created','updated')`,
			`INSERT INTO backend_revisions VALUES ('other-source','other','{}')`,
			`INSERT INTO backend_command_receipts VALUES ('change:proposal','old-key','fingerprint','{ "accepted": true, "escaped": "\u0061" }' || char(10))`,
			`INSERT INTO backend_change_proposals VALUES ('proposal','p',1,'Proposal','draft','draft','hash','created','updated')`,
			`INSERT INTO backend_change_proposal_revisions VALUES ('draft','p','proposal',NULL,'source6','{ "delta": [ ], "x":"\u0061" }')`,
			`INSERT INTO backend_change_proposal_batches VALUES ('p','proposal','draft','apply',NULL,'[ { "commandId":"cmd", "kind":"rename_node" } ]','hash','{ "kept": true }')`,
			`INSERT INTO backend_change_proposal_commands VALUES ('proposal','cmd','draft',0,'{ "commandId":"cmd", "kind":"rename_node" }')`,
			`INSERT INTO backend_change_proposal_identities VALUES ('p','proposal','identity','node','service','draft','{ "id":"identity" }')`,
			`INSERT INTO backend_change_proposal_events VALUES ('p','proposal','draft',1,'{ "event":"created" }')`,

			`INSERT INTO backend_repositories VALUES ('repo','p','Repository')`,
			`INSERT INTO backend_revision_sources VALUES ('source5','{ "source": "five" }')`,
			`INSERT INTO backend_revision_sources VALUES ('source6','{ "source": "six" }')`,
			`INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES ('p','source5','node','n','{ "id": "n" }')`,
			`INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,subject_id,document) VALUES ('p','source5','evidence','e','n','{ "id": "e" }')`,
			`INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES ('p','source6','node','n','{ "id": "n" }')`,
			`INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,subject_id,document) VALUES ('p','source6','evidence','e','n','{ "id": "e" }')`,
			`INSERT INTO backend_revision_assertions VALUES ('p','source6','node','n','repo','provider','key','claim','{ "evidenceIds": ["e"] }')`,
			`INSERT INTO backend_revision_assertion_resolutions VALUES ('p','source6','node','n','name','conflict','{ "select": { "repositoryId":"repo", "providerNamespace":"provider", "assertionHash":"claim" } }')`,
			`INSERT INTO backend_revision_legacy_proof_bases VALUES ('p','source6','e','source5','basis','{ "recordType":"node", "recordId":"n" }')`,
			`INSERT INTO backend_revision_api_artifacts VALUES ('source6','content','semantic','{ "contexts": [] }')`,
			`INSERT INTO backend_annotations VALUES ('annotation','p','node','n','source5','bound annotation','author','created','updated',NULL)`,
			`INSERT INTO backend_identity_bindings VALUES ('p','repo','provider','node','key','n','active','source6')`,
			`INSERT INTO backend_proposals VALUES ('legacy','p',1,'Legacy','draft','source5','hash','repo','n','facet','legacy-draft','hash','created','updated')`,
			`INSERT INTO backend_proposal_revisions VALUES ('legacy-draft','legacy',NULL,'{ "legacy": true }')`,
			`INSERT INTO backend_change_proposal_revisions VALUES ('older','p','proposal',NULL,'source5','{ "old": true }')`,
			`INSERT INTO backend_change_proposal_batches VALUES ('p','proposal','older','create',NULL,'[ ]','hash','{ "old": true }')`,
			`INSERT INTO backend_change_proposal_revisions VALUES ('restored','p','proposal','draft','source6','{ "restore": true }')`,
			`INSERT INTO backend_change_proposal_batches VALUES ('p','proposal','restored','restore','older','[ ]','hash','{ "restore": true }')`,
			`INSERT INTO backend_change_proposal_events VALUES ('p','proposal','restored',2,'{ "event":"restore" }')`,
		}
		for _, q := range queries[:3] {
			if _, err := tx.ExecContext(t.Context(), q); err != nil {
				return err
			}
		}
		for i := 1; i <= 6; i++ {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_revisions VALUES (?, 'p', ?)`, fmt.Sprintf("source%d", i), fmt.Sprintf("{ \"schemaVersion\": \"%d\", \"raw\":\"\\u0061\" }\n", i)); err != nil {
				return err
			}
		}
		for _, q := range queries[3:] {
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

func migration21Definition(t *testing.T) migration {
	t.Helper()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version == 21 {
			return m
		}
	}
	t.Fatal("missing migration21")
	return migration{}
}

func migration21History(t *testing.T, db *DB) []string {
	t.Helper()
	var result []string
	for _, table := range []string{"backend_revisions", "backend_revision_sources", "backend_graph_records", "backend_revision_assertions", "backend_revision_assertion_resolutions", "backend_revision_legacy_proof_bases", "backend_revision_api_artifacts", "backend_annotations", "backend_identity_bindings", "backend_proposals", "backend_proposal_revisions", "backend_command_receipts", "backend_change_proposals", "backend_change_proposal_revisions", "backend_change_proposal_batches", "backend_change_proposal_commands", "backend_change_proposal_identities", "backend_change_proposal_events"} {
		columns := "*"
		if table == "backend_change_proposals" {
			columns = "id,project_id,version,name,status,current_draft_revision_id,current_draft_hash,created_at,updated_at"
		}
		rows, err := db.R.QueryContext(t.Context(), "SELECT "+columns+" FROM "+table+" ORDER BY 1,2")
		if err != nil {
			t.Fatal(err)
		}
		names, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			result = append(result, table+fmt.Sprintf("%#v", values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
	}
	return result
}

func migration21Version(t *testing.T, db *DB, want int) {
	t.Helper()
	got, err := db.SchemaVersion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("schema version=%d, want %d", got, want)
	}
}

func migration21ForeignKeys(t *testing.T, db *DB) {
	t.Helper()
	for _, pool := range []*sql.DB{db.W, db.R} {
		var enabled int
		if err := pool.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != 1 {
			t.Fatalf("foreign keys=%d", enabled)
		}
	}
	rows, err := db.R.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after migration")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), "INSERT INTO backend_revisions VALUES ('unowned','absent','{}')"); err == nil {
		t.Fatal("foreign keys not enforced")
	}
}

// A surviving real SQLite transaction makes PRAGMA foreign_keys=ON a no-op.
// Cleanup must detect that and discard the unsafe pinned connection.
func TestMigration21RestorationFailureDiscardsConnection(t *testing.T) {
	db := migration21Fixture(t)
	conn, err := db.W.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	err = restoreMigration21Connection(t.Context(), conn)
	if err == nil || !strings.Contains(err.Error(), "foreign_keys=0") {
		t.Fatalf("unsafe restoration accepted: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), "SELECT 1"); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("unsafe connection was returned: %v", err)
	}
	migration21Version(t, db, 20)
	migration21ForeignKeys(t, db)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("fresh replacement writer: %v", err)
	}
}

func TestMigration21RejectsForeignKeyViolationsAtomically(t *testing.T) {
	db := migration21Fixture(t)
	before := migration21History(t, db)
	m := migration21Definition(t)
	m.sql += "INSERT INTO backend_revisions VALUES ('unowned','absent','{}');"
	if err := db.applyMigration(t.Context(), m); err == nil || !strings.Contains(err.Error(), "foreign key violation") {
		t.Fatalf("unchecked rebuild accepted: %v", err)
	}
	migration21Version(t, db, 20)
	migration21ForeignKeys(t, db)
	if after := migration21History(t, db); !slices.Equal(before, after) {
		t.Fatal("foreign key check failure changed bytes")
	}
}

func TestMigration21BatchActionsAndCounts(t *testing.T) {
	for _, tc := range []struct {
		name, action       string
		count              int
		restore, wantError bool
	}{
		{name: "create empty", action: "create"},
		{name: "create nonempty", action: "create", count: 1, wantError: true},
		{name: "restore empty", action: "restore", restore: true},
		{name: "restore nonempty", action: "restore", restore: true, count: 1, wantError: true},
		{name: "apply empty", action: "apply", wantError: true},
		{name: "apply one", action: "apply", count: 1},
		{name: "apply hundred", action: "apply", count: 100},
		{name: "apply overflow", action: "apply", count: 101, wantError: true},
		{name: "rebase empty", action: "rebase"},
		{name: "rebase one", action: "rebase", count: 1},
		{name: "rebase hundred", action: "rebase", count: 100},
		{name: "rebase overflow", action: "rebase", count: 101, wantError: true},
		{name: "rebase restore selector", action: "rebase", restore: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := migration21Fixture(t)
			if err := db.Migrate(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			commands := "[" + strings.TrimSuffix(strings.Repeat(`{"commandId":"repair"},`, tc.count), ",") + "]"
			var restore any
			if tc.restore {
				restore = "draft"
			}
			err := db.Write(t.Context(), func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_revisions VALUES ('next','p','proposal','draft','source6','{}')`); err != nil {
					return err
				}
				_, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_batches VALUES ('p','proposal','next',?, ?, ?, 'hash','{}')`, tc.action, restore, commands)
				return err
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("action=%s count=%d restore=%v: %v", tc.action, tc.count, tc.restore, err)
			}
		})
	}
}
