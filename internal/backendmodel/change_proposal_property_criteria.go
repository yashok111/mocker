package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"slices"
	"unicode/utf8"
)

// Expected values are typed declarations. Validate their shape and property
// applicability without requiring the referenced values to match today's graph.
func validateChangeSourceExpected(schema string, payload SourceAssertionPayload, selector TypedSourcePropertySelector, expected SourcePropertyValue) error {
	next, err := ApplySourceProperty(payload, selector, expected)
	if err != nil {
		return err
	}
	switch selector.Kind {
	case "name":
		return validateChangeExpectedName(expected)
	case "parent":
		return validateChangeExpectedParent(expected)
	case "edge_endpoints":
		return validateChangeExpectedEndpoints(expected)
	default:
		return validateSourceStructuralAttributes(structuralProfile(schema), next.Kind, next.Attributes, next.RecordType == "edge")
	}
}
func validateChangeExpectedName(expected SourcePropertyValue) error {
	if !expected.Present {
		return invalid("expected", "A node name cannot be absent")
	}
	var name string
	if err := json.Unmarshal(expected.Value, &name); err != nil {
		return invalid("expected", "A node name must be a string")
	}
	if !nonblank(name) || utf8.RuneCountInString(name) > MaxNameLength {
		return invalid("expected", "A node name requires 1–200 characters without control characters")
	}
	return nil
}
func validateChangeExpectedParent(expected SourcePropertyValue) error {
	if !expected.Present || bytes.Equal(bytes.TrimSpace(expected.Value), []byte("null")) {
		return nil
	}
	var id string
	if err := json.Unmarshal(expected.Value, &id); err != nil || !ValidID(id) {
		return invalid("expected", "A parent must be a canonical UUID or null")
	}
	return nil
}
func validateChangeExpectedEndpoints(expected SourcePropertyValue) error {
	if !expected.Present {
		return invalid("expected", "Edge endpoints cannot be absent")
	}
	object, err := relationalObject(expected.Value)
	if err != nil {
		return invalid("expected", "Edge endpoints require an object")
	}
	if err = relationalFields(object, []string{"from", "to"}, nil); err != nil {
		return invalid("expected", err.Error())
	}
	for _, field := range []string{"from", "to"} {
		var id string
		if err = json.Unmarshal(object[field], &id); err != nil || !ValidID(id) {
			return invalid("expected", "Both endpoints require canonical non-null UUIDs")
		}
	}
	return nil
}

func (r changeCriteriaReader) proposalField(c ChangeCriterion, selector EffectivePropertySelector) error {
	if selector.Kind == "edge_name" {
		if c.RecordType != "edge" {
			return invalid("selector", "Edge display names apply only to edges")
		}
		if !c.Expected.Present {
			return nil
		}
		var name string
		if err := json.Unmarshal(c.Expected.Value, &name); err != nil {
			return invalid("expected", "Edge display name must be a string")
		}
		_, err := normalizeName(name)
		return err
	}
	if selector.RecordType != c.RecordType || selector.ID != c.ID {
		return invalid("selector", "Identity property selector must address its criterion subject")
	}
	switch selector.Kind {
	case "source_identity":
		found := slices.ContainsFunc(r.e.source.Identities, func(identity QualifiedSourceIdentity) bool {
			return identity.RecordType == c.RecordType && identity.ID == c.ID && identity.RepositoryID == selector.RepositoryID && identity.ProviderNamespace == selector.ProviderNamespace
		})
		if !found {
			return invalid("selector", "Source identity property must resolve against the exact baseline provider")
		}
	case "intent_identity":
		reserved, ok := r.e.used[c.ID]
		if !ok || reserved.RecordType != c.RecordType || reserved.Origin.Kind != "intent" {
			return invalid("selector", "Intent identity property requires a proposal-created object")
		}
	default:
		return invalid("selector", "Unsupported proposal property")
	}
	return validateChangeIdentityExpected(c.Expected, selector.Kind == "intent_identity")
}
func validateChangeIdentityExpected(expected *SourcePropertyValue, nullable bool) error {
	if !expected.Present {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(expected.Value), []byte("null")) {
		if nullable {
			return nil
		}
		return invalid("expected", "A source identity key cannot be null")
	}
	var key string
	if err := json.Unmarshal(expected.Value, &key); err != nil || !externalKey(key) {
		return invalid("expected", "Identity key must be a bounded nonempty string")
	}
	return nil
}
