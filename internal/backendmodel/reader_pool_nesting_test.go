package backendmodel

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/store"
)

// holdReaders takes every reader-pool connection but `spare` for the rest of
// the test: the state pool-width concurrent requests leave behind.

// readerWaitBudget bounds a call that must not need (another) reader. A
// deadlock waits forever, so any finite bound still catches it; the bound only
// has to exceed the call's honest cost under -race with the rest of the suite
// running. The sibling backendportable tests timed out at 500 ms and 2 s in a
// full make test (2026-10-07), so every reader-pool test uses this bound.
const readerWaitBudget = 20 * time.Second

func holdReaders(t *testing.T, db *store.DB, spare int) {
	t.Helper()
	for range db.R.Stats().MaxOpenConnections - spare {
		tx, err := db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
	}
}

// Review 2026-10-06, F3 (change-proposal half): prepareChangeProposal kept its
// read transaction open while set_artifact_pin read the owner snapshot (and its
// size, for the read budget) through fresh reader-pool connections. Pool-width
// concurrent previews each held one reader and waited for a second, and every
// GET and worker in the server stalled behind them.
func TestChangePreviewArtifactPinNeedsOneReader(t *testing.T) {
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Pool", BaseRevisionID: base.Revision.ID, IdempotencyKey: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	command := scenarioSet(base, ids, scenario).Commands[0]
	set := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": command.Artifact, "revisionId": command.RevisionID, "editorBindings": command.EditorBindings})
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{set}}
	if _, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in); err != nil {
		t.Fatalf("fixture preview: %v", err)
	}
	holdReaders(t, r.db, 1)
	ctx, cancel := context.WithTimeout(t.Context(), readerWaitBudget)
	defer cancel()
	if _, err = r.PreviewChangeProposal(ctx, base.Project.ID, d.Proposal.ID, in); err != nil {
		t.Fatalf("preview needed a second reader: %v", err)
	}
}

// Review 2026-10-06, F3 (the analysis half): ResolveFrozenChangePreview
// replays the frozen evaluation on one read transaction, and set_artifact_pin
// read the owner snapshot and its budget size through fresh reader-pool
// connections while that transaction stayed open. Pool-width concurrent
// analysis jobs each held one reader and waited for a second.
func TestFrozenPreviewArtifactPinNeedsOneReader(t *testing.T) {
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Frozen pool", BaseRevisionID: base.Revision.ID, IdempotencyKey: "frozen-pool"})
	if err != nil {
		t.Fatal(err)
	}
	command := scenarioSet(base, ids, scenario).Commands[0]
	set := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": command.Artifact, "revisionId": command.RevisionID, "editorBindings": command.EditorBindings})
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{set}}
	preview, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.FreezeChangePreview(t.Context(), base.Project.ID, d.Proposal.ID, in, *preview.CandidateHash)
	if err != nil {
		t.Fatal(err)
	}
	holdReaders(t, r.db, 1)
	ctx, cancel := context.WithTimeout(t.Context(), readerWaitBudget)
	defer cancel()
	if _, err = r.ResolveFrozenChangePreview(ctx, base.Project.ID, frozen); err != nil {
		t.Fatalf("frozen replay needed a second reader: %v", err)
	}
}

// The other half of the cycle: a reader held while waiting for the single
// writer. A writer holder that needs a reader (a diagram save resolving owner
// artifacts) then waits on a pool full of such readers.
func TestChangePreviewReleasesReaderBeforeWriter(t *testing.T) {
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Writer", BaseRevisionID: base.Revision.ID, IdempotencyKey: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	command := scenarioSet(base, ids, scenario).Commands[0]
	set := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": command.Artifact, "revisionId": command.RevisionID, "editorBindings": command.EditorBindings})
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{set}}
	requireReaderFreeWhileWriterWaits(t, r.db, func(ctx context.Context) error {
		_, err := r.PreviewChangeProposal(ctx, base.Project.ID, d.Proposal.ID, in)
		return err
	})
}

// The legacy proposal preview had the same shape: its reservation write and
// its final resize both ran while its read transaction stayed open.
func TestProposalPreviewReleasesReaderBeforeWriter(t *testing.T) {
	r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
	pid := detail.Proposal.ProjectID
	in := proposalPreviewInput(detail, proposalNullable(ids, "required", false))
	requireReaderFreeWhileWriterWaits(t, r.db, func(ctx context.Context) error {
		_, err := r.PreviewProposal(ctx, pid, detail.Proposal.ID, in)
		return err
	})
}

// requireReaderFreeWhileWriterWaits holds the writer, runs call until it is
// queued on the writer, and requires that it holds no reader while it waits.
func requireReaderFreeWhileWriterWaits(t *testing.T, db *store.DB, call func(context.Context) error) {
	t.Helper()
	held, release, writerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		writerDone <- db.Write(context.Background(), func(*sql.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	waits := db.W.Stats().WaitCount
	done := make(chan error, 1)
	go func() { done <- call(t.Context()) }()
	deadline := time.Now().Add(5 * time.Second)
	for db.W.Stats().WaitCount == waits {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("call never queued on the writer")
		}
		time.Sleep(time.Millisecond)
	}
	inUse := db.R.Stats().InUse
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if inUse != 0 {
		t.Fatalf("held %d reader connection(s) while waiting for the writer", inUse)
	}
}
