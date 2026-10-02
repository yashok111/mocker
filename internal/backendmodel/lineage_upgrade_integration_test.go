package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

// A separate controller test runs an actual old binary upgrade. This test pins
// old source3 staging/receipt bytes across an in-process adjacent extension.
func TestLineageExtensionPreservesPendingSource3Receipt(t *testing.T) {
	r, out, old, ids := runtimeCommitted(t)
	p := &out.Project
	pendingIn := runtimeRepeat(p, old.RepositoryID)
	pendingIn.IdempotencyKey = "pending3"
	pending, err := r.BeginImport(t.Context(), p.ID, pendingIn)
	if err != nil {
		t.Fatal(err)
	}
	commands := runtimeCommands(t, pending)
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batchIn := ImportBatchInput{ExpectedImportVersion: pending.Version, PayloadHash: hash, Commands: commands}
	receipt, err := r.PutImportBatch(t.Context(), p.ID, pending.ID, "pending", batchIn)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(receipt)
	var oldDocument, oldReceipt string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_import_sessions WHERE id=?`, pending.ID).Scan(&oldDocument); err != nil {
		t.Fatal(err)
	}
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT receipt FROM backend_import_batches WHERE session_id=?`, pending.ID).Scan(&oldReceipt); err != nil {
		t.Fatal(err)
	}
	oldNode, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["input"])
	if err != nil {
		t.Fatal(err)
	}
	nodeBefore, _ := canonicalJSON(oldNode)
	in := lineageProfileInput(runtimeRepeat(p, old.RepositoryID), true)
	in.IdempotencyKey = "extend4"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := stageRelational(t, r, p, s, lineageCommands(t, s), "extend")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	if _, err = commitFixture(t, r, p, s, v, "extend-commit"); err != nil {
		t.Fatal(err)
	}
	replay, err := r.PutImportBatch(t.Context(), p.ID, pending.ID, "pending", batchIn)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(replay)
	if !slices.Equal(before, after) {
		t.Fatal("source3 receipt changed")
	}
	var doc, rec string
	_ = r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_import_sessions WHERE id=?`, pending.ID).Scan(&doc)
	_ = r.db.R.QueryRowContext(t.Context(), `SELECT receipt FROM backend_import_batches WHERE session_id=?`, pending.ID).Scan(&rec)
	if doc != oldDocument || rec != oldReceipt {
		t.Fatal("source3 staging bytes changed")
	}
	historical, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["input"])
	if err != nil {
		t.Fatal(err)
	}
	nodeAfter, _ := canonicalJSON(historical)
	if !slices.Equal(nodeBefore, nodeAfter) {
		t.Fatal("source3 history changed")
	}
}
