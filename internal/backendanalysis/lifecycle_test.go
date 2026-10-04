package backendanalysis

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"
)

func TestAnalysisCancelCompletionRace(t *testing.T) {
	for _, winner := range []string{"cancel", "complete"} {
		t.Run(winner, func(t *testing.T) {
			r, _ := testRepo(t)
			j := mustStart(t, r, "start")
			if _, err := r.Claim(t.Context(), "token"); err != nil {
				t.Fatal(err)
			}
			s := snapshot(t, j, 1, `[{"id":"accepted"}]`)
			if _, err := r.Publish(t.Context(), projectID, j.ID, "token", s); err != nil {
				t.Fatal(err)
			}
			terminal := s
			terminal.Manifest.ResultVersion = 2
			terminal.Manifest.Complete = true
			if winner == "cancel" {
				cancelled, err := r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"})
				if err != nil {
					t.Fatal(err)
				}
				if cancelled.Status != "cancelled" {
					t.Fatal(cancelled)
				}
				_, err = r.Finalize(t.Context(), projectID, j.ID, "token", TerminalSnapshot{Status: "completed", Snapshot: terminal})
				requireStatus(t, err, 409)
				replay, err := r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"})
				if err != nil {
					t.Fatal(err)
				}
				a, _ := json.Marshal(cancelled)
				b, _ := json.Marshal(replay)
				if string(a) != string(b) {
					t.Fatal("cancel replay changed")
				}
			} else {
				if _, err := r.Finalize(t.Context(), projectID, j.ID, "token", TerminalSnapshot{Status: "completed", Snapshot: terminal}); err != nil {
					t.Fatal(err)
				}
				_, err := r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"})
				requireStatus(t, err, 409)
			}
			p, err := r.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 2, Section: "changes"})
			if err != nil {
				t.Fatal(err)
			}
			if string(p.ItemsJSON) != `[{"id":"accepted"}]` {
				t.Fatal("lost accepted prefix")
			}
		})
	}
}
func TestAnalysisRetrySavedInput(t *testing.T) {
	r, _ := testRepo(t)
	original := mustStart(t, r, "start")
	_, err := r.Retry(t.Context(), projectID, original.ID, RetryInput{IdempotencyKey: "retry"})
	requireStatus(t, err, 409)
	if _, err = r.Cancel(t.Context(), projectID, original.ID, CancelInput{IdempotencyKey: "cancel"}); err != nil {
		t.Fatal(err)
	}
	retry, err := r.Retry(t.Context(), projectID, original.ID, RetryInput{IdempotencyKey: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == original.ID || retry.AnalysisInputHash != original.AnalysisInputHash || retry.Status != "queued" {
		t.Fatal(retry)
	}
	replay, err := r.Start(t.Context(), testPrepared(t, "start"), nil)
	if err != nil || replay.ID != original.ID || replay.Status != "queued" {
		t.Fatalf("start replay %v %v", replay, err)
	}
}

type blockingEngine struct {
	entered chan struct{}
	exited  chan struct{}
}

func (e *blockingEngine) Analyze(ctx context.Context, _ *ImmutableInput, _ func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	close(e.entered)
	<-ctx.Done()
	close(e.exited)
	return nil, ctx.Err()
}
func TestAnalysisRecoveryBeforeRun(t *testing.T) {
	r, db := testRepo(t)
	queued := mustStart(t, r, "queued")
	running := mustStart(t, r, "running")
	if _, err := r.Claim(t.Context(), "old"); err != nil {
		t.Fatal(err)
	}
	s := NewService(r, nil, &blockingEngine{})
	if err := s.Run(t.Context()); err == nil {
		t.Fatal("Run before recovery accepted")
	}
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, j := range []*Job{queued, running} {
		got, err := r.Get(t.Context(), projectID, j.ID)
		if err != nil || got.Status != "interrupted" || got.ResultVersion == nil {
			t.Fatalf("recovery %v %v", got, err)
		}
	}
	mustStart(t, r, "fail-recovery")
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER analysis_recovery_fail BEFORE INSERT ON backend_analysis_manifests BEGIN SELECT RAISE(ABORT,'recovery fault'); END`); err != nil {
		t.Fatal(err)
	}
	s2 := NewService(r, nil, &blockingEngine{})
	if err := s2.RecoverInterrupted(t.Context()); err == nil {
		t.Fatal("recovery failure ignored")
	}
	if err := s2.Run(t.Context()); err == nil {
		t.Fatal("Run accepted failed recovery")
	}
}
func TestAnalysisCloseJoinsWorkers(t *testing.T) {
	r, _ := testRepo(t)
	engine := &blockingEngine{entered: make(chan struct{}), exited: make(chan struct{})}
	s := NewService(r, nil, engine)
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, r, "start")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	select {
	case <-engine.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("durable polling missed job")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-engine.exited:
	default:
		t.Fatal("Close returned before engine joined")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(t.Context(), projectID, j.ID)
	if err != nil || got.Status != "interrupted" {
		t.Fatalf("shutdown %v %v", got, err)
	}
}

type rejectEngine struct{ t *testing.T }

func (e rejectEngine) Analyze(context.Context, *ImmutableInput, func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	e.t.Error("cancelled claim reached engine")
	return nil, context.Canceled
}
func TestAnalysisWorkerCancelBeforeRegistration(t *testing.T) {
	r, _ := testRepo(t)
	j := mustStart(t, r, "start")
	claim, err := r.Claim(t.Context(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"}); err != nil {
		t.Fatal(err)
	}
	s := NewService(r, nil, rejectEngine{t})
	s.execute(t.Context(), claim)
}
