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
	intents, err := s.dispatchIntents(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(intents) > 0 {
		if err = s.witnessIntents(ctx, target, run, id, intents); err != nil {
			return nil, err
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

// dispatchIntent is one mutation the run durably authorized before dispatch.
type dispatchIntent struct {
	endpoint p.Endpoint
	hash     string
	fence    p.Fence
}

// dispatchIntents reads the run's committed step rows in dispatch order.
func (s *Service) dispatchIntents(ctx context.Context, id string) ([]dispatchIntent, error) {
	rows, err := s.repo.db.R.QueryContext(ctx, `SELECT endpoint,request_hash,request_json FROM backend_replay_steps_documents WHERE run_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	intents := []dispatchIntent{}
	for rows.Next() {
		var endpoint p.Endpoint
		var hash string
		var raw []byte
		if err = rows.Scan(&endpoint, &hash, &raw); err != nil {
			return nil, err
		}
		fence, err := persistedFence(endpoint, raw)
		if err != nil {
			return nil, err
		}
		intents = append(intents, dispatchIntent{endpoint, hash, fence})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return intents, nil
}

// persistedFence decodes a persisted request as its endpoint's type and
// returns its fence.
func persistedFence(endpoint p.Endpoint, raw []byte) (p.Fence, error) {
	switch endpoint {
	case p.ResetEndpoint:
		var v p.ResetRequest
		err := p.Decode(raw, &v, p.BodyLimit)
		return v.Fence, err
	case p.FailureEndpoint:
		var v p.FailureRequest
		err := p.Decode(raw, &v, p.BodyLimit)
		return v.Fence, err
	case p.OrderEndpoint:
		var v p.OrderRequest
		err := p.Decode(raw, &v, p.BodyLimit)
		return v.Fence, err
	default:
		return p.Fence{}, errors.New("invalid persisted replay endpoint")
	}
}

// liveIdentity reads the target's identity, which must still be the run's.
func liveIdentity(ctx context.Context, target Target, run *Run) (p.IdentityResponse, error) {
	live, err := target.Transport.Identity(ctx)
	if err != nil || !live.Complete || live.Payload == nil || live.HTTPStatus != 200 {
		return p.IdentityResponse{}, conflictReplay("Live identity unverified")
	}
	if live.Payload.Validate() != nil || live.Payload.Identity != run.Input.Profile.Identity {
		return p.IdentityResponse{}, conflictReplay("Live identity changed")
	}
	return *live.Payload, nil
}

// witnessIntents requires the live journal, under an identity unchanged from
// before the read to after it, to hold a complete positive witness for every
// dispatched request. The journal response is kept as evidence either way.
func (s *Service) witnessIntents(ctx context.Context, target Target, run *Run, id string, intents []dispatchIntent) error {
	live, err := liveIdentity(ctx, target, run)
	if err != nil {
		return err
	}
	journal, err := target.Transport.Journal(ctx, id)
	// Retain the bounded wire response even when no positive witness exists.
	evidence, encodeErr := marshalReplay(struct {
		HTTPStatus int    `json:"httpStatus"`
		Complete   bool   `json:"complete"`
		Body       []byte `json:"body"`
	}{journal.HTTPStatus, journal.Complete, journal.Body})
	if encodeErr != nil {
		return encodeErr
	}
	if e := s.repo.evidence(ctx, id, "recovery_journal", evidence); e != nil {
		return e
	}
	if err != nil || !journal.Complete || journal.Payload == nil || journal.HTTPStatus != 200 {
		return conflictReplay("Journal outcome remains unknown")
	}
	j := *journal.Payload
	if j.Identity != run.Input.Profile.Identity || j.CurrentEpoch != live.Epoch {
		return conflictReplay("Journal live fence changed")
	}
	for _, v := range intents {
		if _, err = p.Reconcile(j, v.fence, v.endpoint, v.hash); err != nil {
			return conflictReplay("No complete positive witness for every dispatched request")
		}
	}
	final, err := target.Transport.Identity(ctx)
	if err != nil || !final.Complete || final.Payload == nil || final.HTTPStatus != 200 || final.Payload.Validate() != nil || *final.Payload != live {
		return conflictReplay("Identity changed during reconciliation")
	}
	return nil
}
