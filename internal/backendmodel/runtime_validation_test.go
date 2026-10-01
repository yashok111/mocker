package backendmodel

import (
	"encoding/json/jsontext"
	"maps"
	"strings"
	"testing"
)

func runtimeAttrs(t *testing.T, value map[string]any) map[string]jsontext.Value {
	t.Helper()
	out := map[string]jsontext.Value{}
	for k, v := range value {
		out[k] = relationalRaw(t, v)
	}
	return out
}

func runtimeStepAttrs(t *testing.T, kind string) map[string]jsontext.Value {
	t.Helper()
	v := map[string]any{"analysisStatus": "complete", "gaps": []string{}, "stepKind": kind, "inputs": []any{}, "outputs": []any{}, "transactionContext": map[string]any{"status": "none", "reason": "No local transaction"}, "nativeText": "source\ntext\twith bytes"}
	switch kind {
	case "condition", "loop":
		v["expression"] = known("ok")
	case "opaque":
		v["reason"] = "Unsupported source operation"
	case "call":
		v["dispatchStatus"] = "complete"
	}
	return runtimeAttrs(t, v)
}

func TestRuntimeAttributesShapes(t *testing.T) {
	valid := []struct {
		kind  string
		edge  bool
		attrs map[string]jsontext.Value
	}{
		{"flow", false, runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "entryStepKey": "entry", "exitStepKeys": []string{"exit"}, "exitStatus": "complete"})},
		{"query", false, runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "dialect": "postgresql", "nativeDefinition": "SELECT 1;\n", "parameters": []any{}, "results": []any{}, "columnScope": "complete"})},
		{"transaction", false, runtimeAttrs(t, map[string]any{"analysisStatus": "partial", "gaps": []string{"isolation unknown"}, "datastoreKey": "db", "connectionScope": known("tx"), "isolationLevel": unknown("Driver default"), "boundaryStatus": "complete"})},
		{"next", true, runtimeAttrs(t, map[string]any{})},
		{"branch", true, runtimeAttrs(t, map[string]any{"label": "condition true", "condition": known("ok")})},
		{"error", true, runtimeAttrs(t, map[string]any{"label": "timeout", "outcome": "timeout"})},
		{"returns", true, runtimeAttrs(t, map[string]any{"label": "success"})},
		{"reads", true, runtimeAttrs(t, map[string]any{"accessMode": "read", "datastoreKey": "db", "facetKey": "sql", "columnScope": "listed"})},
		{"writes", true, runtimeAttrs(t, map[string]any{"accessMode": "upsert", "datastoreKey": "db", "facetKey": "sql", "columnScope": "unknown", "scopeReason": "Dynamic column selection"})},
		{"deletes", true, runtimeAttrs(t, map[string]any{"accessMode": "delete", "datastoreKey": "db", "facetKey": "sql", "columnScope": "unknown", "scopeReason": "Whole table deletion"})},
		{"begins", true, runtimeAttrs(t, map[string]any{})},
		{"commits", true, runtimeAttrs(t, map[string]any{})},
		{"rolls_back", true, runtimeAttrs(t, map[string]any{})},
	}
	for _, kind := range []string{"input", "authorization", "validation", "condition", "call", "query", "transform", "transaction_begin", "transaction_commit", "transaction_rollback", "return", "raise", "loop", "parallel", "join", "opaque"} {
		valid = append(valid, struct {
			kind  string
			edge  bool
			attrs map[string]jsontext.Value
		}{"flow_step", false, runtimeStepAttrs(t, kind)})
	}
	s := &ImportSession{Profile: "runtime-flow-v1"}
	for i, v := range valid {
		t.Run(v.kind+"/"+string(rune('a'+i)), func(t *testing.T) {
			c := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "subject", Kind: v.kind, Name: "subject", Attributes: v.attrs, EvidenceKeys: []string{}}}
			if v.edge {
				c = ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "subject", Kind: v.kind, FromKey: "from", ToKey: "to", Attributes: v.attrs, EvidenceKeys: []string{}}}
			}
			if err := validateCommand(c, s); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeAttributesStrictVariants(t *testing.T) {
	base := runtimeStepAttrs(t, "condition")
	for _, tc := range []struct {
		name   string
		change func(map[string]jsontext.Value)
	}{
		{"missing expression", func(a map[string]jsontext.Value) { delete(a, "expression") }},
		{"null expression", func(a map[string]jsontext.Value) { a["expression"] = jsontext.Value("null") }},
		{"wrong scalar value", func(a map[string]jsontext.Value) { a["expression"] = jsontext.Value(`{"status":"known","value":123}`) }},
		{"duplicate keys", func(a map[string]jsontext.Value) {
			a["expression"] = jsontext.Value(`{"status":"known","status":"unknown","value":"x"}`)
		}},
		{"unexpected scalar reason", func(a map[string]jsontext.Value) {
			a["expression"] = jsontext.Value(`{"status":"known","value":"x","reason":"extra"}`)
		}},
		{"analysis complete with gap", func(a map[string]jsontext.Value) { a["gaps"] = relationalRaw(t, []string{"missing port"}) }},
		{"analysis partial without gap", func(a map[string]jsontext.Value) { a["analysisStatus"] = relationalRaw(t, "partial") }},
		{"native missing reason", func(a map[string]jsontext.Value) { a["nativeText"] = jsontext.Value("null") }},
		{"native reason when known", func(a map[string]jsontext.Value) { a["nativeReason"] = relationalRaw(t, "extra") }},
		{"native too large", func(a map[string]jsontext.Value) { a["nativeText"] = relationalRaw(t, strings.Repeat("x", (64<<10)+1)) }},
		{"expression too large", func(a map[string]jsontext.Value) {
			a["expression"] = relationalRaw(t, known(strings.Repeat("x", 4097)))
		}},
		{"unknown reason too large", func(a map[string]jsontext.Value) {
			a["expression"] = relationalRaw(t, unknown(strings.Repeat("x", 4097)))
		}},
		{"ports too many", func(a map[string]jsontext.Value) {
			ports := []any{}
			for i := range 501 {
				ports = append(ports, map[string]any{"key": string(rune('a' + i)), "name": "x", "nativeType": known("string")})
			}
			a["inputs"] = relationalRaw(t, ports)
		}},
		{"persisted import context", func(a map[string]jsontext.Value) {
			a["transactionContext"] = jsontext.Value(`{"status":"known","transactionId":"11111111-1111-4111-8111-111111111111"}`)
		}},
		{"context extra key", func(a map[string]jsontext.Value) {
			a["transactionContext"] = jsontext.Value(`{"status":"none","reason":"none","transactionKey":"tx"}`)
		}},
		{"illegal conditional field", func(a map[string]jsontext.Value) { a["dispatchStatus"] = relationalRaw(t, "complete") }},
		{"unknown attribute", func(a map[string]jsontext.Value) { a["atomic"] = jsontext.Value("true") }},
		{"port spaces", func(a map[string]jsontext.Value) {
			a["inputs"] = relationalRaw(t, []any{map[string]any{"key": "two words", "name": "x", "nativeType": known("string")}})
		}},
		{"port unknown key", func(a map[string]jsontext.Value) {
			a["inputs"] = relationalRaw(t, []any{map[string]any{"key": "key", "name": "x", "nativeType": known("string"), "value": "bad"}})
		}},
		{"port duplicate across arrays", func(a map[string]jsontext.Value) {
			p := relationalRaw(t, []any{map[string]any{"key": "key", "name": "x", "nativeType": known("string")}})
			a["inputs"], a["outputs"] = p, p
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := maps.Clone(base)
			tc.change(a)
			c := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "step", Kind: "flow_step", Name: "step", Attributes: a, EvidenceKeys: []string{}}}
			if err := validateCommand(c, &ImportSession{Profile: "runtime-flow-v1"}); err == nil {
				t.Fatal("invalid runtime attributes accepted")
			}
		})
	}
}
