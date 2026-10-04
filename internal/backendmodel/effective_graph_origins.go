package backendmodel

import (
	"slices"
	"strings"
)

func effectiveEvaluationOrigin(source *SourceGraphSnapshot, input ChangeEvaluationFieldOrigin) EffectiveFieldOrigin {
	out := EffectiveFieldOrigin{RebaseResolution: input.Origin.RebaseResolution, RecordType: input.RecordType, SubjectID: input.ID, Selector: input.Selector, Kind: input.Origin.Kind, CommandID: input.Origin.CommandID, Reason: input.Origin.Reason, SourceClaims: []BaseAssertionRef{}, EvidenceIDs: []string{}}
	if input.Origin.BaseRef != nil {
		b := input.Origin.BaseRef
		out.BaseRef = &EffectiveBaseRef{RevisionID: b.RevisionID, SemanticHash: b.SemanticHash, RecordType: b.RecordType, SubjectID: b.ID}
	}
	if out.Kind != "source" {
		return out
	}
	if source.SourceVector != nil && input.Selector.Source != nil {
		claims, err := sourceSelectedClaims(source, input.RecordType, input.ID, *input.Selector.Source)
		if err == nil {
			for _, a := range claims {
				out.SourceClaims = append(out.SourceClaims, sourceAssertionRef(a))

			}
			proof, err := sourcePropertyProof(source, input.RecordType, input.ID, *input.Selector.Source)
			if err == nil {
				out.EvidenceIDs = slices.Clone(proof.evidenceIDs)
				status := "current"
				if proof.boundary || proof.status == "stale" || proof.status == "unresolved" {
					status = "stale"
				}
				out.Freshness = &AssertionFreshness{Status: status, Reasons: runtimeSortedKeys(proof.reasons)}
			}
		}
	} else {
		out = effectiveLegacyOriginSupport(source, input, out)
	}

	return out
}

func effectiveSourceOrigins(source *SourceGraphSnapshot, state RevisionState) ([]EffectiveFieldOrigin, error) {
	out := []EffectiveFieldOrigin{}
	add := func(typ, id string, payload SourceAssertionPayload) error {
		properties, err := sourceSelectors([]SourceAssertionPayload{payload})
		if err != nil {
			return err
		}
		for _, property := range properties {
			value, err := SelectSourceProperty(payload, property)
			if err != nil {
				return err
			}
			if !value.Present {
				continue
			}
			origin := ChangeEvaluationFieldOrigin{ChangeRecordRef: ChangeRecordRef{RecordType: typ, ID: id}, Selector: EffectivePropertySelector{Kind: "source", Source: new(property)}, Origin: EffectiveOrigin{Kind: "source", BaseRef: &EffectiveBasis{RevisionID: source.State.Revision.ID, SemanticHash: source.State.Revision.SemanticHash, RecordType: typ, ID: id}}}
			out = append(out, effectiveEvaluationOrigin(source, origin))
		}
		return nil
	}
	for _, n := range state.Nodes {
		if err := add("node", n.ID, sourceNodePayload(n)); err != nil {
			return nil, err
		}
	}
	for _, e := range state.Edges {
		if err := add("edge", e.ID, sourceEdgePayload(e)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func effectiveLegacyOriginSupport(source *SourceGraphSnapshot, input ChangeEvaluationFieldOrigin, out EffectiveFieldOrigin) EffectiveFieldOrigin {

	for _, n := range source.State.Nodes {
		if input.RecordType == "node" && n.ID == input.ID {
			out.EvidenceIDs = slices.Clone(n.EvidenceIDs)
			out.Freshness = n.Freshness
		}
	}
	for _, e := range source.State.Edges {
		if input.RecordType == "edge" && e.ID == input.ID {
			out.EvidenceIDs = slices.Clone(e.EvidenceIDs)
			out.Freshness = e.Freshness
		}
	}
	for _, identity := range SourceIdentities(source, input.RecordType, input.ID) {
		out.SourceClaims = append(out.SourceClaims, BaseAssertionRef{RepositoryID: identity.RepositoryID, ProviderNamespace: identity.ProviderNamespace, RecordType: identity.RecordType, ExternalKey: identity.ExternalKey, ExpectedID: identity.ID, AssertionHash: identity.AssertionHash})
	}
	if input.Selector.Source != nil {
		out = effectiveLegacyPropertySupport(source, input, out)
	}

	return out
}

func effectiveLegacyPropertySupport(source *SourceGraphSnapshot, input ChangeEvaluationFieldOrigin, out EffectiveFieldOrigin) EffectiveFieldOrigin {
	var payload SourceAssertionPayload
	for _, n := range source.State.Nodes {
		if input.RecordType == "node" && n.ID == input.ID {
			payload = sourceNodePayload(n)
		}
	}
	for _, e := range source.State.Edges {
		if input.RecordType == "edge" && e.ID == input.ID {
			payload = sourceEdgePayload(e)
		}
	}
	property := *input.Selector.Source
	if property.Kind == "relational_facet" {
		kind, attrs := payload.Kind, payload.Attributes
		facets, _, err := relationalFacetObject(kind, attrs)
		if err != nil {
			return out
		}
		facet, err := decodeRelationalFacet(kind, facets[property.FacetKey], true)
		if err != nil {
			return out
		}
		out.EvidenceIDs, out.Freshness = slices.Clone(facet.EvidenceIDs), facet.Freshness
	}
	out.EvidenceIDs = slices.DeleteFunc(out.EvidenceIDs, func(eid string) bool {
		for _, e := range source.State.Evidence {
			if e.ID != eid {
				continue
			}
			if e.SubjectID != input.ID {
				return true
			}
			if e.PropertyPath == nil || legacyFacetRootSupport(payload, property, *e.PropertyPath) {
				return false
			}
			selected := sourcePropertyForPointer(payload, *e.PropertyPath)
			return selected == nil || *selected != property
		}
		return true
	})
	return out
}

func legacyFacetRootSupport(payload SourceAssertionPayload, property TypedSourcePropertySelector, path string) bool {
	if property.Kind != "relational_facet" {
		return false
	}
	members, err := sourcePropertyPath(payload, property)
	if err != nil || len(members) < 2 {
		return false
	}
	parts := make([]string, len(members)-1)
	for i, member := range members[:len(members)-1] {
		parts[i] = escapeRelationalPointer(member)
	}
	return path == "/"+strings.Join(parts, "/")
}
