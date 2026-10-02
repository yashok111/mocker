package backendmodel

type GraphScope struct {
	Profile string   `json:"profile"`
	Status  string   `json:"status"`
	Gaps    []string `json:"gaps"`
}
type ImportIdentityMap struct {
	RecordType      string   `json:"recordType"`
	FromExternalKey string   `json:"fromExternalKey"`
	ToExternalKey   string   `json:"toExternalKey"`
	ExpectedID      string   `json:"expectedId"`
	Reason          string   `json:"reason"`
	EvidenceKeys    []string `json:"evidenceKeys"`
}
type ImportDeletion struct {
	RecordType  string `json:"recordType"`
	ExternalKey string `json:"externalKey"`
	ExpectedID  string `json:"expectedId"`
	Reason      string `json:"reason"`
}
type AssertionOwnership struct {
	RepositoryID      string `json:"repositoryId"`
	ProviderNamespace string `json:"providerNamespace"`
	Profile           string `json:"profile"`
}
type AssertionFreshness struct {
	Status              string   `json:"status"`
	ConfirmedSnapshotID string   `json:"confirmedSnapshotId"`
	Reasons             []string `json:"reasons"`
}
type HistoricalEvidenceRef struct {
	RevisionID string `json:"revisionId"`
	EvidenceID string `json:"evidenceId"`
}
type RevisionState struct {
	Revision           Revision
	Nodes              []Node
	Edges              []Edge
	Evidence           []Evidence
	Sources            []SourceSnapshot
	Inventory          []InventoryItem
	APIArtifactContext *APIArtifactContext
	ArtifactContext    *ArtifactContext
}
type SourceChange struct {
	Path              string             `json:"path"`
	Kind              string             `json:"kind"`
	AdditionConfirmed bool               `json:"additionConfirmed"`
	DeletionConfirmed bool               `json:"deletionConfirmed"`
	Before            *SourceFileSummary `json:"before"`
	After             *SourceFileSummary `json:"after"`
}
type SourceFileSummary struct {
	SnapshotID     string `json:"snapshotId"`
	Path           string `json:"path"`
	ContentHash    string `json:"contentHash"`
	AnalysisStatus string `json:"analysisStatus"`
}
type HistoricalSubjectRef struct {
	RevisionID string `json:"revisionId"`
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
}
type StagedEvidenceRef struct {
	SnapshotID  string `json:"snapshotId"`
	EvidenceKey string `json:"evidenceKey"`
}
type IdentityDecision struct {
	Command         ImportIdentityMap       `json:"command"`
	Resolved        bool                    `json:"resolved"`
	OldSubject      *HistoricalSubjectRef   `json:"oldSubject"`
	OldEvidenceRefs []HistoricalEvidenceRef `json:"oldEvidenceRefs"`
	EvidenceRefs    []StagedEvidenceRef     `json:"evidenceRefs"`
}
type DeletionDecision struct {
	Command         ImportDeletion          `json:"command"`
	Resolved        bool                    `json:"resolved"`
	OldSubject      *HistoricalSubjectRef   `json:"oldSubject"`
	OldEvidenceRefs []HistoricalEvidenceRef `json:"oldEvidenceRefs"`
}
type ImportChangeItem struct {
	RecordType string            `json:"recordType"`
	Source     *SourceChange     `json:"source,omitzero"`
	Identity   *IdentityDecision `json:"identity,omitzero"`
	Deletion   *DeletionDecision `json:"deletion,omitzero"`
}
type ImportChangesPage struct {
	SessionID      string             `json:"sessionId"`
	PreviewVersion int64              `json:"previewVersion"`
	CandidateHash  *string            `json:"candidateHash"`
	RecordType     string             `json:"recordType"`
	Items          []ImportChangeItem `json:"items"`
	NextCursor     string             `json:"nextCursor"`
}
type ImportChangesInput struct {
	PreviewVersion int64  `json:"previewVersion"`
	RecordType     string `json:"recordType"`
	Limit          int    `json:"limit,omitzero"`
	Cursor         string `json:"cursor,omitempty"`
}
type CompareRevisionsInput struct {
	FromRevisionID string `json:"fromRevisionId"`
	ToRevisionID   string `json:"toRevisionId"`
	RecordType     string `json:"recordType,omitempty"`
	ChangeKind     string `json:"changeKind,omitempty"`
	Limit          int    `json:"limit,omitzero"`
	Cursor         string `json:"cursor,omitempty"`
}
type ComparisonRef struct {
	ProjectID  string `json:"projectId"`
	RevisionID string `json:"revisionId"`
	RecordType string `json:"recordType"`
	ID         string `json:"id,omitempty"`
	SnapshotID string `json:"snapshotId,omitempty"`
	Path       string `json:"path,omitempty"`
}
type ComparisonItem struct {
	EditorArtifactBefore *EditorArtifactSide `json:"editorArtifactBefore,omitzero"`
	EditorArtifactAfter  *EditorArtifactSide `json:"editorArtifactAfter,omitzero"`
	ArtifactGroupBefore  *ArtifactGroupSide  `json:"artifactGroupBefore,omitzero"`
	ArtifactGroupAfter   *ArtifactGroupSide  `json:"artifactGroupAfter,omitzero"`
	ArtifactBefore       *ArtifactRef        `json:"artifactBefore,omitzero"`
	ArtifactAfter        *ArtifactRef        `json:"artifactAfter,omitzero"`
	ContextChanged       bool                `json:"contextChanged,omitzero"`
	RecordType           string              `json:"recordType"`
	ID                   string              `json:"id"`
	ChangeKinds          []string            `json:"changeKinds"`
	ChangedPaths         []string            `json:"changedPaths"`
	Before               *ComparisonRef      `json:"before"`
	After                *ComparisonRef      `json:"after"`
	NameBefore           *string             `json:"nameBefore"`
	NameAfter            *string             `json:"nameAfter"`
	KeyBefore            *string             `json:"keyBefore"`
	KeyAfter             *string             `json:"keyAfter"`
	FreshnessBefore      *AssertionFreshness `json:"freshnessBefore"`
	FreshnessAfter       *AssertionFreshness `json:"freshnessAfter"`
}
type ComparisonCounts struct {
	Added    int64 `json:"added"`
	Removed  int64 `json:"removed"`
	Modified int64 `json:"modified"`
}
type ComparisonSummary struct {
	Artifacts        *ComparisonCounts `json:"artifacts,omitzero"`
	Nodes            ComparisonCounts  `json:"nodes"`
	Edges            ComparisonCounts  `json:"edges"`
	Evidence         ComparisonCounts  `json:"evidence"`
	SourceChanges    int64             `json:"sourceChanges"`
	IdentityMappings int64             `json:"identityMappings"`
	FreshnessChanges int64             `json:"freshnessChanges"`
}
type ComparisonPin struct {
	RevisionID        string   `json:"revisionId"`
	SemanticHash      string   `json:"semanticHash"`
	PrimarySnapshotID *string  `json:"primarySnapshotId"`
	ManifestHash      *string  `json:"manifestHash"`
	SourceSnapshotIDs []string `json:"sourceSnapshotIds"`
}
type RevisionComparison struct {
	From              ComparisonPin     `json:"from"`
	To                ComparisonPin     `json:"to"`
	ComparisonVersion int64             `json:"comparisonVersion"`
	ComparisonHash    string            `json:"comparisonHash"`
	Summary           ComparisonSummary `json:"summary"`
	CoverageBefore    Coverage          `json:"coverageBefore"`
	CoverageAfter     Coverage          `json:"coverageAfter"`
	Limitations       []string          `json:"limitations"`
	Items             []ComparisonItem  `json:"items"`
	NextCursor        string            `json:"nextCursor"`
}
type RecordSide struct {
	EditorArtifact                   *EditorArtifactSide `json:"editorArtifact,omitzero"`
	ArtifactGroup                    *ArtifactGroupSide  `json:"artifactGroup,omitzero"`
	Artifact                         *ArtifactRef        `json:"artifact,omitzero"`
	RecordType, ID, SnapshotID, Path string
	Name, Key                        *string
	Freshness                        *AssertionFreshness
}
type RecordDelta struct {
	ContextChanged            bool `json:"contextChanged,omitzero"`
	RecordType, ID            string
	ChangeKinds, ChangedPaths []string
	Before, After             *RecordSide
}
type RevisionDelta struct {
	Summary ComparisonSummary
	Changes []RecordDelta
}
type StaleCounts struct {
	Nodes    int64 `json:"nodes"`
	Edges    int64 `json:"edges"`
	Evidence int64 `json:"evidence"`
}
