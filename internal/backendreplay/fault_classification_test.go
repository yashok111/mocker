package backendreplay

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"github.com/yashok111/mocker/internal/testkit"
)

// TestDiagramScopeMismatchIsAReplayConflict pins review 2026-10-06, F1: an
// incomplete or foreign diagram scope is the caller's mistake, but check()
// returned a plain fmt.Errorf that the admin plane answers as a logged 500
// backend_internal. Every sibling failure on this path is a replay FaultError.
func TestDiagramScopeMismatchIsAReplayConflict(t *testing.T) {
	pkg := Template()
	pkg.Profile = Pin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("profile"))}
	pkg.Target = backendmodel.BackendReadTarget{RevisionID: id}
	pkg.TargetHash = p.HashBytes([]byte("target"))
	pin := backendmodel.DiagramPin{ID: id, Version: 1, ContentHash: p.HashBytes([]byte("diagram"))}
	pkg.DiagramScope = &backendmodel.DiagramScopeInput{Pin: pin, Selectors: []backendmodel.DiagramScopeSelector{{Kind: "semantic", ID: id}}}
	pkg.DiagramScopeHash = p.HashBytes([]byte("scope"))
	reader := &diagramReader{scope: &backendmodel.DiagramScope{Pin: pin, Target: pkg.Target, TargetHash: pkg.TargetHash, ScopeHash: pkg.DiagramScopeHash, Truncated: true}}
	err := ValidateDiagramBindings(t.Context(), reader, id, pkg)
	if fault, ok := errors.AsType[*backendmodel.FaultError](err); !ok || fault.Status != 409 || fault.Code != "backend_replay_conflict" {
		t.Fatalf("err = %T %v, want 409 backend_replay_conflict", err, err)
	}
}

// TestReplayQueueFullIsRetryable429 pins review 2026-10-06, F29 and F12: a
// full replay queue answered 409 backend_replay_queue_full with
// retryable:false, while the contract lists 429 on every replay route and the
// analysis queue answers 429, retryable, with Retry-After. A client treated a
// transient queue as a permanent conflict.
func TestReplayQueueFullIsRetryable429(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	if err := s.repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		for range QueueLimit {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_replay_runs(id,project_id,target_id,status,input_hash,input_json,version,author,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				newReplayID(), pid, "test", "queued", p.HashBytes([]byte("filler")), "{}", 1, "actor", nowReplay(), nowReplay()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Start(t.Context(), pid, "actor", in)
	fault, ok := errors.AsType[*backendmodel.FaultError](err)
	if !ok || fault.Status != 429 || fault.Code != "backend_replay_queue_full" || !fault.Retryable {
		t.Fatalf("err = %v (%+v), want retryable 429 backend_replay_queue_full", err, fault)
	}
}

// TestReplayActorLookupFailureIsNotForbidden pins review 2026-10-06, F177:
// the default ActorAllowed reduced every users-lookup failure to false, so a
// cancelled request or a reader-pool failure was answered as a permanent 403
// "Actor is no longer authorized" and the real cause was never seen.
func TestReplayActorLookupFailureIsNotForbidden(t *testing.T) {
	db := testkit.NewDB(t)
	s := NewService(NewRepo(db), backendmodel.NewRepo(db), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := s.actor(ctx, "1")
	if err == nil {
		t.Fatal("a failed lookup admitted the actor")
	}
	if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok && fault.Status == 403 {
		t.Fatalf("lookup failure answered as %d %s", fault.Status, fault.Code)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation itself", err)
	}
	// A genuinely absent account is still the 403.
	err = s.actor(t.Context(), "424242")
	if fault, ok := errors.AsType[*backendmodel.FaultError](err); !ok || fault.Status != 403 {
		t.Fatalf("absent actor: err = %v, want 403", err)
	}
}
