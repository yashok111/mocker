package backendmodel

import (
	"maps"
	"slices"
)

func analysisProof(p lineageProof) EffectiveAnalysisProof {
	out := EffectiveAnalysisProof{Status: p.status, Boundary: p.boundary, Reasons: slices.Sorted(maps.Keys(p.reasons)), EvidenceIDs: slices.Clone(p.evidenceIDs), Assertions: slices.Clone(p.claims)}
	slices.Sort(out.EvidenceIDs)
	out.EvidenceIDs = slices.Compact(out.EvidenceIDs)
	slices.SortFunc(out.Assertions, func(a, b BaseAssertionRef) int {
		ak, _ := requestDigest(a)
		bk, _ := requestDigest(b)
		if ak < bk {
			return -1
		}
		if ak > bk {
			return 1
		}
		return 0
	})
	out.Assertions = slices.Compact(out.Assertions)
	return out
}
func EffectiveRecordAnalysisProof(graph *EffectiveGraphSnapshot, ref ChangeRecordRef) (EffectiveAnalysisProof, error) {
	if graph == nil || graph.Source == nil {
		return EffectiveAnalysisProof{}, invalid("graph", "Proof requires a complete effective snapshot")
	}
	p, err := effectiveRecordProof(graph, ref.RecordType, ref.ID, nil)
	return analysisProof(p), err
}
func EffectivePropertyAnalysisProof(graph *EffectiveGraphSnapshot, ref ChangeRecordRef, selector EffectivePropertySelector) (EffectiveAnalysisProof, error) {
	if graph == nil || graph.Source == nil {
		return EffectiveAnalysisProof{}, invalid("graph", "Proof requires a complete effective snapshot")
	}
	if selector.Source != nil {
		p, err := effectivePropertyProof(graph, ref.RecordType, ref.ID, *selector.Source)
		return analysisProof(p), err
	}
	// Non-source desired properties have explicit intent provenance. Qualified
	// source identities retain the assertion proofs that support their owner.
	for _, origin := range graph.Origins {
		if origin.RecordType == ref.RecordType && origin.SubjectID == ref.ID && effectivePropertyEqual(origin.Selector, selector) {
			p := lineageProof{status: "unresolved", reasons: map[string]bool{}, boundary: true}
			if origin.Kind == "intent" {
				p.status = "desired"
				p.boundary = false
				p.add("desired_structure", false)
			}
			return analysisProof(p), nil
		}
	}
	return EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{"missing_property_origin"}}, nil
}
func effectivePropertyEqual(a, b EffectivePropertySelector) bool {
	ar, _ := requestDigest(a)
	br, _ := requestDigest(b)
	return ar == br
}
func (r *EditorArtifactRequest) ProjectEffective(graph *EffectiveGraphSnapshot, in ArtifactQueryInput) (*ArtifactProjectionPage, error) {
	if graph == nil || graph.Pins.ArtifactContext == nil {
		return nil, invalid("graph", "Projection requires exact effective artifact context")
	}
	old := r.effective
	r.effective = graph
	defer func() { r.effective = old }()
	return r.Project(&graph.State, *graph.Pins.ArtifactContext, in)
}

// EffectiveValueAnalysisProof retains the complete typed value address during
// validation and delegates selected facet/port support to the existing proof.
func EffectiveValueAnalysisProof(graph *EffectiveGraphSnapshot, ref LineageValueRef) (EffectiveAnalysisProof, error) {
	if graph == nil || graph.Source == nil {
		return EffectiveAnalysisProof{}, invalid("graph", "Value proof requires a complete effective snapshot")
	}
	nodes := map[string]Node{}
	edges := map[string]Edge{}
	for _, n := range graph.State.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range graph.State.Edges {
		edges[e.ID] = e
	}
	if err := validateLineageValueTargetForSchema(ref, nodes, edges, graph.Pins.StructuralSchemaVersion); err != nil {
		return EffectiveAnalysisProof{}, err
	}
	p, err := effectiveRecordProof(graph, "node", ref.NodeID, &ref)
	if err != nil {
		return EffectiveAnalysisProof{}, err
	}
	if ref.Kind == "event_field" {
		route, err := effectiveRecordProof(graph, "edge", ref.RouteID, nil)
		if err != nil {
			return EffectiveAnalysisProof{}, err
		}
		mergeSourceProof(&p, route)
	}
	return analysisProof(p), nil
}
