package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"time"
)

const (
	ChangeProposalDocumentVersion       = "proposal-graph-v1"
	MaxChangeProposalCommands           = 100
	MaxChangeProposalCommandBytes       = 1 << 20
	MaxChangeProposalCriteria           = 100
	MaxChangeProposals                  = 100
	MaxChangeProposalRevisions          = 1000
	MaxChangeProposalBytes        int64 = 512 << 20
)

type ChangeProposal struct {
	ImplementedReference   *ChangeProposalImplementedReference `json:"implementedReference,omitzero"`
	ReadyReference         *ChangeProposalReadyReference       `json:"readyReference,omitzero"`
	ID                     string                              `json:"id"`
	ProjectID              string                              `json:"projectId"`
	Name                   string                              `json:"name"`
	Version                int64                               `json:"version"`
	Status                 string                              `json:"status"`
	CurrentDraftRevisionID string                              `json:"currentDraftRevisionId"`
	CurrentDraftHash       string                              `json:"currentDraftHash"`
	CreatedAt              time.Time                           `json:"createdAt"`
	UpdatedAt              time.Time                           `json:"updatedAt"`
}

type ChangeRecordRef struct {
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
}
type ChangeObjectIdentity struct {
	AllocationKind string `json:"allocationKind,omitempty"`
	ChangeRecordRef
	Kind            string          `json:"kind"`
	FirstRevisionID string          `json:"firstRevisionId"`
	Origin          EffectiveOrigin `json:"origin"`
}
type ChangeProperty struct {
	ChangeRecordRef
	Selector TypedSourcePropertySelector `json:"selector"`
	Value    SourcePropertyValue         `json:"value"`
	Origin   EffectiveOrigin             `json:"origin"`
}
type ChangeCreatedRecord struct {
	ChangeRecordRef
	Payload SourceAssertionPayload `json:"payload"`
	Origin  EffectiveOrigin        `json:"origin"`
}
type ChangeRemoval struct {
	ChangeRecordRef
	Origin EffectiveOrigin `json:"origin"`
}
type ChangeIdentityIntent struct {
	Target      ChangeIdentityTarget `json:"target"`
	ExternalKey *string              `json:"externalKey"`
	Origin      EffectiveOrigin      `json:"origin"`
}
type ChangeEdgeName struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Origin EffectiveOrigin `json:"origin"`
}
type ChangeArtifactIntent struct {
	Artifact ArtifactKey     `json:"artifact"`
	Removed  bool            `json:"removed"`
	Origin   EffectiveOrigin `json:"origin"`
}
type ChangeDelta struct {
	CarriedIdentities []ChangeCarriedSourceIdentity `json:"carriedIdentities,omitempty"`
	Created           []ChangeCreatedRecord         `json:"created"`
	Removed           []ChangeRemoval               `json:"removed"`
	Properties        []ChangeProperty              `json:"properties"`
	IdentityIntents   []ChangeIdentityIntent        `json:"identityIntents"`
	ArtifactIntents   []ChangeArtifactIntent        `json:"artifactIntents"`
	EdgeNames         []ChangeEdgeName              `json:"edgeNames"`
}
type ChangeProposalRevision struct {
	ImportOrigin            *PortableAttribution `json:"importOrigin,omitzero"`
	ArtifactContextV3       *ArtifactContextV3   `json:"-"`
	Rebase                  *ChangeRebaseAction  `json:"rebase,omitzero"`
	ID                      string               `json:"id"`
	ProposalID              string               `json:"proposalId"`
	ParentRevisionID        *string              `json:"parentRevisionId"`
	DocumentVersion         string               `json:"documentVersion"`
	BaseRevisionID          string               `json:"baseRevisionId"`
	BaseSemanticHash        string               `json:"baseSemanticHash"`
	BaseSchemaVersion       string               `json:"baseSchemaVersion"`
	SourceSnapshotIDs       []string             `json:"sourceSnapshotIds"`
	SourceVector            SourceVector         `json:"sourceVector"`
	ArtifactPins            []ArtifactPin        `json:"artifactPins"`
	ArtifactContext         ArtifactContext      `json:"artifactContext"`
	Delta                   ChangeDelta          `json:"delta"`
	Criteria                []ChangeCriterion    `json:"criteria"`
	SemanticHash            string               `json:"semanticHash"`
	AcceptedBatchRevisionID string               `json:"acceptedBatchRevisionId"`
	Author                  string               `json:"author"`
	Summary                 string               `json:"summary"`
	CreatedAt               time.Time            `json:"createdAt"`
}

// A draft preserves either legacy or tagged artifact context verbatim. The
// source ArtifactContext decoder intentionally admits only its tagged branch.
func (r *ChangeProposalRevision) UnmarshalJSON(raw []byte) error {
	type plain ChangeProposalRevision
	type contextValue ArtifactContext
	decoded := struct {
		*plain
		ArtifactContext contextValue `json:"artifactContext"`
	}{plain: (*plain)(r)}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var tag struct {
		DocumentVersion string `json:"documentVersion"`
	}
	if err := json.Unmarshal(fields["artifactContext"], &tag); err != nil {
		return err
	}
	if tag.DocumentVersion == ArtifactContextV3Version {
		c, err := DecodeVersionedArtifactContext(fields["artifactContext"], r.ArtifactPins)
		if err != nil {
			return err
		}
		r.ArtifactContextV3 = c.V3
		r.ArtifactContext = ArtifactContext{}
	} else {
		r.ArtifactContextV3 = nil
		r.ArtifactContext = ArtifactContext(decoded.ArtifactContext)
	}
	return nil
}

type ChangeAppliedBatch struct {
	ProposalID        string                  `json:"proposalId"`
	RevisionID        string                  `json:"revisionId"`
	Action            string                  `json:"action"`
	RestoreRevisionID string                  `json:"restoreRevisionId,omitempty"`
	Commands          []ChangeProposalCommand `json:"commands"`
	CommandsHash      string                  `json:"commandsHash"`
}
type ChangeAppliedCommand struct {
	ProposalID string `json:"proposalId"`
	CommandID  string `json:"commandId"`
	RevisionID string `json:"revisionId"`
	Position   int    `json:"position"`
}
type ChangeProposalChange struct {
	CommandID  string `json:"commandId"`
	Type       string `json:"type"`
	RecordType string `json:"recordType,omitempty"`
	ID         string `json:"id,omitempty"`
}
type ChangeProposalCandidate struct {
	ProposalID         string                 `json:"proposalId"`
	ExpectedVersion    int64                  `json:"expectedVersion"`
	ProposalRevisionID string                 `json:"proposalRevisionId"`
	DraftHash          string                 `json:"draftHash"`
	BaseRevisionID     string                 `json:"baseRevisionId"`
	BaseSemanticHash   string                 `json:"baseSemanticHash"`
	DocumentVersion    string                 `json:"documentVersion"`
	Changes            []ChangeProposalChange `json:"changes"`
	Criteria           []ChangeCriterion      `json:"criteria"`
	Diagnostics        []ImportDiagnostic     `json:"diagnostics"`
	SemanticHash       *string                `json:"semanticHash"`
	CandidateHash      *string                `json:"candidateHash"`
}
type ChangeProposalDetail struct {
	receiptJSON               string
	Proposal                  ChangeProposal            `json:"proposal"`
	Revision                  ChangeProposalRevision    `json:"revision"`
	History                   []ProposalRevisionSummary `json:"history"`
	NextCursor                string                    `json:"nextCursor"`
	BaseOutdated              bool                      `json:"baseOutdated"`
	CurrentSourceRevisionID   string                    `json:"currentSourceRevisionId"`
	CurrentSourceSemanticHash string                    `json:"currentSourceSemanticHash"`
}

func (d ChangeProposalDetail) MarshalJSON() ([]byte, error) {
	if d.receiptJSON != "" {
		return []byte(d.receiptJSON), nil
	}
	type plain ChangeProposalDetail
	return json.Marshal(plain(d))
}

type ChangeProposalApplyResult struct {
	receiptJSON  string
	Proposal     ChangeProposal         `json:"proposal"`
	Revision     ChangeProposalRevision `json:"revision"`
	Changes      []ChangeProposalChange `json:"changes"`
	SemanticHash string                 `json:"semanticHash"`
}

func (d ChangeProposalApplyResult) MarshalJSON() ([]byte, error) {
	if d.receiptJSON != "" {
		return []byte(d.receiptJSON), nil
	}
	type plain ChangeProposalApplyResult
	return json.Marshal(plain(d))
}
func (d ChangeProposalDetail) ReceiptBytes() []byte      { return []byte(d.receiptJSON) }
func (d ChangeProposalApplyResult) ReceiptBytes() []byte { return []byte(d.receiptJSON) }

type ChangeProposalPage struct {
	Items      []ChangeProposal `json:"items"`
	NextCursor string           `json:"nextCursor"`
}
type CreateChangeProposalInput struct {
	Name           string `json:"name"`
	BaseRevisionID string `json:"baseRevisionId"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type PreviewChangeProposalInput struct {
	ExpectedVersion    int64                   `json:"expectedVersion"`
	ProposalRevisionID string                  `json:"proposalRevisionId"`
	Commands           []ChangeProposalCommand `json:"commands"`
}
type ApplyChangeProposalInput struct {
	ExpectedVersion    int64                   `json:"expectedVersion"`
	ProposalRevisionID string                  `json:"proposalRevisionId"`
	Commands           []ChangeProposalCommand `json:"commands"`
	CandidateHash      string                  `json:"candidateHash"`
	IdempotencyKey     string                  `json:"idempotencyKey"`
}
type RestoreChangeProposalInput struct {
	ExpectedVersion    int64  `json:"expectedVersion"`
	ProposalRevisionID string `json:"proposalRevisionId"`
	RestoreRevisionID  string `json:"restoreRevisionId"`
	IdempotencyKey     string `json:"idempotencyKey"`
}
type ChangeProposalListInput = ProposalListInput
type GetChangeProposalInput = GetProposalInput

func decodeChangeOperation(b []byte, fields []string, out any) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("body", err.Error())
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return invalid("body", err.Error())
	}
	for k, v := range m {
		if string(v) == "null" {
			return invalid(k, "Null is not accepted")
		}
	}
	if raw, ok := m["expectedVersion"]; ok {
		var version int64
		if err = json.Unmarshal(raw, &version); err != nil || version < 1 {
			return invalid("expectedVersion", "Expected a positive exact int64")
		}
	}
	if err = json.Unmarshal(b, out, json.RejectUnknownMembers(true)); err != nil {
		return invalid("body", err.Error())
	}
	return nil
}
func (in *CreateChangeProposalInput) UnmarshalJSON(b []byte) error {
	type plain CreateChangeProposalInput
	return decodeChangeOperation(b, []string{"name", "baseRevisionId", "idempotencyKey"}, (*plain)(in))
}
func (in *PreviewChangeProposalInput) UnmarshalJSON(b []byte) error {
	type plain PreviewChangeProposalInput
	return decodeChangeOperation(b, []string{"expectedVersion", "proposalRevisionId", "commands"}, (*plain)(in))
}
func (in *ApplyChangeProposalInput) UnmarshalJSON(b []byte) error {
	type plain ApplyChangeProposalInput
	return decodeChangeOperation(b, []string{"expectedVersion", "proposalRevisionId", "commands", "candidateHash", "idempotencyKey"}, (*plain)(in))
}
func (in *RestoreChangeProposalInput) UnmarshalJSON(b []byte) error {
	type plain RestoreChangeProposalInput
	return decodeChangeOperation(b, []string{"expectedVersion", "proposalRevisionId", "restoreRevisionId", "idempotencyKey"}, (*plain)(in))
}

// ChangeProposalCommand exposes a closed payload per type. The custom codec
// keeps the wire flat and preserves explicit null only on nullable fields.
type ChangeProposalCommand struct {
	Type                string                    `json:"type"`
	CommandID           string                    `json:"commandId"`
	Reason              string                    `json:"reason"`
	ID                  string                    `json:"id,omitempty"`
	Kind                string                    `json:"kind,omitempty"`
	RecordType          string                    `json:"recordType,omitempty"`
	Name                string                    `json:"name,omitempty"`
	ParentID            *string                   `json:"parentId"`
	Attributes          map[string]jsontext.Value `json:"attributes,omitempty"`
	Update              *ChangeNodeUpdate         `json:"update,omitzero"`
	From                string                    `json:"from,omitempty"`
	To                  string                    `json:"to,omitempty"`
	ColumnID            string                    `json:"columnId,omitempty"`
	FacetKey            string                    `json:"facetKey,omitempty"`
	Change              *ChangeColumnGroup        `json:"change,omitzero"`
	Action              string                    `json:"action,omitempty"`
	ConstraintID        string                    `json:"constraintId,omitempty"`
	IndexID             string                    `json:"indexId,omitempty"`
	TableID             string                    `json:"tableId,omitempty"`
	Definition          jsontext.Value            `json:"definition,omitzero"`
	StepID              string                    `json:"stepId,omitempty"`
	EdgeID              string                    `json:"edgeId,omitempty"`
	MappingID           string                    `json:"mappingId,omitempty"`
	Sources             []LineageValueRef         `json:"sources,omitempty"`
	Destination         *LineageValueRef          `json:"destination,omitzero"`
	Transform           *LineageTransform         `json:"transform,omitzero"`
	AnalysisStatus      string                    `json:"analysisStatus,omitempty"`
	Gaps                []string                  `json:"gaps,omitempty"`
	Transport           *LineageTransport         `json:"transport,omitzero"`
	Description         string                    `json:"description,omitempty"`
	Artifact            *ArtifactKey              `json:"artifact,omitzero"`
	RevisionID          string                    `json:"revisionId,omitempty"`
	APIBindings         []APIPinBindingInput      `json:"apiBindings,omitzero"`
	EditorBindings      []EditorBindingInput      `json:"editorBindings,omitzero"`
	Target              *ChangeIdentityTarget     `json:"target,omitzero"`
	ExpectedExternalKey *string                   `json:"expectedExternalKey"`
	NewExternalKey      string                    `json:"newExternalKey,omitempty"`
	Criteria            []ChangeCriterion         `json:"criteria,omitempty"`
}
type ChangeNodeUpdate struct {
	Kind       string                    `json:"kind"`
	Group      string                    `json:"group"`
	Attributes map[string]jsontext.Value `json:"attributes,omitzero"`
	ParentID   *string                   `json:"parentId"`
}
type ChangeColumnGroup struct {
	Group             string         `json:"group"`
	NativeType        jsontext.Value `json:"nativeType,omitzero"`
	TypeFamily        jsontext.Value `json:"typeFamily,omitzero"`
	Nullable          jsontext.Value `json:"nullable,omitzero"`
	DefaultExpression jsontext.Value `json:"defaultExpression,omitzero"`
}

func (r ChangeProposalRevision) MarshalJSON() ([]byte, error) {
	type plain ChangeProposalRevision
	if r.ArtifactContextV3 == nil {
		return json.Marshal(plain(r))
	}
	if len(r.ArtifactPins) != 0 {
		return nil, invalid("pins", "V3 pins must remain namespaced")
	}
	raw, err := EncodeArtifactContextV3(*r.ArtifactContextV3)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		plain
		ArtifactContext jsontext.Value `json:"artifactContext"`
	}{plain(r), raw})
}
