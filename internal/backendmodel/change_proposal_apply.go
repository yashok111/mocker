package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

type preparedChangeProposal struct {
	input       PreviewChangeProposalInput
	proposal    ChangeProposal
	draft       ChangeProposalRevision
	evaluation  *changeEvaluation
	candidate   *ChangeProposalCandidate
	reservation *store.TransientReservation
	// revisionBytes is the encoded desired revision, sized for admit.
	revisionBytes int64
}

func loadChangeIdentities(ctx context.Context, q importReader, id string) (map[string]ChangeObjectIdentity, error) {
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_change_proposal_identities_documents WHERE proposal_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]ChangeObjectIdentity{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var identity ChangeObjectIdentity
		if err = json.Unmarshal([]byte(raw), &identity); err != nil {
			return nil, err
		}
		out[identity.ID] = identity
	}
	return out, rows.Err()
}
func checkChangeCommandHistory(ctx context.Context, q importReader, id string, commands []ChangeProposalCommand) error {
	for _, c := range commands {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM backend_change_proposal_commands_documents WHERE proposal_id=? AND command_id=?`, id, c.CommandID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return &FaultError{Status: 409, Code: "backend_change_command_conflict", Message: "Command ID is permanently reserved by an accepted batch", Details: map[string]any{"commandId": c.CommandID}}
		}
	}
	return nil
}

func (r *Repo) prepareChangeProposal(ctx context.Context, pid, id string, in PreviewChangeProposalInput) (*preparedChangeProposal, error) {
	if in.ExpectedVersion < 1 {
		return nil, invalid("expectedVersion", "Expected positive int64")
	}
	if err := validateChangeCommands(in.Commands); err != nil {
		return nil, err
	}
	if err := r.validateAnalysisChangePreparation(ctx, pid, id, in); err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := loadChangeProposal(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	if err = requireChangeDraft(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return nil, err
	}
	var baseRevisionID string
	var draftBytes int64
	if err = tx.QueryRowContext(ctx, `SELECT base_revision_id,length(CAST(document AS BLOB)) FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=?`, pid, id, in.ProposalRevisionID).Scan(&baseRevisionID, &draftBytes); err != nil {
		return nil, err
	}
	if err = checkChangeCommandHistory(ctx, tx, id, in.Commands); err != nil {
		return nil, err
	}
	inputBytes, err := changeSourceInputBytes(ctx, tx, pid, baseRevisionID)
	if err != nil {
		return nil, err
	}
	ledgerBytes, err := changeIdentityInputBytes(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	historicalBytes, err := changeHistoricalIdentityBytes(ctx, tx, pid, id, in.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	inputBytes += draftBytes + ledgerBytes + historicalBytes
	// The reservation is admitted under the single writer. Waiting there with
	// this reader open closed a reader/writer cycle with any writer holder that
	// needs a reader once the pool was full of such waiters (review 2026-10-06,
	// F3), so the reader is released first and the loads below run on a fresh
	// snapshot that re-checks the same proposal version: every mutation of the
	// proposal bumps it, so the sizes measured above still describe what is read.
	_ = tx.Rollback()
	reservation, err := r.reserveChangeInput(ctx, pid, inputBytes)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained && analysisLease(ctx) == nil {
			reservation.Release()
		}
	}()
	if tx, err = r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}); err != nil {
		return nil, err
	}
	if p, err = loadChangeProposal(ctx, tx, pid, id); err != nil {
		return nil, err
	}
	if err = requireChangeDraft(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return nil, err
	}
	draft, err := loadChangeProposalRevision(ctx, tx, pid, id, in.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	source, err := loadChangeSourceForDraft(ctx, tx, pid, draft)
	if err != nil {
		return nil, err
	}

	used, err := loadChangeReservedIdentities(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	evaluation, err := newChangeEvaluation(source, *draft, used)
	if err != nil {
		return nil, err
	}
	evaluation.readBudget = &changeReadBudget{repo: r, pid: pid, reservation: reservation, bytes: inputBytes, seen: map[string]bool{"source:" + draft.BaseRevisionID: true}, lease: analysisLease(ctx)}
	prepared := &preparedChangeProposal{proposal: *p, draft: *draft, evaluation: evaluation, reservation: reservation, input: in}
	// Release the reader before evaluation. Evaluation reads owner artifacts
	// through fresh reader connections and admits each into the read budget
	// under the single writer; holding this snapshot across both let
	// pool-width concurrent previews each wait for a second reader, and closed
	// a reader/writer cycle with any writer holder that needs a reader (review
	// 2026-10-06, F3). What evaluation still reads (source ancestry, historical
	// revisions, analyzed files of the exact owned snapshot) is immutable, so
	// it reads the pool directly; the proposal version is re-checked under the
	// writer at apply.
	_ = tx.Rollback()
	if err = r.evaluateChangeDraft(ctx, r.db.R, pid, prepared); err != nil {
		return nil, err
	}
	if analysisLease(ctx) == nil {
		if err = r.admitPreparedChange(ctx, pid, prepared); err != nil {
			return nil, err
		}
	}
	retained = true
	return prepared, nil
}

// admitPreparedChange resizes the transient reservation to the measured
// candidate against the project's staged bytes, under the writer.
func (r *Repo) admitPreparedChange(ctx context.Context, pid string, prepared *preparedChangeProposal) error {
	return r.db.Write(ctx, func(writer *sql.Tx) error {
		staged, err := stagingBytes(ctx, writer, pid)
		if err != nil {
			return err
		}
		if !prepared.reservation.Resize(4*prepared.evaluation.readBudget.bytes+2*prepared.revisionBytes+MaxChangeProposalCommandBytes, MaxProjectStagingBytes-staged) {
			return limitFault("Prepared proposal exceeds transient memory budget")
		}
		return nil
	})
}
func loadChangeSourceForDraft(ctx context.Context, tx importReader, pid string, draft *ChangeProposalRevision) (*SourceGraphSnapshot, error) {
	source, err := loadComposedBase(ctx, tx, pid, draft.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	if source.State.Revision.SemanticHash != draft.BaseSemanticHash || source.State.Revision.SchemaVersion != draft.BaseSchemaVersion {
		return nil, invalid("baseline", "Immutable source baseline does not match draft")
	}
	actualVector, err := requestDigest(source.SourceVector)
	if err != nil {
		return nil, err
	}
	pinnedVector, err := requestDigest(draft.SourceVector)
	if err != nil {
		return nil, err
	}
	if actualVector != pinnedVector {
		return nil, invalid("sourceVector", "Draft vector differs from exact baseline")
	}
	return source, nil
}
func (r *Repo) evaluateChangeDraft(ctx context.Context, tx importReader, pid string, prepared *preparedChangeProposal) error {
	var err error
	p, draft, evaluation := &prepared.proposal, &prepared.draft, prepared.evaluation
	in := prepared.input
	for _, c := range in.Commands {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = evaluation.apply(c); err != nil {
			return err
		}
	}
	if err = r.prepareChangeArtifacts(ctx, evaluation, in.Commands); err != nil {
		return err
	}
	snapshot, diagnostics, err := evaluation.validate(ctx)
	if err != nil {
		return err
	}
	if err = validateChangeHistoricalReferences(ctx, tx, pid, evaluation); err != nil {
		diagnostics = append(diagnostics, ImportDiagnostic{Code: "backend_change_invalid", Path: "historicalReferences", Message: err.Error()})
	}
	if err = r.validateChangeCriteriaReferences(ctx, tx, pid, evaluation); err != nil {
		diagnostics = append(diagnostics, ImportDiagnostic{Code: "backend_change_invalid", Path: "criteria", Message: err.Error()})
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	candidate := &ChangeProposalCandidate{ProposalID: p.ID, ExpectedVersion: p.Version, ProposalRevisionID: draft.ID, DraftHash: draft.SemanticHash, BaseRevisionID: draft.BaseRevisionID, BaseSemanticHash: draft.BaseSemanticHash, DocumentVersion: ChangeProposalDocumentVersion, Changes: evaluation.changes, Criteria: evaluation.revision.Criteria, Diagnostics: diagnostics}
	if len(diagnostics) == 0 {
		semantic, err := changeSemanticHash(snapshot)
		if err != nil {
			return err
		}
		candidate.SemanticHash = new(semantic)
		hash, err := changeCandidateHash(*p, *draft, in, semantic)
		if err != nil {
			return err
		}
		candidate.CandidateHash = new(hash)
		evaluation.revision.SemanticHash = semantic
	}
	raw, err := json.Marshal(evaluation.revision)
	if err != nil {
		return err
	}
	if len(raw) > MaxRevisionBytes {
		return limitFault("Proposal draft exceeds materialization bounds")
	}
	if lease := analysisLease(ctx); lease != nil {
		if err = lease.reconcilePrepared(ctx, int64(len(raw))); err != nil {
			return err
		}
	}
	// Without a lease the caller admits the measured size under the writer
	// after releasing its reader (admitPreparedChange).
	prepared.revisionBytes = int64(len(raw))
	prepared.candidate = candidate
	return nil
}

func (r *Repo) PreviewChangeProposal(ctx context.Context, pid, id string, in PreviewChangeProposalInput) (*ChangeProposalCandidate, error) {
	prepared, err := r.prepareChangeProposal(ctx, pid, id, in)
	if err != nil {
		return nil, err
	}
	defer func() {
		if analysisLease(ctx) == nil {
			prepared.reservation.Release()
		}
	}()
	return prepared.candidate, nil
}
func (r *Repo) ApplyChangeProposal(ctx context.Context, pid, id string, in ApplyChangeProposalInput) (*ChangeProposalApplyResult, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "change-proposal-apply:" + pid + ":" + id
	out := new(ChangeProposalApplyResult)
	if found, err := readChangeReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	prepared, prepareErr := r.prepareChangeProposal(ctx, pid, id, PreviewChangeProposalInput{ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: in.ProposalRevisionID, Commands: in.Commands})
	if prepared != nil {
		defer func() {
			if analysisLease(ctx) == nil {
				prepared.reservation.Release()
			}
		}()
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		return r.applyChangeTx(ctx, tx, changeApplyWrite{pid: pid, id: id, scope: scope, digest: digest, in: in, prepared: prepared, prepareErr: prepareErr, out: out})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func updateChangeAggregate(ctx context.Context, tx *sql.Tx, p ChangeProposal) error {
	_, err := tx.ExecContext(ctx, `UPDATE backend_change_proposals SET status='draft',ready_reference=NULL,version=?,current_draft_revision_id=?,current_draft_hash=?,updated_at=? WHERE project_id=? AND id=?`, p.Version, p.CurrentDraftRevisionID, p.CurrentDraftHash, p.UpdatedAt.Format(time.RFC3339Nano), p.ProjectID, p.ID)
	return err
}

type changeApplyWrite struct {
	pid, id, scope, digest string
	in                     ApplyChangeProposalInput
	prepared               *preparedChangeProposal
	prepareErr             error
	out                    *ChangeProposalApplyResult
}

func (r *Repo) applyChangeTx(ctx context.Context, tx *sql.Tx, w changeApplyWrite) error {
	pid, id, scope, digest, in, prepared, prepareErr, out := w.pid, w.id, w.scope, w.digest, w.in, w.prepared, w.prepareErr, w.out
	if found, err := readChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return err
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
	if err = checkChangeCommandHistory(ctx, tx, id, in.Commands); err != nil {
		return err
	}
	if prepared.candidate.CandidateHash == nil {
		return changeInvalid(prepared.candidate.Diagnostics)
	}
	if in.CandidateHash != *prepared.candidate.CandidateHash {
		return &FaultError{Status: 409, Code: "backend_change_preview_conflict", Message: "Candidate hash differs from the exact preview input"}
	}
	if err = prepared.evaluation.checkArtifactDigests(ctx, tx, p.Version); err != nil {
		return err
	}
	now := time.Now().UTC()
	rev := prepared.evaluation.revision
	rev.ParentRevisionID = new(prepared.draft.ID)
	rev.ID = uuid.NewV7().String()
	rev.AcceptedBatchRevisionID = rev.ID
	rev.CreatedAt = now
	rev.Author = "user"
	rev.Summary = "Apply desired graph commands"
	p.Version++
	p.Status = "draft"
	p.ReadyReference = nil
	p.CurrentDraftRevisionID, p.CurrentDraftHash, p.UpdatedAt = rev.ID, rev.SemanticHash, now
	if err = persistChangeRevision(ctx, tx, *p, rev, "apply", "", in.Commands, prepared.evaluation.newIDs); err != nil {
		return err
	}
	if err = updateChangeAggregate(ctx, tx, *p); err != nil {
		return err
	}
	*out = ChangeProposalApplyResult{Proposal: *p, Revision: rev, Changes: prepared.candidate.Changes, SemanticHash: rev.SemanticHash}
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
