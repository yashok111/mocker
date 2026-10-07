package backendmodel

import (
	"encoding/json/jsontext"
	"maps"
	"slices"
)

type EffectiveFacet struct {
	Origin             string                            `json:"origin"`
	ProposalID         string                            `json:"proposalId"`
	ProposalRevisionID *string                           `json:"proposalRevisionId"`
	FacetKey           string                            `json:"facetKey"`
	SubjectID          string                            `json:"subjectId"`
	Base               *ProposalBasis                    `json:"base"`
	Values             map[string]jsontext.Value         `json:"values"`
	PropertyOrigins    map[string]ProposalPropertyOrigin `json:"propertyOrigins"`
	BasisEvidenceIDs   []string                          `json:"basisEvidenceIds"`
	Limitations        []string                          `json:"limitations"`
}

type ProposalProjectedNode struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	ParentID       *string         `json:"parentId"`
	SourceRecord   *Node           `json:"sourceRecord"`
	EffectiveFacet *EffectiveFacet `json:"effectiveFacet"`
}

type ProposalProjectedEdge struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	From           string          `json:"from"`
	To             string          `json:"to"`
	SourceRecord   *Edge           `json:"sourceRecord"`
	EffectiveFacet *EffectiveFacet `json:"effectiveFacet"`
}

// proposalOverlayFits reports whether an overlay belongs to this subject
// and facet and, when it edits a source record, to this proposal's baseline.
func proposalOverlayFits(p Proposal, id, kind string, overlay *ProposalOverlay) bool {
	if overlay.SubjectID != id || overlay.Kind != kind || overlay.FacetKey != p.FacetKey {
		return false
	}
	return overlay.Base == nil || overlay.Base.SubjectID == id && overlay.Base.RevisionID == p.BaseRevisionID && overlay.Base.SemanticHash == p.BaseSemanticHash && overlay.Base.FacetKey == p.FacetKey
}

func effectiveProposalFacet(p Proposal, revisionID *string, id, kind string, attrs map[string]jsontext.Value, overlay *ProposalOverlay) (*EffectiveFacet, error) {
	if overlay != nil && !proposalOverlayFits(p, id, kind, overlay) {
		return nil, notFound()
	}
	f := &EffectiveFacet{Origin: "proposal", ProposalID: p.ID, ProposalRevisionID: revisionID, FacetKey: p.FacetKey, SubjectID: id, Values: map[string]jsontext.Value{}, PropertyOrigins: map[string]ProposalPropertyOrigin{}, BasisEvidenceIDs: []string{}, Limitations: []string{"Desired structure; existing data, writers and runtime enforcement are unverified"}}
	if attrs != nil {
		if !relationalSubject(kind, attrs, kind == "references") {
			return nil, nil
		}
		facets, _, err := relationalFacetObject(kind, attrs)
		if err != nil {
			return nil, err
		}
		if facets[p.FacetKey] == nil {
			return nil, nil
		}
		source, err := decodeRelationalFacet(kind, facets[p.FacetKey], true)
		if err != nil {
			return nil, err
		}
		f.Values, err = proposalFacetValues(facets[p.FacetKey])
		if err != nil {
			return nil, err
		}
		f.Base = &ProposalBasis{RevisionID: p.BaseRevisionID, SemanticHash: p.BaseSemanticHash, SubjectID: id, FacetKey: p.FacetKey}
		f.BasisEvidenceIDs = slices.Clone(source.EvidenceIDs)
		for key := range f.Values {
			path := "/" + key
			f.PropertyOrigins[path] = ProposalPropertyOrigin{Kind: "source", Base: f.Base, Property: path, EvidenceIDs: slices.Clone(source.EvidenceIDs)}
		}
		if source.Freshness.Status != "current" {
			f.Limitations = append(f.Limitations, "Selected baseline source facet is stale")
		}
		if source.AnalysisStatus != "complete" {
			f.Limitations = append(f.Limitations, source.Gaps...)
		}
	}
	if overlay != nil {
		f.Base = overlay.Base
		f.Values = maps.Clone(overlay.Values)
		f.PropertyOrigins = maps.Clone(overlay.PropertyOrigins)
		if overlay.Kind == "constraint" {
			f.Limitations = append(f.Limitations, "Designed definition has not been collected from source; baseline native text remains separate")
		}
	}
	return f, nil
}

func projectProposalNode(p Proposal, revisionID *string, source *Node, overlay *ProposalOverlay) (*ProposalProjectedNode, error) {
	if source == nil && (overlay == nil || overlay.RecordType != "node" || overlay.Base != nil) {
		return nil, notFound()
	}
	out := &ProposalProjectedNode{SourceRecord: source}
	var attrs map[string]jsontext.Value
	if source != nil {
		out.ID, out.Kind, out.Name, out.ParentID = source.ID, source.Kind, source.Name, source.ParentID
		attrs = source.Attributes
	} else {
		out.ID, out.Kind, out.Name, out.ParentID = overlay.SubjectID, overlay.Kind, overlay.Name, overlay.ParentID
	}
	var err error
	out.EffectiveFacet, err = effectiveProposalFacet(p, revisionID, out.ID, out.Kind, attrs, overlay)
	return out, err
}

func projectProposalEdge(p Proposal, revisionID *string, source *Edge, overlay *ProposalOverlay) (*ProposalProjectedEdge, error) {
	if source == nil && (overlay == nil || overlay.RecordType != "edge" || overlay.Base != nil) {
		return nil, notFound()
	}
	out := &ProposalProjectedEdge{SourceRecord: source}
	var attrs map[string]jsontext.Value
	if source != nil {
		out.ID, out.Kind, out.From, out.To = source.ID, source.Kind, source.From, source.To
		attrs = source.Attributes
	} else {
		out.ID, out.Kind = overlay.SubjectID, overlay.Kind
	}
	if overlay != nil {
		out.From, out.To = overlay.FromID, overlay.ToID
	}
	var err error
	out.EffectiveFacet, err = effectiveProposalFacet(p, revisionID, out.ID, out.Kind, attrs, overlay)
	return out, err
}
