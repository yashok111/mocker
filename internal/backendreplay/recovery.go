package backendreplay

import (
	"context"
	"database/sql"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

// Reconcile appends observations to a terminal run. It never resumes the run,
// sends mutations, or rewrites the original terminal status/report.
func (s *Service) Reconcile(ctx context.Context, pid, actor, id string) (*Run, error) {
	if err := s.actor(ctx, actor); err != nil {
		return nil, err
	}
	run, err := s.Get(ctx, pid, id)
	if err != nil {
		return nil, err
	}
	if run.Status == "queued" || run.Status == "running" {
		return nil, conflictReplay("Only terminal runs can be reconciled")
	}
	var author string
	if err = s.repo.db.R.QueryRowContext(ctx, `SELECT author FROM backend_replay_runs WHERE project_id=? AND id=?`, pid, id).Scan(&author); err != nil {
		return nil, err
	}
	if author != actor {
		return nil, replayFault(403, "forbidden", "Only run author may reconcile")
	}
	s.mu.Lock()
	_, active := s.active[id]
	s.mu.Unlock()
	if active {
		return nil, conflictReplay("Dispatch is still draining")
	}
	target, err := s.target(run.Input.Profile)
	if err != nil {
		return nil, err
	}
	// Every mutation has a committed step row before dispatch. No step rows is
	// positive local evidence that this process never authorized a mutation.
	rows, err := s.repo.db.R.QueryContext(ctx, `SELECT endpoint,request_hash,request_json FROM backend_replay_steps_documents WHERE run_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	type intent struct {
		endpoint p.Endpoint
		hash     string
		fence    p.Fence
	}
	intents := []intent{}
	for rows.Next() {
		var endpoint p.Endpoint
		var hash string
		var raw []byte
		if err = rows.Scan(&endpoint, &hash, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var fence p.Fence
		switch endpoint {
		case p.ResetEndpoint:
			var v p.ResetRequest
			err = p.Decode(raw, &v, p.BodyLimit)
			fence = v.Fence
		case p.FailureEndpoint:
			var v p.FailureRequest
			err = p.Decode(raw, &v, p.BodyLimit)
			fence = v.Fence
		case p.OrderEndpoint:
			var v p.OrderRequest
			err = p.Decode(raw, &v, p.BodyLimit)
			fence = v.Fence
		default:
			err = errors.New("invalid persisted replay endpoint")
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		intents = append(intents, intent{endpoint, hash, fence})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(intents) > 0 {
		live, err := target.Transport.Identity(ctx)
		if err != nil || !live.Complete || live.Payload == nil || live.HTTPStatus != 200 {
			return nil, conflictReplay("Live identity unverified")
		}
		if live.Payload.Validate() != nil || live.Payload.Identity != run.Input.Profile.Identity {
			return nil, conflictReplay("Live identity changed")
		}
		journal, err := target.Transport.Journal(ctx, id)
		// Retain the bounded wire response even when no positive witness exists.
		evidence, encodeErr := marshalReplay(struct {
			HTTPStatus int    `json:"httpStatus"`
			Complete   bool   `json:"complete"`
			Body       []byte `json:"body"`
		}{journal.HTTPStatus, journal.Complete, journal.Body})
		if encodeErr != nil {
			return nil, encodeErr
		}
		if e := s.repo.evidence(ctx, id, "recovery_journal", evidence); e != nil {
			return nil, e
		}
		if err != nil || !journal.Complete || journal.Payload == nil || journal.HTTPStatus != 200 {
			return nil, conflictReplay("Journal outcome remains unknown")
		}
		j := *journal.Payload
		if j.Identity != run.Input.Profile.Identity || j.CurrentEpoch != live.Payload.Epoch {
			return nil, conflictReplay("Journal live fence changed")
		}
		for _, v := range intents {
			if _, err = p.Reconcile(j, v.fence, v.endpoint, v.hash); err != nil {
				return nil, conflictReplay("No complete positive witness for every dispatched request")
			}
		}
		final, err := target.Transport.Identity(ctx)
		if err != nil || !final.Complete || final.Payload == nil || final.HTTPStatus != 200 || final.Payload.Validate() != nil || *final.Payload != *live.Payload {
			return nil, conflictReplay("Identity changed during reconciliation")
		}
	}
	err = s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM backend_replay_target_leases WHERE target_id=? AND run_id=?`, run.Input.Profile.TargetID, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state != "uncertain" {
			return conflictReplay("Lease is not reconcilable")
		}
		raw := []byte(`{"positiveWitness":true}`)
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_evidence(run_id,sequence,kind,content_hash,body) SELECT ?,COALESCE(max(sequence),0)+1,'reconciled',?,? FROM backend_replay_evidence_documents WHERE run_id=?`, id, p.HashBytes(raw), raw, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM backend_replay_target_leases WHERE target_id=? AND run_id=? AND state='uncertain'`, run.Input.Profile.TargetID, id)
		return err
	})
	return run, err
}
