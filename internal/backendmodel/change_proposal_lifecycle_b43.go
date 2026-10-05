package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
	"strings"
	"time"
)

func lifecycleConflict(message string) error {
	return &FaultError{Status: 409, Code: "backend_change_conformance_conflict", Message: message}
}
func requireB43Lifecycle(p *ChangeProposal, in ApplyChangeProposalLifecycleInput) error {
	if err := requireChangeCAS(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return err
	}
	var allowed bool
	switch in.Action {
	case "implemented":
		allowed = p.Status == "ready"
	case "archive":
		allowed = slices.Contains([]string{"draft", "ready", "implemented"}, p.Status)
	case "unarchive":
		allowed = p.Status == "archived"
	default:
		return invalid("action", "Unsupported lifecycle action")
	}
	if !allowed {
		return &FaultError{Status: 409, Code: "backend_change_status_conflict", Message: "Lifecycle action is invalid in the current state"}
	}
	return nil
}
func (r *Repo) applyChangeLifecycleB43Prepared(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, reader ConformanceEvidenceReader) (*ChangeProposalApplyResult, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	hash, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "change-proposal-lifecycle:" + pid + ":" + id
	out := new(ChangeProposalApplyResult)
	if found, err := readChangeReceipt(ctx, r.db.R, scope, in.IdempotencyKey, hash, out); err != nil || found {
		return out, err
	}
	p, err := loadChangeProposal(ctx, r.db.R, pid, id)
	if err != nil {
		return nil, err
	}
	if err = requireB43Lifecycle(p, in); err != nil {
		return nil, err
	}
	if in.ExpectedVersion < 1 || !ValidID(in.ProposalRevisionID) {
		return nil, invalid("lifecycle", "Exact version and revision required")
	}
	target, req, evidence, err := r.prepareB43LifecycleInput(ctx, pid, id, in, reader)
	if err != nil {
		return nil, err
	}
	footprint, err := r.AnalysisEvidenceInputFootprint(ctx, pid, req)
	if err != nil {
		return nil, err
	}
	leased, lease, err := r.ReserveAnalysisInput(ctx, pid, footprint)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	graph, err := r.ResolveEffectiveGraph(leased, pid, target)
	if err != nil {
		return nil, err
	}
	rev, err := loadChangeProposalRevision(leased, r.db.R, pid, id, in.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	if in.Action == "implemented" {
		if err = r.validatePreparedImplementation(leased, pid, id, in, evidence, rev, graph, req); err != nil {
			return nil, err
		}
	}
	err = r.db.Write(leased, func(tx *sql.Tx) error {
		return r.writeB43Lifecycle(leased, tx, pid, id, in, scope, hash, out, rev, evidence)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) writeB43Lifecycle(leased context.Context, tx *sql.Tx, pid, id string, in ApplyChangeProposalLifecycleInput, scope, hash string, out *ChangeProposalApplyResult, rev *ChangeProposalRevision, evidence *ConformanceEvidence) error {
	if found, err := readChangeReceipt(leased, tx, scope, in.IdempotencyKey, hash, out); err != nil || found {
		return err
	}
	current, err := loadChangeProposal(leased, tx, pid, id)
	if err != nil {
		return err
	}
	if err = requireB43Lifecycle(current, in); err != nil {
		return err
	}
	if current.CurrentDraftHash != rev.SemanticHash {
		return lifecycleConflict("Saved draft hash differs")
	}
	previousStatus, previousAssociation := current.Status, proposalAssociation(current)
	switch in.Action {
	case "implemented":
		exceptions := slices.Clone(in.Exceptions)
		slices.SortFunc(exceptions, func(a, b ChangeProposalException) int { return strings.Compare(a.CriterionKey, b.CriterionKey) })
		current.Status = "implemented"
		current.ImplementedReference = &ChangeProposalImplementedReference{Report: in.Report, ProposalRevisionID: rev.ID, DraftHash: rev.SemanticHash, ResultRevisionID: in.ResultRevisionID, ResultSemanticHash: evidence.ResultSemanticHash, Exceptions: exceptions, BehaviorStatus: "unverified"}
	case "archive":
		current.Status = "archived"
	case "unarchive":
		current.Status = "draft"
		current.ReadyReference = nil
		current.ImplementedReference = nil
	}
	current.Version++
	current.UpdatedAt = time.Now().UTC()
	association := proposalAssociation(current)
	raw, err := json.Marshal(association)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(leased, `UPDATE backend_change_proposals SET status=?,ready_reference=?,version=?,updated_at=? WHERE project_id=? AND id=?`, current.Status, string(raw), current.Version, current.UpdatedAt.Format(time.RFC3339Nano), pid, id); err != nil {
		return err
	}
	event, err := json.Marshal(ChangeProposalLifecycleEventV1{DocumentVersion: "backend-change-lifecycle-event/v1", Action: in.Action, RevisionID: rev.ID, Version: current.Version, CreatedAt: current.UpdatedAt, PreviousStatus: previousStatus, Status: current.Status, PreviousAssociation: previousAssociation, Association: association})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(leased, `INSERT INTO backend_change_proposal_events(project_id,proposal_id,revision_id,version,document) VALUES(?,?,?,?,?)`, pid, id, rev.ID, current.Version, string(event)); err != nil {
		return err
	}
	*out = ChangeProposalApplyResult{Proposal: *current, Revision: *rev, Changes: []ChangeProposalChange{}, SemanticHash: rev.SemanticHash}
	receipt, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err = writeChangeReceipt(leased, tx, scope, in.IdempotencyKey, hash, string(receipt)); err != nil {
		return err
	}
	if err = checkChangeProposalQuota(leased, tx, pid, id); err != nil {
		return err
	}
	out.receiptJSON = string(receipt)
	return nil
}

func (r *Repo) validatePreparedImplementation(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, evidence *ConformanceEvidence, rev *ChangeProposalRevision, graph *EffectiveGraphSnapshot, req AnalysisEvidenceRequest) error {
	baseline, err := r.ResolveEffectiveGraph(ctx, pid, BackendReadTarget{RevisionID: req.BaselineRevisionID})
	if err != nil {
		return err
	}
	result, err := r.ResolveEffectiveGraph(ctx, pid, BackendReadTarget{RevisionID: in.ResultRevisionID})
	if err != nil {
		return err
	}
	pins, err := r.ReadAnalysisEvidencePins(ctx, pid, req)
	if err != nil {
		return err
	}
	if err = validateConformanceLifecycle(pid, id, in, evidence, rev, baseline, graph, result, pins); err != nil {
		return err
	}
	return validateLifecycleExceptions(in.Exceptions, rev.Criteria)
}
func analysisSourcePinsForGraph(g *EffectiveGraphSnapshot) AnalysisSourcePins {
	p := AnalysisSourcePins{RevisionID: g.Pins.BaseRevisionID, SemanticHash: g.Pins.BaseSemanticHash, SourceVectorHash: g.Pins.SourceVectorHash, SourceSnapshotIDs: g.Pins.SourceSnapshotIDs}
	if g.Source != nil {
		p.ContentHash = g.Source.SourceContentHash
	}
	return p
}
func validateConformanceLifecycle(pid, id string, in ApplyChangeProposalLifecycleInput, e *ConformanceEvidence, rev *ChangeProposalRevision, base, draft, result *EffectiveGraphSnapshot, pins []AnalysisEvidenceDocumentPin) error {
	if err := validateConformanceLifecycleIdentity(pid, id, in, e, rev, result); err != nil {
		return err
	}
	for _, pair := range [][2]any{{e.BasePins, base.Pins}, {e.DraftPins, draft.Pins}, {e.ResultPins, result.Pins}, {e.BaseSource, analysisSourcePinsForGraph(base)}, {e.DraftSource, analysisSourcePinsForGraph(draft)}, {e.ResultSource, analysisSourcePinsForGraph(result)}, {e.EvidencePins, pins}} {
		a, err := requestDigest(pair[0])
		if err != nil {
			return err
		}
		b, err := requestDigest(pair[1])
		if err != nil {
			return err
		}
		if a != b {
			return lifecycleConflict("Report source or draft pins differ")
		}
	}
	return validateConformanceRoster(e.Criteria, rev.Criteria)
}

// Repeat receipt resolution under the writer even when preparation lost a race
// or failed: a concurrently committed exact receipt outranks a stale CAS/gate.
func (r *Repo) applyChangeLifecycleB43(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, reader ConformanceEvidenceReader) (*ChangeProposalApplyResult, error) {
	out, prepareErr := r.applyChangeLifecycleB43Prepared(ctx, pid, id, in, reader)
	if prepareErr == nil {
		return out, nil
	}
	hash, err := requestDigest(in)
	if err != nil {
		return nil, prepareErr
	}
	replay := new(ChangeProposalApplyResult)
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		found, err := readChangeReceipt(ctx, tx, "change-proposal-lifecycle:"+pid+":"+id, in.IdempotencyKey, hash, replay)
		if err != nil || found {
			return err
		}
		return prepareErr
	})
	if err != nil {
		return nil, err
	}
	return replay, nil
}

func validateConformanceLifecycleIdentity(pid, id string, in ApplyChangeProposalLifecycleInput, e *ConformanceEvidence, rev *ChangeProposalRevision, result *EffectiveGraphSnapshot) error {
	if e == nil || e.ProjectID != pid || e.Kind != "conformance" || e.Status != "completed" || e.Report != in.Report || e.ChangeProposal != (ProposalReadTarget{ProposalID: id, ProposalRevisionID: rev.ID}) || e.BaseRevisionID != rev.BaseRevisionID || e.DraftHash != rev.SemanticHash || e.ResultRevisionID != in.ResultRevisionID || e.ResultSemanticHash != result.Pins.BaseSemanticHash || !e.Complete || len(e.TruncationReasons) > 0 || e.RuleSetVersion != "b43-rules/v1" || e.TraversalVersion != "b42-traversal/v1" || e.BehaviorStatus != "unverified" {
		return lifecycleConflict("Report does not establish exact completed structural conformance")
	}
	return nil
}

func validateConformanceRoster(evidence []ConformanceCriterionEvidence, criteria []ChangeCriterion) error {
	if len(evidence) != len(criteria) {
		return lifecycleConflict("Report must cover the exact saved criterion roster")
	}
	rows := map[string]ConformanceCriterionEvidence{}
	for _, row := range evidence {
		if _, exists := rows[row.Key]; exists {
			return lifecycleConflict("Duplicate conformance criterion")
		}
		rows[row.Key] = row
	}
	for _, c := range criteria {
		row, ok := rows[c.Key]
		if c.Kind == "runtime_check" && (c.Required || row.Outcome != "unverified") {
			return lifecycleConflict("Static conformance cannot satisfy runtime criteria")
		}
		if !ok || row.Kind != c.Kind || row.Required != c.Required || !slices.Contains([]string{"satisfied", "violated", "unverified"}, row.Outcome) || c.Required && row.Outcome != "satisfied" {
			return lifecycleConflict("Every required saved criterion must have one satisfied result")
		}
	}
	return nil
}

func (r *Repo) prepareB43LifecycleInput(ctx context.Context, pid, id string, in ApplyChangeProposalLifecycleInput, reader ConformanceEvidenceReader) (BackendReadTarget, AnalysisEvidenceRequest, *ConformanceEvidence, error) {
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: id, ProposalRevisionID: in.ProposalRevisionID}}
	base, err := r.AnalysisProposalBaseRevision(ctx, pid, *target.ChangeProposal)
	if err != nil {
		return BackendReadTarget{}, AnalysisEvidenceRequest{}, nil, err
	}
	req := AnalysisEvidenceRequest{Targets: []BackendReadTarget{target, {RevisionID: base}}, BaselineRevisionID: base}
	var evidence *ConformanceEvidence
	if in.Action == "implemented" {
		if reader == nil || !ValidID(in.ResultRevisionID) || !ValidID(in.Report.JobID) || in.Report.ResultVersion < 1 || !validHash(in.Report.InputHash) || !validHash(in.Report.ResultHash) {
			return BackendReadTarget{}, AnalysisEvidenceRequest{}, nil, lifecycleConflict("Exact completed conformance report required")
		}
		evidence, err = reader.ReadConformanceEvidence(ctx, pid, in.Report)
		if err != nil {
			return BackendReadTarget{}, AnalysisEvidenceRequest{}, nil, err
		}
		req.ResultRevisionID = in.ResultRevisionID
		req.Targets = append(req.Targets, BackendReadTarget{RevisionID: in.ResultRevisionID})
	}
	return target, req, evidence, nil
}
