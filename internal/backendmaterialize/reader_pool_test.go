package backendmaterialize

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/store"
)

// occupyReaders holds every reader-pool connection but `spare` for the rest
// of the test: the state max(4, NumCPU) concurrent requests leave behind.

// readerWaitBudget bounds a call that must not need (another) reader. A
// deadlock waits forever, so any finite bound still catches it; the bound only
// has to exceed the call's honest cost under -race with the rest of the suite
// running. The sibling backendportable tests timed out at 500 ms and 2 s in a
// full make test (2026-10-07), so every reader-pool test uses this bound.
const readerWaitBudget = 20 * time.Second

func occupyReaders(t *testing.T, db *store.DB, spare int) {
	t.Helper()
	for range db.R.Stats().MaxOpenConnections - spare {
		tx, err := db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
	}
}

// Review 2026-10-06, F183: previewTx read the installation id through the
// reader pool while Preview already held a reader, so pool-width concurrent
// previews each waited for a second connection and none could finish.
func TestMaterializationPreviewNeedsOneReader(t *testing.T) {
	s, pid, in := fixture(t)
	occupyReaders(t, s.db, 1)
	ctx, cancel := context.WithTimeout(t.Context(), readerWaitBudget)
	defer cancel()
	if _, err := s.Preview(ctx, pid, in); err != nil {
		t.Fatalf("preview needed a second reader: %v", err)
	}
}

// The Apply half: the single writer was held while waiting for a reader, so a
// full reader pool whose holders wait for the writer never drained.
func TestMaterializationApplyNeedsNoReader(t *testing.T) {
	s, pid, in := fixture(t)
	request := applyInput(t, s, pid, in, "apply")
	occupyReaders(t, s.db, 0)
	ctx, cancel := context.WithTimeout(t.Context(), readerWaitBudget)
	defer cancel()
	if _, err := s.Apply(ctx, pid, request); err != nil {
		t.Fatalf("apply waited on a reader while holding the writer: %v", err)
	}
}
