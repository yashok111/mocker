package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func graphTarget(revision string, legacy, full *ProposalReadTarget, candidate *ImportCandidateReadTarget) BackendReadTarget {
	return BackendReadTarget{RevisionID: revision, Proposal: legacy, ChangeProposal: full, ImportCandidate: candidate}
}
func rejectStagedView(target BackendReadTarget) error {
	if target.ImportCandidate != nil {
		return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Staged targets support graph/node/evidence/coverage/assertions only"}
	}
	return nil
}

func advancedReadQuery(raw []byte) bool {
	fields, err := relationalObject(raw)
	if err != nil {
		return false
	}
	return fields["proposal"] != nil || fields["changeProposal"] != nil || fields["importCandidate"] != nil
}
func decodeAdvancedReadQuery(raw []byte, out any) error {
	// A dedicated target decoder retains explicit presence/XOR before numeric or
	// pointer zero values can erase an invalid advanced arm.
	fields, err := relationalObject(raw)
	if err != nil {
		return err
	}
	targetFields := map[string]jsontext.Value{}
	for _, key := range []string{"revisionId", "proposal", "changeProposal", "importCandidate"} {
		if value, ok := fields[key]; ok {
			targetFields[key] = value
		}
	}
	targetRaw, err := json.Marshal(targetFields)
	if err != nil {
		return err
	}
	var target BackendReadTarget
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		return err
	}
	if err := rejectStagedView(target); err != nil {
		return err
	}
	switch value := out.(type) {
	case *FlowQueryInput:
		if err := validateAdvancedQueryWire(raw, []string{"view"}, MaxGraphPageSize); err != nil {
			return err
		}
		type plain FlowQueryInput
		var decoded plain
		if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		*value = FlowQueryInput(decoded)
		value.present = map[string]bool{}
		for key := range fields {
			value.present[key] = true
		}
		return value.validate()
	case *LineageQueryInput:
		if err := validateAdvancedQueryWire(raw, []string{"seed", "direction"}, 100); err != nil {
			return err
		}
		type plain LineageQueryInput
		var decoded plain
		if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		*value = LineageQueryInput(decoded)
		return value.validate()
	case *EventsQueryInput:
		if err := validateAdvancedQueryWire(raw, []string{"view"}, 100); err != nil {
			return err
		}
		type plain EventsQueryInput
		var decoded plain
		if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
			return err
		}
		*value = EventsQueryInput(decoded)
		value.present = map[string]bool{}
		for key := range fields {
			value.present[key] = true
		}
		return value.validate()
	}
	return invalid("body", "Unsupported effective query input")
}

func validateAdvancedQueryWire(raw []byte, required []string, maxLimit int) error {
	fields, err := relationalObject(raw)
	if err != nil {
		return invalid("body", "Expected a strict query object")
	}
	for _, key := range required {
		if _, present := fields[key]; !present {
			return invalid(key, "Required member is absent")
		}
	}
	targetFields := map[string]jsontext.Value{}
	for _, key := range []string{"revisionId", "proposal", "changeProposal", "importCandidate"} {
		if value, present := fields[key]; present {
			targetFields[key] = value
		}
	}
	targetRaw, err := json.Marshal(targetFields)
	if err != nil {
		return err
	}
	var target BackendReadTarget
	if err := json.Unmarshal(targetRaw, &target); err != nil {
		return err
	}
	if err := rejectStagedView(target); err != nil {
		return err
	}
	for key, value := range fields {
		if string(value) == "null" {
			return invalid(key, "Null is not a query selector")
		}
		if key == "limit" || key == "maxDepth" {
			var number int
			if err := json.Unmarshal(value, &number); err != nil {
				return invalid(key, "Expected a positive exact integer")
			}
			ceiling := maxLimit
			if key == "maxDepth" {
				ceiling = 32
			}
			if number < 1 || number > ceiling {
				return invalid(key, "Query integer outside range")
			}
		}
	}
	return nil
}
