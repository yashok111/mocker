package backendmodel

import "encoding/json/v2"

// These admission records are server-created and are never client wire inputs.
type PreviewAdmission struct {
	CommandIDs []string          `json:"commandIds"`
	CreatedIDs []ChangeRecordRef `json:"createdIds"`
}
type AnalysisSourcePins struct {
	RevisionID        string   `json:"revisionId"`
	SemanticHash      string   `json:"semanticHash"`
	ContentHash       string   `json:"contentHash"`
	SourceVectorHash  string   `json:"sourceVectorHash"`
	SourceSnapshotIDs []string `json:"sourceSnapshotIds"`
}
type AnalysisCommandPreviewInput struct {
	ChangeProposal ProposalReadTarget         `json:"changeProposal"`
	Preview        PreviewChangeProposalInput `json:"preview"`
	CandidateHash  string                     `json:"candidateHash"`
}
type FrozenChangePreview struct {
	DocumentVersion       string                  `json:"documentVersion"`
	ProjectID             string                  `json:"projectId"`
	ChangeProposal        ProposalReadTarget      `json:"changeProposal"`
	ExpectedVersion       int64                   `json:"expectedVersion"`
	BaseRevisionID        string                  `json:"baseRevisionId"`
	BaseSemanticHash      string                  `json:"baseSemanticHash"`
	DraftHash             string                  `json:"draftHash"`
	Commands              []ChangeProposalCommand `json:"commands"`
	CommandsHash          string                  `json:"commandsHash"`
	CandidateHash         string                  `json:"candidateHash"`
	EffectiveSemanticHash string                  `json:"effectiveSemanticHash"`
	SourcePins            AnalysisSourcePins      `json:"sourcePins"`
	SourceSnapshotIDs     []string                `json:"sourceSnapshotIds"`
	ArtifactPins          []ArtifactPin           `json:"artifactPins"`
	ArtifactContext       ArtifactContext         `json:"artifactContext"`
	Admission             PreviewAdmission        `json:"admission"`
	AdmissionHash         string                  `json:"admissionHash"`
}
type AnalysisDocumentBytes struct {
	Key   string
	Bytes int64
}
type AnalysisInputFootprint struct {
	Documents       []AnalysisDocumentBytes
	TotalBytes      int64
	repo            *Repo
	projectID, seal string
}
type EffectiveAnalysisProof struct {
	Status               string
	Boundary             bool
	Reasons, EvidenceIDs []string
	Assertions           []BaseAssertionRef
}

// Frozen server documents preserve the same legacy/tagged context representation
// as ChangeProposalRevision. The public ArtifactContext decoder is tagged-only.
func (f *FrozenChangePreview) UnmarshalJSON(raw []byte) error {
	type plain FrozenChangePreview
	type contextValue ArtifactContext
	var next FrozenChangePreview
	decoded := struct {
		*plain
		ArtifactContext contextValue `json:"artifactContext"`
	}{plain: (*plain)(&next)}
	if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	next.ArtifactContext = ArtifactContext(decoded.ArtifactContext)
	if err := next.ArtifactContext.Validate(next.ArtifactPins); err != nil {
		return err
	}
	*f = next
	return nil
}
