package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

func changeSemanticAttributes(attrs map[string]jsontext.Value) (map[string]jsontext.Value, error) {
	var strip func(jsontext.Value) (jsontext.Value, error)
	strip = func(raw jsontext.Value) (jsontext.Value, error) {
		b := bytes.TrimSpace(raw)
		if len(b) == 0 || b[0] != '{' {
			return raw, nil
		}
		m, err := relationalObject(raw)
		if err != nil {
			return nil, err
		}
		for key, value := range m {
			if slices.Contains([]string{"sourceKind", "sourceSnapshotId", "evidenceIds", "evidenceKeys", "freshness", "ownership", "facetComparison"}, key) {
				delete(m, key)
				continue
			}
			m[key], err = strip(value)
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(m)
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	raw, err = strip(raw)
	if err != nil {
		return nil, err
	}
	return relationalObject(raw)
}

func changeSemanticHash(s *ChangeEvaluationSnapshot) (string, error) {
	type record struct {
		ID      string                 `json:"id"`
		Payload SourceAssertionPayload `json:"payload"`
	}
	records := []record{}
	add := func(id string, p SourceAssertionPayload) error {
		var err error
		p.Attributes, err = changeSemanticAttributes(p.Attributes)
		if err != nil {
			return err
		}
		records = append(records, record{ID: id, Payload: p})
		return nil
	}
	for _, n := range s.Nodes {
		if err := add(n.ID, sourceNodePayload(n)); err != nil {
			return "", err
		}
	}
	for _, e := range s.Edges {
		if err := add(e.ID, sourceEdgePayload(e)); err != nil {
			return "", err
		}
	}
	slices.SortFunc(records, func(a, b record) int { return strings.Compare(a.ID, b.ID) })
	type identity struct {
		CarriedSource     *ChangeCarriedSourceIdentity `json:"carriedSource,omitzero"`
		Kind              string                       `json:"kind"`
		RecordType        string                       `json:"recordType"`
		ID                string                       `json:"id"`
		RepositoryID      string                       `json:"repositoryId,omitempty"`
		ProviderNamespace string                       `json:"providerNamespace,omitempty"`
		ExternalKey       *string                      `json:"externalKey"`
	}
	identities := []identity{}
	for _, i := range s.Identities {
		ref := changeIdentityRef(i.Target)
		value := identity{Kind: i.Target.Kind, RecordType: ref.RecordType, ID: ref.ID, ExternalKey: i.ExternalKey}
		if i.Target.Source != nil {
			value.RepositoryID, value.ProviderNamespace = i.Target.Source.RepositoryID, i.Target.Source.ProviderNamespace
		}
		if i.Target.Kind == "carried_source_identity" {
			value.CarriedSource = &ChangeCarriedSourceIdentity{Source: *i.Target.Source, Basis: *i.Target.Basis}
		}
		identities = append(identities, value)
	}
	slices.SortFunc(identities, func(a, b identity) int {
		x, _ := canonicalJSON(a)
		y, _ := canonicalJSON(b)
		return bytes.Compare(x, y)
	})
	names := map[string]string{}
	for _, n := range s.EdgeNames {
		names[n.ID] = n.Name
	}
	criteria := slices.Clone(s.Criteria)
	slices.SortFunc(criteria, func(a, b ChangeCriterion) int { return strings.Compare(a.Key, b.Key) })
	pins := slices.Clone(s.ArtifactPins)
	slices.SortFunc(pins, func(a, b ArtifactPin) int { return strings.Compare(a.Kind+"\x00"+a.ID, b.Kind+"\x00"+b.ID) })
	artifact := s.ArtifactContext
	artifact.APIBindings = slices.Clone(artifact.APIBindings)
	artifact.EditorBindings = slices.Clone(artifact.EditorBindings)
	// Binding reasons and origin labels are command provenance, like field origins.
	for i := range artifact.APIBindings {
		artifact.APIBindings[i].Reason = ""
		artifact.APIBindings[i].Origin = ""
	}
	for i := range artifact.EditorBindings {
		artifact.EditorBindings[i].Reason = ""
		artifact.EditorBindings[i].Origin = ""
	}
	slices.SortFunc(artifact.APIBindings, func(a, b APIArtifactBinding) int {
		x, _ := canonicalJSON(a)
		y, _ := canonicalJSON(b)
		return bytes.Compare(x, y)
	})
	slices.SortFunc(artifact.EditorBindings, func(a, b EditorBinding) int {
		x, _ := canonicalJSON(a)
		y, _ := canonicalJSON(b)
		return bytes.Compare(x, y)
	})
	legacyHash, err := requestDigest(struct {
		DocumentVersion  string
		Schema           string
		BaseRevisionID   string
		BaseSemanticHash string
		SourceVector     SourceVector
		Snapshots        []string
		Records          []record
		Identities       []identity
		EdgeNames        map[string]string
		ArtifactPins     []ArtifactPin
		ArtifactContext  ArtifactContext
		Criteria         []ChangeCriterion
	}{DocumentVersion: ChangeProposalDocumentVersion, Schema: s.SchemaVersion, BaseRevisionID: s.BaseRevisionID, BaseSemanticHash: s.BaseSemanticHash, SourceVector: s.SourceVector, Snapshots: s.SourceSnapshotIDs, Records: records, Identities: identities, EdgeNames: names, ArtifactPins: pins, ArtifactContext: artifact, Criteria: criteria})
	if err != nil || s.ArtifactContextV3 == nil {
		return legacyHash, err
	}
	contextHash, err := ArtifactContextV3Hash(*s.ArtifactContextV3)
	if err != nil {
		return "", err
	}
	return sourceDomainHash("backend-change-context-v3-semantic-v1", struct{ Graph, Context string }{legacyHash, contextHash})
}
func changeCandidateHash(p ChangeProposal, draft ChangeProposalRevision, in PreviewChangeProposalInput, semantic string) (string, error) {
	return requestDigest(struct {
		Protocol           string
		ProposalID         string
		ExpectedVersion    int64
		ProposalRevisionID string
		DraftHash          string
		BaseRevisionID     string
		BaseSemanticHash   string
		Commands           []ChangeProposalCommand
		SemanticHash       string
	}{Protocol: "backend-change-candidate-v1", ProposalID: p.ID, ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: draft.ID, DraftHash: draft.SemanticHash, BaseRevisionID: draft.BaseRevisionID, BaseSemanticHash: draft.BaseSemanticHash, Commands: in.Commands, SemanticHash: semantic})
}
