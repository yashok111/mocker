package backendanalysis

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestAnalysisWorkerRequestCancellationDoesNotCancelJob(t *testing.T) {
	r, db := testRepo(t)
	graphs := backendmodel.NewRepo(db)
	project, err := graphs.Create(t.Context(), backendmodel.CreateInput{Name: "Actual immutable input", IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	engine := &blockingEngine{entered: make(chan struct{}), exited: make(chan struct{})}
	s := NewService(r, graphs, engine)
	if err = s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	input := StartInput{Kind: "diff", FromRevisionID: project.CurrentRevisionID, Target: AnalysisTarget{RevisionID: project.CurrentRevisionID}, Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: "start"}
	request, cancelRequest := context.WithCancel(t.Context())
	j, err := s.Start(request, project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest()
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	select {
	case <-engine.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not claim acknowledged job")
	}
	select {
	case <-engine.exited:
		t.Fatal("request cancelled background work")
	default:
	}
	saved, err := r.Input(t.Context(), project.ID, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.BeforePins.EffectiveSemanticHash == "" || saved.AfterPins.EffectiveSemanticHash == "" {
		t.Fatal("effective hashes missing")
	}
	s.graphs = nil // replay must not touch graph preparation again.
	replay, err := s.Start(t.Context(), project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(j)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("replay changed bytes")
	}
	if _, err = s.Cancel(t.Context(), project.ID, j.ID, CancelInput{IdempotencyKey: "cancel"}); err != nil {
		t.Fatal(err)
	}
	closeCtx, release := context.WithTimeout(t.Context(), 3*time.Second)
	defer release()
	if err = s.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestAnalysisCloseDeadlineDoesNotPretendJoined(t *testing.T) {
	r, _ := testRepo(t)
	engine := &delayedEngine{entered: make(chan struct{}), release: make(chan struct{})}
	s := NewService(r, nil, engine)
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustStart(t, r, "start")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	<-engine.entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close should report unjoined worker: %v", err)
	}
	close(engine.release)
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type delayedEngine struct{ entered, release chan struct{} }

func (e *delayedEngine) Analyze(context.Context, *ImmutableInput, func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	close(e.entered)
	<-e.release
	return nil, context.Canceled
}

func TestAnalysisWorkerPublicationRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	attempts := 0
	err := retryPublication(ctx, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary storage failure")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("safe retry: %d %v", attempts, err)
	}
	attempts = 0
	err = retryPublication(ctx, func() error { attempts++; return fault(409, "lost_claim", "cancel won") })
	requireStatus(t, err, 409)
	if attempts != 1 {
		t.Fatal("lost claim retried")
	}
}

type completeEngine struct{}

func (completeEngine) Analyze(context.Context, *ImmutableInput, func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	return &TerminalSnapshot{Status: "completed", Snapshot: PreparedSnapshot{Manifest: ResultManifest{Complete: true, Verdict: "compatible_within_scope"}}}, nil
}
func TestAnalysisWorkerPersistenceFailureRefusesContinuedRun(t *testing.T) {
	r, db := testRepo(t)
	s := NewService(r, nil, completeEngine{})
	s.persistTimeout = 20 * time.Millisecond
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, r, "start")
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER analysis_terminal_fail BEFORE INSERT ON backend_analysis_manifests BEGIN SELECT RAISE(ABORT,'storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.Run(ctx); err == nil {
		t.Fatal("Run silently abandoned failed durable publication")
	}
	current, err := r.Get(t.Context(), projectID, j.ID)
	if err != nil || current.Status != "running" {
		t.Fatalf("failed publication lost durable claim %v %v", current, err)
	}
	if _, err = r.Start(t.Context(), testPrepared(t, "start"), nil); err != nil {
		t.Fatal("lost acknowledged receipt", err)
	}
}
func TestAnalysisWorkerDeadlinePublishesIncomplete(t *testing.T) {
	r, _ := testRepo(t)
	e := &blockingEngine{entered: make(chan struct{}), exited: make(chan struct{})}
	s := NewService(r, nil, e)
	s.jobTimeout = 10 * time.Millisecond
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, r, "start")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	<-e.exited
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		current, err := r.Get(t.Context(), projectID, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "failed" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("timeout never persisted")
		case <-time.After(time.Millisecond):
		}
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	page, err := r.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 1, Section: "gaps"})
	if err != nil || page.Manifest.Complete || page.Manifest.Verdict != "unknown" {
		t.Fatalf("deadline result %v %v", page, err)
	}
}
