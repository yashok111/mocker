package backendanalysis

import (
	"context"
	"database/sql"
	"encoding/json/v2"

	"github.com/yashok111/mocker/internal/backendmodel"
)

var _ backendmodel.AnalysisGateReader = (*Repo)(nil)

// ReadAnalysisGate reads one published immutable report in a consistent snapshot.
// It never resolves a current graph or substitutes the latest available report.
func (r *Repo) ReadAnalysisGate(ctx context.Context, pid string, ref backendmodel.AnalysisReportRef) (*backendmodel.AnalysisGateEvidence, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := readJob(ctx, tx, pid, ref.JobID)
	if err != nil {
		return nil, err
	}
	if j.Status != "completed" || j.Kind != "impact" || j.ResultVersion == nil || *j.ResultVersion != ref.ResultVersion || j.AnalysisInputHash != ref.InputHash {
		return nil, fault(409, "gate_conflict", "Report must be the exact completed impact result")
	}
	in, err := readGateInput(ctx, tx, pid, ref)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT document FROM backend_analysis_manifests_documents WHERE project_id=? AND job_id=? AND result_version=?`, pid, ref.JobID, ref.ResultVersion).Scan(&raw); err != nil {
		return nil, err
	}
	var m ResultManifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.JobID != ref.JobID || m.ResultVersion != ref.ResultVersion || m.AnalysisInputHash != ref.InputHash || m.SemanticResultHash != ref.ResultHash || m.RuleSetVersion != in.RuleSetVersion || m.TraversalVersion != in.TraversalVersion {
		return nil, fault(409, "gate_conflict", "Immutable report pins differ")
	}
	return gateEvidence(pid, ref, j, in, m), nil
}

func gateEvidence(pid string, ref backendmodel.AnalysisReportRef, j *Job, in *ImmutableInput, m ResultManifest) *backendmodel.AnalysisGateEvidence {
	e := &backendmodel.AnalysisGateEvidence{ProjectID: pid, Kind: j.Kind, Status: j.Status, Report: ref, ChangeProposal: *in.To.ChangeProposal, FromRevisionID: in.From.RevisionID, BaseRevisionID: in.AfterPins.BaseRevisionID, BaseSemanticHash: in.AfterPins.BaseSemanticHash, DraftHash: in.AfterPins.EffectiveSemanticHash, EffectiveSemanticHash: in.AfterPins.EffectiveSemanticHash, TargetHash: in.AfterPins.TargetHash, SourcePins: in.AfterSource, ArtifactPins: in.AfterPins.ArtifactPins, Complete: m.Complete, RuleSetVersion: m.RuleSetVersion, TraversalVersion: m.TraversalVersion}
	for _, a := range m.ChangedIDs {
		e.ChangedIDs = append(e.ChangedIDs, backendmodel.ChangeRecordRef{RecordType: a.RecordType, ID: a.ID})
	}
	for _, a := range m.CoveredChangedIDs {
		e.CoveredChangedIDs = append(e.CoveredChangedIDs, backendmodel.ChangeRecordRef{RecordType: a.RecordType, ID: a.ID})
	}
	for _, g := range m.Gaps {
		e.GapIDs = append(e.GapIDs, g.ID)
	}
	for _, g := range m.TruncationReasons {
		e.TruncationReasons = append(e.TruncationReasons, g.ID)
	}
	return e
}

func readGateInput(ctx context.Context, q reader, pid string, ref backendmodel.AnalysisReportRef) (*ImmutableInput, error) {
	raw, err := inputBytes(ctx, q, pid, ref.JobID)
	if err != nil {
		return nil, err
	}
	var in ImmutableInput
	if err = json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if digest(raw) != ref.InputHash || in.ProjectID != pid || in.Kind != "impact" || in.CommandPreview != nil || in.To == nil || in.To.ChangeProposal == nil || in.From.RevisionID == "" {
		return nil, fault(409, "gate_conflict", "Report must target an exact saved full draft")
	}
	return &in, nil
}
