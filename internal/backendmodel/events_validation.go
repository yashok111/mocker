package backendmodel

import (
	"encoding/json/jsontext"
	"maps"
	"slices"
)

func eventsSubject(kind string, edge bool) bool {
	if edge {
		return slices.Contains([]string{"emits", "delivered_to", "retries", "dead_letters"}, kind)
	}
	return slices.Contains([]string{"channel", "message", "consumer", "job", "event_field"}, kind)
}
func eventsEmit(kind string, attrs map[string]jsontext.Value, edge bool) bool {
	return !edge && kind == "flow_step" && runtimeString(attrs["stepKind"]) == "emit"
}
func validateEventsAttributes(kind string, a map[string]jsontext.Value, edge, persisted bool) error {
	// Emit has the same common runtime member set as transform. Normalize only
	// the enum for the unchanged strict3/4 validator, then retain the original.
	if eventsEmit(kind, a, edge) {
		normalized := maps.Clone(a)
		normalized["stepKind"] = jsontext.Value(`"transform"`)
		return validateRuntimeAttributes(kind, normalized, edge, persisted)
	}
	if !eventsSubject(kind, edge) {
		return validateEventsLineageAttributes(kind, a, edge, persisted)
	}
	required, scalar, text, refs := eventsAttributeMembers(kind, a, edge, persisted)
	if err := relationalFields(a, required, []string{"description"}); err != nil {
		return err
	}
	if err := validateEventsAttributeValues(a, edge, persisted, scalar, text, refs); err != nil {
		return err
	}
	return validateEventsKindAttributes(kind, a)
}
func eventsAttributeMembers(kind string, a map[string]jsontext.Value, edge, persisted bool) (required, scalar, text, refs []string) {
	required = []string{}
	scalar, text, refs = []string{}, []string{}, []string{}
	if !edge {
		required = append(required, "analysisStatus", "gaps")
	}
	switch kind {
	case "channel":
		scalar = []string{"protocol", "address", "scope"}
	case "message":
		required = append(required, "fieldInventory")
	case "consumer", "job":
		required = append(required, "dispatchStatus")
		if runtimeString(a["dispatchStatus"]) != "complete" {
			text = append(text, "dispatchReason")
		}
		if kind == "job" {
			required = append(required, "trigger")
		}
	case "event_field":
		required = append(required, "section", "path")
		scalar = []string{"nativeType"}
	case "emits":
		refs = []string{"channelKey"}
		required = append(required, "deliveryStatus")
	case "delivered_to":
		refs = []string{"messageKey"}
		scalar = []string{"condition", "group"}
		required = append(required, "deliveryStatus")
	case "retries":
		refs = []string{"messageKey"}
		text = []string{"reason"}
		scalar = []string{"delay", "maxAttempts"}
	case "dead_letters":
		refs = []string{"messageKey"}
		text = []string{"reason"}
	}
	if kind == "emits" || kind == "delivered_to" {
		if runtimeString(a["deliveryStatus"]) == "unknown" {
			text = append(text, "deliveryReason")
		}
	}
	required = append(required, scalar...)
	required = append(required, text...)
	for _, key := range refs {
		required = append(required, runtimeReferenceName(key, persisted))
	}
	return required, scalar, text, refs
}
func validateEventsAttributeValues(a map[string]jsontext.Value, edge, persisted bool, scalar, text, refs []string) error {
	if !edge {
		if err := relationalEnum(a["analysisStatus"], "complete", "partial", "unsupported"); err != nil {
			return err
		}
		gaps, err := relationalArray(a["gaps"], MaxRevisionEvidence)
		if err != nil {
			return err
		}
		for _, g := range gaps {
			if err := lineageText(g); err != nil {
				return err
			}
		}
		if (runtimeString(a["analysisStatus"]) == "complete") != (len(gaps) == 0) {
			return semantic("gaps", "Incomplete analysis requires gaps")
		}
	}
	if d, ok := a["description"]; ok {
		if err := lineageText(d); err != nil {
			return importAttributeError(err, "description")
		}
	}
	for _, key := range scalar {
		if err := runtimeScalar(a[key], true); err != nil {
			return importAttributeError(err, key)
		}
	}
	for _, key := range text {
		if err := lineageText(a[key]); err != nil {
			return importAttributeError(err, key)
		}
	}
	for _, key := range refs {
		key = runtimeReferenceName(key, persisted)
		value := runtimeString(a[key])
		if !externalKey(value) || persisted && !ValidID(value) {
			return semantic(key, "Invalid selected reference mode")
		}
	}
	return nil
}
func validateEventsKindAttributes(kind string, a map[string]jsontext.Value) error {
	switch kind {
	case "message", "consumer", "job":
		key := "dispatchStatus"
		if kind == "message" {
			key = "fieldInventory"
		}
		if err := relationalEnum(a[key], "complete", "partial", "unknown"); err != nil {
			return err
		}
		if runtimeString(a[key]) != "complete" && runtimeString(a["analysisStatus"]) == "complete" {
			return semantic(key, "Complete analysis cannot hide incomplete inventory or dispatch")
		}
		if kind == "job" {
			return validateEventsTrigger(a["trigger"])
		}
	case "event_field":
		if err := relationalEnum(a["section"], "payload", "headers", "key"); err != nil {
			return err
		}
		return validateEventsFieldPath(a["path"])
	case "emits", "delivered_to":
		return relationalEnum(a["deliveryStatus"], "declared", "unknown")
	}
	return nil
}
func validateEventsFieldPath(raw jsontext.Value) error {
	path, err := relationalArray(raw, 32)
	if err != nil {
		return err
	}
	for _, raw := range path {
		m, err := relationalObject(raw)
		if err != nil {
			return err
		}
		if len(m) != 1 {
			return semantic("path", "Expected one property or items segment")
		}
		if p, ok := m["property"]; ok {
			if err := lineageFieldName(p); err != nil {
				return err
			}
		} else if string(m["items"]) != "true" {
			return semantic("path", "Expected items:true")
		}
	}
	return nil
}
func validateEventsTrigger(raw jsontext.Value) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	fields := []string{"kind"}
	scalars := []string{}
	switch runtimeString(m["kind"]) {
	case "cron":
		scalars = []string{"expression", "timezone"}
	case "interval":
		scalars = []string{"duration"}
	case "manual":
	case "unknown":
		fields = append(fields, "reason")
	default:
		return semantic("trigger/kind", "Unsupported trigger")
	}
	fields = append(fields, scalars...)
	if err := relationalFields(m, fields, nil); err != nil {
		return err
	}
	for _, key := range scalars {
		if err := runtimeScalar(m[key], true); err != nil {
			return err
		}
	}
	if runtimeString(m["kind"]) == "unknown" {
		return lineageText(m["reason"])
	}
	return nil
}
