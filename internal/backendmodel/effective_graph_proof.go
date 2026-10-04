package backendmodel

import (
	"slices"
)

func effectivePropertyProof(graph *EffectiveGraphSnapshot, typ, id string, property TypedSourcePropertySelector) (lineageProof, error) {
	index := graph.indexedReads()
	origin, hasOrigin := index.origins[typ+"\x00"+id+"\x00"+sourcePropertyKey(property)]
	if hasOrigin && origin.Kind == "intent" {
		return lineageProof{status: "desired", reasons: map[string]bool{"desired_structure": true}}, nil
	}

	if graph.Source.SourceVector != nil {
		return sourcePropertyProof(graph.Source, typ, id, property)
	}
	p := lineageProof{status: "explicit", reasons: map[string]bool{}}
	found := hasOrigin && origin.Kind == "source"
	if found {
		p.evidenceIDs = append(p.evidenceIDs, origin.EvidenceIDs...)
		if origin.Freshness != nil {
			sourceFreshnessProof(&p, *origin.Freshness)
		}
	}

	if !found {
		p.status = "unresolved"
		p.add("missing_property_origin", true)
		return p, nil
	}
	if len(p.evidenceIDs) == 0 {
		p.status = runtimeWorseStatus(p.status, "inferred")
		p.add("missing_semantic_support", false)
	}
	for _, eid := range p.evidenceIDs {
		for _, e := range graph.Source.State.Evidence {
			if e.ID == eid {
				p.status = runtimeWorseStatus(p.status, e.Status)
				if e.Freshness != nil {
					sourceFreshnessProof(&p, *e.Freshness)
				}
				if e.Status == "stale" || e.Status == "unresolved" {
					p.add(e.Status+"_evidence", true)
				}
			}
		}
	}
	return p, nil
}
func effectiveRecordProof(graph *EffectiveGraphSnapshot, typ, id string, ref *LineageValueRef) (lineageProof, error) {
	p := lineageProof{status: "explicit", reasons: map[string]bool{}}
	payload, found := graph.indexedReads().payloads[typ+"\x00"+id]

	if !found {
		p.status = "unresolved"
		p.add("missing_effective_record", true)
		return p, nil
	}
	groups, err := sourceSelectors([]SourceAssertionPayload{payload})
	if err != nil {
		return p, err
	}
	for _, property := range groups {
		if ref != nil && property.Kind == "relational_facet" && (ref.Kind != "column" || property.FacetKey != ref.FacetKey) {
			continue
		}
		if ref != nil && property.Kind == "flow_ports" && (ref.Kind != "port" || property.Collection != ref.Collection) {
			continue
		}
		value, err := SelectSourceProperty(payload, property)
		if err != nil {
			return p, err
		}
		if !value.Present {
			continue
		}
		proof, err := effectivePropertyProof(graph, typ, id, property)
		if err != nil {
			return p, err
		}
		mergeSourceProof(&p, proof)
	}
	slices.Sort(p.evidenceIDs)
	p.evidenceIDs = slices.Compact(p.evidenceIDs)
	return p, nil
}

func effectiveFacetProof(graph *EffectiveGraphSnapshot, typ, id, facetKey string) (lineageProof, error) {
	p := lineageProof{status: "explicit", reasons: map[string]bool{}}
	payload, found := graph.indexedReads().payloads[typ+"\x00"+id]
	if !found {
		p.status = "unresolved"
		p.add("missing_effective_record", true)
		return p, nil
	}
	properties, err := sourceSelectors([]SourceAssertionPayload{payload})
	if err != nil {
		return p, err
	}
	for _, property := range properties {
		relevant := property.Kind == "relational_facet" && property.FacetKey == facetKey
		if property.Kind == "edge_endpoints" || property.Kind == "parent" {
			origin := graph.indexedReads().origins[typ+"\x00"+id+"\x00"+sourcePropertyKey(property)]
			relevant = graph.Source.SourceVector != nil || origin.Kind == "intent"
		}
		if !relevant {
			continue
		}
		value, err := SelectSourceProperty(payload, property)
		if err != nil {
			return p, err
		}
		if !value.Present {
			continue
		}
		proof, err := effectivePropertyProof(graph, typ, id, property)
		if err != nil {
			return p, err
		}
		mergeSourceProof(&p, proof)
	}
	slices.Sort(p.evidenceIDs)
	p.evidenceIDs = slices.Compact(p.evidenceIDs)
	return p, nil
}
