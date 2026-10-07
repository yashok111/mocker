package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

// storeSchemaHead is the newest embedded migration. Tests that reopen a store
// and assert that no further migration ran pin it here, in one place: three
// copies of the literal 22 went stale when 0023..0026 and then 0027 (48dce80,
// B6.3 immutable payload blobs) landed without them.
const storeSchemaHead = 27

// rowQuerier is satisfied by *sql.DB and *sql.Tx, so a fixture can capture
// bytes from inside a rewind transaction before the upgrade runs.
type rowQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// rewindStoreFixture turns a current test store into the schema shape of
// migration `version`, applies edit, and then runs the production migrations
// forward again (testkit.EditLegacyBackendFixture's final Migrate).
//
// Data is written by the CURRENT code at the head schema and only then
// rewound: since Store27 (48dce80, B6.3) the current writers need
// backend_payload_blobs, so a store stopped at an old migration can no longer
// be filled through the repository API, which is what these upgrade tests did
// before. The rewind is generic on purpose: every schema object that a fresh
// store at `version` (built from the committed migration files) does not have
// is dropped, so a later migration cannot silently break the fixture the way
// the hand-written drop list in TestIdentityUpgrade... did. Only migrations
// 0001..0020 are plain SQL files; 0021 and 0027 have Go hooks, so a reference
// past 0020 is refused rather than built wrongly.
func rewindStoreFixture(t *testing.T, db *store.DB, version int, edit func(*sql.Tx) error) {
	t.Helper()
	if version > 20 {
		t.Fatalf("rewind reference %d needs a Go migration hook", version)
	}
	keep := referenceSchemaObjects(t, version)
	err := testkit.EditLegacyBackendFixture(t.Context(), db, func(tx *sql.Tx) error {
		drops, err := rewindDrops(t.Context(), tx, keep)
		if err != nil {
			return err
		}
		for _, drop := range drops {
			if _, err = tx.ExecContext(t.Context(), drop); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(t.Context(), "PRAGMA user_version="+strconv.Itoa(version)); err != nil {
			return err
		}
		return edit(tx)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// rewindDrops lists a DROP for every schema object outside keep: views first,
// then triggers and indexes, tables last.
func rewindDrops(ctx context.Context, tx *sql.Tx, keep map[string]bool) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT type,name FROM sqlite_schema WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY CASE type WHEN 'view' THEN 0 WHEN 'trigger' THEN 1 WHEN 'index' THEN 2 ELSE 3 END,name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var drops []string
	for rows.Next() {
		var typ, name string
		if err = rows.Scan(&typ, &name); err != nil {
			return nil, err
		}
		if !keep[typ+" "+name] {
			// Identifiers come from sqlite_schema of this isolated test store.
			drops = append(drops, "DROP "+strings.ToUpper(typ)+" IF EXISTS \""+name+"\"")
		}
	}
	return drops, rows.Err()
}

// referenceSchemaObjects lists "type name" of every object a fresh store has
// after the committed migration files 0001..version, applied as plain SQL.
func referenceSchemaObjects(t *testing.T, version int) map[string]bool {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "reference.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	entries, err := os.ReadDir("../store/migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		n, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || n > version {
			continue
		}
		b, err := os.ReadFile(filepath.Join("../store/migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.W.ExecContext(t.Context(), string(b)); err != nil {
			t.Fatalf("reference migration %s: %v", entry.Name(), err)
		}
	}
	rows, err := db.W.QueryContext(t.Context(), `SELECT type,name FROM sqlite_schema`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var typ, name string
		if err := rows.Scan(&typ, &name); err != nil {
			t.Fatal(err)
		}
		out[typ+" "+name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func immutableBytes(t *testing.T, r *Repo) map[string]string {
	t.Helper()
	return immutableBytesFrom(t, r.db.R)
}

func immutableBytesFrom(t *testing.T, q rowQuerier) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, query := range []string{`SELECT 'graph:'||revision_id||':'||id,document FROM backend_graph_records`, `SELECT 'revision:'||id,document FROM backend_revisions`, `SELECT 'source:'||revision_id,document FROM backend_revision_sources`, `SELECT 'receipt:'||scope||':'||key,response FROM backend_command_receipts`, `SELECT 'batch:'||session_id||':'||batch_id,receipt FROM backend_import_batches`} {
		var version int
		if err := q.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version >= 27 {
			for _, table := range []string{"backend_graph_records", "backend_revisions", "backend_revision_sources"} {
				query = strings.ReplaceAll(query, "FROM "+table, "FROM "+table+"_documents")
			}
		}
		if err := scanKeyValues(t.Context(), q, query, out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func scanKeyValues(ctx context.Context, q rowQuerier, query string, out map[string]string) error {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		out[key] = value
	}
	return rows.Err()
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
	// Restore the legacy schema/data shape, then run the actual production
	// migration. The hand-written table/index drop list this used to carry
	// stopped at 0022; rewindStoreFixture derives it from the committed 0001..0014
	// files instead, and the bytes are captured inside the rewind, at v14,
	// before the production migrations 0015..head run.
	var before map[string]string
	rewindStoreFixture(t, db, 14, func(tx *sql.Tx) error {
		for _, query := range []string{`UPDATE backend_import_sessions SET document=json_remove(document,'$.mode','$.graphScope')`, `UPDATE backend_graph_records SET document=json_remove(document,'$.ownership','$.freshness')`, `UPDATE backend_revision_sources SET document=json_remove(document,'$.staleCounts','$.reconciliationGaps','$.snapshots[0].role')`} {
			if _, err := tx.ExecContext(t.Context(), query); err != nil {
				return err
			}
		}
		before = immutableBytesFrom(t, tx)
		return nil
	})
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
