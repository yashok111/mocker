package backendmodel

import "encoding/json/jsontext"

type SourceGraphSnapshot struct {
	rawAssertions     map[string]jsontext.Value
	proofIndex        *sourceProofIndex
	legacyBasisBytes  int
	State             RevisionState
	SourceVector      *SourceVector
	Assertions        []ProviderAssertion
	Selections        []SourceAssertionResolution
	Currentness       []SourceClaimCurrentness
	Identities        []QualifiedSourceIdentity
	LegacyProofBases  []LegacyProofBasis
	RawEvidence       map[string]jsontext.Value
	SourceContentHash string
}
type SourceStructuralGraph struct {
	SchemaVersion string
	Nodes         []Node
	Edges         []Edge
}
type QualifiedSourceIdentity struct {
	RecordType        string `json:"recordType"`
	ID                string `json:"id"`
	RepositoryID      string `json:"repositoryId"`
	ProviderNamespace string `json:"providerNamespace"`
	ExternalKey       string `json:"externalKey"`
	AssertionHash     string `json:"assertionHash"`
}
type LegacyProofRawBasis struct {
	RevisionDocument jsontext.Value
	SourceDocument   jsontext.Value
	SubjectDocument  jsontext.Value
	EvidenceDocument jsontext.Value
}
type SourceReadPin struct {
	ProjectID             string `json:"projectId"`
	BaseRevisionID        string `json:"baseRevisionId"`
	TargetHash            string `json:"targetHash"`
	EffectiveSemanticHash string `json:"effectiveSemanticHash"`
	SourceVectorHash      string `json:"sourceVectorHash"`
}
type SourceAssertionsQuery struct {
	RecordType, ID, RepositoryID, ProviderNamespace string
	Limit                                           int
	Cursor                                          string
}
type SourceAssertionsPage struct {
	DocumentVersion string                `json:"documentVersion"`
	Pins            SourceReadPin         `json:"pins"`
	Items           []SourceAssertionItem `json:"items"`
	NextCursor      string                `json:"nextCursor"`
}
type SourceAssertionItem struct {
	Assertion   ProviderAssertion           `json:"assertion"`
	Currentness SourceClaimCurrentness      `json:"currentness"`
	Selections  []SourceAssertionResolution `json:"selections"`
	Conflicts   []SourceAssertionConflict   `json:"conflicts"`
}

// SourceReadContext carries the immutable multi-provider provenance beside a
// structural projection. It never picks a singular owner for a source6 record.
type SourceReadContext struct {
	SourceVector      *SourceVector               `json:"sourceVector,omitzero"`
	SourceContentHash string                      `json:"sourceContentHash"`
	Identities        []QualifiedSourceIdentity   `json:"identities"`
	AssertionRefs     []BaseAssertionRef          `json:"assertionRefs"`
	Selections        []SourceAssertionResolution `json:"selections"`
	Currentness       []SourceClaimCurrentness    `json:"currentness"`
	LegacyProofBases  []LegacyProofBasis          `json:"legacyProofBases"`
}
