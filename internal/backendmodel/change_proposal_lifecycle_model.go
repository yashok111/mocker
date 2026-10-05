package backendmodel

import (
	"context"
	"encoding/json/v2"
)

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
	ResultRevisionID   string                    `json:"-"`
	Exceptions         []ChangeProposalException `json:"-"`
	ExpectedVersion    int64                     `json:"expectedVersion"`
	ProposalRevisionID string                    `json:"proposalRevisionId"`
	Action             string                    `json:"action"`
	IdempotencyKey     string                    `json:"idempotencyKey"`
	Report             AnalysisReportRef         `json:"report"`
	AcknowledgedGapIDs []string                  `json:"acknowledgedGapIds"`
}

func (in *ApplyChangeProposalLifecycleInput) UnmarshalJSON(raw []byte) error {
	var discriminator struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return err
	}
	fields := []string{"expectedVersion", "proposalRevisionId", "action", "idempotencyKey"}
	unsupported := false
	switch discriminator.Action {
	case "ready":
		fields = append(fields, "report", "acknowledgedGapIds")
	case "implemented":
		fields = append(fields, "report", "resultRevisionId", "exceptions")
	case "archive", "unarchive":
	default:
		// Historical clients sent the ready-shaped body for unsupported actions.
		// Preserve their 422 only after the old closed body passes decoding.
		unsupported = true
		fields = append(fields, "report", "acknowledgedGapIds")
	}
	type plain ApplyChangeProposalLifecycleInput
	var next plain
	wire := struct {
		*plain
		ResultRevisionID string                    `json:"resultRevisionId"`
		Exceptions       []ChangeProposalException `json:"exceptions"`
	}{plain: &next}
	if err := decodeChangeOperation(raw, fields, &wire); err != nil {
		return err
	}
	if unsupported {
		return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Unsupported lifecycle action"}
	}
	next.ResultRevisionID, next.Exceptions = wire.ResultRevisionID, wire.Exceptions
	if discriminator.Action == "implemented" && (len(next.Exceptions) > 100 || !ValidID(next.ResultRevisionID)) {
		return invalid("lifecycle", "Invalid implemented associations")
	}
	*in = ApplyChangeProposalLifecycleInput(next)
	return nil
}
func (in *AnalysisReportRef) UnmarshalJSON(raw []byte) error {
	type plain AnalysisReportRef
	return decodeChangeOperation(raw, []string{"jobId", "resultVersion", "inputHash", "resultHash"}, (*plain)(in))
}
