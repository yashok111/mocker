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
	MaxRevisionBytes        = 256 << 20
	MaxProjectStagingBytes  = 512 << 20
	MaxOpenImportSessions   = 5
	DefaultGraphPageSize    = 100
	MaxGraphPageSize        = 500
	MaxExternalKeyLength    = 200
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
	Mode            string          `json:"mode,omitempty"`
	RepositoryID    *string         `json:"repositoryId,omitzero"`
	GraphScope      *GraphScope     `json:"graphScope,omitzero"`
	ExpectedVersion int64           `json:"expectedVersion"`
	BaseRevisionID  string          `json:"baseRevisionId"`
	IdempotencyKey  string          `json:"idempotencyKey"`
	Manifest        SourceManifest  `json:"manifest"`
	Inventory       []InventoryItem `json:"inventory"`
}
type ImportSession struct {
	// Legacy receipts retain their original response shape. Ordinary session loads
	// never set this field, so current reads expose the additive B0.3 metadata.
	legacyReceiptJSON  string
	Mode               string          `json:"mode"`
	GraphScope         *GraphScope     `json:"graphScope"`
	ID                 string          `json:"id"`
	ProjectID          string          `json:"projectId"`
	BaseRevisionID     string          `json:"baseRevisionId"`
	RepositoryID       string          `json:"repositoryId"`
	SnapshotID         string          `json:"snapshotId"`
	ManifestHash       string          `json:"manifestHash"`
	Manifest           SourceManifest  `json:"manifest"`
	Inventory          []InventoryItem `json:"inventory"`
	State              string          `json:"state"`
	Version            int64           `json:"version"`
	CandidateHash      *string         `json:"candidateHash"`
	AcceptedBatchCount int64           `json:"acceptedBatchCount"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

// MarshalJSON preserves an acknowledged legacy response without changing the
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
	Identities []RecordIdentity `json:"identities"`
}
type ImportNode struct {
	ExternalKey  string                    `json:"externalKey"`
	Kind         string                    `json:"kind"`
	Name         string                    `json:"name"`
	ParentKey    *string                   `json:"parentKey,omitzero"`
	Attributes   map[string]jsontext.Value `json:"attributes"`
	EvidenceKeys []string                  `json:"evidenceKeys"`
}
type ImportEdge struct {
	ExternalKey  string                    `json:"externalKey"`
	Kind         string                    `json:"kind"`
	FromKey      string                    `json:"fromKey"`
	ToKey        string                    `json:"toKey"`
	Attributes   map[string]jsontext.Value `json:"attributes"`
	EvidenceKeys []string                  `json:"evidenceKeys"`
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
	Identity *ImportIdentityMap `json:"identity,omitzero"`
	Deletion *ImportDeletion    `json:"deletion,omitzero"`
	Op       string             `json:"op"`
	Node     *ImportNode        `json:"node,omitzero"`
	Edge     *ImportEdge        `json:"edge,omitzero"`
	Evidence *ImportEvidence    `json:"evidence,omitzero"`
	Remove   *ImportRemove      `json:"remove,omitzero"`
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
	ComparisonSummary     *ComparisonSummary `json:"comparisonSummary"`
	SourceChangeCount     int64              `json:"sourceChangeCount"`
	IdentityDecisionCount int64              `json:"identityDecisionCount"`
	DeletionDecisionCount int64              `json:"deletionDecisionCount"`
	SessionID             string             `json:"sessionId"`
	Version               int64              `json:"version"`
	State                 string             `json:"state"`
	CandidateHash         *string            `json:"candidateHash"`
	Summary               ImportSummary      `json:"summary"`
	Diagnostics           []ImportDiagnostic `json:"diagnostics"`
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
	Ownership   *AssertionOwnership       `json:"ownership,omitzero"`
	Freshness   *AssertionFreshness       `json:"freshness,omitzero"`
	ID          string                    `json:"id"`
	ExternalKey string                    `json:"externalKey"`
	Kind        string                    `json:"kind"`
	Name        string                    `json:"name"`
	ParentID    *string                   `json:"parentId"`
	Attributes  map[string]jsontext.Value `json:"attributes"`
	EvidenceIDs []string                  `json:"evidenceIds"`
}
type Edge struct {
	Ownership   *AssertionOwnership       `json:"ownership,omitzero"`
	Freshness   *AssertionFreshness       `json:"freshness,omitzero"`
	ID          string                    `json:"id"`
	ExternalKey string                    `json:"externalKey"`
	Kind        string                    `json:"kind"`
	From        string                    `json:"from"`
	To          string                    `json:"to"`
	Attributes  map[string]jsontext.Value `json:"attributes"`
	EvidenceIDs []string                  `json:"evidenceIds"`
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
	ID         string `json:"id,omitempty"`
	RevisionID string `json:"revisionId"`
	RecordType string `json:"recordType"`
	Kind       string `json:"kind,omitempty"`
	Search     string `json:"search,omitempty"`
	ParentID   string `json:"parentId,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	Limit      int    `json:"limit,omitzero"`
	Cursor     string `json:"cursor,omitempty"`
}
type GraphPage struct {
	Nodes      []Node `json:"nodes"`
	Edges      []Edge `json:"edges"`
	NextCursor string `json:"nextCursor"`
}
type EvidenceQueryInput struct {
	EvidenceID string `json:"evidenceId,omitempty"`
	SubjectID  string `json:"subjectId,omitempty"`
	Limit      int    `json:"limit,omitzero"`
	Cursor     string `json:"cursor,omitempty"`
}
type EvidencePage struct {
	Items      []Evidence `json:"items"`
	NextCursor string     `json:"nextCursor"`
}
type RevisionCoverage struct {
	StaleCounts        StaleCounts      `json:"staleCounts"`
	ReconciliationGaps []string         `json:"reconciliationGaps"`
	Coverage           Coverage         `json:"coverage"`
	Inventory          []InventoryItem  `json:"inventory"`
	Snapshots          []SourceSnapshot `json:"snapshots"`
}
