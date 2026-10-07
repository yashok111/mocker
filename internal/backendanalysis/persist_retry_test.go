package backendanalysis

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/store"
)

// writerHoldingEngine completes only after another transaction has taken the
// single writer connection, which it keeps for `hold`: the shape of a long
// portable import or export finishing while a job publishes its result.
type writerHoldingEngine struct {
	db   *store.DB
	hold time.Duration
	done chan error
}

func (e *writerHoldingEngine) Analyze(ctx context.Context, _ *ImmutableInput, _ func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	held := make(chan struct{})
	go func() {
		e.done <- e.db.Write(context.WithoutCancel(ctx), func(*sql.Tx) error {
			close(held)
			time.Sleep(e.hold)
			return nil
		})
	}()
	<-held
	return &TerminalSnapshot{Status: "completed", Snapshot: PreparedSnapshot{Manifest: ResultManifest{Complete: true, Verdict: "compatible_within_scope"}}}, nil
}

// Review 2026-10-06, F4: a writer held longer than persistTimeout made the
// terminal write answer "context deadline exceeded", execute returned it and the
// worker cancelled the service, stopping the server over a busy writer. The
// wait is now retried with backoff while the service runs.
func TestAnalysisBusyWriterAtTerminalWriteDoesNotStopService(t *testing.T) {
	r, db := testRepo(t)
	engine := &writerHoldingEngine{db: db, hold: 400 * time.Millisecond, done: make(chan error, 1)}
	s := NewService(r, nil, engine)
	s.persistTimeout = 30 * time.Millisecond
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, r, "start")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("service stopped over a busy writer: %v", err)
		default:
		}
		current, err := r.Get(t.Context(), projectID, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "completed" {
			break
		}
		if current.Status != "running" && current.Status != "queued" {
			t.Fatalf("want completed, got %+v", current)
		}
		if time.Now().After(deadline) {
			t.Fatal("job never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := <-engine.done; err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run reported a busy writer: %v", err)
	}
}

// Review 2026-10-06, F193: shutdown landing between Claim and the first read of
// the job returned context.Canceled from execute, which the worker recorded as
// the service's failure (a clean shutdown exiting non-zero) and the claimed job
// stayed running. It is now closed as interrupted, like an engine stopped by
// the same shutdown.
func TestAnalysisShutdownAtJobStartClosesJobWithoutError(t *testing.T) {
	r, _ := testRepo(t)
	s := NewService(r, nil, completeEngine{})
	j := mustStart(t, r, "start")
	claim, err := r.Claim(t.Context(), "worker")
	if err != nil || claim == nil || claim.Job.ID != j.ID {
		t.Fatalf("claim %+v %v", claim, err)
	}
	app, cancel := context.WithCancel(t.Context())
	cancel()
	if err = s.execute(app, claim); err != nil {
		t.Fatalf("shutdown at job start reported as a service failure: %v", err)
	}
	current, err := r.Get(t.Context(), projectID, j.ID)
	if err != nil || current.Status != "interrupted" {
		t.Fatalf("want interrupted, got %+v %v", current, err)
	}
}
