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
		return slices.Contains([]string{"next", "branch", "error", "returns", "reads", "writes", "deletes", "begins", "commits", "rolls_back", "callback_argument"}, kind)
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
	fs, err := runtimeAttributeFields(kind, a, edge, persisted)
	if err != nil {
		return err
	}
	if err := relationalFields(a, fs.required, fs.optional); err != nil {
		return err
	}
	if d, ok := a["description"]; ok {
		if err := validateAttributes(kind, map[string]jsontext.Value{"description": d}, edge); err != nil {
			return err
		}
	}
	if !edge {
		if err := validateRuntimeAnalysis(a); err != nil {
			return err
		}
	}
	if err := fs.validateValues(a); err != nil {
		return err
	}
	return validateRuntimeKindRules(kind, a, persisted)
}

// runtimeFieldSet is the member plan of one runtime record: which members
// must exist, and which of them are scalars, plain text or native text.
type runtimeFieldSet struct {
	required, optional   []string
	scalar, text         []string
	native, nativeReason string
}

func runtimeAttributeFields(kind string, a map[string]jsontext.Value, edge, persisted bool) (*runtimeFieldSet, error) {
	fs := &runtimeFieldSet{required: []string{}, optional: []string{"description"}, scalar: []string{}, text: []string{}}
	if !edge {
		fs.required = append(fs.required, "analysisStatus", "gaps")
	}
	ref := runtimeReferenceName("datastoreKey", persisted)
	switch kind {
	case "flow":
		fs.required = append(fs.required, runtimeReferenceName("entryStepKey", persisted), runtimeExitStepsKey(persisted), "exitStatus")
	case "flow_step":
		if err := fs.addStepFields(a); err != nil {
			return nil, err
		}
	case "query":
		fs.required = append(fs.required, "dialect", "nativeDefinition", "parameters", "results", "columnScope")
		fs.native, fs.nativeReason = "nativeDefinition", "definitionReason"
	case "transaction":
		fs.required = append(fs.required, ref, "connectionScope", "isolationLevel", "boundaryStatus")
		fs.scalar = append(fs.scalar, "connectionScope", "isolationLevel")
	case "branch":
		fs.required = append(fs.required, "label", "condition")
		fs.text = append(fs.text, "label")
		fs.scalar = append(fs.scalar, "condition")
	case "error":
		fs.required = append(fs.required, "label", "outcome")
		fs.text = append(fs.text, "label")
	case "returns":
		fs.required = append(fs.required, "label")
		fs.text = append(fs.text, "label")
	case "callback_argument":
		fs.required = append(fs.required, "argumentPosition", "invocationKnowledge", "reason")
		fs.text = append(fs.text, "reason")
	case "reads", "writes", "deletes":
		fs.required = append(fs.required, "accessMode", ref, "facetKey", "columnScope")
		fs.text = append(fs.text, "facetKey")
		if runtimeString(a["columnScope"]) == "unknown" {
			fs.required = append(fs.required, "scopeReason")
			fs.text = append(fs.text, "scopeReason")
		}
	}
	if fs.native != "" && bytes.Equal(bytes.TrimSpace(a[fs.native]), []byte("null")) {
		fs.required = append(fs.required, fs.nativeReason)
		fs.text = append(fs.text, fs.nativeReason)
	}
	return fs, nil
}

func runtimeExitStepsKey(persisted bool) string {
	return map[bool]string{false: "exitStepKeys", true: "exitStepIds"}[persisted]
}

// addStepFields adds what a flow step's own kind requires on top of the
// members every step carries.
func (fs *runtimeFieldSet) addStepFields(a map[string]jsontext.Value) error {
	fs.required = append(fs.required, "stepKind", "inputs", "outputs", "transactionContext", "nativeText")
	fs.native, fs.nativeReason = "nativeText", "nativeReason"
	var step string
	if err := json.Unmarshal(a["stepKind"], &step); err != nil {
		return semantic("stepKind", "Step kind is required")
	}
	if err := relationalEnum(a["stepKind"], "input", "authorization", "validation", "condition", "call", "query", "transform", "transaction_begin", "transaction_commit", "transaction_rollback", "return", "raise", "loop", "parallel", "join", "opaque"); err != nil {
		return err
	}
	switch step {
	case "condition", "loop":
		fs.required = append(fs.required, "expression")
		fs.scalar = append(fs.scalar, "expression")
	case "opaque":
		fs.required = append(fs.required, "reason")
		fs.text = append(fs.text, "reason")
	case "call":
		fs.required = append(fs.required, "dispatchStatus")
		if err := relationalEnum(a["dispatchStatus"], "complete", "partial", "unknown"); err != nil {
			return err
		}
		if runtimeString(a["dispatchStatus"]) != "complete" {
			fs.required = append(fs.required, "dispatchReason")
			fs.text = append(fs.text, "dispatchReason")
		}
	}
	return nil
}

// validateRuntimeAnalysis requires gaps exactly when analysis is incomplete.
func validateRuntimeAnalysis(a map[string]jsontext.Value) error {
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
	return nil
}

func (fs *runtimeFieldSet) validateValues(a map[string]jsontext.Value) error {
	for _, key := range fs.scalar {
		if err := runtimeScalar(a[key], key == "expression" || key == "condition"); err != nil {
			return err
		}
	}
	for _, key := range fs.text {
		if err := runtimeText(a[key]); err != nil {
			return err
		}
	}
	if fs.native != "" {
		if err := relationalText(a[fs.native], true, true); err != nil {
			return err
		}
	}
	return nil
}

func runtimeCheckRef(a map[string]jsontext.Value, key string, persisted bool) error {
	var value string
	if json.Unmarshal(a[key], &value) != nil || !externalKey(value) || persisted && !ValidID(value) {
		return semantic(key, "Reference must use the selected external-key or UUID mode")
	}
	return nil
}

// validateRuntimeKindRules holds the value rules only one record kind has.
func validateRuntimeKindRules(kind string, a map[string]jsontext.Value, persisted bool) error {
	ref := runtimeReferenceName("datastoreKey", persisted)
	switch kind {
	case "callback_argument":
		var position *int
		if err := json.Unmarshal(a["argumentPosition"], &position); err != nil || position == nil || *position < 0 || *position > 65535 {
			return semantic("argumentPosition", "Use a zero-based integer argument position from 0 to 65535")
		}
		// Proved invocations use a separate calls edge. The argument relation
		// itself never promises execution, count, atomicity or success.
		return relationalEnum(a["invocationKnowledge"], "unknown")
	case "flow":
		return validateRuntimeFlowExits(a, persisted)
	case "flow_step":
		if err := validateRuntimePorts(a, "inputs", "outputs"); err != nil {
			return err
		}
		return validateRuntimeTransactionContext(a["transactionContext"], persisted)
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
		if err := runtimeCheckRef(a, ref, persisted); err != nil {
			return err
		}
		if err := relationalEnum(a["boundaryStatus"], "complete", "partial", "unknown"); err != nil {
			return err
		}
	case "error":
		return relationalEnum(a["outcome"], "error", "timeout", "retry")
	case "reads", "writes", "deletes":
		if err := runtimeCheckRef(a, ref, persisted); err != nil {
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

func validateRuntimeFlowExits(a map[string]jsontext.Value, persisted bool) error {
	if err := runtimeCheckRef(a, runtimeReferenceName("entryStepKey", persisted), persisted); err != nil {
		return err
	}
	if _, err := relationalStrings(a[runtimeExitStepsKey(persisted)], MaxRuntimeExitReferences, persisted); err != nil {
		return err
	}
	if err := relationalEnum(a["exitStatus"], "complete", "partial", "unknown"); err != nil {
		return err
	}
	if runtimeString(a["exitStatus"]) != "complete" && runtimeString(a["analysisStatus"]) == "complete" {
		return semantic("exitStatus", "Incomplete exit inventory requires analysis gaps")
	}
	return nil
}

// validateRuntimeTransactionContext checks a step's transaction context: a
// known one names a local transaction, none/unknown carries a reason.
func validateRuntimeTransactionContext(raw jsontext.Value, persisted bool) error {
	m, err := relationalObject(raw)
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
		return nil
	}
	if status != "none" && status != "unknown" {
		return semantic("transactionContext/status", "Invalid transaction context status")
	}
	if err := relationalFields(m, []string{"status", "reason"}, nil); err != nil {
		return err
	}
	return runtimeText(m["reason"])
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
