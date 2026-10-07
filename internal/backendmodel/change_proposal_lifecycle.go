package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
	"time"

	"github.com/yashok111/mocker/internal/backendblob"
)

func readyConflict(message string) error {
	return &FaultError{Status: 409, Code: "backend_change_ready_conflict", Message: message}
}
func requireReadyDraft(p *ChangeProposal, in ApplyChangeProposalLifecycleInput) error {
	if err := requireChangeCAS(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return err
	}
	if p.Status != "draft" {
		return &FaultError{Status: 409, Code: "backend_change_status_conflict", Message: "Only a draft may become ready"}
	}
	return nil
}

// ApplyChangeProposalLifecycle binds a saved completed report without creating a
// structural revision. Preparation is read-only; receipt and CAS are repeated at
// the transaction boundary so concurrent edits cannot make evidence applicable.
func (r *Repo) applyChangeReady(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, reader AnalysisGateReader) (*ChangeProposalApplyResult, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "change-proposal-lifecycle:" + pid + ":" + id
	out := new(ChangeProposalApplyResult)
	if found, err := readChangeReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	prepared, prepareErr := r.prepareChangeReady(ctx, pid, id, in, reader)
	if prepared != nil {
		defer prepared.lease.Release()
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := readChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
			return err
		}
		p, err := loadChangeProposal(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		if err = requireReadyDraft(p, in); err != nil {
			return err
		}
		if prepareErr != nil {
			return prepareErr
		}
		rev := prepared.revision
		if p.CurrentDraftHash != rev.SemanticHash {
			return readyConflict("Selected immutable draft hash differs")
		}
		p.Status = "ready"
		p.Version++
		p.UpdatedAt = time.Now().UTC()
		gaps := slices.Clone(in.AcknowledgedGapIDs)
		slices.Sort(gaps)
		p.ReadyReference = &ChangeProposalReadyReference{Report: in.Report, ProposalRevisionID: rev.ID, DraftHash: rev.SemanticHash, AcknowledgedGapIDs: gaps}
		association, err := json.Marshal(p.ReadyReference)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE backend_change_proposals SET status='ready',ready_reference=?,version=?,updated_at=? WHERE project_id=? AND id=?`, string(association), p.Version, p.UpdatedAt.Format(time.RFC3339Nano), pid, id); err != nil {
			return err
		}
		event, err := json.Marshal(struct {
			Action         string                        `json:"action"`
			RevisionID     string                        `json:"revisionId"`
			CreatedAt      time.Time                     `json:"createdAt"`
			ReadyReference *ChangeProposalReadyReference `json:"readyReference"`
		}{"ready", rev.ID, p.UpdatedAt, p.ReadyReference})
		if err != nil {
			return err
		}
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_change_proposal_events(project_id,proposal_id,revision_id,version,document) VALUES(?,?,?,?,?)`, pid, id, rev.ID, p.Version, string(event)); err != nil {
			return err
		}
		*out = ChangeProposalApplyResult{Proposal: *p, Revision: *rev, Changes: []ChangeProposalChange{}, SemanticHash: rev.SemanticHash}
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
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type preparedChangeReady struct {
	revision *ChangeProposalRevision
	lease    *AnalysisInputReservation
}

func (r *Repo) prepareChangeReady(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, reader AnalysisGateReader) (*preparedChangeReady, error) {
	if in.Action != "ready" {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Only ready is supported"}
	}
	if in.ExpectedVersion < 1 || !ValidID(in.ProposalRevisionID) || !ValidID(in.Report.JobID) || in.Report.ResultVersion < 1 || !validHash(in.Report.InputHash) || !validHash(in.Report.ResultHash) {
		return nil, invalid("lifecycle", "Invalid exact lifecycle input")
	}
	p, err := loadChangeProposal(ctx, r.db.R, pid, id)
	if err != nil {
		return nil, err
	}
	if err = requireReadyDraft(p, in); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, readyConflict("Analysis reader is required")
	}
	e, err := reader.ReadAnalysisGate(ctx, pid, in.Report)
	if err != nil {
		return nil, err
	}
	if err = validateReadyEvidence(pid, id, in, e); err != nil {
		return nil, err
	}
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: id, ProposalRevisionID: in.ProposalRevisionID}}
	footprint, err := r.EffectiveGraphInputFootprint(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	leased, lease, err := r.ReserveAnalysisInput(ctx, pid, footprint)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			lease.Release()
		}
	}()
	g, err := r.ResolveEffectiveGraph(leased, pid, target)
	if err != nil {
		return nil, err
	}
	rev, err := loadChangeProposalRevision(ctx, r.db.R, pid, id, in.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	if err = validateReadyPins(e, rev, g); err != nil {
		return nil, err
	}
	retained = true
	return &preparedChangeReady{revision: rev, lease: lease}, nil
}

func validateReadyPins(e *AnalysisGateEvidence, rev *ChangeProposalRevision, g *EffectiveGraphSnapshot) error {
	source := AnalysisSourcePins{RevisionID: g.Pins.BaseRevisionID, SemanticHash: g.Pins.BaseSemanticHash, SourceVectorHash: g.Pins.SourceVectorHash, SourceSnapshotIDs: g.Pins.SourceSnapshotIDs}
	if g.Source != nil {
		source.ContentHash = g.Source.SourceContentHash
	}
	sourceHash, err := requestDigest(source)
	if err != nil {
		return err
	}
	evidenceSource, err := requestDigest(e.SourcePins)
	if err != nil {
		return err
	}
	artifacts, err := requestDigest(g.Pins.ArtifactPins)
	if err != nil {
		return err
	}
	evidenceArtifacts, err := requestDigest(e.ArtifactPins)
	if err != nil {
		return err
	}
	if e.FromRevisionID != rev.BaseRevisionID || e.BaseRevisionID != rev.BaseRevisionID || e.BaseSemanticHash != rev.BaseSemanticHash || e.DraftHash != rev.SemanticHash || e.EffectiveSemanticHash != g.Pins.EffectiveSemanticHash || e.TargetHash != g.Pins.TargetHash || sourceHash != evidenceSource || artifacts != evidenceArtifacts {
		return readyConflict("Report does not describe the exact saved draft and source/artifact pins")
	}
	return nil
}
func validateReadyEvidence(pid, id string, in ApplyChangeProposalLifecycleInput, e *AnalysisGateEvidence) error {
	if e == nil || e.ProjectID != pid || e.Report != in.Report || e.Kind != "impact" || e.Status != "completed" || e.CommandPreview || e.ChangeProposal.ProposalID != id || e.ChangeProposal.ProposalRevisionID != in.ProposalRevisionID || !e.Complete || len(e.TruncationReasons) > 0 || e.RuleSetVersion == "" || e.TraversalVersion == "" {
		return readyConflict("Exact completed saved-draft impact evidence is required")
	}
	covered := make(map[ChangeRecordRef]bool, len(e.CoveredChangedIDs))
	for _, a := range e.CoveredChangedIDs {
		covered[a] = true
	}
	for _, a := range e.ChangedIDs {
		if !covered[a] {
			return readyConflict("Report does not cover every changed address")
		}
	}
	gaps := slices.Clone(e.GapIDs)
	slices.Sort(gaps)
	gaps = slices.Compact(gaps)
	ack := slices.Clone(in.AcknowledgedGapIDs)
	slices.Sort(ack)
	if len(slices.Compact(slices.Clone(ack))) != len(ack) || !slices.Equal(gaps, ack) {
		return readyConflict("Acknowledge the complete unique documented gap set")
	}
	return nil
}

func (r *Repo) ApplyChangeProposalLifecycle(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, reader AnalysisGateReader) (*ChangeProposalApplyResult, error) {
	if in.Action != "ready" {
		conformance, _ := reader.(ConformanceEvidenceReader)
		return r.applyChangeLifecycleB43(ctx, pid, id, in, conformance)
	}
	return r.applyChangeReady(ctx, pid, id, in, reader)
}
