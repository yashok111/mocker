package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	MaxProposalCommands     = 100
	MaxProposalCriteria     = 100
	MaxProposalTextBytes    = 4096
	MaxProposalCommandBytes = 1 << 20
)

func (c ProposalCommand) MarshalJSON() ([]byte, error) {
	type command ProposalCommand
	b, err := json.Marshal(command(c))
	if err != nil {
		return nil, err
	}
	if c.Type == "set_criteria" {
		m, err := relationalObject(b)
		if err != nil {
			return nil, err
		}
		m["criteria"], err = json.Marshal(c.Criteria, json.FormatNilSliceAsNull(false))
		if err != nil {
			return nil, err
		}
		return json.Marshal(m)
	}
	return b, nil
}

func (c *ProposalCommand) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("commands", "Expected a strict typed proposal command")
	}
	var typ, action string
	if json.Unmarshal(m["type"], &typ) != nil {
		return invalid("type", "Command type is required")
	}
	required := []string{"type", "commandId", "reason"}
	switch typ {
	case "alter_column":
		required = append(required, "columnId", "nullable")
	case "alter_constraint":
		if json.Unmarshal(m["action"], &action) != nil {
			return invalid("action", "FK action is required")
		}
		required = append(required, "action", "targetTableId", "columnPairs", "updateAction", "deleteAction", "matchType", "deferrable", "initiallyDeferred")
		switch action {
		case "create":
			required = append(required, "name", "tableId")
		case "update":
			required = append(required, "constraintId")
		default:
			return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Only create/update FK commands are supported"}
		}
	case "set_criteria":
		required = append(required, "criteria")
	default:
		return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Unsupported database proposal command"}
	}
	if err := relationalFields(m, required, nil); err != nil {
		return invalid("commands", err.Error())
	}
	for k, raw := range m {
		raw = bytes.TrimSpace(raw)
		switch k {
		case "nullable", "deferrable", "initiallyDeferred":
			if string(raw) != "true" && string(raw) != "false" {
				return invalid(k, "Expected a boolean")
			}
		case "columnPairs", "criteria":
			if len(raw) == 0 || raw[0] != '[' {
				return invalid(k, "Expected an array")
			}
		default:
			if len(raw) == 0 || raw[0] != '"' {
				return invalid(k, "Expected a string")
			}
		}
	}
	if typ == "alter_constraint" {
		var pairs []jsontext.Value
		if err := json.Unmarshal(m["columnPairs"], &pairs); err != nil {
			return invalid("columnPairs", err.Error())
		}
		if len(pairs) == 0 {
			return invalid("columnPairs", "Use nonempty ordered column pairs")
		}
		if len(pairs) > MaxRelationalOrderedColumns {
			return proposalLimit("At most 64 ordered column pairs are supported")
		}
		for _, pair := range pairs {
			fields, err := relationalObject(pair)
			if err != nil {
				return invalid("columnPairs", "Expected a strict column pair object")
			}
			if err := relationalFields(fields, []string{"fromColumnId", "toColumnId"}, nil); err != nil {
				return invalid("columnPairs", err.Error())
			}
			for _, raw := range fields {
				if err := relationalText(raw, false, false); err != nil {
					return invalid("columnPairs", err.Error())
				}
			}
		}
	}
	if typ == "set_criteria" {
		var criteria []jsontext.Value
		if err := json.Unmarshal(m["criteria"], &criteria); err != nil {
			return invalid("criteria", err.Error())
		}
		if len(criteria) > MaxProposalCriteria {
			return proposalLimit("At most 100 supplemental criteria are supported")
		}
		keys := map[string]bool{}
		for _, raw := range criteria {
			fields, err := relationalObject(raw)
			if err != nil {
				return invalid("criteria", "Expected a strict recommendation object")
			}
			if err := relationalFields(fields, []string{"key", "kind", "targetIds", "description"}, nil); err != nil {
				return invalid("criteria", err.Error())
			}
			for _, k := range []string{"key", "kind", "description"} {
				if err := relationalText(fields[k], false, false); err != nil {
					return invalid("criteria", err.Error())
				}
			}
			var criterion ProposalCriterionInput
			if err := json.Unmarshal(raw, &criterion, json.RejectUnknownMembers(true)); err != nil {
				return invalid("criteria", err.Error())
			}
			if validateKey(criterion.Key) != nil || keys[criterion.Key] {
				return invalid("criteria", "Recommendation keys must be unique printable ASCII without spaces")
			}
			keys[criterion.Key] = true
			if !slices.Contains([]string{"existing_data", "writers", "referential_integrity", "target_uniqueness", "migration_plan"}, criterion.Kind) {
				return invalid("criteria", "Unsupported recommendation kind")
			}
			if !proposalText(criterion.Description) {
				return invalid("criteria", "Description must be nonblank UTF-8, at most 4096 bytes")
			}
			if _, err := relationalStrings(fields["targetIds"], MaxRevisionNodes, true); err != nil || len(criterion.TargetIDs) == 0 {
				return invalid("targetIds", "Select nonempty unique canonical target UUIDs")
			}
		}
	}
	type command ProposalCommand
	var decoded command
	if err := json.Unmarshal(b, &decoded, json.RejectUnknownMembers(true)); err != nil {
		return invalid("commands", err.Error())
	}
	if validateKey(decoded.CommandID) != nil {
		return invalid("commandId", "Use 1–128 printable ASCII characters without spaces")
	}
	if !proposalText(decoded.Reason) {
		return invalid("reason", "Reason must be nonblank UTF-8, at most 4096 bytes")
	}
	if typ == "alter_constraint" {
		if action == "create" && (!nonblank(decoded.Name) || !utf8.ValidString(decoded.Name)) {
			return invalid("name", "Constraint name must be nonblank UTF-8")
		}
		for _, value := range []string{decoded.UpdateAction, decoded.DeleteAction} {
			if !slices.Contains([]string{"no_action", "restrict", "cascade", "set_null", "set_default"}, value) {
				return invalid("action", "Unsupported FK action")
			}
		}
		if !slices.Contains([]string{"simple", "full", "partial"}, decoded.MatchType) {
			return invalid("matchType", "Unsupported FK match mode")
		}
	}
	*c = ProposalCommand(decoded)
	return nil
}

func proposalText(s string) bool {
	return utf8.ValidString(s) && len(s) <= MaxProposalTextBytes && strings.TrimSpace(s) != ""
}

func validateProposalCommands(commands []ProposalCommand) error {
	if len(commands) == 0 {
		return invalid("commands", "At least one typed command is required")
	}
	if len(commands) > MaxProposalCommands {
		return proposalLimit("At most 100 proposal commands are supported")
	}
	b, err := json.Marshal(commands)
	if err != nil {
		return invalid("commands", err.Error())
	}
	if len(b) > MaxProposalCommandBytes {
		return proposalLimit("Proposal command batch exceeds 1 MiB")
	}
	var decoded []ProposalCommand
	if err := json.Unmarshal(b, &decoded); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range commands {
		if seen[c.CommandID] {
			return invalid("commandId", "Command IDs must be unique within the batch")
		}
		seen[c.CommandID] = true
	}
	return nil
}

func proposalLimit(message string) *FaultError {
	return &FaultError{Status: 413, Code: "backend_limit", Message: message}
}
