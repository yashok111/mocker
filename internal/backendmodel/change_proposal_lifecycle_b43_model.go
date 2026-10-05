package backendmodel

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type ChangeProposalException struct {
	CriterionKey string `json:"criterionKey"`
	Author       string `json:"author"`
	Reason       string `json:"reason"`
}
type ChangeProposalImplementedReference struct {
	Report             AnalysisReportRef         `json:"report"`
	ProposalRevisionID string                    `json:"proposalRevisionId"`
	DraftHash          string                    `json:"draftHash"`
	ResultRevisionID   string                    `json:"resultRevisionId"`
	ResultSemanticHash string                    `json:"resultSemanticHash"`
	Exceptions         []ChangeProposalException `json:"exceptions"`
	BehaviorStatus     string                    `json:"behaviorStatus"`
}
type ChangeProposalLifecycleAssociation struct {
	DocumentVersion      string                              `json:"documentVersion"`
	ReadyReference       *ChangeProposalReadyReference       `json:"readyReference"`
	ImplementedReference *ChangeProposalImplementedReference `json:"implementedReference"`
}
type ChangeProposalLifecycleEventV1 struct {
	DocumentVersion     string                             `json:"documentVersion"`
	Action              string                             `json:"action"`
	RevisionID          string                             `json:"revisionId"`
	Version             int64                              `json:"version"`
	CreatedAt           time.Time                          `json:"createdAt"`
	PreviousStatus      string                             `json:"previousStatus"`
	Status              string                             `json:"status"`
	PreviousAssociation ChangeProposalLifecycleAssociation `json:"previousAssociation"`
	Association         ChangeProposalLifecycleAssociation `json:"association"`
}
type ConformanceCriterionEvidence struct {
	Key, Kind, Outcome string
	Required           bool
}
type ConformanceEvidence struct {
	ProjectID, Kind, Status                                         string
	Report                                                          AnalysisReportRef
	ChangeProposal                                                  ProposalReadTarget
	BaseRevisionID, DraftHash, ResultRevisionID, ResultSemanticHash string
	BasePins, DraftPins, ResultPins                                 EffectiveGraphPins
	BaseSource, DraftSource, ResultSource                           AnalysisSourcePins
	EvidencePins                                                    []AnalysisEvidenceDocumentPin
	Complete                                                        bool
	TruncationReasons                                               []string
	RuleSetVersion, TraversalVersion                                string
	Criteria                                                        []ConformanceCriterionEvidence
	BehaviorStatus                                                  string
}
type ConformanceEvidenceReader interface {
	ReadConformanceEvidence(context.Context, string, AnalysisReportRef) (*ConformanceEvidence, error)
}

func (in ApplyChangeProposalLifecycleInput) MarshalJSON() ([]byte, error) {
	switch in.Action {
	case "archive", "unarchive":
		return json.Marshal(struct {
			ExpectedVersion    int64  `json:"expectedVersion"`
			ProposalRevisionID string `json:"proposalRevisionId"`
			Action             string `json:"action"`
			IdempotencyKey     string `json:"idempotencyKey"`
		}{in.ExpectedVersion, in.ProposalRevisionID, in.Action, in.IdempotencyKey})
	case "implemented":
		return json.Marshal(struct {
			ExpectedVersion    int64                     `json:"expectedVersion"`
			ProposalRevisionID string                    `json:"proposalRevisionId"`
			Action             string                    `json:"action"`
			IdempotencyKey     string                    `json:"idempotencyKey"`
			Report             AnalysisReportRef         `json:"report"`
			ResultRevisionID   string                    `json:"resultRevisionId"`
			Exceptions         []ChangeProposalException `json:"exceptions"`
		}{in.ExpectedVersion, in.ProposalRevisionID, in.Action, in.IdempotencyKey, in.Report, in.ResultRevisionID, in.Exceptions})
	}
	type plain ApplyChangeProposalLifecycleInput
	return json.Marshal(plain(in))
}
func (e *ChangeProposalException) UnmarshalJSON(raw []byte) error {
	type plain ChangeProposalException
	var next plain
	if err := decodeChangeOperation(raw, []string{"criterionKey", "author", "reason"}, &next); err != nil {
		return err
	}
	if !externalKey(next.CriterionKey) || !lifecycleText(next.Author) || !lifecycleText(next.Reason) {
		return invalid("exceptions", "Exact key and nonblank bounded author/reason required")
	}
	*e = ChangeProposalException(next)
	return nil
}
func lifecycleText(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(s) <= 4096
}
func decodeLifecycleAssociation(raw []byte, p *ChangeProposal) error {
	var version struct {
		DocumentVersion string `json:"documentVersion"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		return err
	}
	if version.DocumentVersion == "" {
		return json.Unmarshal(raw, &p.ReadyReference)
	}
	if version.DocumentVersion != "backend-change-lifecycle/v1" {
		return invalid("readyReference", "Unsupported lifecycle association")
	}
	var association ChangeProposalLifecycleAssociation
	if err := json.Unmarshal(raw, &association, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	p.ReadyReference, p.ImplementedReference = association.ReadyReference, association.ImplementedReference
	return nil
}
func proposalAssociation(p *ChangeProposal) ChangeProposalLifecycleAssociation {
	return ChangeProposalLifecycleAssociation{DocumentVersion: "backend-change-lifecycle/v1", ReadyReference: p.ReadyReference, ImplementedReference: p.ImplementedReference}
}
func validateLifecycleExceptions(exceptions []ChangeProposalException, criteria []ChangeCriterion) error {
	if len(exceptions) > 100 {
		return invalid("exceptions", "At most100 exceptions")
	}
	keys := map[string]bool{}
	for _, e := range exceptions {
		if keys[e.CriterionKey] || !externalKey(e.CriterionKey) || !lifecycleText(e.Author) || !lifecycleText(e.Reason) || !slices.ContainsFunc(criteria, func(c ChangeCriterion) bool { return c.Key == e.CriterionKey }) {
			return invalid("exceptions", "Unique saved criterion keys and bounded annotations required")
		}
		keys[e.CriterionKey] = true
	}
	return nil
}
