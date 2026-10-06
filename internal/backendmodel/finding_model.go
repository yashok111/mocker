package backendmodel

import "slices"

// Finding is immutable diagnostic evidence. Review metadata is stored separately.
type FindingWitness struct {
	Records            []ChangeRecordRef     `json:"records"`
	MissingRef         *DiagramRef           `json:"missingRef,omitzero"`
	OriginalDiagramPin *DiagramPin           `json:"originalDiagramPin,omitzero"`
	RuleID             string                `json:"ruleId,omitempty"`
	TransitionID       string                `json:"transitionId,omitempty"`
	FacetDifferences   []FacetPairDifference `json:"facetDifferences"`
}

type Finding struct {
	Witness       FindingWitness    `json:"witness"`
	Fingerprint   string            `json:"fingerprint"`
	BasisHash     string            `json:"basisHash"`
	Rule          string            `json:"rule"`
	RuleVersion   string            `json:"ruleVersion"`
	Severity      string            `json:"severity"`
	Certainty     string            `json:"certainty"`
	Subjects      []ChangeRecordRef `json:"subjects"`
	EvidenceIDs   []string          `json:"evidenceIds"`
	Prerequisites []string          `json:"prerequisites"`
	Gaps          []string          `json:"gaps"`
	Message       string            `json:"message"`
	TargetHash    string            `json:"targetHash"`
	DiagramScope  *DiagramScope     `json:"diagramScope,omitzero"`
}
type FindingCheck struct {
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"` // present | absent | unknown
	ScopeKey    string `json:"scopeKey"`
}
type FindingAnalysisRef struct {
	JobID         string `json:"jobId"`
	ResultVersion int64  `json:"resultVersion"`
}
type FindingReviewInput struct {
	ExpectedVersion    int64               `json:"expectedVersion"`
	BasisHash          string              `json:"basisHash"`
	Status             string              `json:"status"`
	Reason             string              `json:"reason"`
	IdempotencyKey     string              `json:"idempotencyKey"`
	ResolutionAnalysis *FindingAnalysisRef `json:"resolutionAnalysis,omitzero"`
}
type FindingReviewEvent struct {
	Version            int64               `json:"version"`
	BasisHash          string              `json:"basisHash"`
	Status             string              `json:"status"`
	Reason             string              `json:"reason"`
	Author             string              `json:"author"`
	At                 string              `json:"at"`
	ResolutionAnalysis *FindingAnalysisRef `json:"resolutionAnalysis,omitzero"`
}
type FindingReview struct {
	Fingerprint string               `json:"fingerprint"`
	Version     int64                `json:"version"`
	BasisHash   string               `json:"basisHash"`
	Status      string               `json:"status"`
	History     []FindingReviewEvent `json:"history"`
}
type FindingItem struct {
	Finding  Finding            `json:"finding"`
	Analysis FindingAnalysisRef `json:"analysis"`
	Review   FindingReview      `json:"review"`
}
type FindingPage struct {
	Items      []FindingItem `json:"items"`
	NextCursor string        `json:"nextCursor"`
}

func (in *FindingReviewInput) UnmarshalJSON(raw []byte) error {
	type plain FindingReviewInput
	if err := strictAPIObject(raw, []string{"expectedVersion", "basisHash", "status", "reason", "idempotencyKey"}, []string{"resolutionAnalysis"}, (*plain)(in)); err != nil {
		return err
	}
	return in.Validate()
}
func (in *FindingAnalysisRef) UnmarshalJSON(raw []byte) error {
	type plain FindingAnalysisRef
	if err := strictAPIObject(raw, []string{"jobId", "resultVersion"}, nil, (*plain)(in)); err != nil {
		return err
	}
	if !ValidID(in.JobID) || in.ResultVersion < 1 {
		return invalid("resolutionAnalysis", "Exact completed result required")
	}
	return nil
}
func (in FindingReviewInput) Validate() error {
	if in.ExpectedVersion < 1 || !validHash(in.BasisHash) || !validAPIText(in.Reason, 1, 4096) || !validAPIText(in.IdempotencyKey, 1, MaxKeyLength) || !slices.Contains([]string{"open", "accepted_risk", "false_positive", "resolved"}, in.Status) {
		return invalid("review", "Invalid version, basis, status, reason or key")
	}
	if (in.Status == "resolved") != (in.ResolutionAnalysis != nil) {
		return invalid("resolutionAnalysis", "Only resolved requires an exact recheck")
	}
	if in.ResolutionAnalysis != nil && (!ValidID(in.ResolutionAnalysis.JobID) || in.ResolutionAnalysis.ResultVersion < 1) {
		return invalid("resolutionAnalysis", "Exact recheck required")
	}
	return nil
}
