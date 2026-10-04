package backendmodel

import "encoding/json/jsontext"

const (
	ComposedProfile       = "composed-source-v1"
	ComposedSchemaVersion = "6"
	WholeSourcePolicy     = "whole-source-v1"
	MaxSourceRepositories = 32
	MaxSourceProviders    = 8
)

type SourceScope struct {
	Kind                  string `json:"kind"`
	RepositoryID          string `json:"repositoryId,omitempty"`
	ProviderNamespace     string `json:"providerNamespace,omitempty"`
	FromProviderNamespace string `json:"fromProviderNamespace,omitempty"`
	FromSnapshotID        string `json:"fromSnapshotId,omitempty"`
	Reason                string `json:"reason,omitempty"`
}
type SourceScopeStatus struct {
	Status string   `json:"status"`
	Gaps   []string `json:"gaps"`
}
type SourcePartition struct {
	RepositoryID      string            `json:"repositoryId"`
	ProviderNamespace string            `json:"providerNamespace"`
	SnapshotID        string            `json:"snapshotId"`
	Provider          SourceProvider    `json:"provider"`
	Inventory         []InventoryItem   `json:"inventory"`
	ScopeStatus       SourceScopeStatus `json:"scopeStatus"`
}
type SourceVector struct {
	DocumentVersion string            `json:"documentVersion"`
	Partitions      []SourcePartition `json:"partitions"`
	Snapshots       []SourceSnapshot  `json:"snapshots"`
}
type BaseAssertionRef struct {
	RepositoryID      string `json:"repositoryId"`
	ProviderNamespace string `json:"providerNamespace"`
	RecordType        string `json:"recordType"`
	ExternalKey       string `json:"externalKey"`
	ExpectedID        string `json:"expectedId"`
	AssertionHash     string `json:"assertionHash"`
}
type ImportRecordRef struct {
	LocalKey string            `json:"localKey,omitempty"`
	Base     *BaseAssertionRef `json:"base,omitzero"`
}
type TypedSourcePropertySelector struct {
	Kind       string `json:"kind"`
	Group      string `json:"group,omitempty"`
	FacetKey   string `json:"facetKey,omitempty"`
	Collection string `json:"collection,omitempty"`
}
type SourceAssertionPayload struct {
	RecordType string                    `json:"recordType"`
	Kind       string                    `json:"kind"`
	Name       string                    `json:"name,omitempty"`
	ParentID   *string                   `json:"parentId,omitzero"`
	From       string                    `json:"from,omitempty"`
	To         string                    `json:"to,omitempty"`
	Attributes map[string]jsontext.Value `json:"attributes"`
}
type SourceDependencyBinding struct {
	Basis          string                      `json:"basis"`
	BaseRevisionID string                      `json:"baseRevisionId,omitempty"`
	Target         BaseAssertionRef            `json:"target"`
	Site           string                      `json:"site"`
	Property       TypedSourcePropertySelector `json:"property"`
	ValueContext   *LineageValueRef            `json:"valueContext,omitzero"`
}
type ProviderAssertion struct {
	RecordType       string                    `json:"recordType"`
	RecordID         string                    `json:"recordId"`
	Owner            AssertionOwnership        `json:"owner"`
	ExternalKey      string                    `json:"externalKey"`
	AssertionHash    string                    `json:"assertionHash"`
	Payload          SourceAssertionPayload    `json:"payload"`
	EvidenceIDs      []string                  `json:"evidenceIds"`
	DependencyClaims []SourceDependencyBinding `json:"dependencyClaims"`
	Freshness        AssertionFreshness        `json:"freshness"`
	FieldCurrentness []TypedFieldCurrentness   `json:"fieldCurrentness"`
}
type TypedFieldCurrentness struct {
	Property   TypedSourcePropertySelector `json:"property"`
	Own        AssertionFreshness          `json:"own"`
	Dependency AssertionFreshness          `json:"dependency"`
}
type SourceClaimCurrentness struct {
	RecordType        string                  `json:"recordType"`
	RecordID          string                  `json:"recordId"`
	RepositoryID      string                  `json:"repositoryId"`
	ProviderNamespace string                  `json:"providerNamespace"`
	AssertionHash     string                  `json:"assertionHash"`
	Own               AssertionFreshness      `json:"own"`
	Dependency        AssertionFreshness      `json:"dependency"`
	Fields            []TypedFieldCurrentness `json:"fields"`
}
type SourceClaimIdentity struct {
	DecisionID   string           `json:"decisionId"`
	RecordType   string           `json:"recordType"`
	ExternalKey  string           `json:"externalKey"`
	Target       BaseAssertionRef `json:"target"`
	Reason       string           `json:"reason"`
	EvidenceKeys []string         `json:"evidenceKeys"`
}
type SourceAssertionSelection struct {
	RepositoryID      string `json:"repositoryId"`
	ProviderNamespace string `json:"providerNamespace"`
	AssertionHash     string `json:"assertionHash"`
}
type SourceAssertionResolution struct {
	DecisionID   string                      `json:"decisionId"`
	RecordType   string                      `json:"recordType"`
	ID           string                      `json:"id"`
	Property     TypedSourcePropertySelector `json:"property"`
	ConflictHash string                      `json:"conflictHash"`
	Select       SourceAssertionSelection    `json:"select"`
	Reason       string                      `json:"reason"`
}
type SourcePropertyValue struct {
	Present bool           `json:"present"`
	Value   jsontext.Value `json:"value,omitzero"`
}
type SourcePropertyGroup struct {
	Selector TypedSourcePropertySelector
	Atomic   bool
}
type SourceAssertionConflict struct {
	RecordType   string                      `json:"recordType"`
	ID           string                      `json:"id"`
	Property     TypedSourcePropertySelector `json:"property"`
	ConflictHash string                      `json:"conflictHash"`
	Contenders   []SourceAssertionContender  `json:"contenders"`
}
type SourceAssertionContender struct {
	Owner         AssertionOwnership     `json:"owner"`
	AssertionHash string                 `json:"assertionHash"`
	Value         SourcePropertyValue    `json:"value"`
	EvidenceIDs   []string               `json:"evidenceIds"`
	Currentness   SourceClaimCurrentness `json:"currentness"`
}
type LegacyProofBasis struct {
	DocumentVersion      string                       `json:"documentVersion"`
	BasisHash            string                       `json:"basisHash"`
	SourceSchemaVersion  string                       `json:"sourceSchemaVersion"`
	ProjectID            string                       `json:"projectId"`
	SourceRevisionID     string                       `json:"sourceRevisionId"`
	SourceSemanticHash   string                       `json:"sourceSemanticHash"`
	RecordType           string                       `json:"recordType"`
	RecordID             string                       `json:"recordId"`
	EvidenceID           string                       `json:"evidenceId"`
	RevisionDocumentHash string                       `json:"revisionDocumentHash"`
	SourceDocumentHash   string                       `json:"sourceDocumentHash"`
	SubjectDocumentHash  string                       `json:"subjectDocumentHash"`
	EvidenceDocumentHash string                       `json:"evidenceDocumentHash"`
	Support              string                       `json:"support"`
	Property             *TypedSourcePropertySelector `json:"property,omitzero"`
}
type SourceRevisionContext struct {
	RevisionCoverage
	ViewSchemaVersion string                   `json:"viewSchemaVersion"`
	SourceVector      SourceVector             `json:"sourceVector"`
	ClaimCurrentness  []SourceClaimCurrentness `json:"claimCurrentness"`
	SourceContentHash string                   `json:"sourceContentHash"`
}
type SourceDecision struct {
	Sequence int64         `json:"sequence"`
	State    string        `json:"state"`
	Command  ImportCommand `json:"command"`
}

type SourceBatchCommitment struct {
	AcceptedVersion int64  `json:"acceptedVersion"`
	BatchID         string `json:"batchId"`
	PayloadHash     string `json:"payloadHash"`
	RequestHash     string `json:"requestHash"`
}
type composedCandidate struct {
	IncrementalScope *IncrementalAffectedScope
	Graph            *graphCandidate
	Source           *SourceGraphSnapshot
	Conflicts        []SourceAssertionConflict
	Decisions        []SourceDecision
	LegacyDecisions  []ImportCommand
	BatchCommitments []SourceBatchCommitment
}
