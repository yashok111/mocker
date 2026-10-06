package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"log/slog"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func immutableBytes(t *testing.T, r *Repo) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, query := range []string{`SELECT 'graph:'||revision_id||':'||id,document FROM backend_graph_records`, `SELECT 'revision:'||id,document FROM backend_revisions`, `SELECT 'source:'||revision_id,document FROM backend_revision_sources`, `SELECT 'receipt:'||scope||':'||key,response FROM backend_command_receipts`, `SELECT 'batch:'||session_id||':'||batch_id,receipt FROM backend_import_batches`} {
		var version int
		if err := r.db.R.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version >= 27 {
			for _, table := range []string{"backend_graph_records", "backend_revisions", "backend_revision_sources"} {
				query = strings.ReplaceAll(query, "FROM "+table, "FROM "+table+"_documents")
			}
		}
		rows, err := r.db.R.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				t.Fatal(err)
			}
			out[key] = value
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func TestIdentityUpgradePreservesBytesAndCommittedAllocations(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	in := firstImportFixture(p)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	openIn := in
	openIn.IdempotencyKey = "open"
	open, err := r.BeginImport(t.Context(), p.ID, openIn)
	if err != nil {
		t.Fatal(err)
	}
	putFixture(t, r, p, open)
	abortIn := in
	abortIn.IdempotencyKey = "aborted"
	aborted, err := r.BeginImport(t.Context(), p.ID, abortIn)
	if err != nil {
		t.Fatal(err)
	}
	ab := putFixture(t, r, p, aborted)
	if _, err := r.AbortImport(t.Context(), p.ID, aborted.ID, AbortImportInput{ExpectedImportVersion: ab.AcceptedVersion, IdempotencyKey: "abort"}); err != nil {
		t.Fatal(err)
	}
	cs := fixtureCommands(s)
	orphan := *cs[0].Node
	orphan.ExternalKey = "orphan"
	b := sendCommands(t, r, p, s, 1, "orphan", ImportCommand{Op: "upsert_node", Node: &orphan})
	orphanID := b.Identities[0].ID
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "remove", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "orphan"}})
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "records", cs...)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "commit")
	// Restore the legacy schema/data shape, then run the actual production migration.
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		for _, table := range []string{
			"backend_analysis_receipts", "backend_analysis_chunks", "backend_analysis_manifests",
			"backend_diagram_receipts", "backend_diagram_catalog", "backend_diagram_view_versions", "backend_diagram_views", "backend_diagram_versions", "backend_diagrams",
			"backend_analysis_jobs", "backend_analysis_inputs",
			"backend_change_proposal_commands", "backend_change_proposal_batches",
			"backend_change_proposal_identities", "backend_change_proposal_events",
			"backend_change_proposal_revisions", "backend_change_proposals",
			"backend_revision_legacy_proof_bases", "backend_revision_assertion_resolutions",
			"backend_revision_assertions", "backend_import_source_decisions", "backend_annotations",
		} {
			if _, err := tx.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
				return err
			}
		}
		for _, table := range []string{"proxy_recordings", "proxy_configs", "proxy_versions", "backend_revision_api_artifacts", "backend_saved_view_versions", "backend_saved_views", "backend_proposal_revisions", "backend_proposals", "backend_import_aliases", "backend_identity_bindings", "backend_import_decisions", "backend_import_previews", "backend_revision_decisions"} {
			if _, err := tx.ExecContext(t.Context(), "DROP TABLE "+table); err != nil {
				return err
			}
		}
		for _, query := range []string{`DROP INDEX backend_revision_owner`, `DROP INDEX backend_repository_owner`, `UPDATE backend_import_sessions SET document=json_remove(document,'$.mode','$.graphScope')`, `UPDATE backend_graph_records SET document=json_remove(document,'$.ownership','$.freshness')`, `UPDATE backend_revision_sources SET document=json_remove(document,'$.staleCounts','$.reconciliationGaps','$.snapshots[0].role')`, `PRAGMA user_version=14`} {
			if _, err := tx.ExecContext(t.Context(), query); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := immutableBytes(t, r)
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(reopened)
	after := immutableBytes(t, r)
	if len(before) != len(after) {
		t.Fatal("immutable row count changed")
	}
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("migration rewrote %s", key)
		}
	}
	var count int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_identity_bindings`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("bootstrap bindings %d %v", count, err)
	}
	var id, state string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT id,state FROM backend_identity_bindings WHERE external_key='orphan'`).Scan(&id, &state); err != nil || id != orphanID || state != "reserved" {
		t.Fatalf("reservation %s %s %v", id, state, err)
	}
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil || graph.Nodes[0].Ownership == nil || graph.Nodes[0].Freshness == nil || graph.Nodes[0].Freshness.ConfirmedSnapshotID != s.SnapshotID {
		t.Fatalf("derived metadata %+v %v", graph, err)
	}
	next := beginRepeat(t, r, &out.Project, s)
	newBatch := putFixture(t, r, &out.Project, next)
	if newBatch.Identities[0].ID != b.Identities[0].ID {
		t.Fatal("bootstrap lost UUID")
	}
}
func TestReconcileCommitRollbackAllPublications(t *testing.T) {
	for _, table := range []string{"backend_graph_records", "backend_identity_bindings"} {
		t.Run(table, func(t *testing.T) {
			r, p, old, _ := committedBase(t)
			s := beginRepeat(t, r, p, old)
			b := putFixture(t, r, p, s)
			v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err != nil {
				t.Fatal(err)
			}
			before := immutableBytes(t, r)
			var registryBefore string
			rows, err := r.db.R.QueryContext(t.Context(), `SELECT project_id,repository_id,provider_namespace,record_type,external_key,id,state,revision_id FROM backend_identity_bindings ORDER BY record_type,external_key`)
			if err != nil {
				t.Fatal(err)
			}
			all := [][]string{}
			for rows.Next() {
				values := make([]string, 8)
				if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6], &values[7]); err != nil {
					t.Fatal(err)
				}
				all = append(all, values)
			}
			rows.Close()
			raw, _ := json.Marshal(all)
			registryBefore = string(raw)
			trigger := "CREATE TRIGGER fail_reconcile AFTER INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'forced rollback'); END"
			if table == "backend_identity_bindings" {
				trigger = "CREATE TRIGGER fail_reconcile AFTER UPDATE ON backend_identity_bindings BEGIN SELECT RAISE(ABORT,'forced rollback'); END"
			}
			if _, err := r.db.W.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			_, err = r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "fail"})
			if err == nil {
				t.Fatal("forced failure succeeded")
			}
			for key, value := range immutableBytes(t, r) {
				if before[key] != value {
					t.Fatalf("partial publication %s", key)
				}
			}
			project, err := r.Get(t.Context(), p.ID)
			if err != nil || project.CurrentRevisionID != p.CurrentRevisionID || project.Version != p.Version {
				t.Fatalf("head advanced %+v %v", project, err)
			}
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil || status.Session.State != "ready" || status.Session.Version != v.Version {
				t.Fatalf("session advanced %+v %v", status, err)
			}
			rows, err = r.db.R.QueryContext(t.Context(), `SELECT project_id,repository_id,provider_namespace,record_type,external_key,id,state,revision_id FROM backend_identity_bindings ORDER BY record_type,external_key`)
			if err != nil {
				t.Fatal(err)
			}
			all = [][]string{}
			for rows.Next() {
				values := make([]string, 8)
				if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6], &values[7]); err != nil {
					t.Fatal(err)
				}
				all = append(all, values)
			}
			rows.Close()
			raw, _ = json.Marshal(all)
			if string(raw) != registryBefore {
				t.Fatal("registry escaped rollback")
			}
			if _, err := r.db.W.ExecContext(t.Context(), "DROP TRIGGER fail_reconcile"); err != nil {
				t.Fatal(err)
			}
			if _, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "retry"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
