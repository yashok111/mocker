package backendmodel

import (
	"encoding/json/jsontext"
	"slices"
)

func representationOwner(kind string) bool {
	return slices.Contains([]string{"domain_entity", "dto", "api_schema"}, kind)
}

func representationSubject(kind string, edge bool) bool {
	return !edge && (representationOwner(kind) || kind == "representation_field")
}

func representationMapping(kind string, attrs map[string]jsontext.Value, edge bool) bool {
	if edge || kind != "field_mapping" {
		return false
	}
	sources, _ := relationalArray(attrs["sources"], MaxLineageSources)
	for _, raw := range append(sources, attrs["destination"]) {
		ref, _ := relationalObject(raw)
		if runtimeString(ref["kind"]) == "representation_field" {
			return true
		}
	}
	return false
}

func validateRepresentationAttributes(kind string, a map[string]jsontext.Value, edge, persisted bool) error {
	if !representationSubject(kind, edge) {
		if kind == "field_mapping" && !edge {
			return validateLineageAttributesProfile(kind, a, edge, persisted, true, true)
		}
		return validateEventsAttributes(kind, a, edge, persisted)
	}
	required := []string{"analysisStatus", "gaps"}
	if kind == "representation_field" {
		required = append(required, "selector", "nativeType", "nullable", "cardinality")
	} else {
		required = append(required, "qualifiedName")
	}
	if err := relationalFields(a, required, []string{"description"}); err != nil {
		return err
	}
	if err := relationalEnum(a["analysisStatus"], "complete", "partial", "unknown"); err != nil {
		return err
	}
	gaps, err := relationalArray(a["gaps"], MaxRevisionEvidence)
	if err != nil {
		return err
	}
	for _, gap := range gaps {
		if err := lineageText(gap); err != nil {
			return err
		}
	}
	if (runtimeString(a["analysisStatus"]) == "complete") != (len(gaps) == 0) {
		return semantic("gaps", "Incomplete analysis requires gaps")
	}
	if d, ok := a["description"]; ok {
		if err := lineageText(d); err != nil {
			return err
		}
	}
	if kind != "representation_field" {
		return lineageText(a["qualifiedName"])
	}
	return validateRepresentationField(a)
}

func validateRepresentationField(a map[string]jsontext.Value) error {
	if err := validateRepresentationSelector(a["selector"]); err != nil {
		return err
	}
	if err := runtimeScalar(a["nativeType"], false); err != nil {
		return err
	}
	if err := relationalScalarValue(a["nullable"], "bool", false); err != nil {
		return err
	}
	if err := relationalScalarValue(a["cardinality"], "string", false, "one", "many"); err != nil {
		return err
	}
	for _, key := range []string{"nullable", "cardinality"} {
		value, _ := relationalObject(a[key])
		if runtimeString(value["status"]) == "unknown" {
			if err := lineageText(value["reason"]); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRepresentationSelector(raw jsontext.Value) error {
	path, err := relationalArray(raw, MaxRepresentationSelectorSegments)
	if err != nil {
		return err
	}
	if len(path) == 0 {
		return semantic("selector", "A representation selector requires at least one property")
	}
	for _, raw := range path {
		segment, err := relationalObject(raw)
		if err != nil {
			return err
		}
		if err := relationalFields(segment, []string{"property"}, nil); err != nil {
			return err
		}
		if err := lineageFieldName(segment["property"]); err != nil {
			return err
		}
	}
	return nil
}

func validateRepresentationLineageRef(raw jsontext.Value, persisted bool) error {
	return validateLineageRefProfile(raw, persisted, true, true)
}
