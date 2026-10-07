package backendportable

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Review 2026-10-06, F183: Export read the installation id through the reader
// pool inside its writer callback. With every reader held by requests that wait
// for the writer, neither side could move. Preview's own read is fixed the same
// way, but its diagram import still resolves owner artifacts through the reader
// pool under the writer (the owner-read half of F3), so it is not pinned here.
func TestPortableExportNeedsNoReader(t *testing.T) {
	f := makeRoundtripFixture(t)
	db := f.service.db
	for range db.R.Stats().MaxOpenConnections {
		tx, err := db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		check(t, err)
		t.Cleanup(func() { _ = tx.Rollback() })
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := f.service.Export(ctx, ExportInput{Selection: f.selection, IdempotencyKey: "export"}); err != nil {
		t.Fatalf("export waited on a reader while holding the writer: %v", err)
	}
}
