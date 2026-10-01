package backendmodel

import (
	"context"
	"log/slog"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestRuntimeSourceReceiptReplayRollbackAndCancellation(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	begin := runtimeInput(p)
	s, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	commands := runtimeCommands(t, s)
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batchIn := ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands}
	batch, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "source-batch", batchIn)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.CandidateHash == nil {
		t.Fatalf("preview %+v %v", v, err)
	}
	commit := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "source-commit"}
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_runtime_publish BEFORE UPDATE ON backend_projects BEGIN SELECT RAISE(ABORT,'forced runtime rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitImport(t.Context(), p.ID, s.ID, commit); err == nil {
		t.Fatal("late runtime publication failure committed")
	}
	current, err := r.Get(t.Context(), p.ID)
	if err != nil || current.CurrentRevisionID != p.CurrentRevisionID || current.Version != p.Version {
		t.Fatal("failed runtime publication changed source pointer", err)
	}
	if _, err := db.W.ExecContext(t.Context(), `DROP TRIGGER fail_runtime_publish`); err != nil {
		t.Fatal(err)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	var batchReceipt, commitReceipt string
	if err := db.R.QueryRowContext(t.Context(), `SELECT receipt FROM backend_import_batches WHERE session_id=? AND batch_id=?`, s.ID, "source-batch").Scan(&batchReceipt); err != nil {
		t.Fatal(err)
	}
	if err := db.R.QueryRowContext(t.Context(), `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, "import:"+p.ID+":"+s.ID+":commit", commit.IdempotencyKey).Scan(&commitReceipt); err != nil {
		t.Fatal(err)
	}
	p = &out.Project
	next, err := r.BeginImport(t.Context(), p.ID, runtimeRepeat(p, s.RepositoryID))
	if err != nil {
		t.Fatal(err)
	}
	before := profilePublishedState(t, r, p)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	cs := runtimeCommands(t, next)
	h, err := ImportBatchHash(cs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutImportBatch(cancelled, p.ID, next.ID, "cancelled", ImportBatchInput{ExpectedImportVersion: next.Version, PayloadHash: h, Commands: cs}); err == nil {
		t.Fatal("cancelled runtime batch succeeded")
	}
	if profilePublishedState(t, r, p) != before {
		t.Fatal("cancelled runtime batch changed durable state")
	}
	preview, _ := stageRelational(t, r, p, next, cs, "advance")
	advanced, err := commitFixture(t, r, p, next, preview, "advance-commit")
	if err != nil {
		t.Fatal(err)
	}
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
	replayedBatch, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "source-batch", batchIn)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptBytes(t, replayedBatch, batchReceipt)
	replayedCommit, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptBytes(t, replayedCommit, commitReceipt)
	changed := batchIn
	changed.Commands = runtimeCommands(t, s)
	changed.Commands[0].Node.Name = "Different source body"
	changed.PayloadHash, err = ImportBatchHash(changed.Commands)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "source-batch", changed)
	assertFault(t, err, "backend_import_batch_conflict")
	current, err = r.Get(t.Context(), p.ID)
	if err != nil || current.CurrentRevisionID != advanced.Revision.ID {
		t.Fatal("replay moved source head", err)
	}
}
