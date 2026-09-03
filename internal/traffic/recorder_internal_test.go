// This file is package traffic, not traffic_test, for one reason: the panic
// guard it exercises lives behind an unexported seam (writeBatchFn), and the
// only honest way to prove a guard holds is to make the thing it guards
// against actually happen. Everything else in this package is tested from
// outside, as it should be.
package traffic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/store"
)

// internalTestDB is a private copy of the black-box tests' fixture: their
// helpers live in package traffic_test and are not reachable from here.
func internalTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, revision, settings, created_at, updated_at)
		VALUES ('alex', 'alex', 1, '{}', unixepoch(), unixepoch())`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := res.LastInsertId(); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return db
}

// TestWriteBatchSafe_PanicLosesTheBatchAndNotTheProcess proves the guard in
// writeBatchSafe. The write it protects runs on Run's BACKGROUND goroutine,
// where httpx.Recover cannot reach — that middleware only wraps a request's
// own stack — so an unguarded panic would not fail one batch, it would take
// the process down and every workspace's mock with it, long after the request
// that produced the offending event had already answered 200.
//
// Not parallel: it swaps a package-level var.
func TestWriteBatchSafe_PanicLosesTheBatchAndNotTheProcess(t *testing.T) {
	db := internalTestDB(t)
	rec := NewRecorder(db, nil, Options{})

	original := writeBatchFn
	writeBatchFn = func(*Recorder, context.Context, []queuedEvent, bool) (map[int64]struct{}, error) {
		panic("a batch blew up")
	}
	t.Cleanup(func() { writeBatchFn = original })

	rec.Record(Event{WorkspaceID: 1, TS: time.Now(), Method: "GET", Path: "/x", Status: 200})

	err := rec.Flush(t.Context())
	if err == nil {
		t.Fatal("Flush() = nil after the batch write panicked: swallowing it whole is worse than crashing, because a caller cannot tell the events were lost")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("Flush() error = %v, want it to name the panic so whoever reads the log knows what happened", err)
	}
	if got := rec.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d, want 1: the batch was already off the queue when it blew up, so those events are lost and must be counted where every other lost event is", got)
	}

	// The Recorder is still usable: the guard returns an error, it does not
	// poison the writer.
	writeBatchFn = original
	rec.Record(Event{WorkspaceID: 1, TS: time.Now(), Method: "GET", Path: "/y", Status: 200})
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush() after a recovered panic: %v", err)
	}
	var stored int
	if err := db.R.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM traffic WHERE workspace_id = 1").Scan(&stored); err != nil {
		t.Fatalf("count traffic rows: %v", err)
	}
	if stored != 1 {
		t.Errorf("stored rows = %d, want 1 — only the event recorded after the panic", stored)
	}
}
