//go:build integration

package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

// The seed is captured through the frozen B0.4 binary, independently of this
// implementation. Its original store is copied and never modified by this test.
func TestRelationalProfileActualB04Store(t *testing.T) {
	seedPath := os.Getenv("MOCKER_B04_UPGRADE_SEED")
	if seedPath == "" {
		t.Skip("set MOCKER_B04_UPGRADE_SEED to an actual frozen B0.4 capture")
	}
	var seed struct {
		DBPath            string            `json:"dbPath"`
		BinaryHash        string            `json:"binaryHash"`
		ProjectID         string            `json:"projectId"`
		CommittedRevision string            `json:"committedRevision"`
		NodeID            string            `json:"nodeId"`
		ReservedID        string            `json:"reservedId"`
		BaselineRows      map[string]string `json:"baselineRows"`
		OriginalRequests  map[string]struct {
			Input  jsontext.Value `json:"input"`
			Args   jsontext.Value `json:"args"`
			Result jsontext.Value `json:"result"`
		} `json:"originalRequests"`
	}
	doc, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(doc, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.BinaryHash != "c2ecaa7d64518f63345ed10364ccd355cc1338c837b22f5b379668b055585112" || len(seed.BaselineRows) != 13 {
		t.Fatal("seed does not match the frozen B0.4 capture")
	}
	dbBytes, err := os.ReadFile(seed.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "b04-copy.db")
	if err := os.WriteFile(copyPath, dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	openCopy := func() *store.DB {
		t.Helper()
		db, err := store.Open(t.Context(), copyPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := db.Migrate(t.Context(), slog.Default()); err != nil {
			t.Fatal(err)
		}
		var schema int
		if err := db.R.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&schema); err != nil || schema != 18 {
			t.Fatalf("schema17 compatibility: %d, %v", schema, err)
		}
		return db
	}
	db := openCopy()
	r := NewRepo(db)
	assertRows := func() {
		t.Helper()
		for address, want := range seed.BaselineRows {
			kind, key, _ := strings.Cut(address, ":")
			var query string
			var args []any
			switch kind {
			case "graph":
				revision, id, _ := strings.Cut(key, ":")
				query, args = `SELECT document FROM backend_graph_records WHERE revision_id=? AND id=?`, []any{revision, id}
			case "revision":
				query, args = `SELECT document FROM backend_revisions WHERE id=?`, []any{key}
			case "source":
				query, args = `SELECT document FROM backend_revision_sources WHERE revision_id=?`, []any{key}
			case "receipt":
				scope, id, _ := strings.CutLast(key, ":")
				query, args = `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, []any{scope, id}
			case "batch":
				session, id, _ := strings.Cut(key, ":")
				query, args = `SELECT receipt FROM backend_import_batches WHERE session_id=? AND batch_id=?`, []any{session, id}
			default:
				t.Fatalf("unrecognized baseline row %s", address)
			}
			var got string
			if err := db.R.QueryRowContext(t.Context(), query, args...).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("original B0.4 bytes changed: %s", address)
			}
		}
	}
	assertRows()
	var begin BeginImportInput
	var original ImportSession
	var batch ImportBatchInput
	var commit CommitImportInput
	var abort AbortImportInput
	var abortBegin BeginImportInput
	var aborted ImportSession
	for _, decode := range []struct {
		data []byte
		out  any
	}{
		{seed.OriginalRequests["begin"].Input, &begin},
		{seed.OriginalRequests["begin"].Result, &original},
		{seed.OriginalRequests["batch"].Args, &batch},
		{seed.OriginalRequests["commit"].Input, &commit},
		{seed.OriginalRequests["abort"].Input, &abort},
		{seed.OriginalRequests["abortBegin"].Input, &abortBegin},
		{seed.OriginalRequests["abortBegin"].Result, &aborted},
	} {
		if err := json.Unmarshal(decode.data, decode.out); err != nil {
			t.Fatal(err)
		}
	}
	// Recompute the acknowledged schema1 candidate with its original version,
	// proving exact old hash canonicalization rather than merely reading the hash.
	s, err := loadSession(t.Context(), db.R, seed.ProjectID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Version = commit.ExpectedImportVersion
	g, diagnostics, err := prepareGraph(t.Context(), db.R, s)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("original candidate: %+v, %v", diagnostics, err)
	}
	candidate, err := candidateJSON(s, g)
	if err != nil || hashBytes(candidate) != commit.CandidateHash {
		t.Fatalf("schema1 canonicalization changed: got %s want %s, %v", hashBytes(candidate), commit.CandidateHash, err)
	}
	p, err := r.Get(t.Context(), seed.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	in := relationalInput(begin, true)
	in.Mode = "reconcile"
	in.RepositoryID = new(original.RepositoryID)
	in.GraphScope = &GraphScope{Profile: RelationalProfile, Status: "complete", Gaps: []string{}}
	in.ExpectedVersion = p.Version
	in.BaseRevisionID = p.CurrentRevisionID
	in.IdempotencyKey = "actual-b04-extension"
	next, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	for i := range batch.Commands {
		if batch.Commands[i].Evidence != nil {
			batch.Commands[i].Evidence.Source.SnapshotID = next.SnapshotID
		}
	}
	b := sendCommands(t, r, p, next, next.Version, "actual-b04-extension", batch.Commands...)
	if b.Identities[0].ID != seed.NodeID {
		t.Fatal("actual upgrade changed foundation UUID")
	}
	out := commitStaged(t, r, p, next, b.AcceptedVersion, "actual-b04-extension-commit")
	if out.Revision.SchemaVersion != RelationalSchemaVersion {
		t.Fatal("actual upgrade did not publish schema2")
	}
	var reserved string
	if err := db.R.QueryRowContext(t.Context(), `SELECT state FROM backend_identity_bindings WHERE id=?`, seed.ReservedID).Scan(&reserved); err != nil || reserved != "reserved" {
		t.Fatalf("B04 reservation lost: %s, %v", reserved, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openCopy()
	r = NewRepo(db)
	for _, old := range []struct {
		in      BeginImportInput
		session string
	}{{begin, original.ID}, {abortBegin, aborted.ID}} {
		replay, err := r.BeginImport(t.Context(), p.ID, old.in)
		if err != nil {
			t.Fatal(err)
		}
		assertReceiptBytes(t, replay, seed.BaselineRows["receipt:import:"+p.ID+":begin:"+old.in.IdempotencyKey])
	}
	replay, err := r.AbortImport(t.Context(), p.ID, aborted.ID, abort)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptBytes(t, replay, seed.BaselineRows["receipt:import:"+p.ID+":"+aborted.ID+":abort:"+abort.IdempotencyKey])
	oldCommit, err := r.CommitImport(t.Context(), p.ID, original.ID, commit)
	if err != nil || oldCommit.Revision.SemanticHash != commit.CandidateHash {
		t.Fatalf("actual B04 commit replay: %+v, %v", oldCommit, err)
	}
	assertReceiptBytes(t, oldCommit, seed.BaselineRows["receipt:import:"+p.ID+":"+original.ID+":commit:"+commit.IdempotencyKey])
	// Restore original batch proof, which remains an exact receipt after schema2.
	if err := json.Unmarshal(seed.OriginalRequests["batch"].Args, &batch); err != nil {
		t.Fatal(err)
	}
	oldBatch, err := r.PutImportBatch(t.Context(), p.ID, original.ID, "base", batch)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptBytes(t, oldBatch, seed.BaselineRows["batch:"+original.ID+":base"])
	assertRows()
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].ID != seed.NodeID || graph.Nodes[0].Ownership.Profile != GraphProfile {
		t.Fatalf("upgraded foundation assertion: %+v, %v", graph, err)
	}
}
