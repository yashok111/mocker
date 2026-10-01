package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

func runtimeSubject(kind string, edge bool) bool {
	if edge {
		return slices.Contains([]string{"next", "branch", "error", "returns", "reads", "writes", "deletes", "begins", "commits", "rolls_back"}, kind)
	}
	return slices.Contains([]string{"flow", "flow_step", "query", "transaction"}, kind)
}

func runtimeReferenceName(key string, persisted bool) string {
	if persisted {
		return strings.TrimSuffix(key, "Key") + "Id"
	}
	return key
}

func runtimeString(raw jsontext.Value) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func runtimeText(raw jsontext.Value) error {
	if err := relationalText(raw, false, false); err != nil {
		return err
	}
	if len(runtimeString(raw)) > MaxRuntimeTextBytes {
		return limitFault("Runtime expression, reason or label byte limit exceeded")
	}
	return nil
}

func runtimeScalar(raw jsontext.Value, expression bool) error {
	if err := relationalScalarValue(raw, "string", false); err != nil {
		return err
	}
	m, _ := relationalObject(raw)
	if runtimeString(m["status"]) == "unknown" {
		return runtimeText(m["reason"])
	}
	if expression && len(runtimeString(m["value"])) > MaxRuntimeTextBytes {
		return limitFault("Runtime expression byte limit exceeded")
	}
	return nil
}

func validateRuntimeAttributes(kind string, a map[string]jsontext.Value, edge, persisted bool) error {
	if !runtimeSubject(kind, edge) {
		return validateRelationalAttributes(kind, a, edge, persisted)
	}
	if a == nil {
		return semantic("attributes", "Runtime attributes must be an object")
	}
	required, optional := []string{}, []string{"description"}
	if !edge {
		required = append(required, "analysisStatus", "gaps")
	}
	scalar := []string{}
	text := []string{}
	native, nativeReason := "", ""
	ref := runtimeReferenceName("datastoreKey", persisted)
	switch kind {
	case "flow":
		required = append(required, runtimeReferenceName("entryStepKey", persisted), map[bool]string{false: "exitStepKeys", true: "exitStepIds"}[persisted], "exitStatus")
	case "flow_step":
		required = append(required, "stepKind", "inputs", "outputs", "transactionContext", "nativeText")
		native, nativeReason = "nativeText", "nativeReason"
		var step string
		if err := json.Unmarshal(a["stepKind"], &step); err != nil {
			return semantic("stepKind", "Step kind is required")
		}
		if err := relationalEnum(a["stepKind"], "input", "authorization", "validation", "condition", "call", "query", "transform", "transaction_begin", "transaction_commit", "transaction_rollback", "return", "raise", "loop", "parallel", "join", "opaque"); err != nil {
			return err
		}
		switch step {
		case "condition", "loop":
			required = append(required, "expression")
			scalar = append(scalar, "expression")
		case "opaque":
			required = append(required, "reason")
			text = append(text, "reason")
		case "call":
			required = append(required, "dispatchStatus")
			if err := relationalEnum(a["dispatchStatus"], "complete", "partial", "unknown"); err != nil {
				return err
			}
			if runtimeString(a["dispatchStatus"]) != "complete" {
				required = append(required, "dispatchReason")
				text = append(text, "dispatchReason")
			}
		}
	case "query":
		required = append(required, "dialect", "nativeDefinition", "parameters", "results", "columnScope")
		native, nativeReason = "nativeDefinition", "definitionReason"
	case "transaction":
		required = append(required, ref, "connectionScope", "isolationLevel", "boundaryStatus")
		scalar = append(scalar, "connectionScope", "isolationLevel")
	case "branch":
		required = append(required, "label", "condition")
		text = append(text, "label")
		scalar = append(scalar, "condition")
	case "error":
		required = append(required, "label", "outcome")
		text = append(text, "label")
	case "returns":
		required = append(required, "label")
		text = append(text, "label")
	case "reads", "writes", "deletes":
		required = append(required, "accessMode", ref, "facetKey", "columnScope")
		text = append(text, "facetKey")
		if runtimeString(a["columnScope"]) == "unknown" {
			required = append(required, "scopeReason")
			text = append(text, "scopeReason")
		}
	}
	if native != "" && bytes.Equal(bytes.TrimSpace(a[native]), []byte("null")) {
		required = append(required, nativeReason)
		text = append(text, nativeReason)
	}
	if err := relationalFields(a, required, optional); err != nil {
		return err
	}
	if d, ok := a["description"]; ok {
		if err := validateAttributes(kind, map[string]jsontext.Value{"description": d}, edge); err != nil {
			return err
		}
	}
	if !edge {
		if err := relationalEnum(a["analysisStatus"], "complete", "partial", "unsupported"); err != nil {
			return err
		}
		gaps, err := relationalArray(a["gaps"], MaxRevisionEvidence)
		if err != nil {
			return err
		}
		for _, gap := range gaps {
			if err := runtimeText(gap); err != nil {
				return err
			}
		}
		var status string
		_ = json.Unmarshal(a["analysisStatus"], &status)
		if status == "complete" && len(gaps) != 0 || status != "complete" && len(gaps) == 0 {
			return semantic("gaps", "Complete analysis has no gaps; partial and unsupported analysis require gaps")
		}
	}
	for _, key := range scalar {
		if err := runtimeScalar(a[key], key == "expression" || key == "condition"); err != nil {
			return err
		}
	}
	for _, key := range text {
		if err := runtimeText(a[key]); err != nil {
			return err
		}
	}
	if native != "" {
		if err := relationalText(a[native], true, true); err != nil {
			return err
		}
	}
	checkRef := func(key string) error {
		var value string
		if json.Unmarshal(a[key], &value) != nil || !externalKey(value) || persisted && !ValidID(value) {
			return semantic(key, "Reference must use the selected external-key or UUID mode")
		}
		return nil
	}
	switch kind {
	case "flow":
		if err := checkRef(runtimeReferenceName("entryStepKey", persisted)); err != nil {
			return err
		}
		if _, err := relationalStrings(a[map[bool]string{false: "exitStepKeys", true: "exitStepIds"}[persisted]], MaxRuntimeExitReferences, persisted); err != nil {
			return err
		}
		if err := relationalEnum(a["exitStatus"], "complete", "partial", "unknown"); err != nil {
			return err
		}
		if runtimeString(a["exitStatus"]) != "complete" && runtimeString(a["analysisStatus"]) == "complete" {
			return semantic("exitStatus", "Incomplete exit inventory requires analysis gaps")
		}
	case "flow_step":
		if err := validateRuntimePorts(a, "inputs", "outputs"); err != nil {
			return err
		}
		m, err := relationalObject(a["transactionContext"])
		if err != nil {
			return err
		}
		var status string
		_ = json.Unmarshal(m["status"], &status)
		if status == "known" {
			key := runtimeReferenceName("transactionKey", persisted)
			if err := relationalFields(m, []string{"status", key}, nil); err != nil {
				return err
			}
			var v string
			if json.Unmarshal(m[key], &v) != nil || !externalKey(v) || persisted && !ValidID(v) {
				return semantic("transactionContext", "Invalid local transaction reference")
			}
		} else {
			if status != "none" && status != "unknown" {
				return semantic("transactionContext/status", "Invalid transaction context status")
			}
			if err := relationalFields(m, []string{"status", "reason"}, nil); err != nil {
				return err
			}
			if err := runtimeText(m["reason"]); err != nil {
				return err
			}
		}
	case "query":
		if err := relationalEnum(a["dialect"], "postgresql", "sqlite", "unknown"); err != nil {
			return err
		}
		if err := relationalEnum(a["columnScope"], "complete", "partial", "unknown"); err != nil {
			return err
		}
		if err := validateRuntimePorts(a, "parameters", "results"); err != nil {
			return err
		}
	case "transaction":
		if err := checkRef(ref); err != nil {
			return err
		}
		if err := relationalEnum(a["boundaryStatus"], "complete", "partial", "unknown"); err != nil {
			return err
		}
	case "error":
		return relationalEnum(a["outcome"], "error", "timeout", "retry")
	case "reads", "writes", "deletes":
		if err := checkRef(ref); err != nil {
			return err
		}
		if err := relationalEnum(a["columnScope"], "listed", "unknown"); err != nil {
			return err
		}
		modes := map[string][]string{"reads": {"read"}, "writes": {"insert", "update", "upsert"}, "deletes": {"delete"}}
		return relationalEnum(a["accessMode"], modes[kind]...)
	}
	return nil
}

func validateRuntimePorts(a map[string]jsontext.Value, first, second string) error {
	seen := map[string]bool{}
	count := 0
	for _, key := range []string{first, second} {
		ports, err := relationalArray(a[key], MaxRuntimePorts)
		if err != nil {
			return err
		}
		count += len(ports)
		if count > MaxRuntimePorts {
			return limitFault("Runtime local port limit exceeded")
		}
		for i, p := range ports {
			m, err := relationalObject(p)
			if err != nil {
				return err
			}
			if err := relationalFields(m, []string{"key", "name", "nativeType"}, nil); err != nil {
				return err
			}
			var address string
			if json.Unmarshal(m["key"], &address) != nil || len(address) < 1 || len(address) > 128 || strings.ContainsFunc(address, func(r rune) bool { return r < 33 || r > 126 }) || seen[address] {
				return semantic(fmt.Sprintf("%s/%d/key", key, i), "Local port keys must be unique printable ASCII without spaces")
			}
			seen[address] = true
			if err := relationalText(m["name"], false, false); err != nil {
				return err
			}
			if err := runtimeScalar(m["nativeType"], false); err != nil {
				return err
			}
		}
	}
	return nil
}
