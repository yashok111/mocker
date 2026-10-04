package backendmodel

import (
	"encoding/json/v2"
	"slices"
)

type EffectivePropertySelector struct {
	CarriedSourceIdentity *ChangeCarriedSourceIdentity `json:"carriedSourceIdentity,omitzero"`
	Kind                  string                       `json:"kind"`
	Source                *TypedSourcePropertySelector `json:"source,omitzero"`
	RecordType            string                       `json:"recordType,omitempty"`
	ID                    string                       `json:"id,omitempty"`
	RepositoryID          string                       `json:"repositoryId,omitempty"`
	ProviderNamespace     string                       `json:"providerNamespace,omitempty"`
	EdgeName              bool                         `json:"-"`
	SourceIdentity        *SourceIdentitySelector      `json:"-"`
	IntentIdentity        *IntentIdentitySelector      `json:"-"`
}

type EffectiveGraphPins struct {
	TargetHash              string           `json:"targetHash"`
	ViewSchemaVersion       string           `json:"viewSchemaVersion"`
	StructuralSchemaVersion string           `json:"structuralSchemaVersion"`
	EffectiveSemanticHash   string           `json:"effectiveSemanticHash"`
	BaseRevisionID          string           `json:"baseRevisionId"`
	BaseSemanticHash        string           `json:"baseSemanticHash"`
	SourceVectorHash        string           `json:"sourceVectorHash"`
	SourceSnapshotIDs       []string         `json:"sourceSnapshotIds"`
	ArtifactPins            []ArtifactPin    `json:"artifactPins"`
	ArtifactContext         *ArtifactContext `json:"artifactContext"`
}
type EffectiveGraphSnapshot struct {
	coverage         *RevisionCoverage
	readIndex        *effectiveGraphReadIndex
	Target           BackendReadTarget
	State            RevisionState
	Source           *SourceGraphSnapshot
	Pins             EffectiveGraphPins
	Origins          []EffectiveFieldOrigin
	Criteria         []ChangeCriterion
	BaselineEvidence []EffectiveEvidenceBasis
	EdgeNames        map[string]string
	Identities       []EffectiveIdentity
}
type EffectiveEvidenceBasis struct {
	RevisionID   string `json:"revisionId"`
	SemanticHash string `json:"semanticHash"`
	RecordType   string `json:"recordType"`
	SubjectID    string `json:"subjectId"`
	EvidenceID   string `json:"evidenceId"`
}
type EffectiveBaseRef struct {
	RevisionID   string `json:"revisionId"`
	SemanticHash string `json:"semanticHash"`
	RecordType   string `json:"recordType"`
	SubjectID    string `json:"subjectId"`
}
type SourceIdentitySelector struct {
	RecordType        string `json:"recordType"`
	ID                string `json:"id"`
	RepositoryID      string `json:"repositoryId"`
	ProviderNamespace string `json:"providerNamespace"`
}
type IntentIdentitySelector struct {
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
}
type EffectiveFieldOrigin struct {
	RebaseResolution *RebaseResolutionOrigin   `json:"rebaseResolution,omitzero"`
	RecordType       string                    `json:"recordType"`
	SubjectID        string                    `json:"subjectId"`
	Selector         EffectivePropertySelector `json:"selector"`
	Kind             string                    `json:"kind"`
	CommandID        string                    `json:"commandId,omitempty"`
	Reason           string                    `json:"reason,omitempty"`
	BaseRef          *EffectiveBaseRef         `json:"baseRef,omitzero"`
	SourceClaims     []BaseAssertionRef        `json:"sourceClaims"`
	EvidenceIDs      []string                  `json:"evidenceIds"`
	Freshness        *AssertionFreshness       `json:"freshness,omitzero"`
}
type EffectiveIdentity struct {
	Target      ChangeIdentityTarget `json:"target"`
	ExternalKey *string              `json:"externalKey"`
	Origin      EffectiveFieldOrigin `json:"origin"`
}

func (s EffectivePropertySelector) MarshalJSON() ([]byte, error) {
	switch {
	case s.CarriedSourceIdentity != nil:
		s.Kind = "carried_source_identity"
		s.RecordType = ""
		s.ID = ""
		s.RepositoryID = ""
		s.ProviderNamespace = ""
	case s.Source != nil:
		s.Kind = "source"
	case s.EdgeName:
		s.Kind = "edge_name"
	case s.SourceIdentity != nil:
		s.Kind = "source_identity"
		s.RecordType, s.ID, s.RepositoryID, s.ProviderNamespace = s.SourceIdentity.RecordType, s.SourceIdentity.ID, s.SourceIdentity.RepositoryID, s.SourceIdentity.ProviderNamespace
	case s.IntentIdentity != nil:
		s.Kind = "intent_identity"
		s.RecordType, s.ID = s.IntentIdentity.RecordType, s.IntentIdentity.ID
	}
	type plain EffectivePropertySelector
	return json.Marshal(plain(s))
}

func (s *EffectivePropertySelector) UnmarshalJSON(b []byte) error {
	type plain EffectivePropertySelector
	var v plain
	if err := json.Unmarshal(b, &v, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	fields := []string{"kind"}
	switch v.Kind {
	case "carried_source_identity":
		fields = append(fields, "carriedSourceIdentity")
		if v.CarriedSourceIdentity == nil {
			return invalid("selector", "Exact carried identity required")
		}
		c := v.CarriedSourceIdentity
		if err := (ChangeIdentityTarget{Kind: v.Kind, Source: &c.Source, Basis: &c.Basis}).Validate(); err != nil {
			return err
		}
	case "source":
		fields = append(fields, "source")
		if v.Source == nil {
			return invalid("selector", "Source selector required")
		}
	case "edge_name":
	case "source_identity":
		fields = append(fields, "recordType", "id", "repositoryId", "providerNamespace")
	case "intent_identity":
		fields = append(fields, "recordType", "id")
	default:
		return invalid("selector", "Unknown effective property selector")
	}
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, fields, nil); err != nil {
		return err
	}
	if err = validateEffectiveIdentitySelector(EffectivePropertySelector(v)); err != nil {
		return err
	}
	*s = EffectivePropertySelector(v)
	switch v.Kind {
	case "edge_name":
		s.EdgeName = true
	case "source_identity":
		s.SourceIdentity = &SourceIdentitySelector{RecordType: v.RecordType, ID: v.ID, RepositoryID: v.RepositoryID, ProviderNamespace: v.ProviderNamespace}
	case "intent_identity":
		s.IntentIdentity = &IntentIdentitySelector{RecordType: v.RecordType, ID: v.ID}
	}
	return nil
}

type EffectiveBasis struct {
	RevisionID   string `json:"revisionId"`
	SemanticHash string `json:"semanticHash"`
	RecordType   string `json:"recordType"`
	ID           string `json:"id"`
}
type EffectiveOrigin struct {
	RebaseResolution *RebaseResolutionOrigin `json:"rebaseResolution,omitzero"`
	Kind             string                  `json:"kind"`
	CommandID        string                  `json:"commandId,omitempty"`
	Reason           string                  `json:"reason,omitempty"`
	BaseRef          *EffectiveBasis         `json:"baseRef,omitzero"`
}
type ChangeEvaluationFieldOrigin struct {
	ChangeRecordRef
	Selector EffectivePropertySelector `json:"selector"`
	Origin   EffectiveOrigin           `json:"origin"`
}
type ChangeEvaluationIdentity struct {
	Target      ChangeIdentityTarget `json:"target"`
	ExternalKey *string              `json:"externalKey"`
	Origin      EffectiveOrigin      `json:"origin"`
}
type ChangeEvaluationSnapshot struct {
	BaselineSchemaVersion string
	DocumentVersion       string
	ProjectID             string
	ProposalID            string
	ProposalRevisionID    string
	BaseRevisionID        string
	BaseSemanticHash      string
	SemanticHash          string
	SchemaVersion         string
	Source                *SourceGraphSnapshot
	Nodes                 []Node
	Edges                 []Edge
	EdgeNames             []ChangeEdgeName
	Identities            []ChangeEvaluationIdentity
	Criteria              []ChangeCriterion
	SourceVector          SourceVector
	SourceSnapshotIDs     []string
	ArtifactPins          []ArtifactPin
	ArtifactContext       ArtifactContext
	Coverage              Coverage
	Origins               []ChangeEvaluationFieldOrigin
	BaselineEvidence      []Evidence
}

func validateEffectiveIdentitySelector(s EffectivePropertySelector) error {
	if s.Kind == "source_identity" || s.Kind == "intent_identity" {
		if !ValidID(s.ID) || !slices.Contains([]string{"node", "edge"}, s.RecordType) {
			return invalid("selector", "Identity selectors require canonical record identity")
		}
	}
	if s.Kind == "source_identity" {
		if !ValidID(s.RepositoryID) || !externalKey(s.ProviderNamespace) {
			return invalid("selector", "Source identity selectors require a qualified owner")
		}
	}
	return nil
}
