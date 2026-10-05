package backendanalysis

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type GraphReader interface {
	ReserveAnalysisInput(context.Context, string, backendmodel.AnalysisInputFootprint) (context.Context, *backendmodel.AnalysisInputReservation, error)
	ResolveEffectiveGraph(context.Context, string, backendmodel.BackendReadTarget) (*backendmodel.EffectiveGraphSnapshot, error)
	FreezeChangePreview(context.Context, string, string, backendmodel.PreviewChangeProposalInput, string) (*backendmodel.FrozenChangePreview, error)
	ValidateFrozenChangePreviewAdmission(context.Context, *sql.Tx, string, *backendmodel.FrozenChangePreview) error
	ResolveFrozenChangePreview(context.Context, string, *backendmodel.FrozenChangePreview) (*backendmodel.EffectiveGraphSnapshot, error)
	EffectiveGraphInputFootprint(context.Context, string, backendmodel.BackendReadTarget) (backendmodel.AnalysisInputFootprint, error)
	AnalysisPairInputFootprint(context.Context, string, string, backendmodel.BackendReadTarget, *backendmodel.AnalysisCommandPreviewInput) (backendmodel.AnalysisInputFootprint, error)
}
type Engine interface {
	Analyze(context.Context, *ImmutableInput, func(PreparedSnapshot) error) (*TerminalSnapshot, error)
}
type ArtifactProjectionReader func(context.Context) *backendmodel.EditorArtifactRequest

type Service struct {
	ready                      chan struct{}
	finished                   bool
	jobTimeout, persistTimeout time.Duration
	runErr                     error
	repo                       *Repo
	graphs                     GraphReader
	engine                     Engine
	mu                         sync.Mutex
	recovered, started, closed bool
	cancel                     context.CancelFunc
	done                       chan struct{}
	wake                       chan struct{}
	active                     map[string]context.CancelFunc
}

func NewService(repo *Repo, graphs GraphReader, engine Engine) *Service {
	return &Service{ready: make(chan struct{}), jobTimeout: 60 * time.Second, persistTimeout: 5 * time.Second, repo: repo, graphs: graphs, engine: engine, done: make(chan struct{}), wake: make(chan struct{}, 1), active: map[string]context.CancelFunc{}}
}
func (s *Service) RecoverInterrupted(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return errors.New("analysis recovery after run/close")
	}
	s.recovered = false
	if err := s.repo.RecoverInterrupted(ctx); err != nil {
		return err
	}
	s.recovered = true
	return nil
}
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if !s.recovered || s.started || s.closed || s.engine == nil {
		s.mu.Unlock()
		return errors.New("analysis service is not ready to run")
	}
	app, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()
	defer cancel()
	defer close(s.done)
	defer func() { s.mu.Lock(); s.finished = true; s.mu.Unlock() }()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { s.worker(app) })
	}
	close(s.ready)
	wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runErr
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	cancel, started := s.cancel, s.started
	s.mu.Unlock()
	if !started {
		return nil
	}
	cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) hint() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Start(ctx context.Context, pid string, in StartInput) (*Job, error) {
	raw, err := canonical(in)
	if err != nil {
		return nil, err
	}
	var normalized StartInput
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	in = normalized
	hash, err := requestHash(in)
	if err != nil {
		return nil, err
	}
	if rec, e := s.repo.LookupReceipt(ctx, pid, "start", in.IdempotencyKey); e != nil {
		return nil, e
	} else if rec != nil {
		return replay(rec, hash)
	}
	if s.graphs == nil {
		return nil, errors.New("analysis graph reader missing")
	}
	if b43Kind(in.Kind) {
		return s.startB43(ctx, pid, in, hash)
	}
	target, preview := targetInput(in.Target)
	footprint, err := s.graphs.AnalysisPairInputFootprint(ctx, pid, in.FromRevisionID, target, preview)
	if err != nil {
		return nil, err
	}
	leased, lease, err := s.graphs.ReserveAnalysisInput(ctx, pid, footprint)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	before, err := s.graphs.ResolveEffectiveGraph(leased, pid, backendmodel.BackendReadTarget{RevisionID: in.FromRevisionID})
	if err != nil {
		return nil, err
	}
	input := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", Kind: in.Kind, ProjectID: pid, From: before.Target, BeforePins: before.Pins, BeforeSource: sourcePins(before), Scope: in.Scope, Limits: in.Limits, RuleSetVersion: "b42-rules/v1", TraversalVersion: "b42-traversal/v1", ObservationMode: "none"}
	var after *backendmodel.EffectiveGraphSnapshot
	var admission AdmissionCheck
	if preview != nil {
		input.CommandPreview, err = s.graphs.FreezeChangePreview(leased, pid, preview.ChangeProposal.ProposalID, preview.Preview, preview.CandidateHash)
		if err != nil {
			return nil, err
		}
		after, err = s.graphs.ResolveFrozenChangePreview(leased, pid, input.CommandPreview)
		admission = func(c context.Context, tx *sql.Tx) error {
			return s.graphs.ValidateFrozenChangePreviewAdmission(c, tx, pid, input.CommandPreview)
		}
	} else {
		input.To = &target
		after, err = s.graphs.ResolveEffectiveGraph(leased, pid, target)
	}
	if err != nil {
		return nil, err
	}
	input.AfterPins = after.Pins
	input.AfterSource = sourcePins(after)
	raw, err = canonical(input)
	if err != nil {
		return nil, err
	}
	job, err := s.repo.Start(ctx, PreparedStart{ProjectID: pid, InputJSON: raw, InputHash: digest(raw), RequestHash: hash, Key: in.IdempotencyKey, OutputReservation: in.Limits.ResultBytes}, admission)
	if err == nil {
		s.hint()
	}
	return job, err
}
func targetInput(t AnalysisTarget) (backendmodel.BackendReadTarget, *backendmodel.AnalysisCommandPreviewInput) {
	target := backendmodel.BackendReadTarget{RevisionID: t.RevisionID, Proposal: t.Proposal, ChangeProposal: t.ChangeProposal}
	if t.CommandPreview == nil {
		return target, nil
	}
	p := t.CommandPreview
	return target, &backendmodel.AnalysisCommandPreviewInput{ChangeProposal: p.ChangeProposal, CandidateHash: p.CandidateHash, Preview: backendmodel.PreviewChangeProposalInput{ExpectedVersion: p.ExpectedVersion, ProposalRevisionID: p.ChangeProposal.ProposalRevisionID, Commands: p.Commands}}
}
func sourcePins(g *backendmodel.EffectiveGraphSnapshot) backendmodel.AnalysisSourcePins {
	p := backendmodel.AnalysisSourcePins{RevisionID: g.Pins.BaseRevisionID, SemanticHash: g.Pins.BaseSemanticHash, SourceVectorHash: g.Pins.SourceVectorHash, SourceSnapshotIDs: g.Pins.SourceSnapshotIDs}
	if g.Source != nil {
		p.ContentHash = g.Source.SourceContentHash
	}
	return p
}
func (s *Service) Retry(ctx context.Context, pid, id string, in RetryInput) (*Job, error) {
	if !validKey(in.IdempotencyKey) {
		return nil, malformed("Invalid idempotency key")
	}
	j, err := s.repo.Retry(ctx, pid, id, in)
	if err == nil {
		s.hint()
	}
	return j, err
}
func (s *Service) Cancel(ctx context.Context, pid, id string, in CancelInput) (*Job, error) {
	if !validKey(in.IdempotencyKey) {
		return nil, malformed("Invalid idempotency key")
	}
	j, err := s.repo.Cancel(ctx, pid, id, in)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	cancel := s.active[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return j, nil
}
func (s *Service) worker(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		claim, err := s.repo.Claim(ctx, opaqueID())
		if err == nil && claim != nil {
			if err = s.execute(ctx, claim); err != nil {
				s.mu.Lock()
				if s.runErr == nil {
					s.runErr = err
				}
				cancel := s.cancel
				s.mu.Unlock()
				cancel()
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
func (s *Service) execute(app context.Context, claim *ClaimedJob) error {
	ctx, cancel := context.WithTimeout(app, s.jobTimeout)
	defer cancel()
	s.mu.Lock()
	s.active[claim.Job.ID] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, claim.Job.ID); s.mu.Unlock() }()
	// Cancellation may win between Claim and registration. Reading after
	// registration closes that gap; later cancellation finds the registered cancel.
	current, readErr := s.repo.Get(ctx, claim.Job.ProjectID, claim.Job.ID)
	if readErr != nil {
		return readErr
	}
	if current.Status != "running" {
		return nil
	}
	version := int64(0)
	bind := func(p PreparedSnapshot) PreparedSnapshot {
		p.Manifest.JobID = claim.Job.ID
		p.Manifest.AnalysisInputHash = claim.Job.AnalysisInputHash
		p.Manifest.ResultVersion = version + 1
		return p
	}
	terminal, err := s.engine.Analyze(ctx, &claim.Input, func(p PreparedSnapshot) error {
		p = bind(p)
		e := retryPublication(ctx, func() error {
			_, err := s.repo.Publish(ctx, claim.Job.ProjectID, claim.Job.ID, claim.Token, p)
			return err
		})
		if e == nil {
			version++
		}
		return e
	})
	// Persist shutdown with an independent bounded context before Run joins and the
	// application closes the DB. A losing cancellation keeps its original receipt.
	persist, release := context.WithTimeout(context.WithoutCancel(app), s.persistTimeout)
	defer release()
	if err != nil || terminal == nil {
		status := "failed"
		if app.Err() != nil {
			status = "interrupted"
		}
		code := status
		if errors.Is(err, context.DeadlineExceeded) {
			code = "deadline_exceeded"
		}
		_, prefix, e := s.repo.acceptedPrefix(persist, claim.Job.ProjectID, claim.Job.ID, code)
		if e != nil {
			return e
		}
		terminal = &TerminalSnapshot{Status: status, Snapshot: prefix, Diagnostic: &Diagnostic{ID: code, Code: code, Message: "Analysis did not complete"}}
	} else {
		terminal.Snapshot = bind(terminal.Snapshot)
	}
	err = retryPublication(persist, func() error {
		_, e := s.repo.Finalize(persist, claim.Job.ProjectID, claim.Job.ID, claim.Token, *terminal)
		return e
	})
	var f *backendmodel.FaultError
	if errors.As(err, &f) && f.Code == "backend_analysis_lost_claim" {
		return nil
	}
	return err
}

// A retry reuses the same immutable prefix and version, including after an
// ambiguous commit. It never repeats model admission or graph evaluation.
func retryPublication(ctx context.Context, publish func() error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := publish()
		if err == nil {
			return nil
		}
		var f *backendmodel.FaultError
		if errors.As(err, &f) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Running reports actual worker lifetime, including drain after Close begins.
func (s *Service) Running() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.started && !s.finished }
func (s *Service) WaitRunning(ctx context.Context) error {
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
