package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
)

// sourceDependencyValue projects only semantic values at the bound address.
// A facet excludes proof wrappers, and a port is one slot rather than its array.
func sourceDependencyValue(binding SourceDependencyBinding, p SourceAssertionPayload) (SourcePropertyValue, error) {
	ref := binding.ValueContext
	if ref == nil || binding.Target.RecordType != "node" || binding.Target.ExpectedID != ref.NodeID {
		return sourceSemanticPayloadValue(p)
	}
	if ref.Kind != "event_field" {
		if err := validateLineageValueTargetForSchema(*ref, map[string]Node{ref.NodeID: {ID: ref.NodeID, Kind: p.Kind, ParentID: p.ParentID, Attributes: p.Attributes}}, nil, ComposedSchemaVersion); err != nil {
			return SourcePropertyValue{}, err
		}
	}
	switch ref.Kind {
	case "column":
		values := map[string]SourcePropertyValue{}
		for _, group := range sourceFacetGroups(p.Kind) {
			value, err := SelectSourceProperty(p, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: ref.FacetKey, Group: group})
			if err != nil {
				return SourcePropertyValue{}, err
			}
			values[group] = value
		}
		raw, err := canonicalJSON(values)
		return SourcePropertyValue{Present: true, Value: raw}, err
	case "port":
		var ports []jsontext.Value
		if err := json.Unmarshal(p.Attributes[ref.Collection], &ports); err != nil {
			return SourcePropertyValue{}, err
		}
		for _, raw := range ports {
			port, err := relationalObject(raw)
			if err != nil {
				return SourcePropertyValue{}, err
			}
			if runtimeString(port["key"]) == ref.PortKey {
				value, err := canonicalJSON(raw)
				return SourcePropertyValue{Present: true, Value: value}, err
			}
		}
		return SourcePropertyValue{}, semantic("valueContext", "Selected port is absent from its bound payload")
	default:
		return sourceSemanticPayloadValue(p)
	}
}

func sourceSemanticPayloadValue(p SourceAssertionPayload) (SourcePropertyValue, error) {
	selectors, err := sourceSelectors([]SourceAssertionPayload{p})
	if err != nil {
		return SourcePropertyValue{}, err
	}
	values := map[string]SourcePropertyValue{}
	for _, selector := range selectors {
		value, err := SelectSourceProperty(p, selector)
		if err != nil {
			return SourcePropertyValue{}, err
		}
		values[sourcePropertyKey(selector)] = value
	}
	raw, err := canonicalJSON(struct {
		Kind       string                         `json:"kind"`
		RecordType string                         `json:"recordType"`
		Values     map[string]SourcePropertyValue `json:"values"`
	}{p.Kind, p.RecordType, values})
	return SourcePropertyValue{Present: true, Value: raw}, err
}

func applySourceEffectiveDependencyGaps(graph *SourceGraphSnapshot, projection *graphCandidate, current map[string]SourceClaimCurrentness) map[string]bool {
	changedClaims := map[string]bool{}
	claims := map[string]ProviderAssertion{}
	effective := map[string]SourceAssertionPayload{}
	for _, a := range graph.Assertions {
		claims[sourceAssertionKey(a)] = a
	}
	for _, n := range projection.Nodes {
		effective["node\x00"+n.ID] = sourceNodePayload(n)
	}
	for _, e := range projection.Edges {
		effective["edge\x00"+e.ID] = sourceEdgePayload(e)
	}
	for _, consumer := range graph.Assertions {
		key := sourceAssertionKey(consumer)
		for _, binding := range consumer.DependencyClaims {
			target, exists := claims[sourceClaimKey(binding.Target.RecordType, binding.Target.ExpectedID, binding.Target.RepositoryID, binding.Target.ProviderNamespace)]
			if !exists || sourceAssertionRef(target) != binding.Target {
				continue
			}
			value, exists := effective[binding.Target.RecordType+"\x00"+binding.Target.ExpectedID]
			changed := !exists
			if exists {
				before, err := sourceConsumerDependencyValue(consumer, binding, target.Payload)
				if err != nil {
					changed = true
				} else {
					after, err := sourceConsumerDependencyValue(consumer, binding, value)
					changed = err != nil || before.Present != after.Present || !bytes.Equal(before.Value, after.Value)
				}
			}
			if changed {
				changedClaims[key] = true
				f := current[key]
				f.Dependency = sourceStaleReason(f.Dependency, "dependency_changed")
				current[key] = f
				break
			}
		}
	}
	return changedClaims
}

func sourceDependencyFacetKeys(consumer ProviderAssertion, binding SourceDependencyBinding) []string {
	p := consumer.Payload
	if runtimeAccessEdgeKind(p.Kind) && (binding.Site == "/to" || binding.Site == "/attributes/datastoreId") {
		return []string{runtimeString(p.Attributes["facetKey"])}
	}
	if p.Kind == "references" && binding.Site == "/from" {
		facets, _, _ := relationalFacetObject(p.Kind, p.Attributes)
		return slices.Sorted(maps.Keys(facets))
	}
	if p.Kind == "migration" && binding.Property.Kind == "relational_facet" && binding.Property.Group == "parentIds" {
		return []string{binding.Property.FacetKey}
	}
	return nil
}

func sourceConsumerDependencyValue(consumer ProviderAssertion, binding SourceDependencyBinding, payload SourceAssertionPayload) (SourcePropertyValue, error) {
	keys := sourceDependencyFacetKeys(consumer, binding)
	if len(keys) == 0 || payload.Kind == "unresolved_target" {
		return sourceDependencyValue(binding, payload)
	}
	facets, _, err := relationalFacetObject(payload.Kind, payload.Attributes)
	if err != nil {
		return SourcePropertyValue{}, err
	}
	values := map[string]map[string]SourcePropertyValue{}
	for _, key := range keys {
		if facets[key] == nil {
			return SourcePropertyValue{}, semantic(binding.Site, "Exact dependency facet is absent")
		}
		groups := map[string]SourcePropertyValue{}
		for _, group := range sourceFacetGroups(payload.Kind) {
			value, err := SelectSourceProperty(payload, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: key, Group: group})
			if err != nil {
				return SourcePropertyValue{}, err
			}
			groups[group] = value
		}
		values[key] = groups
	}
	raw, err := canonicalJSON(struct {
		Kind     string                                    `json:"kind"`
		ParentID *string                                   `json:"parentId"`
		Facets   map[string]map[string]SourcePropertyValue `json:"facets"`
	}{payload.Kind, payload.ParentID, values})
	return SourcePropertyValue{Present: true, Value: raw}, err
}
