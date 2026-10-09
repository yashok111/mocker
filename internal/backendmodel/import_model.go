package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"time"
)

const (
	GraphProfile            = "foundation-graph-v1"
	MaxImportCommands       = 500
	MaxImportBatchBytes     = 1 << 20
	MaxManifestFiles        = 100000
	MaxEvidenceSnippetBytes = 4096
	MaxRevisionNodes        = 50000
	MaxRevisionEdges        = 200000
	MaxRevisionEvidence     = 250000
	// Source ownership and freshness metadata expand the audited Education
	// Platform corpus beyond 256 MiB; keep that complete graph bounded at 384 MiB.
	MaxRevisionBytes       = 384 << 20
	MaxProjectStagingBytes = 1 << 30
	MaxOpenImportSessions  = 5
	DefaultGraphPageSize   = 100
	MaxGraphPageSize       = 500
	MaxExternalKeyLength   = 200
)

func SupportedNodeKinds() []string {
	return []string{"system", "service", "module", "external_system", "datastore", "symbol", "http_operation", "handler", "unresolved_target"}
}
func SupportedEdgeKinds() []string { return []string{"contains", "handles", "calls", "derived_from"} }

type SourceProvider struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Namespace   string   `json:"namespace"`
	Method      string   `json:"method"`
	Profiles    []string `json:"profiles"`
	Limitations []string `json:"limitations"`
}
type ManifestFile struct {
	Path           string `json:"path"`
	ContentHash    string `json:"contentHash"`
	FileType       string `json:"fileType"`
	AnalysisStatus string `json:"analysisStatus"`
	Reason         string `json:"reason,omitempty"`
}
type SnapshotManifest struct {
	Commit      *string        `json:"commit,omitzero"`
	Dirty       bool           `json:"dirty"`
	Consistency string         `json:"consistency"`
	CapturedAt  time.Time      `json:"capturedAt"`
	Files       []ManifestFile `json:"files"`
}
type SourceManifest struct {
	RepositoryName string           `json:"repositoryName"`
	Provider       SourceProvider   `json:"provider"`
	Snapshot       SnapshotManifest `json:"snapshot"`
}
type SourceSnapshot struct {
	Role         string         `json:"role"`
	ID           string         `json:"id"`
	RepositoryID string         `json:"repositoryId"`
	ManifestHash string         `json:"manifestHash"`
	Provider     SourceProvider `json:"provider"`
	SnapshotManifest
}
type InventoryItem struct {
	Category        string   `json:"category"`
	Status          string   `json:"status"`
	KnownCount      int64    `json:"knownCount"`
	Denominator     *int64   `json:"denominator"`
	DiscoverySource string   `json:"discoverySource"`
	Gaps            []string `json:"gaps"`
	Reason          string   `json:"reason"`
}
type BeginImportInput struct {
	ChangeManifest   *ChangeManifest         `json:"changeManifest,omitzero"`
	SourceScope      *SourceScope            `json:"sourceScope,omitzero"`
	ScopeStatus      *SourceScopeStatus      `json:"scopeStatus,omitzero"`
	SyncPolicy       string                  `json:"syncPolicy,omitempty"`
	Profile          string                  `json:"profile,omitempty"`
	ProfileExtension *ImportProfileExtension `json:"profileExtension,omitzero"`
	Mode             string                  `json:"mode,omitempty"`
	RepositoryID     *string                 `json:"repositoryId,omitzero"`
	GraphScope       *GraphScope             `json:"graphScope,omitzero"`
	ExpectedVersion  int64                   `json:"expectedVersion"`
	BaseRevisionID   string                  `json:"baseRevisionId"`
	IdempotencyKey   string                  `json:"idempotencyKey"`
	Manifest         SourceManifest          `json:"manifest"`
	Inventory        []InventoryItem         `json:"inventory"`
}
type ImportSession struct {
	ChangeManifest    *ChangeManifest    `json:"changeManifest,omitzero"`
	SourceScope       *SourceScope       `json:"sourceScope,omitzero"`
	ScopeStatus       *SourceScopeStatus `json:"scopeStatus,omitzero"`
	SyncPolicy        string             `json:"syncPolicy,omitempty"`
	BaseVectorHash    string             `json:"baseVectorHash,omitempty"`
	SelectedPartition *SourcePartition   `json:"selectedPartition,omitzero"`
	BasePartition     *SourcePartition   `json:"basePartition,omitzero"`
	// Acknowledged receipts retain their original response bytes. Ordinary session
	// loads never set this field, so current reads expose the additive metadata.
	legacyReceiptJSON  string
	Profile            string                  `json:"profile"`
	ProfileExtension   *ImportProfileExtension `json:"profileExtension,omitzero"`
	Mode               string                  `json:"mode"`
	GraphScope         *GraphScope             `json:"graphScope"`
	ID                 string                  `json:"id"`
	ProjectID          string                  `json:"projectId"`
	BaseRevisionID     string                  `json:"baseRevisionId"`
	RepositoryID       string                  `json:"repositoryId"`
	SnapshotID         string                  `json:"snapshotId"`
	ManifestHash       string                  `json:"manifestHash"`
	Manifest           SourceManifest          `json:"manifest"`
	Inventory          []InventoryItem         `json:"inventory"`
	State              string                  `json:"state"`
	Version            int64                   `json:"version"`
	CandidateHash      *string                 `json:"candidateHash"`
	AcceptedBatchCount int64                   `json:"acceptedBatchCount"`
	CreatedAt          time.Time               `json:"createdAt"`
	UpdatedAt          time.Time               `json:"updatedAt"`
}

// MarshalJSON preserves an acknowledged response without changing the
// current session representation or rewriting the stored receipt.
func (s ImportSession) MarshalJSON() ([]byte, error) {
	if s.legacyReceiptJSON != "" {
		return []byte(s.legacyReceiptJSON), nil
	}
	type currentSession ImportSession
	return json.Marshal(currentSession(s))
}

type ImportPage struct {
	Items      []ImportSession `json:"items"`
	NextCursor string          `json:"nextCursor"`
}
type BatchSummary struct {
	BatchID         string `json:"batchId"`
	PayloadHash     string `json:"payloadHash"`
	AcceptedVersion int64  `json:"acceptedVersion"`
}
type ImportStatus struct {
	Preview             *ImportPreview `json:"preview"`
	CommittedRevisionID *string        `json:"committedRevisionId"`
	Session             ImportSession  `json:"session"`
	AcceptedBatches     []BatchSummary `json:"acceptedBatches"`
	NextCursor          string         `json:"nextCursor"`
}
type RecordIdentity struct {
	RecordType  string `json:"recordType"`
	ExternalKey string `json:"externalKey"`
	ID          string `json:"id"`
}
type BatchReceipt struct {
	BatchSummary
	DecisionIDs []string         `json:"decisionIds,omitempty"`
	Identities  []RecordIdentity `json:"identities"`
}
type ImportNode struct {
	parentKeyPresent bool
	nullParentRef    bool
	ParentRef        *ImportRecordRef          `json:"parentRef,omitzero"`
	ExternalKey      string                    `json:"externalKey"`
	Kind             string                    `json:"kind"`
	Name             string                    `json:"name"`
	ParentKey        *string                   `json:"parentKey,omitzero"`
	Attributes       map[string]jsontext.Value `json:"attributes"`
	EvidenceKeys     []string                  `json:"evidenceKeys"`
}
type ImportEdge struct {
	legacyKeysPresent bool
	nullEndpointRef   bool
	FromRef           *ImportRecordRef          `json:"fromRef,omitzero"`
	ToRef             *ImportRecordRef          `json:"toRef,omitzero"`
	ExternalKey       string                    `json:"externalKey"`
	Kind              string                    `json:"kind"`
	FromKey           string                    `json:"fromKey,omitempty"`
	ToKey             string                    `json:"toKey,omitempty"`
	Attributes        map[string]jsontext.Value `json:"attributes"`
	EvidenceKeys      []string                  `json:"evidenceKeys"`
}
type EvidenceSource struct {
	RepositoryID string  `json:"repositoryId"`
	SnapshotID   string  `json:"snapshotId"`
	File         string  `json:"file"`
	ContentHash  string  `json:"contentHash"`
	Symbol       *string `json:"symbol,omitzero"`
	StartLine    *int64  `json:"startLine,omitzero"`
	EndLine      *int64  `json:"endLine,omitzero"`
}
type ImportEvidence struct {
	ExternalKey  string         `json:"externalKey"`
	SubjectType  string         `json:"subjectType"`
	SubjectKey   string         `json:"subjectKey"`
	PropertyPath *string        `json:"propertyPath,omitzero"`
	Method       string         `json:"method"`
	Status       string         `json:"status"`
	Source       EvidenceSource `json:"source"`
	Explanation  string         `json:"explanation"`
	Snippet      *string        `json:"snippet,omitzero"`
}
type ImportRemove struct {
	RecordType  string `json:"recordType"`
	ExternalKey string `json:"externalKey"`
}
type ImportCommand struct {
	ClaimIdentity *SourceClaimIdentity       `json:"claimIdentity,omitzero"`
	Resolution    *SourceAssertionResolution `json:"resolution,omitzero"`
	Identity      *ImportIdentityMap         `json:"identity,omitzero"`
	Deletion      *ImportDeletion            `json:"deletion,omitzero"`
	Op            string                     `json:"op"`
	Node          *ImportNode                `json:"node,omitzero"`
	Edge          *ImportEdge                `json:"edge,omitzero"`
	Evidence      *ImportEvidence            `json:"evidence,omitzero"`
	Remove        *ImportRemove              `json:"remove,omitzero"`
}
type ImportBatchInput struct {
	ExpectedImportVersion int64           `json:"expectedImportVersion"`
	PayloadHash           string          `json:"payloadHash"`
	Commands              []ImportCommand `json:"commands"`
}
type PreviewImportInput struct {
	ExpectedImportVersion int64  `json:"expectedImportVersion"`
	BaseRevisionID        string `json:"baseRevisionId"`
}
type ImportSummary struct {
	Nodes      int64 `json:"nodes"`
	Edges      int64 `json:"edges"`
	Evidence   int64 `json:"evidence"`
	Unresolved int64 `json:"unresolved"`
}
type ImportDiagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
type ImportPreview struct {
	Preflight             *ImportPreflight          `json:"preflight,omitzero"`
	AffectedScope         *IncrementalAffectedScope `json:"affectedScope,omitzero"`
	ModelSchemaVersion    string                    `json:"modelSchemaVersion"`
	ProfileExtension      *ImportProfileExtension   `json:"profileExtension,omitzero"`
	ComparisonSummary     *ComparisonSummary        `json:"comparisonSummary"`
	SourceChangeCount     int64                     `json:"sourceChangeCount"`
	IdentityDecisionCount int64                     `json:"identityDecisionCount"`
	DeletionDecisionCount int64                     `json:"deletionDecisionCount"`
	SessionID             string                    `json:"sessionId"`
	Version               int64                     `json:"version"`
	State                 string                    `json:"state"`
	CandidateHash         *string                   `json:"candidateHash"`
	Summary               ImportSummary             `json:"summary"`
	Diagnostics           []ImportDiagnostic        `json:"diagnostics"`
}
type CommitImportInput struct {
	ExpectedVersion       int64  `json:"expectedVersion"`
	ExpectedImportVersion int64  `json:"expectedImportVersion"`
	CandidateHash         string `json:"candidateHash"`
	IdempotencyKey        string `json:"idempotencyKey"`
}
type ImportCommitResult struct {
	Project   Project  `json:"project"`
	Revision  Revision `json:"revision"`
	SessionID string   `json:"sessionId"`
}
type AbortImportInput struct {
	ExpectedImportVersion int64  `json:"expectedImportVersion"`
	IdempotencyKey        string `json:"idempotencyKey"`
}
type Node struct {
	Source          *SourceReadContext        `json:"source,omitzero"`
	FacetComparison *FacetComparison          `json:"facetComparison,omitzero"`
	Ownership       *AssertionOwnership       `json:"ownership,omitzero"`
	Freshness       *AssertionFreshness       `json:"freshness,omitzero"`
	ID              string                    `json:"id"`
	ExternalKey     string                    `json:"externalKey"`
	Kind            string                    `json:"kind"`
	Name            string                    `json:"name"`
	ParentID        *string                   `json:"parentId"`
	Attributes      map[string]jsontext.Value `json:"attributes"`
	EvidenceIDs     []string                  `json:"evidenceIds"`
}
type Edge struct {
	Source          *SourceReadContext        `json:"source,omitzero"`
	FacetComparison *FacetComparison          `json:"facetComparison,omitzero"`
	Ownership       *AssertionOwnership       `json:"ownership,omitzero"`
	Freshness       *AssertionFreshness       `json:"freshness,omitzero"`
	ID              string                    `json:"id"`
	ExternalKey     string                    `json:"externalKey"`
	Kind            string                    `json:"kind"`
	From            string                    `json:"from"`
	To              string                    `json:"to"`
	Attributes      map[string]jsontext.Value `json:"attributes"`
	EvidenceIDs     []string                  `json:"evidenceIds"`
}
type Evidence struct {
	Ownership    *AssertionOwnership `json:"ownership,omitzero"`
	Freshness    *AssertionFreshness `json:"freshness,omitzero"`
	ID           string              `json:"id"`
	ExternalKey  string              `json:"externalKey"`
	SubjectID    string              `json:"subjectId"`
	PropertyPath *string             `json:"propertyPath,omitzero"`
	Method       string              `json:"method"`
	Status       string              `json:"status"`
	Source       EvidenceSource      `json:"source"`
	Explanation  string              `json:"explanation"`
	Snippet      *string             `json:"snippet,omitzero"`
}
type GraphQueryInput struct {
	ServiceID        string                     `json:"serviceId,omitempty"`
	SourceSnapshotID string                     `json:"sourceSnapshotId,omitempty"`
	Certainty        string                     `json:"certainty,omitempty"`
	ChangeProposal   *ProposalReadTarget        `json:"changeProposal,omitzero"`
	ImportCandidate  *ImportCandidateReadTarget `json:"importCandidate,omitzero"`
	ID               string                     `json:"id,omitempty"`
	RevisionID       string                     `json:"revisionId,omitempty"`
	Proposal         *ProposalReadTarget        `json:"proposal,omitzero"`
	RecordType       string                     `json:"recordType"`
	Kind             string                     `json:"kind,omitempty"`
	Search           string                     `json:"search,omitempty"`
	ParentID         string                     `json:"parentId,omitempty"`
	From             string                     `json:"from,omitempty"`
	To               string                     `json:"to,omitempty"`
	Limit            int                        `json:"limit,omitzero"`
	Cursor           string                     `json:"cursor,omitempty"`
}
type EffectiveEdgeName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type GraphPage struct {
	Total              *int                     `json:"total,omitzero"`
	EdgeNames          []EffectiveEdgeName      `json:"edgeNames,omitempty"`
	Origins            []EffectiveFieldOrigin   `json:"origins,omitempty"`
	Identities         []EffectiveIdentity      `json:"identities,omitempty"`
	BaselineEvidence   []EffectiveEvidenceBasis `json:"baselineEvidence,omitempty"`
	Target             *BackendReadTarget       `json:"target,omitzero"`
	Pins               *EffectiveGraphPins      `json:"pins,omitzero"`
	Source             *SourceReadContext       `json:"source,omitzero"`
	ViewSchemaVersion  string                   `json:"viewSchemaVersion,omitempty"`
	ProposalPins       *ProposalReadPins        `json:"proposalPins,omitzero"`
	ProposalProjection *ProposalGraphProjection `json:"proposalProjection,omitzero"`
	Nodes              []Node                   `json:"nodes"`
	Edges              []Edge                   `json:"edges"`
	NextCursor         string                   `json:"nextCursor"`
}
type EvidenceQueryInput struct {
	EvidenceID string `json:"evidenceId,omitempty"`
	SubjectID  string `json:"subjectId,omitempty"`
	Limit      int    `json:"limit,omitzero"`
	Cursor     string `json:"cursor,omitempty"`
}
type EvidencePage struct {
	Basis             string                   `json:"basis,omitempty"`
	BaselineEvidence  []EffectiveEvidenceBasis `json:"baselineEvidence,omitempty"`
	Target            *BackendReadTarget       `json:"target,omitzero"`
	Pins              *EffectiveGraphPins      `json:"pins,omitzero"`
	Source            *SourceReadContext       `json:"source,omitzero"`
	ViewSchemaVersion string                   `json:"viewSchemaVersion,omitempty"`
	ProposalPins      *ProposalReadPins        `json:"proposalPins,omitzero"`
	Items             []Evidence               `json:"items"`
	NextCursor        string                   `json:"nextCursor"`
}
type RevisionCoverage struct {
	Target             *BackendReadTarget  `json:"target,omitzero"`
	Pins               *EffectiveGraphPins `json:"pins,omitzero"`
	Source             *SourceReadContext  `json:"source,omitzero"`
	ViewSchemaVersion  string              `json:"viewSchemaVersion,omitempty"`
	ProposalPins       *ProposalReadPins   `json:"proposalPins,omitzero"`
	StaleCounts        StaleCounts         `json:"staleCounts"`
	ReconciliationGaps []string            `json:"reconciliationGaps"`
	Coverage           Coverage            `json:"coverage"`
	Inventory          []InventoryItem     `json:"inventory"`
	Snapshots          []SourceSnapshot    `json:"snapshots"`
}
