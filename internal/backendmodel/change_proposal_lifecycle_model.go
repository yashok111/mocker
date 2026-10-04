package backendmodel

import "context"

type AnalysisReportRef struct {
	JobID         string `json:"jobId"`
	ResultVersion int64  `json:"resultVersion"`
	InputHash     string `json:"inputHash"`
	ResultHash    string `json:"resultHash"`
}
type AnalysisGateEvidence struct {
	ProjectID, Kind, Status                          string
	Report                                           AnalysisReportRef
	ChangeProposal                                   ProposalReadTarget
	CommandPreview                                   bool
	FromRevisionID, BaseRevisionID, BaseSemanticHash string
	DraftHash, EffectiveSemanticHash, TargetHash     string
	SourcePins                                       AnalysisSourcePins
	ArtifactPins                                     []ArtifactPin
	ChangedIDs, CoveredChangedIDs                    []ChangeRecordRef
	Complete                                         bool
	GapIDs, TruncationReasons                        []string
	RuleSetVersion, TraversalVersion                 string
}
type AnalysisGateReader interface {
	ReadAnalysisGate(context.Context, string, AnalysisReportRef) (*AnalysisGateEvidence, error)
}
type ChangeProposalReadyReference struct {
	Report             AnalysisReportRef `json:"report"`
	ProposalRevisionID string            `json:"proposalRevisionId"`
	DraftHash          string            `json:"draftHash"`
	AcknowledgedGapIDs []string          `json:"acknowledgedGapIds"`
}
type ApplyChangeProposalLifecycleInput struct {
	ExpectedVersion    int64             `json:"expectedVersion"`
	ProposalRevisionID string            `json:"proposalRevisionId"`
	Action             string            `json:"action"`
	IdempotencyKey     string            `json:"idempotencyKey"`
	Report             AnalysisReportRef `json:"report"`
	AcknowledgedGapIDs []string          `json:"acknowledgedGapIds"`
}

func (in *ApplyChangeProposalLifecycleInput) UnmarshalJSON(raw []byte) error {
	type plain ApplyChangeProposalLifecycleInput
	return decodeChangeOperation(raw, []string{"expectedVersion", "proposalRevisionId", "action", "idempotencyKey", "report", "acknowledgedGapIds"}, (*plain)(in))
}
func (in *AnalysisReportRef) UnmarshalJSON(raw []byte) error {
	type plain AnalysisReportRef
	return decodeChangeOperation(raw, []string{"jobId", "resultVersion", "inputHash", "resultHash"}, (*plain)(in))
}
