package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
)

func TestResponseRuleFollowupsMCPLifecycle(t *testing.T) {
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
	detail := func(raw []byte) apidesign.Detail {
		t.Helper()
		var d apidesign.Detail
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := detail(call("create_api_design", map[string]any{"name": "Followups", "document": `{"openapi":"3.1.0","info":{"title":"Followups","version":"1"},"paths":{"/widgets":{"get":{"responses":{"200":{"description":"OK"}}}}}}`}))
	var rule map[string]any
	const rawRule = `{"id":"check","name":"Compare results","binding":{"method":"GET","path":"/widgets"},"nodes":[
{"id":"start","type":"start","name":"Start","x":0,"y":0},
{"id":"left","type":"entity_read","name":"Left","x":200,"y":0,"entity":{"family":"/widgets","operation":"list"}},
{"id":"right","type":"entity_read","name":"Right","x":400,"y":0,"entity":{"family":"/widgets","operation":"list"}},
{"id":"predicate","type":"condition","name":"Compare","x":600,"y":0,"resultCondition":{"any":[{"all":[
{"source":{"source":"result","nodeId":"left","pointer":"/0/n"},"op":"exists"},
{"source":{"source":"result","nodeId":"left","pointer":"/0/n"},"op":"greater_than","valueJSON":"9007199254740992"},
{"source":{"source":"result","nodeId":"left","pointer":"/0/n"},"op":"less_than","valueFrom":{"source":"result","nodeId":"right","pointer":"/1/n"}}
]},{"source":{"source":"result","nodeId":"left","pointer":"/0/fallback"},"op":"exists"}]}},
{"id":"yes","type":"response","name":"Yes","x":800,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"true"}},
{"id":"no","type":"response","name":"No","x":800,"y":200,"response":{"status":409,"mediaType":"application/json","headers":[],"bodyJSON":"false"}}
],"edges":[{"id":"a","from":"start","port":"next","to":"left"},{"id":"b","from":"left","port":"next","to":"right"},{"id":"c","from":"right","port":"next","to":"predicate"},{"id":"yes-edge","from":"predicate","port":"true","to":"yes"},{"id":"no-edge","from":"predicate","port":"false","to":"no"}]}`
	if err := json.Unmarshal([]byte(rawRule), &rule); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"query": []any{}, "headers": []any{}, "entities": []any{map[string]any{"family": "/widgets", "idField": "id", "idType": "integer", "rows": []any{
		map[string]any{"key": "1", "scope": []any{}, "dataJSON": `{"id":1,"n":9007199254740993}`},
		map[string]any{"key": "2", "scope": []any{}, "dataJSON": `{"id":2,"n":9007199254740994}`},
	}}}}
	example := map[string]any{"id": "precise", "name": "Exact neighboring integers", "request": request}
	rule["examples"] = []any{example}
	d = detail(call("create_response_rule", map[string]any{"designId": d.Design.ID, "expectedVersion": d.Design.Version, "rule": rule}))
	address := map[string]any{"designId": d.Design.ID, "ruleId": "check"}
	saved := call("get_response_rule", address)
	if err := followupRESTSchema(t, "ResponseRuleDetail").Validate(responseRuleIntegrationValue(t, saved)); err != nil {
		t.Fatal(err)
	}
	var item struct {
		Rule map[string]any `json:"rule"`
	}
	if err := json.Unmarshal(saved, &item); err != nil || !reflect.DeepEqual(item.Rule, rule) {
		t.Fatalf("saved rule differs: %s %v", saved, err)
	}
	byID := call("simulate_response_rule", map[string]any{"designId": d.Design.ID, "ruleId": "check", "exampleId": "precise"})
	explicit := call("simulate_response_rule", map[string]any{"designId": d.Design.ID, "ruleId": "check", "request": request})
	if !reflect.DeepEqual(responseRuleIntegrationValue(t, byID), responseRuleIntegrationValue(t, explicit)) {
		t.Fatalf("named and explicit differ: %s / %s", byID, explicit)
	}
	if err := followupRESTSchema(t, "ResponseRuleSimulation").Validate(responseRuleIntegrationValue(t, byID)); err != nil {
		t.Fatal(err)
	}
	var run responseRuleIntegrationResult
	if err := json.Unmarshal(byID, &run); err != nil || !run.Valid || run.Response == nil || run.Response.Status != 200 {
		t.Fatalf("wrong run: %s %v", byID, err)
	}
	// Assert wire trace, including the skipped OR operand and exact RHS capture.
	parsed := responseRuleIntegrationValue(t, byID).(map[string]any)
	trace := parsed["trace"].([]any)[3].(map[string]any)["resultCondition"].(map[string]any)
	if trace["op"] != "any" || trace["shortCircuited"] != true || len(trace["children"].([]any)) != 1 {
		t.Fatalf("wrong OR trace: %#v", trace)
	}
	inner := trace["children"].([]any)[0].(map[string]any)["children"].([]any)[2].(map[string]any)
	if inner["actualJSON"] != "9007199254740993" || inner["expectedJSON"] != "9007199254740994" || inner["valueFrom"].(map[string]any)["nodeId"] != "right" {
		t.Fatalf("rounded RHS trace: %#v", inner)
	}
	status, direct, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", fmt.Sprintf("/api/designs/%d/response-rules/check/simulate", d.Design.ID), []byte(`{"exampleId":"precise"}`))
	if err != nil || status != 200 || !reflect.DeepEqual(responseRuleIntegrationValue(t, byID), responseRuleIntegrationValue(t, direct)) {
		t.Fatalf("REST/MCP differ: %d %s %v", status, direct, err)
	}
	// Proposed example selection must use the proposed snapshot, without saving it.
	var proposal map[string]any
	if err := json.Unmarshal([]byte(d.Draft.Document), &proposal); err != nil {
		t.Fatal(err)
	}
	proposalRule := proposal["x-mocker-response-rules"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	proposalRequest := proposalRule["examples"].([]any)[0].(map[string]any)["request"].(map[string]any)
	proposalRequest["entities"].([]any)[0].(map[string]any)["rows"].([]any)[0].(map[string]any)["dataJSON"] = `{"id":1,"n":9007199254740992}`
	out := call("simulate_response_rule", map[string]any{"designId": d.Design.ID, "ruleId": "check", "exampleId": "precise", "document": responseRuleIntegrationJSON(t, proposal)})
	if err := json.Unmarshal(out, &run); err != nil || run.Response == nil || run.Response.Status != 409 || run.Source.Kind != "proposal" {
		t.Fatalf("wrong proposal case: %s %v", out, err)
	}
	commands := []any{map[string]any{"type": "update_example", "example": map[string]any{"id": "precise", "name": "Renamed", "request": request}}, map[string]any{"type": "add_example", "example": map[string]any{"id": "temporary", "name": "Temporary", "request": request}}, map[string]any{"type": "remove_example", "exampleId": "temporary"}}
	d = detail(call("apply_response_rule_commands", map[string]any{"designId": d.Design.ID, "ruleId": "check", "expectedVersion": d.Design.Version, "commands": commands}))
	before := call("get_response_rule", address)
	_, message := callTool(t, server, "apply_response_rule_commands", responseRuleIntegrationJSON(t, map[string]any{"designId": d.Design.ID, "ruleId": "check", "expectedVersion": d.Design.Version, "commands": []any{map[string]any{"type": "remove_example", "exampleId": "precise"}, map[string]any{"type": "remove_example", "exampleId": "missing"}}}))
	if !strings.Contains(message, "400") {
		t.Fatalf("failed batch accepted: %s", message)
	}
	if after := call("get_response_rule", address); string(after) != string(before) {
		t.Fatal("failed example batch changed saved rule/version")
	}
	_, message = callTool(t, server, "simulate_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": d.Design.ID, "ruleId": "check", "exampleId": "missing"}))
	if !strings.Contains(message, "404") {
		t.Fatalf("missing example: %s", message)
	}
}
