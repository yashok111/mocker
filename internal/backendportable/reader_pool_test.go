package backendportable

import (
	"context"
	"database/sql"
	"testing"
	"time"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
)

// holdAllReaders takes every reader-pool connection for the rest of the test:
// the state left by pool-width requests that each wait for the writer.
func holdAllReaders(t *testing.T, db *store.DB) {
	t.Helper()
	for range db.R.Stats().MaxOpenConnections {
		tx, err := db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		check(t, err)
		t.Cleanup(func() { _ = tx.Rollback() })
	}
}

// Review 2026-10-06, F183: Export read the installation id through the reader
// pool inside its writer callback. With every reader held by requests that wait
// for the writer, neither side could move.
func TestPortableExportNeedsNoReader(t *testing.T) {
	f := makeRoundtripFixture(t)
	holdAllReaders(t, f.service.db)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := f.service.Export(ctx, ExportInput{Selection: f.selection, IdempotencyKey: "export"}); err != nil {
		t.Fatalf("export waited on a reader while holding the writer: %v", err)
	}
}

// Review 2026-10-06, F3/F183 (the diagram half): Preview and Commit import
// diagrams inside the single writer, and the diagram artifact resolver read
// the installation id through the reader pool there. With every reader held
// by requests that wait for the writer, the import waited forever. A mapped
// (local) owner is used so the resolver needs the installation id and reads
// the owner snapshot.
func TestPortableMappedPreviewAndCommitNeedNoReader(t *testing.T) {
	f := makeRoundtripFixture(t)
	_, session := stageRoundtrip(t, f)
	holdAllReaders(t, f.service.db)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	preview, err := f.service.Preview(ctx, session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{{Origin: f.pin, Local: f.pin}}, IdempotencyKey: "mapped-preview"})
	if err != nil {
		t.Fatalf("preview waited on a reader while holding the writer: %v", err)
	}
	if _, err = f.service.Commit(ctx, session.ID, CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "mapped-commit"}); err != nil {
		t.Fatalf("commit waited on a reader while holding the writer: %v", err)
	}
}
