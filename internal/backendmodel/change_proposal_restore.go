package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"time"
	"uuid"
)

func (r *Repo) RestoreChangeProposal(ctx context.Context, pid, id string, in RestoreChangeProposalInput) (*ChangeProposalApplyResult, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "change-proposal-restore:" + pid + ":" + id
	out := new(ChangeProposalApplyResult)
	if found, err := readChangeReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	reservation, prepareErr := r.reserveChangeRestore(ctx, pid, id, in)
	if reservation != nil {
		defer reservation.Release()
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		return r.restoreChangeTx(ctx, tx, changeRestoreWrite{pid: pid, id: id, scope: scope, digest: digest, in: in, prepareErr: prepareErr, out: out})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type changeRestoreWrite struct {
	pid, id, scope, digest string
	in                     RestoreChangeProposalInput
	prepareErr             error
	out                    *ChangeProposalApplyResult
}

func (r *Repo) restoreChangeTx(ctx context.Context, tx *sql.Tx, w changeRestoreWrite) error {
	pid, id, scope, digest, in, prepareErr, out := w.pid, w.id, w.scope, w.digest, w.in, w.prepareErr, w.out
	if found, err := readChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return err
	}
	if in.ExpectedVersion < 1 {
		return invalid("expectedVersion", "Expected a positive exact int64")
	}
	if prepareErr != nil {
		return prepareErr
	}
	p, err := loadChangeProposal(ctx, tx, pid, id)
	if err != nil {
		return err
	}
	if err = requireChangeDraft(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return err
	}
	current, err := loadChangeProposalRevision(ctx, tx, pid, id, in.ProposalRevisionID)
	if err != nil {
		return err
	}
	old, err := loadChangeProposalRevision(ctx, tx, pid, id, in.RestoreRevisionID)
	if err != nil {
		return err
	}
	a, err := requestDigest(current.SourceVector)
	if err != nil {
		return err
	}
	b, err := requestDigest(old.SourceVector)
	if err != nil {
		return err
	}
	if current.BaseRevisionID != old.BaseRevisionID || current.BaseSemanticHash != old.BaseSemanticHash || current.BaseSchemaVersion != old.BaseSchemaVersion || a != b {
		return &FaultError{Status: 409, Code: "backend_change_base_conflict", Message: "Restore requires the exact current baseline and source vector"}
	}
	now := time.Now().UTC()
	rev := *old
	rev.ID = uuid.NewV7().String()
	rev.ParentRevisionID = new(current.ID)
	rev.AcceptedBatchRevisionID = rev.ID
	rev.CreatedAt = now
	rev.Author = "user"
	rev.Summary = "Restore historical desired graph"
	p.Version++
	p.Status = "draft"
	p.ReadyReference = nil
	p.CurrentDraftRevisionID, p.CurrentDraftHash, p.UpdatedAt = rev.ID, rev.SemanticHash, now
	if err = persistChangeRevision(ctx, tx, *p, rev, "restore", old.ID, []ChangeProposalCommand{}, nil); err != nil {
		return err
	}
	if err = updateChangeAggregate(ctx, tx, *p); err != nil {
		return err
	}
	*out = ChangeProposalApplyResult{Proposal: *p, Revision: rev, Changes: []ChangeProposalChange{}, SemanticHash: rev.SemanticHash}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err = writeChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, string(raw)); err != nil {
		return err
	}
	if err = checkChangeProposalQuota(ctx, tx, pid, id); err != nil {
		return err
	}
	out.receiptJSON = string(raw)
	return nil
}
