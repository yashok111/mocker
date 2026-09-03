package scenarios

// This file is package scenarios (white-box), not scenarios_test, purely
// so it can reach the unexported Repo.coherentRead field — see that
// field's own doc comment for why the branch it exercises is not reachable
// through a real database from a single goroutine. Every other test in
// this package lives in repo_test.go (package scenarios_test) and never
// needs this access.

import (
	"context"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

// TestCreateFromCurrentState_exhaustsRetriesAgainstAFakeSource is A11's
// three-attempt exhaustion cover. A real SQLite-backed Repo driven from one
// goroutine cannot land a commit BETWEEN CreateFromCurrentState's two
// sequential reads without a second goroutine racing that exact window —
// which would be a flaky test proving nothing more directly than this
// fake does. The fake reports a STRICTLY INCREASING revision on every
// call, so every attempt's "before" and "after" reads disagree, and the
// loop must give up after maxCoherentReadAttempts rather than spinning
// forever.
func TestCreateFromCurrentState_exhaustsRetriesAgainstAFakeSource(t *testing.T) {
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("close db: %v", cerr)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, revision, settings, created_at, updated_at)
		VALUES ('ws', 'ws', 1, '{}', unixepoch(), unixepoch())`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	wsID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}

	repo := NewRepo(db, overrides.NewRepo(db))

	// Every call returns a NEW, higher revision than the one before —
	// exactly what "the workspace kept changing between reads" looks like
	// from CreateFromCurrentState's point of view, on EVERY attempt.
	calls := 0
	repo.coherentRead = func(_ context.Context, _ int64) (workspaceCore, bool, error) {
		calls++
		return workspaceCore{revision: int64(calls), name: "ws", settingsJSON: "{}"}, true, nil
	}

	_, err = repo.CreateFromCurrentState(t.Context(), wsID, "will-never-land")
	if !errors.Is(err, ErrConcurrentEdit) {
		t.Fatalf("CreateFromCurrentState against an always-moving fake: got %v, want ErrConcurrentEdit", err)
	}
	// Two reads per attempt (before/after), maxCoherentReadAttempts attempts.
	if want := 2 * maxCoherentReadAttempts; calls != want {
		t.Errorf("coherentRead called %d times, want exactly %d (%d attempts x 2 reads)", calls, want, maxCoherentReadAttempts)
	}
}
