package backendanalysis

import (
	"context"
	"database/sql"
	"encoding/json/v2"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// JobDetail exposes the saved input identity without server admission records.
type JobDetail struct {
	Job   *Job                 `json:"job"`
	Input AnalysisInputContext `json:"input"`
}
type AnalysisContextPreview struct {
	ChangeProposal  backendmodel.ProposalReadTarget `json:"changeProposal"`
	ExpectedVersion int64                           `json:"expectedVersion"`
	DraftHash       string                          `json:"draftHash"`
	CandidateHash   string                          `json:"candidateHash"`
	CommandsHash    string                          `json:"commandsHash"`
}
type AnalysisContextTarget struct {
	RevisionID     string                           `json:"revisionId,omitempty"`
	Proposal       *backendmodel.ProposalReadTarget `json:"proposal,omitzero"`
	ChangeProposal *backendmodel.ProposalReadTarget `json:"changeProposal,omitzero"`
	CommandPreview *AnalysisContextPreview          `json:"commandPreview,omitzero"`
}
type AnalysisInputContext struct {
	V2               *InputContextV2                 `json:"-"`
	DocumentVersion  string                          `json:"documentVersion"`
	Kind             string                          `json:"kind"`
	FromRevisionID   string                          `json:"fromRevisionId"`
	Target           AnalysisContextTarget           `json:"target"`
	BeforePins       backendmodel.EffectiveGraphPins `json:"beforePins"`
	AfterPins        backendmodel.EffectiveGraphPins `json:"afterPins"`
	BeforeSource     backendmodel.AnalysisSourcePins `json:"beforeSource"`
	AfterSource      backendmodel.AnalysisSourcePins `json:"afterSource"`
	Scope            Scope                           `json:"scope"`
	Limits           Limits                          `json:"limits"`
	RuleSetVersion   string                          `json:"ruleSetVersion"`
	TraversalVersion string                          `json:"traversalVersion"`
	ObservationMode  string                          `json:"observationMode"`
}

func (in AnalysisInputContext) MarshalJSON() ([]byte, error) {
	if in.V2 != nil {
		return json.Marshal(in.V2)
	}
	type plain AnalysisInputContext
	before, err := encodePins(in.BeforePins)
	if err != nil {
		return nil, err
	}
	after, err := encodePins(in.AfterPins)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		*plain
		BeforePins persistedPins `json:"beforePins"`
		AfterPins  persistedPins `json:"afterPins"`
	}{(*plain)(&in), before, after})
}

func (r *Repo) Detail(ctx context.Context, pid, id string) (*JobDetail, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := readJob(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	raw, err := inputBytes(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	var saved ImmutableInput
	if err = json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	if v := saved.V2; v != nil {
		return &JobDetail{Job: job, Input: AnalysisInputContext{V2: &InputContextV2{DocumentVersion: "backend-analysis-context-v2", Kind: v.Kind, Payload: v.Payload, Limits: v.Limits, RuleSetVersion: v.RuleSetVersion, TraversalVersion: v.TraversalVersion, ObservationMode: v.ObservationMode}}}, nil
	}
	target := AnalysisContextTarget{}
	if saved.CommandPreview != nil {
		p := saved.CommandPreview
		target.CommandPreview = &AnalysisContextPreview{p.ChangeProposal, p.ExpectedVersion, p.DraftHash, p.CandidateHash, p.CommandsHash}
	} else if saved.To != nil {
		target.RevisionID, target.Proposal, target.ChangeProposal = saved.To.RevisionID, saved.To.Proposal, saved.To.ChangeProposal
	}
	return &JobDetail{Job: job, Input: AnalysisInputContext{DocumentVersion: "backend-analysis-context-v1", Kind: saved.Kind, FromRevisionID: saved.From.RevisionID, Target: target, BeforePins: saved.BeforePins, AfterPins: saved.AfterPins, BeforeSource: saved.BeforeSource, AfterSource: saved.AfterSource, Scope: saved.Scope, Limits: saved.Limits, RuleSetVersion: saved.RuleSetVersion, TraversalVersion: saved.TraversalVersion, ObservationMode: saved.ObservationMode}}, nil
}
