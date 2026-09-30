package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/responserules"
)

// Raw payloads exercise the original-byte MCP adapter and the shared admin
// decoder, so an SDK conversion through float64 cannot silently pass.
func TestResponseRuleResultConditionMCPIntegration(t *testing.T) {
	t.Parallel()
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	call := func(name string, input any) json.RawMessage {
		t.Helper()
		out, message := callTool(t, server, name, responseRuleIntegrationJSON(t, input))
		if message != "" {
			t.Fatalf("%s: %s", name, message)
		}
		return out
	}
	decodeDetail := func(raw []byte) apidesign.Detail {
		t.Helper()
		var detail apidesign.Detail
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}
	document := `{"openapi":"3.1.0","info":{"title":"Results","version":"1"},"paths":{"/widgets":{"get":{"responses":{"200":{"description":"OK"}}}}},"x-retained":{"n":9007199254740993}}`
	detail := decodeDetail(call("create_api_design", map[string]any{"name": "Result conditions", "document": document}))
	designID := detail.Design.ID
	const ruleJSON = `{"id":"result-rule","name":"Exact result","binding":{"method":"GET","path":"/widgets"},"nodes":[
{"id":"yes","type":"response","name":"Yes","x":600,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"{\"branch\":\"yes\"}"}},
{"id":"start","type":"start","name":"Start","x":0,"y":0},
{"id":"read","type":"entity_read","name":"Read","x":200,"y":0,"entity":{"family":"/widgets","operation":"get","key":{"source":"query","name":"id"}}},
{"id":"check","type":"condition","name":"Check","x":400,"y":0,"resultCondition":{"source":{"source":"result","nodeId":"read","pointer":"/n"},"op":"equals","valueJSON":"9007199254740993"}},
{"id":"no","type":"response","name":"No","x":600,"y":200,"response":{"status":409,"mediaType":"application/json","headers":[],"bodyJSON":"null"}},
{"id":"missing","type":"response","name":"Missing","x":400,"y":200,"response":{"status":404,"mediaType":"application/json","headers":[],"bodyJSON":"null"}}],
"edges":[{"id":"begin","from":"start","port":"next","to":"read"},{"id":"found","from":"read","port":"found","to":"check"},{"id":"absent","from":"read","port":"missing","to":"missing"},{"id":"true","from":"check","port":"true","to":"yes"},{"id":"false","from":"check","port":"false","to":"no"}]}`
	var rule map[string]any
	if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
		t.Fatal(err)
	}
	detail = decodeDetail(call("create_response_rule", map[string]any{"designId": designID, "expectedVersion": 1, "rule": rule}))
	if detail.Design.Version != 2 || !strings.Contains(detail.Draft.Document, "9007199254740993") {
		t.Fatalf("create lost exact values: %+v", detail)
	}
	rule["name"] = "Saved exact result"
	detail = decodeDetail(call("save_response_rule", map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": 2, "rule": rule}))
	node := rule["nodes"].([]any)[3].(map[string]any)
	condition := node["resultCondition"].(map[string]any)
	condition["valueJSON"] = "9007199254740993.0"
	added := maps.Clone(node)
	added["id"] = "scratch"
	detail = decodeDetail(call("apply_response_rule_commands", map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": 3, "commands": []any{
		map[string]any{"type": "add_node", "node": added}, map[string]any{"type": "update_node", "node": node}, map[string]any{"type": "remove_node", "nodeId": "scratch"},
	}}))
	if detail.Design.Version != 4 || len(detail.Revisions) != 4 || !strings.Contains(detail.Draft.Document, "9007199254740993.0") || !strings.Contains(detail.Draft.Document, "x-retained") {
		t.Fatalf("update lost authored text or revisions: %+v", detail)
	}
	readArgs := map[string]any{"designId": designID, "ruleId": "result-rule"}
	read := call("get_response_rule", readArgs)
	if err := resultConditionContractSchema(t, "ResponseRuleDetail").Validate(responseRuleIntegrationValue(t, read)); err != nil {
		t.Fatalf("saved graph violates output schema: %s\n%v", read, err)
	}
	var saved struct {
		Version int64              `json:"version"`
		Rule    responserules.Rule `json:"rule"`
	}
	if err := json.Unmarshal(read, &saved); err != nil || saved.Version != 4 || saved.Rule.Nodes[3].ResultCondition == nil || saved.Rule.Nodes[3].ResultCondition.ValueJSON == nil || *saved.Rule.Nodes[3].ResultCondition.ValueJSON != "9007199254740993.0" {
		t.Fatalf("read lost predicate: %s %v", read, err)
	}
	validation := call("validate_response_rule", readArgs)
	var valid responserules.Validation
	if err := json.Unmarshal(validation, &valid); err != nil || !valid.Valid {
		t.Fatalf("valid result graph rejected: %s %v", validation, err)
	}
	if err := resultConditionContractSchema(t, "ResponseRuleValidation").Validate(responseRuleIntegrationValue(t, validation)); err != nil {
		t.Fatal(err)
	}
	simulationSchema := resultConditionContractSchema(t, "ResponseRuleSimulation")
	for _, tt := range []struct {
		name, row, id, terminal, actual string
		status                          int
		matched                         bool
	}{
		{"exact large integer", `{"id":1,"n":9007199254740993}`, "1", "yes", "9007199254740993", 200, true},
		{"neighbor does not round equal", `{"id":1,"n":9007199254740992}`, "1", "no", "9007199254740992", 409, false},
		{"exponent decimal equality", `{"id":1,"n":90071992547409930e-1}`, "1", "yes", "90071992547409930e-1", 200, true},
		{"null is present unequal", `{"id":1,"n":null}`, "1", "no", "null", 409, false},
		{"missing entity skips condition", `{"id":1,"n":9007199254740993}`, "2", "missing", "", 404, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := map[string]any{"query": []any{map[string]any{"name": "id", "value": tt.id}, map[string]any{"name": "n", "value": "9007199254740993"}}, "headers": []any{}, "entities": []any{map[string]any{
				"family": "/widgets", "idField": "id", "idType": "integer", "rows": []any{map[string]any{"key": "1", "scope": []any{}, "dataJSON": tt.row}},
			}}}
			out := call("simulate_response_rule", map[string]any{"designId": designID, "ruleId": "result-rule", "request": request})
			var run responseRuleIntegrationResult
			if err := json.Unmarshal(out, &run); err != nil || !run.Valid || run.TerminalNodeID != tt.terminal || run.Response == nil || run.Response.Status != tt.status {
				t.Fatalf("wrong branch: %s %v", out, err)
			}
			if tt.actual != "" {
				step := run.Trace[2]
				trace := step.ResultCondition
				if step.NodeID != "check" || step.Matched == nil || *step.Matched != tt.matched || step.EdgeID != fmt.Sprint(tt.matched) || trace == nil || trace.SourceNodeID != "read" || trace.Pointer != "/n" || trace.Op != "equals" || !trace.Present || trace.ActualJSON == nil || *trace.ActualJSON != tt.actual || trace.ExpectedJSON == nil || *trace.ExpectedJSON != "9007199254740993.0" {
					t.Fatalf("imprecise trace: %+v", run.Trace)
				}
			} else if len(run.Trace) != 3 || run.Trace[2].ResultCondition != nil {
				t.Fatalf("missing entity evaluated predicate: %+v", run.Trace)
			}
			if err := simulationSchema.Validate(responseRuleIntegrationValue(t, out)); err != nil {
				t.Fatalf("simulation violates output schema: %s\n%v", out, err)
			}
			status, direct, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", fmt.Sprintf("/api/designs/%d/response-rules/result-rule/simulate", designID), []byte(responseRuleIntegrationJSON(t, map[string]any{"request": request})))
			if err != nil || status != 200 || !reflect.DeepEqual(responseRuleIntegrationValue(t, out), responseRuleIntegrationValue(t, direct)) {
				t.Fatalf("HTTP/MCP differ: status=%d MCP=%s HTTP=%s %v", status, out, direct, err)
			}
		})
	}

	_, message := callTool(t, server, "save_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": 3, "rule": rule}))
	if !strings.Contains(message, "409") || !strings.Contains(message, `"version":4`) {
		t.Fatalf("missing CAS conflict: %s", message)
	}
	condition["op"] = "not_equals"
	commands := []any{map[string]any{"type": "update_node", "node": node}, map[string]any{"type": "remove_edge", "edgeId": "unavailable"}}
	_, message = callTool(t, server, "apply_response_rule_commands", responseRuleIntegrationJSON(t, map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": 4, "commands": commands}))
	if !strings.Contains(message, "400") {
		t.Fatalf("bad batch accepted: %s", message)
	}
	condition["op"] = "equals"
	both := resultConditionSchemaNode(map[string]any{"source": map[string]any{"source": "result", "nodeId": "read"}, "op": "exists"})
	both["condition"] = map[string]any{"in": "query", "name": "n", "op": "exists"}
	for _, badNode := range []map[string]any{
		resultConditionSchemaNode(map[string]any{"source": map[string]any{"source": "query", "name": "n"}, "op": "exists"}),
		resultConditionSchemaNode(map[string]any{"source": map[string]any{"source": "result", "nodeId": "read"}, "op": "exists", "valueJSON": nil}),
		resultConditionSchemaNode(map[string]any{"source": map[string]any{"source": "result", "nodeId": "read"}, "op": "equals", "valueJSON": "{}"}),
		both,
	} {
		badNode["id"] = "check"
		commands := []any{map[string]any{"type": "update_node", "node": badNode}}
		_, message = callTool(t, server, "apply_response_rule_commands", responseRuleIntegrationJSON(t, map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": 4, "commands": commands}))
		if message == "" {
			t.Fatal("MCP accepted malformed condition")
		}
		status, direct, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", fmt.Sprintf("/api/designs/%d/response-rules/result-rule/commands", designID), []byte(responseRuleIntegrationJSON(t, map[string]any{"expectedVersion": 4, "commands": commands})))
		if err != nil || status != http.StatusBadRequest {
			t.Fatalf("REST accepted malformed condition: status=%d body=%s err=%v", status, direct, err)
		}
	}
	if unchanged := decodeDetail(call("get_api_design", map[string]any{"designId": designID})); !reflect.DeepEqual(unchanged, detail) {
		t.Fatal("simulation, CAS or refused commands changed document/version/revisions")
	}

	// Structural admission preserves an unavailable selection for editing, while
	// semantic validation and simulation refuse executing it.
	condition["source"].(map[string]any)["nodeId"] = "future"
	detail = decodeDetail(call("save_response_rule", map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": 4, "rule": rule}))
	read = call("get_response_rule", readArgs)
	if err := json.Unmarshal(read, &saved); err != nil || detail.Design.Version != 5 || saved.Rule.Nodes[3].ResultCondition == nil || saved.Rule.Nodes[3].ResultCondition.Source.NodeID != "future" {
		t.Fatalf("unavailable draft source was not retained: %+v", detail)
	}
	validation = call("validate_response_rule", readArgs)
	if err := json.Unmarshal(validation, &valid); err != nil || valid.Valid || len(valid.Diagnostics) == 0 {
		t.Fatalf("unavailable source was executable: %s %v", validation, err)
	}
	var invalid responseRuleIntegrationResult
	out := call("simulate_response_rule", map[string]any{"designId": designID, "ruleId": "result-rule", "request": map[string]any{"query": []any{}, "headers": []any{}}})
	if err := json.Unmarshal(out, &invalid); err != nil || invalid.Valid || invalid.Outcome != "invalid" || len(invalid.Trace) != 0 {
		t.Fatalf("unavailable source was simulated: %s %v", out, err)
	}
	condition["source"].(map[string]any)["nodeId"] = "read"
	for _, tt := range []struct {
		name, op, pointer, expected, actual string
		present, matched                    bool
	}{
		{"exists explicit null", "exists", "/n", "", "null", true, true},
		{"exists absent pointer", "exists", "/absent", "", "", false, false},
		{"not exists absent pointer", "not_exists", "/absent", "", "", false, true},
		{"not exists explicit null", "not_exists", "/n", "", "null", true, false},
		{"exists whole container", "exists", "", "", `{"id":1,"n":null}`, true, true},
		{"null not unequal to null", "not_equals", "/n", "null", "null", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			condition["op"] = tt.op
			condition["source"].(map[string]any)["pointer"] = tt.pointer
			delete(condition, "valueJSON")
			if tt.expected != "" {
				condition["valueJSON"] = tt.expected
			}
			detail = decodeDetail(call("save_response_rule", map[string]any{"designId": designID, "ruleId": "result-rule", "expectedVersion": detail.Design.Version, "rule": rule}))
			request := map[string]any{"query": []any{map[string]any{"name": "id", "value": "1"}}, "headers": []any{}, "entities": []any{map[string]any{
				"family": "/widgets", "idField": "id", "idType": "integer", "rows": []any{map[string]any{"key": "1", "scope": []any{}, "dataJSON": `{"id":1,"n":null}`}},
			}}}
			out := call("simulate_response_rule", map[string]any{"designId": designID, "ruleId": "result-rule", "request": request})
			var run responseRuleIntegrationResult
			if err := json.Unmarshal(out, &run); err != nil || !run.Valid || len(run.Trace) != 4 {
				t.Fatalf("predicate failed: %s %v", out, err)
			}
			step := run.Trace[2]
			trace := step.ResultCondition
			if step.Matched == nil || *step.Matched != tt.matched || step.EdgeID != fmt.Sprint(tt.matched) || trace == nil || trace.SourceNodeID != "read" || trace.Pointer != tt.pointer || trace.Op != tt.op || trace.Present != tt.present {
				t.Fatalf("wrong presence/branch trace: %+v", run.Trace)
			}
			if tt.actual == "" && trace.ActualJSON != nil || tt.actual != "" && (trace.ActualJSON == nil || *trace.ActualJSON != tt.actual) || tt.expected == "" && trace.ExpectedJSON != nil || tt.expected != "" && (trace.ExpectedJSON == nil || *trace.ExpectedJSON != tt.expected) {
				t.Fatalf("incorrect optional JSON trace fields: %s", out)
			}
			if err := simulationSchema.Validate(responseRuleIntegrationValue(t, out)); err != nil {
				t.Fatalf("simulation violates output schema: %s\n%v", out, err)
			}
			status, direct, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", fmt.Sprintf("/api/designs/%d/response-rules/result-rule/simulate", designID), []byte(responseRuleIntegrationJSON(t, map[string]any{"request": request})))
			if err != nil || status != 200 || !reflect.DeepEqual(responseRuleIntegrationValue(t, out), responseRuleIntegrationValue(t, direct)) {
				t.Fatalf("HTTP/MCP differ: status=%d MCP=%s HTTP=%s %v", status, out, direct, err)
			}
		})
	}
}

func resultConditionContractSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/result-condition-integration.json"
	if err := compiler.AddResource(uri, responseRuleIntegrationValue(t, raw)); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri + "#/components/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
