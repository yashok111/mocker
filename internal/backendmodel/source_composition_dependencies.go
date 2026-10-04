package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"
)

func sourcePayloadReferenceSites(p SourceAssertionPayload) ([]relationalReference, error) {
	refs := []relationalReference{}
	if p.RecordType == "node" && p.ParentID != nil {
		refs = append(refs, relationalReference{Path: "/parentId", ID: *p.ParentID, RecordType: "node"})
	}
	if p.RecordType == "edge" {
		refs = append(refs, relationalReference{Path: "/from", ID: p.From, RecordType: "node"}, relationalReference{Path: "/to", ID: p.To, RecordType: "node"})
	}
	nested, err := sourceAttributeReferences(p.Kind, p.Attributes, p.RecordType == "edge", true)
	if err != nil {
		return nil, err
	}
	for _, ref := range nested {
		if ref.Kind == "evidence" || ref.HistoricalRevisionID != "" {
			continue
		}
		if ref.RecordType == "" {
			ref.RecordType = "node"
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func sourceReferenceValueContext(p SourceAssertionPayload, path string) (*LineageValueRef, error) {
	if p.Kind != "field_mapping" {
		return nil, nil
	}
	var raw jsontext.Value
	if strings.HasPrefix(path, "/attributes/destination/") {
		raw = p.Attributes["destination"]
	} else if after, ok := strings.CutPrefix(path, "/attributes/sources/"); ok {
		index, _, found := strings.Cut(after, "/")
		if !found {
			return nil, semantic("dependencyClaims", "Invalid mapping source site")
		}
		i, err := strconv.Atoi(index)
		if err != nil {
			return nil, err
		}
		var values []jsontext.Value
		if err := json.Unmarshal(p.Attributes["sources"], &values); err != nil {
			return nil, err
		}
		if i < 0 || i >= len(values) {
			return nil, semantic("dependencyClaims", "Missing mapping source")
		}
		raw = values[i]
	} else {
		return nil, nil
	}
	var value LineageValueRef
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func validateSourceBindings(a ProviderAssertion) error {
	refs, err := sourcePayloadReferenceSites(a.Payload)
	if err != nil {
		return err
	}
	sites := map[string]relationalReference{}
	for _, ref := range refs {
		sites[ref.Path] = ref
	}
	seen := map[string]bool{}
	for _, binding := range a.DependencyClaims {
		if err := validateSourceDependencyBinding(binding); err != nil {
			return err
		}
		site, ok := sites[binding.Site]
		if !ok || seen[binding.Site] || site.ID != binding.Target.ExpectedID || site.RecordType != binding.Target.RecordType {
			return semantic("dependencyClaims", "Dependency binding does not match a declared payload reference")
		}
		seen[binding.Site] = true
		property := sourcePropertyForPointer(a.Payload, binding.Site)
		if property == nil || *property != binding.Property {
			return semantic("dependencyClaims.property", "Dependency property does not match its typed site")
		}
		context, err := sourceReferenceValueContext(a.Payload, binding.Site)
		if err != nil {
			return err
		}
		if (context == nil) != (binding.ValueContext == nil) || context != nil && *context != *binding.ValueContext {
			return semantic("dependencyClaims.valueContext", "Dependency value context differs from its normalized payload")
		}
	}
	if len(seen) != len(sites) {
		return semantic("dependencyClaims", "Normalized assertion is missing a declared dependency binding")
	}
	return nil
}

func bootstrapSourceDependencies(graph *SourceGraphSnapshot, rid string) error {
	byID := map[string]ProviderAssertion{}
	for _, a := range graph.Assertions {
		byID[a.RecordType+"\x00"+a.RecordID] = a
	}
	for i := range graph.Assertions {
		a := &graph.Assertions[i]
		refs, err := sourcePayloadReferenceSites(a.Payload)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			target, ok := byID[ref.RecordType+"\x00"+ref.ID]
			if !ok {
				return semantic(ref.Path, "Legacy source reference target is missing")
			}
			property := sourcePropertyForPointer(a.Payload, ref.Path)
			if property == nil {
				return semantic(ref.Path, "Legacy reference has no typed semantic property")
			}
			context, err := sourceReferenceValueContext(a.Payload, ref.Path)
			if err != nil {
				return err
			}
			a.DependencyClaims = append(a.DependencyClaims, SourceDependencyBinding{Basis: "base", BaseRevisionID: rid, Target: sourceAssertionRef(target), Site: ref.Path, Property: *property, ValueContext: context})
		}
	}
	return nil
}

func populateSourceFields(a ProviderAssertion, f SourceClaimCurrentness, selected, fresh bool, snapshotID string) SourceClaimCurrentness {
	selectors, _ := sourceSelectors([]SourceAssertionPayload{a.Payload})
	prior := f.Fields
	f.Fields = []TypedFieldCurrentness{}
	for _, property := range selectors {
		value, err := SelectSourceProperty(a.Payload, property)
		if err != nil || !value.Present {
			continue
		}
		field := TypedFieldCurrentness{Property: property, Own: f.Own, Dependency: AssertionFreshness{Status: "current", Reasons: []string{}}}
		retained := false
		if !fresh {
			for _, old := range prior {
				if old.Property == property {
					field = old
					retained = true
					if selected {
						field.Own.Status = "stale"
						field.Own.Reasons = append(slices.Clone(field.Own.Reasons), "not_reobserved")
					}
					break
				}
			}
		}
		if retained {
			f.Fields = append(f.Fields, field)
			continue
		}
		if property.Kind == "relational_facet" {
			facets, _, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
			if err == nil {
				var facet relationalFacet
				if json.Unmarshal(facets[property.FacetKey], &facet) == nil && facet.Freshness != nil {
					field.Own = *facet.Freshness
					if selected && (!fresh || facet.SourceSnapshotID != snapshotID) {
						field.Own.Status = "stale"
						field.Own.Reasons = append(slices.Clone(field.Own.Reasons), "not_reobserved")
					}
				}
			}
		}
		f.Fields = append(f.Fields, field)
	}
	return f
}

func retainSourceFacets(a, old ProviderAssertion, retained map[string]bool) ProviderAssertion {
	prior, _, err := relationalFacetObject(old.Payload.Kind, old.Payload.Attributes)
	if err != nil {
		return a
	}
	current, _, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
	if err != nil {
		if a.Payload.Kind != "datastore" && a.Payload.Kind != "symbol" {
			return a
		}
		current = map[string]jsontext.Value{}
	}
	for key, raw := range prior {
		if _, ok := current[key]; ok {
			continue
		}
		current[key] = raw
		var facet relationalFacet
		if json.Unmarshal(raw, &facet) == nil {
			for _, eid := range facet.EvidenceIDs {
				retained[eid] = true
				a.EvidenceIDs = append(a.EvidenceIDs, eid)
			}
		}
		for _, binding := range old.DependencyClaims {
			if binding.Property.Kind == "relational_facet" && binding.Property.FacetKey == key {
				a.DependencyClaims = append(a.DependencyClaims, binding)
			}
		}
	}
	a.Payload.Attributes = replaceRelationalFacets(a.Payload.Kind, a.Payload.Attributes, current)
	slices.Sort(a.EvidenceIDs)
	a.EvidenceIDs = slices.Compact(a.EvidenceIDs)
	return a
}

func sourceStructuralAttributes(p SourceAssertionPayload) map[string]jsontext.Value {
	if !relationalSubject(p.Kind, p.Attributes, p.RecordType == "edge") {
		return p.Attributes
	}
	facets, _, err := relationalFacetObject(p.Kind, p.Attributes)
	if err != nil {
		return p.Attributes
	}
	for key, raw := range facets {
		facet, err := relationalObject(raw)
		if err != nil {
			continue
		}
		for _, member := range []string{"sourceKind", "sourceSnapshotId", "evidenceIds", "freshness"} {
			delete(facet, member)
		}
		facets[key], _ = json.Marshal(facet)
	}
	return replaceRelationalFacets(p.Kind, p.Attributes, facets)
}
