package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/workspaces"
)

func waitScenarioRun(t *testing.T, s *Server, scenarioID int64, runID string) designscenario.RunReport {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		report, _, err := s.scenarioRuns.repo.Lookup(t.Context(), scenarioID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if report.Status != "running" {
			return report
		}
		select {
		case <-deadline.C:
			t.Fatal("run did not finish")
		case <-ticker.C:
		}
	}
}

func TestScenarioRunConcurrentReplayAndGlobalCapacity(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	t.Cleanup(func() {
		if err := s.CloseScenarioRuns(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	entered := make(chan struct{}, 4)
	var calls atomic.Int64
	s.SetScenarioExecutor(scenarioStepExecutorFunc(func(_ http.ResponseWriter, r *http.Request, _ *workspaces.Workspace) {
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			report, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, RunDesignScenarioRequest{RunID: "same", RevisionID: scenario.Draft.ID}, "ui")
			if err == nil && report.ID != "same" {
				err = fmt.Errorf("wrong run ID %q", report.ID)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	<-entered
	for i := 2; i <= 5; i++ {
		next, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: scenario.Draft.Document, Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.scenarioRuns.start(t.Context(), next.Scenario.ID, RunDesignScenarioRequest{RunID: "run", RevisionID: next.Draft.ID}, "ui")
		if i == 5 {
			if !errors.Is(err, designscenario.ErrRunBusy) {
				t.Fatalf("global capacity err=%v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			<-entered
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("dispatches=%d", calls.Load())
	}
}

func TestScenarioRunProgressFailureStopsDispatch(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	t.Cleanup(func() {
		if err := s.CloseScenarioRuns(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_run_progress BEFORE UPDATE ON design_scenario_runs WHEN NEW.status='running' BEGIN SELECT RAISE(ABORT,'private database failure'); END`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	s.SetScenarioExecutor(scenarioStepExecutorFunc(func(http.ResponseWriter, *http.Request, *workspaces.Workspace) { calls.Add(1) }))
	if _, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, RunDesignScenarioRequest{RunID: "failure", RevisionID: scenario.Draft.ID}, "ui"); err != nil {
		t.Fatal(err)
	}
	report := waitScenarioRun(t, s, scenario.Scenario.ID, "failure")
	if report.Status != "failed" || calls.Load() != 0 {
		t.Fatalf("status=%s calls=%d", report.Status, calls.Load())
	}
	if strings.Contains(report.Reason, "private database failure") {
		t.Fatalf("internal database error leaked: %s", report.Reason)
	}
}

func TestScenarioRunRetriesFinalPersistenceWithoutLosingResult(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := s.CloseScenarioRuns(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_run_finish BEFORE UPDATE ON design_scenario_runs WHEN NEW.status!='running' BEGIN SELECT RAISE(ABORT,'temporarily unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = s.db.W.ExecContext(context.WithoutCancel(t.Context()), `DROP TRIGGER IF EXISTS fail_run_finish`)
	}()
	input := RunDesignScenarioRequest{RunID: "retry", RevisionID: scenario.Draft.ID}
	if _, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, input, "ui"); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		report, err := s.scenarioRuns.get(t.Context(), scenario.Scenario.ID, "retry")
		if err != nil {
			t.Fatal(err)
		}
		if report.Status == "passed" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("terminal result unavailable during persistence failure")
		case <-ticker.C:
		}
	}
	stored, _, err := s.scenarioRuns.repo.Lookup(t.Context(), scenario.Scenario.ID, "retry")
	if err != nil || stored.Status != "running" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	replay, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, input, "mcp")
	if err != nil || replay.Status != "passed" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	changed := input
	changed.Name = "changed"
	if _, err = s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, changed, "ui"); !errors.Is(err, designscenario.ErrRunConflict) {
		t.Fatalf("changed replay err=%v", err)
	}
	cancelled, err := s.scenarioRuns.cancel(t.Context(), scenario.Scenario.ID, "retry")
	if err != nil || cancelled.Status != "passed" {
		t.Fatalf("cancel=%+v err=%v", cancelled, err)
	}
	list, err := s.scenarioRuns.list(t.Context(), scenario.Scenario.ID)
	if err != nil || len(list) != 1 || list[0].Status != "passed" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if _, err = s.db.W.ExecContext(t.Context(), `DROP TRIGGER fail_run_finish`); err != nil {
		t.Fatal(err)
	}
	finished := waitScenarioRun(t, s, scenario.Scenario.ID, "retry")
	if finished.Status != "passed" {
		t.Fatalf("retry lost terminal outcome: %+v", finished)
	}
	input.RunID = "after-recovery"
	if _, err = s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, input, "ui"); err != nil {
		t.Fatalf("scenario stayed blocked: %v", err)
	}
}

func TestScenarioRunShutdownBoundsFailedFinalPersistence(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	if _, err := s.db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_run_finish BEFORE UPDATE ON design_scenario_runs WHEN NEW.status!='running' BEGIN SELECT RAISE(ABORT,'unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, RunDesignScenarioRequest{RunID: "stop", RevisionID: scenario.Draft.ID}, "ui"); err != nil {
		t.Fatal(err)
	}
	s.scenarioRuns.mu.Lock()
	active := s.scenarioRuns.active[scenarioRunKey{scenario.Scenario.ID, "stop"}]
	s.scenarioRuns.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := s.CloseScenarioRuns(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
	select {
	case <-active.done:
	case <-time.After(time.Second):
		t.Fatal("persistence worker leaked after shutdown deadline")
	}
}

func TestScenarioRunHTTPContractAndStrictInput(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	t.Cleanup(func() {
		if err := s.CloseScenarioRuns(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	source := httptest.NewRequest(http.MethodPost, "http://admin.test/mcp", nil)
	path := fmt.Sprintf("/api/design-scenarios/%d/runs", scenario.Scenario.ID)
	for _, body := range []string{`null`, `{}`, `{"runId":null,"revisionId":1}`, `{"runId":"bad/id","revisionId":1}`, `{"runId":"x","revisionId":null}`, fmt.Sprintf(`{"runId":"x","revisionId":%d,"variables":{"a":null}}`, scenario.Draft.ID), fmt.Sprintf(`{"runId":"x","revisionId":%d,"source":"ui"}`, scenario.Draft.ID)} {
		status, raw, err := s.CallAsMCP(t.Context(), source, http.MethodPost, path, []byte(body))
		if err != nil || status != 400 {
			t.Fatalf("body=%s status=%d response=%s err=%v", body, status, raw, err)
		}
	}
	body := []byte(fmt.Sprintf(`{"runId":"http","revisionId":%d}`, scenario.Draft.ID))
	status, raw, err := s.CallAsMCP(t.Context(), source, http.MethodPost, path, body)
	if err != nil || status != 202 {
		t.Fatalf("status=%d response=%s err=%v", status, raw, err)
	}
	var report designscenario.RunReport
	if err = jsonx.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Source != "mcp" || report.ID != "http" {
		t.Fatalf("report=%+v", report)
	}
	waitScenarioRun(t, s, scenario.Scenario.ID, "http")
	status, raw, err = s.CallAsMCP(t.Context(), source, http.MethodPost, path, body)
	if err != nil || status != 200 {
		t.Fatalf("terminal replay status=%d response=%s err=%v", status, raw, err)
	}
	status, raw, err = s.CallAsMCP(t.Context(), source, http.MethodGet, path, nil)
	if err != nil || status != 200 || !strings.Contains(string(raw), `"http"`) {
		t.Fatalf("list status=%d response=%s err=%v", status, raw, err)
	}
	status, raw, err = s.CallAsMCP(t.Context(), source, http.MethodGet, fmt.Sprintf("/api/design-scenarios/%d/runs/http", scenario.Scenario.ID+1), nil)
	if err != nil || status != 404 {
		t.Fatalf("scoped get status=%d response=%s err=%v", status, raw, err)
	}
}

func TestScenarioRunAsyncIdempotencyAndDetachedRequest(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	t.Cleanup(func() {
		if err := s.CloseScenarioRuns(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	s.SetScenarioExecutor(scenarioStepExecutorFunc(func(w http.ResponseWriter, r *http.Request, _ *workspaces.Workspace) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			w.WriteHeader(201)
		case <-r.Context().Done():
		}
	}))
	ctx, cancel := context.WithCancel(t.Context())
	input := RunDesignScenarioRequest{RunID: "one", RevisionID: scenario.Draft.ID}
	report, err := s.scenarioRuns.start(ctx, scenario.Scenario.ID, input, "ui")
	if err != nil || report.Status != "running" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	<-entered
	cancel()
	duplicate, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, input, "mcp")
	if err != nil || duplicate.ID != "one" || duplicate.Source != "ui" {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	changed := input
	changed.Name = "different"
	if _, err = s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, changed, "ui"); !errors.Is(err, designscenario.ErrRunConflict) {
		t.Fatalf("changed input=%v", err)
	}
	busy := input
	busy.RunID = "two"
	if _, err = s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, busy, "ui"); !errors.Is(err, designscenario.ErrRunBusy) {
		t.Fatalf("busy=%v", err)
	}
	close(release)
	finished := waitScenarioRun(t, s, scenario.Scenario.ID, "one")
	if finished.Status != "passed" || calls.Load() != 1 {
		t.Fatalf("report=%+v calls=%d", finished, calls.Load())
	}
	if _, err = s.scenarioRuns.cancel(t.Context(), scenario.Scenario.ID, "one"); err != nil {
		t.Fatal(err)
	}
	unchanged := waitScenarioRun(t, s, scenario.Scenario.ID, "one")
	if unchanged.Status != "passed" {
		t.Fatalf("terminal cancellation changed result: %+v", unchanged)
	}
}

func TestScenarioRunCancelAndShutdownJoin(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	entered, exited := make(chan struct{}), make(chan struct{})
	s.SetScenarioExecutor(scenarioStepExecutorFunc(func(_ http.ResponseWriter, r *http.Request, _ *workspaces.Workspace) {
		close(entered)
		<-r.Context().Done()
		close(exited)
	}))
	if _, err := s.scenarioRuns.start(t.Context(), scenario.Scenario.ID, RunDesignScenarioRequest{RunID: "one", RevisionID: scenario.Draft.ID}, "mcp"); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := s.scenarioRuns.cancel(t.Context(), scenario.Scenario.ID+1, "one"); !errors.Is(err, designscenario.ErrNotFound) {
		t.Fatalf("unscoped cancel=%v", err)
	}
	if _, err := s.scenarioRuns.cancel(t.Context(), scenario.Scenario.ID, "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseScenarioRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("shutdown did not join executor")
	}
	report := waitScenarioRun(t, s, scenario.Scenario.ID, "one")
	if report.Status != "cancelled" || report.Source != "mcp" {
		t.Fatalf("report=%+v", report)
	}
}
