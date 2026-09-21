package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

var errScenarioRunsUnavailable = errors.New("scenario execution unavailable")

type scenarioRunKey struct {
	scenarioID int64
	runID      string
}
type activeScenarioRun struct {
	cancel        context.CancelFunc
	done          chan struct{}
	inputHash     string
	final         *designscenario.RunReport
	persistCtx    context.Context
	persistCancel context.CancelFunc
}
type scenarioRunService struct {
	server  *Server
	repo    *designscenario.RunRepo
	mu      sync.Mutex
	active  map[scenarioRunKey]*activeScenarioRun
	closing bool
}

// InitializeScenarioRuns runs before the listener starts. Persisted active runs
// from the previous process become cancelled and are never replayed.
func (s *Server) InitializeScenarioRuns(ctx context.Context) error {
	return s.scenarioRuns.repo.RecoverInterrupted(ctx)
}

// CloseScenarioRuns prevents new work, cancels workers and joins them before
// callers stop the traffic recorder or close the database.
func (s *Server) CloseScenarioRuns(ctx context.Context) error {
	r := s.scenarioRuns
	r.mu.Lock()
	r.closing = true
	done := make([]<-chan struct{}, 0, len(r.active))
	cancelPersistence := make([]context.CancelFunc, 0, len(r.active))
	for _, active := range r.active {
		active.cancel()
		cancelPersistence = append(cancelPersistence, active.persistCancel)
		done = append(done, active.done)
	}
	r.mu.Unlock()
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			for _, cancel := range cancelPersistence {
				cancel()
			}
			return ctx.Err()
		}
	}
	return nil
}

func runInputHash(input RunDesignScenarioRequest) (string, error) {
	if input.Variables == nil {
		input.Variables = designscenario.ExecutionValues{}
	}
	raw, err := jsonx.Marshal(struct {
		RevisionID int64                          `json:"revisionId"`
		Variables  designscenario.ExecutionValues `json:"variables"`
		Name       string                         `json:"name"`
	}{input.RevisionID, input.Variables, input.Name})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r *scenarioRunService) start(ctx context.Context, scenarioID int64, input RunDesignScenarioRequest, source string) (designscenario.RunReport, error) {
	if err := input.validate(); err != nil {
		return designscenario.RunReport{}, err
	}
	hash, err := runInputHash(input)
	if err != nil {
		return designscenario.RunReport{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := scenarioRunKey{scenarioID, input.RunID}
	if active := r.active[key]; active != nil && active.final != nil {
		if active.inputHash != hash {
			return designscenario.RunReport{}, designscenario.ErrRunConflict
		}
		return *active.final, nil
	}
	existing, existingHash, err := r.repo.Lookup(ctx, scenarioID, input.RunID)
	if err == nil || errors.Is(err, designscenario.ErrRunGone) {
		if existingHash != hash {
			return designscenario.RunReport{}, designscenario.ErrRunConflict
		}
		return existing, err
	}
	if !errors.Is(err, designscenario.ErrNotFound) {
		return designscenario.RunReport{}, err
	}
	if r.closing || r.server.scenarioExecutor == nil {
		return designscenario.RunReport{}, errScenarioRunsUnavailable
	}
	if len(r.active) >= 4 {
		return designscenario.RunReport{}, designscenario.ErrRunBusy
	}
	for key := range r.active {
		if key.scenarioID == scenarioID {
			return designscenario.RunReport{}, designscenario.ErrRunBusy
		}
	}
	revision, err := r.server.designScenariosRepo.Revision(ctx, scenarioID, input.RevisionID)
	if err != nil {
		return designscenario.RunReport{}, err
	}
	initial, err := designscenario.PrepareRun(revision, input.RunID, input.Name, source, input.Variables)
	if err != nil {
		return designscenario.RunReport{}, err
	}
	stored, created, err := r.repo.Create(ctx, initial, hash)
	if err != nil || !created {
		return stored, err
	}
	// An accepted asynchronous run outlives its HTTP/MCP request. Only the
	// explicit cancel endpoint, shutdown and the run deadline cancel it.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 120*time.Second)
	persistCtx, persistCancel := context.WithCancel(context.WithoutCancel(ctx))
	active := &activeScenarioRun{cancel: cancel, done: make(chan struct{}), inputHash: hash, persistCtx: persistCtx, persistCancel: persistCancel}
	r.active[key] = active
	go r.execute(runCtx, key, active, revision, initial)
	return stored, nil
}

func (r *scenarioRunService) execute(ctx context.Context, key scenarioRunKey, active *activeScenarioRun, revision designscenario.Revision, initial designscenario.RunReport) {
	defer close(active.done)
	defer active.cancel()
	defer active.persistCancel()
	final := designscenario.Run(ctx, revision, initial, func(stepCtx context.Context, input designscenario.StepRequest) (designscenario.StepResponse, error) {
		return r.server.executeScenarioStep(stepCtx, revision, input)
	}, func(report designscenario.RunReport) error {
		// Terminal persistence belongs to the finalization critical section,
		// so a concurrently accepted cancellation cannot be overwritten.
		if report.Status != "running" {
			return nil
		}
		if err := r.repo.SaveProgress(ctx, report); err != nil {
			r.server.log.Error("persist scenario run progress", "run_id", key.runID, "err", err)
			return errors.New("не удалось сохранить прогресс")
		}
		return nil
	})
	r.mu.Lock()
	if ctx.Err() != nil {
		final = designscenario.CancelRunReport(final, "Прогон отменён: "+ctx.Err().Error())
	}
	// Decide cancellation versus completion exactly once. This immutable
	// snapshot remains readable while persistence recovers; cancelling a
	// completed run must not rewrite its outcome during a storage outage.
	active.final = &final
	r.mu.Unlock()
	for {
		finalCtx, cancel := context.WithTimeout(active.persistCtx, 5*time.Second)
		err := r.repo.Finish(finalCtx, final)
		cancel()
		if err == nil {
			r.mu.Lock()
			delete(r.active, key)
			r.mu.Unlock()
			return
		}
		r.server.log.Error("persist final scenario run; retrying", "scenario_id", key.scenarioID, "run_id", key.runID, "err", err)
		select {
		case <-active.persistCtx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (r *scenarioRunService) get(ctx context.Context, scenarioID int64, runID string) (designscenario.RunReport, error) {
	r.mu.Lock()
	active := r.active[scenarioRunKey{scenarioID, runID}]
	if active != nil && active.final != nil {
		report := *active.final
		r.mu.Unlock()
		return report, nil
	}
	r.mu.Unlock()
	report, _, err := r.repo.Lookup(ctx, scenarioID, runID)
	return report, err
}

func (r *scenarioRunService) list(ctx context.Context, scenarioID int64) ([]designscenario.RunSummary, error) {
	runs, err := r.repo.List(ctx, scenarioID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range runs {
		if active := r.active[scenarioRunKey{scenarioID, runs[i].ID}]; active != nil && active.final != nil {
			runs[i] = active.final.RunSummary
		}
	}
	return runs, nil
}

func (r *scenarioRunService) cancel(ctx context.Context, scenarioID int64, runID string) (designscenario.RunReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	active := r.active[scenarioRunKey{scenarioID, runID}]
	if active != nil && active.final != nil {
		return *active.final, nil
	}
	report, _, err := r.repo.Lookup(ctx, scenarioID, runID)
	if err != nil {
		return designscenario.RunReport{}, err
	}
	if report.Status == "running" {
		if active != nil {
			active.cancel()
		}
	}
	return report, nil
}
