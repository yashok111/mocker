package backendreplay

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/backendblob"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func (s *Service) RecoverInterrupted(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return errors.New("replay recovery after start or close")
	}
	targets := make([]Target, 0, len(s.targets))
	for _, t := range s.targets {
		targets = append(targets, t)
	}
	if err := s.repo.registerConfigs(ctx, targets); err != nil {
		return err
	}
	if err := s.repo.RecoverInterrupted(ctx); err != nil {
		return err
	}
	s.recovered = true
	return nil
}
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if !s.recovered || s.started || s.closed {
		s.mu.Unlock()
		return errors.New("replay service not ready")
	}
	app, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()
	defer cancel()
	defer close(s.done)
	defer func() { s.mu.Lock(); s.finished = true; s.mu.Unlock() }()
	var wg sync.WaitGroup
	for range Workers {
		wg.Go(func() { s.worker(app) })
	}
	close(s.ready)
	wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runErr
}
func (s *Service) WaitRunning(ctx context.Context) error {
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Running() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.started && !s.finished }
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
func (s *Service) claim(ctx context.Context) (*Run, string, error) {
	var out *Run
	var author string
	err := s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		var id, pid string
		err := tx.QueryRowContext(ctx, `SELECT id,project_id,author FROM backend_replay_runs WHERE status='queued' ORDER BY created_at,id LIMIT 1`).Scan(&id, &pid, &author)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE backend_replay_runs SET status='running',version=version+1,updated_at=? WHERE id=? AND status='queued'`, nowReplay(), id); err != nil {
			return err
		}
		out, err = readRun(ctx, tx, pid, id)
		return err
	})
	return out, author, err
}
func (s *Service) worker(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		run, actor, err := s.claim(ctx)
		if err == nil && run != nil {
			err = s.execute(ctx, actor, run)
		}
		if err != nil && ctx.Err() == nil {
			s.mu.Lock()
			if s.runErr == nil {
				s.runErr = err
			}
			s.cancel()
			s.mu.Unlock()
			return
		}
		if run != nil && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) execute(app context.Context, actor string, run *Run) error {
	ctx, cancel := context.WithTimeout(app, time.Duration(ExecutionSeconds)*time.Second)
	defer cancel()
	s.mu.Lock()
	s.active[run.ID] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, run.ID); s.mu.Unlock() }()
	// A failed read here is this run's failure (nothing was dispatched yet),
	// closed unverified below; returning it stopped the service (F192). A
	// cancellation that already won keeps its report: finalize sees it.
	current, err := s.Get(ctx, run.ProjectID, run.ID)
	if err == nil && current.Status != "running" {
		return nil
	}
	report := initialReport(run.Input, run.Provenance)
	var target Target
	if err == nil {
		target, err = s.target(run.Input.Profile)
	}
	if err == nil {
		err = s.actor(ctx, actor)
	}
	if err == nil {
		err = authorizeProfile(ctx, s.repo.db.R, run.ProjectID, actor, run.Input.Profile)
	}
	executed := false
	if err == nil {
		executed = true
		report, err = (Engine{Transport: target.Transport}).Execute(ctx, run.Input, run.Provenance, Hooks{
			BeforeMutation: func(c context.Context, e p.Endpoint, f p.Fence, h string, b []byte) error {
				if err := s.actor(c, actor); err != nil {
					return err
				}
				if _, err := s.target(run.Input.Profile); err != nil {
					return err
				}
				return s.repo.beforeMutation(c, run.ProjectID, actor, run.Input, e, f, h, b)
			},
			Evidence: func(c context.Context, k string, b []byte) error {
				persist, release := context.WithTimeout(context.WithoutCancel(c), 5*time.Second)
				defer release()
				return s.repo.evidence(persist, run.ID, k, b)
			},
		})
	}
	if err != nil {
		report.Status = "unverified"
		// Engine.Execute and Check put the specific cause into the report
		// (a rejected mutation, an incomplete journal, a changed identity).
		// Overwriting it for every error (review 2026-10-06, F126) stored an
		// immutable terminal report that blamed evidence or authorization
		// whatever went wrong. The fixed text stays for failures before the
		// engine ran (target, actor, authorization), whose raw errors are
		// not meant for the report.
		if !executed || report.Reason == "" {
			report.Reason = "Replay evidence or authorization could not be verified"
		}
	}
	// Only a run the shutdown actually cut short is interrupted. A verdict the
	// engine already returned (err == nil) is complete: rewriting it to
	// interrupted (review 2026-10-06, F7/F129) lost a positive witness and,
	// with steps on record, fenced the target until someone acknowledged it.
	if app.Err() != nil && err != nil {
		report.Status = "interrupted"
		report.Reason = "Worker stopped; no automatic resume"
	}
	return s.finalize(app, run, report)
}

// finalize stores the terminal report under the rule the analysis worker uses
// (review 2026-10-06, F192, the sibling of F4): a failure the worker can pin on
// this run closes the run, and only a store that fails for a whole window stops
// the service. Returning every finalize error stopped the server, and startup
// recovery then overwrote the real receipts with an interrupted report.
func (s *Service) finalize(app context.Context, run *Run, report Report) error {
	if raw, err := marshalReplay(report); err == nil && len(raw) > p.ReportLimit {
		// Deterministic: every attempt would answer the same conflict. Close
		// the run unverified on its initial report; steps already dispatched
		// keep the lease uncertain, so the target stays fenced until the
		// author acknowledges it, and the run's evidence rows are kept.
		bounded := initialReport(run.Input, run.Provenance)
		bounded.Status = "unverified"
		if report.Status == "interrupted" {
			bounded.Status = "interrupted"
		}
		bounded.Reason = "Replay report exceeded the 4 MiB report limit; recorded evidence is kept"
		report = bounded
	}
	backoff := 50 * time.Millisecond
	for {
		persist, release := context.WithTimeout(context.WithoutCancel(app), s.persistTimeout)
		err := s.repo.finalize(persist, run, report)
		release()
		// A window that ends only on the deadline is a busy writer, not a
		// failing store: wait for it while the service runs. During shutdown
		// the run is left for startup recovery.
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || app.Err() != nil {
			return err
		}
		select {
		case <-app.Done():
			return err
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 2*time.Second)
	}
}
func (r *Repo) finalize(ctx context.Context, run *Run, report Report) error {
	raw, err := marshalReplay(report)
	if err != nil {
		return err
	}
	if len(raw) > p.ReportLimit {
		return conflictReplay("Report limit exceeded")
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM backend_replay_runs WHERE id=?`, run.ID).Scan(&status); err != nil {
			return err
		}
		if status != "running" {
			// Cancellation won. Preserve its terminal report and append late evidence.
			_, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_evidence(run_id,sequence,kind,content_hash,body) SELECT ?,COALESCE(max(sequence),0)+1,'late_terminal',?,? FROM backend_replay_evidence_documents WHERE run_id=?`, run.ID, p.HashBytes(raw), raw, run.ID)
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE backend_replay_runs SET status=?,terminal_report_json=?,version=version+1,updated_at=? WHERE id=? AND status='running'`, report.Status, string(raw), nowReplay(), run.ID); err != nil {
			return err
		}
		var steps int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_replay_steps_documents WHERE run_id=?`, run.ID).Scan(&steps); err != nil {
			return err
		}
		if report.Status == "succeeded" || report.Status == "failed" || steps == 0 {
			_, err := tx.ExecContext(ctx, `DELETE FROM backend_replay_target_leases WHERE run_id=?`, run.ID)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE backend_replay_target_leases SET state='uncertain' WHERE run_id=?`, run.ID)
		return err
	})
}
