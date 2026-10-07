package backendreplay

import (
	"context"
	"database/sql"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"
	"time"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func (s *Service) Start(ctx context.Context, pid, actor string, in StartInput) (*Run, error) {
	if err := s.actor(ctx, actor); err != nil {
		return nil, err
	}
	if in.Validate() != nil {
		return nil, invalidReplay()
	}
	hash, err := replayHash(struct {
		Actor string
		Input StartInput
	}{actor, in})
	if err != nil {
		return nil, err
	}
	var out *Run
	err = s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		var prior Run
		if found, err := receiptRead(ctx, tx, pid, "start", in.IdempotencyKey, hash, &prior); err != nil || found {
			out = &prior
			return err
		}
		pkg, err := readPackage(ctx, tx, pid, in.Package.ID, in.Package.Version)
		if err != nil {
			return err
		}
		profile, err := readProfile(ctx, tx, pid, in.Profile.ID, in.Profile.Version)
		if err != nil {
			return err
		}
		if pkg.Pin != in.Package || profile.Pin != in.Profile || pkg.Package.Profile != in.Profile || profile.IdentityHash != in.ExpectedIdentityHash || profile.Authorization.ID != in.ResetAuthorizationID || profile.Authorization.Version != in.ResetAuthorizationVersion {
			return conflictReplay("Start requires exact package, profile and authorization pins")
		}
		if _, err = s.target(*profile); err != nil {
			return err
		}
		if err = authorizeProfile(ctx, tx, pid, actor, *profile); err != nil {
			return err
		}
		var previous, state string
		err = tx.QueryRowContext(ctx, `SELECT run_id,state FROM backend_replay_target_leases WHERE target_id=?`, profile.TargetID).Scan(&previous, &state)
		if err == nil {
			if previous != in.AcknowledgedPreviousRunID || state != "uncertain" {
				return conflictReplay("Target is leased; uncertain previous run needs explicit acknowledgment")
			}
			var oldStatus, oldActor, oldPID string
			if err = tx.QueryRowContext(ctx, `SELECT status,author,project_id FROM backend_replay_runs WHERE id=?`, previous).Scan(&oldStatus, &oldActor, &oldPID); err != nil {
				return err
			}
			if oldStatus == "queued" || oldStatus == "running" || oldActor != actor || oldPID != pid {
				return conflictReplay("Cannot acknowledge active or foreign run")
			}
			// Local worker must finish draining before the old lease can be acknowledged.
			s.mu.Lock()
			_, active := s.active[previous]
			s.mu.Unlock()
			if active {
				return conflictReplay("Previous dispatch is still draining")
			}
			if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_evidence(run_id,sequence,kind,content_hash,body) SELECT ?,COALESCE(max(sequence),0)+1,'uncertainty_acknowledged',?,? FROM backend_replay_evidence_documents WHERE run_id=?`, previous, p.HashBytes([]byte(in.IdempotencyKey)), []byte(in.IdempotencyKey), previous); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM backend_replay_target_leases WHERE target_id=? AND run_id=?`, profile.TargetID, previous); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else if in.AcknowledgedPreviousRunID != "" {
			return conflictReplay("Acknowledged run does not hold this target")
		}
		var queued int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_replay_runs WHERE status='queued'`).Scan(&queued); err != nil {
			return err
		}
		if queued >= QueueLimit {
			return replayFault(409, "queue_full", "Replay queue is full")
		}
		runID := newReplayID()
		input := RunInput{RunID: runID, Start: in, Package: pkg.Package, Profile: *profile, BusinessKey: newReplayID(), Requests: []p.Fence{}}
		for i := range 4 {
			input.Requests = append(input.Requests, p.Fence{RunID: runID, StepID: input.Package.Steps[i].ID, RequestKey: newReplayID(), IdentityHash: profile.IdentityHash})
		}
		if err = validateRun(input, pkg.Provenance); err != nil {
			return err
		}
		frozen := struct {
			Input      RunInput   `json:"input"`
			Provenance Provenance `json:"provenance"`
		}{input, pkg.Provenance}
		raw, err := marshalReplay(frozen)
		if err != nil {
			return err
		}
		if len(raw) > p.ReportLimit {
			return invalidReplay()
		}
		now := time.Now().UTC()
		out = &Run{ID: runID, ProjectID: pid, Status: "queued", Input: input, Provenance: pkg.Provenance, CreatedAt: now, UpdatedAt: now}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_replay_runs(id,project_id,target_id,status,input_hash,input_json,version,author,created_at,updated_at) VALUES(?,?,?,'queued',?,?,1,?,?,?)`, runID, pid, profile.TargetID, p.HashBytes(raw), string(raw), actor, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_replay_target_leases VALUES(?,?,'active')`, profile.TargetID, runID); err != nil {
			return err
		}
		return receiptWrite(ctx, tx, pid, "start", in.IdempotencyKey, hash, out)
	})
	return out, err
}
func (s *Service) Get(ctx context.Context, pid, id string) (*Run, error) {
	return readRun(ctx, s.repo.db.R, pid, id)
}

// runPageSize bounds one page of Runs (a var only so tests can page small).
var runPageSize = 100

// Runs lists a project's runs newest first, one page at a time. Replay runs
// are never deleted, and the list used to answer 409 for good once a project
// passed 100 of them (review 2026-10-06, F123/F9), which broke list-based
// polling and the only way to rediscover a run after a lost Start receipt.
// cursor is the id of the last run of the previous page ("" for the first);
// a page shorter than runPageSize (100) is the last. The response stays a plain
// array, so a request without a cursor is valid exactly as before.
func (s *Service) Runs(ctx context.Context, pid, cursor string) ([]Run, error) {
	if err := s.project(ctx, pid); err != nil {
		return nil, err
	}
	var afterCreated, afterID string
	if cursor != "" {
		if !p.ValidID(cursor) {
			return nil, invalidReplay()
		}
		err := s.repo.db.R.QueryRowContext(ctx, `SELECT created_at,id FROM backend_replay_runs WHERE project_id=? AND id=?`, pid, cursor).Scan(&afterCreated, &afterID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidReplay()
		}
		if err != nil {
			return nil, err
		}
	}
	out := []Run{}
	rows, err := s.repo.db.R.QueryContext(ctx, `SELECT id FROM backend_replay_runs WHERE project_id=? AND (?='' OR created_at<? OR (created_at=? AND id>?)) ORDER BY created_at DESC,id LIMIT ?`, pid, afterID, afterCreated, afterCreated, afterID, runPageSize)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		v, err := s.Get(ctx, pid, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}
func (s *Service) Cancel(ctx context.Context, pid, actor, id string) (*Run, error) {
	if err := s.actor(ctx, actor); err != nil {
		return nil, err
	}
	var out *Run
	err := s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = readRun(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		var author string
		if err = tx.QueryRowContext(ctx, `SELECT author FROM backend_replay_runs WHERE id=?`, id).Scan(&author); err != nil {
			return err
		}
		if author != actor {
			return replayFault(403, "forbidden", "Only run author may cancel")
		}
		if out.Status != "queued" && out.Status != "running" {
			return nil
		}
		// A queued run has no step row either, so one rule covers both.
		if err = fenceIfDispatched(ctx, tx, id); err != nil {
			return err
		}
		report := initialReport(out.Input, out.Provenance)
		report.Status = "cancelled"
		report.Reason = "Cancelled explicitly; in-flight effects may be unknown"
		raw, err := marshalReplay(report)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE backend_replay_runs SET status='cancelled',version=version+1,updated_at=?,terminal_report_json=? WHERE id=?`, nowReplay(), string(raw), id); err != nil {
			return err
		}
		out, err = readRun(ctx, tx, pid, id)
		return err
	})
	if err == nil {
		s.mu.Lock()
		cancel := s.active[id]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return out, err
}
func (s *Service) Compare(ctx context.Context, pid, leftID, rightID string) (*Comparison, error) {
	left, err := s.Get(ctx, pid, leftID)
	if err != nil {
		return nil, err
	}
	right, err := s.Get(ctx, pid, rightID)
	if err != nil {
		return nil, err
	}
	if left.Report == nil || right.Report == nil {
		return nil, conflictReplay("Both runs need terminal reports")
	}
	out, err := Compare(left.Input, *left.Report, right.Input, *right.Report)
	if err != nil {
		return nil, conflictReplay(err.Error())
	}
	return &out, nil
}
