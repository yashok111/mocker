package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/responserules"
)

type responseRuleIntegrationResult struct {
	responserules.Simulation
	Source apidesign.ResponseRuleSource `json:"source"`
}

func responseRuleIntegrationJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func responseRuleIntegrationValue(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// The same graph is authored through real MCP tool calls, evaluated through
// those tools and admin HTTP loopback, and read back from the real store.
func TestResponseRuleMCPIntegrationLifecycle(t *testing.T) {
	t.Parallel()
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	fixture := newToolFixture(server)
	call := func(name string, input any) json.RawMessage {
		t.Helper()
		out, message := fixture.Call(t, name, responseRuleIntegrationJSON(t, input))
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
	document := `{"openapi":"3.1.0","info":{"title":"Rules","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"example":{"n":9007199254740993},"schema":{"type":"object"}}}}}}}},"x-retained":{"keep":true}}`
	detail := decodeDetail(call("create_api_design", map[string]any{"name": "MCP response rules", "document": document}))
	designID := detail.Design.ID
	readArgs := map[string]any{"designId": designID, "ruleId": "auth"}
	var list apidesign.ResponseRules
	if err := json.Unmarshal(call("list_response_rules", map[string]any{"designId": designID}), &list); err != nil || len(list.Rules) != 0 || list.Version != 1 {
		t.Fatalf("initial list: %+v %v", list, err)
	}
	rule := responserules.Rule{ID: "auth", Name: "Initial", Nodes: []responserules.Node{}, Edges: []responserules.Edge{}}
	detail = decodeDetail(call("create_response_rule", map[string]any{"designId": designID, "expectedVersion": 1, "rule": rule}))
	if detail.Design.Version != 2 {
		t.Fatalf("create version: %d", detail.Design.Version)
	}
	rule.Name = "Header rule"
	detail = decodeDetail(call("save_response_rule", map[string]any{"designId": designID, "ruleId": "auth", "expectedVersion": 2, "rule": rule}))
	if detail.Design.Version != 3 {
		t.Fatalf("save version: %d", detail.Design.Version)
	}

	missingBody := `{"branch":"missing","n":1.0}`
	allowedBody := `{"branch":"allowed","n":9007199254740993,"lexical":1e0}`
	delay := 250
	// Response nodes intentionally precede start, and both statuses are 200:
	// traversal must follow the selected edge, never array/status order.
	nodes := []responserules.Node{
		{ID: "missing", Type: "response", Name: "Missing header", X: 600, Y: 250, Response: &responserules.Response{Status: 200, MediaType: "application/json", Headers: []responserules.Field{}, BodyJSON: &missingBody}},
		{ID: "start", Type: "start", Name: "Request", X: 0, Y: 120},
		{ID: "auth", Type: "condition", Name: "Has header", X: 280, Y: 120, Condition: &overrides.Condition{In: "header", Name: "Authorization", Op: "exists"}},
		{ID: "slow", Type: "delay", Name: "Delay", X: 600, Y: 0, DelayMs: &delay},
		{ID: "allowed", Type: "response", Name: "Allowed", X: 900, Y: 0, Response: &responserules.Response{Status: 200, MediaType: "application/json", Headers: []responserules.Field{{Name: "X-Rule", Value: "allowed"}}, BodyJSON: &allowedBody}},
	}
	edges := []responserules.Edge{{ID: "e1", From: "start", Port: "next", To: "auth"}, {ID: "e2", From: "auth", Port: "true", To: "slow"}, {ID: "e3", From: "auth", Port: "false", To: "missing"}, {ID: "e4", From: "slow", Port: "next", To: "allowed"}}
	name := "Header rule"
	commands := make([]responserules.Command, 0, 1+len(nodes)+len(edges))
	commands = append(commands, responserules.Command{Type: "set_rule", Name: &name, Binding: &responserules.Binding{Method: "GET", Path: "/orders"}})
	for i := range nodes {
		commands = append(commands, responserules.Command{Type: "add_node", Node: &nodes[i]})
	}
	for i := range edges {
		commands = append(commands, responserules.Command{Type: "add_edge", Edge: &edges[i]})
	}
	detail = decodeDetail(call("apply_response_rule_commands", map[string]any{"designId": designID, "ruleId": "auth", "expectedVersion": 3, "commands": commands}))
	if detail.Design.Version != 4 || len(detail.Revisions) != 4 || !strings.Contains(detail.Draft.Document, "9007199254740993") || !strings.Contains(detail.Draft.Document, "x-retained") {
		t.Fatalf("commands lost contract/history: %+v", detail)
	}
	var item struct {
		DesignID   int64              `json:"designId"`
		Version    int64              `json:"version"`
		RevisionID int64              `json:"revisionId"`
		Rule       responserules.Rule `json:"rule"`
	}
	if err := json.Unmarshal(call("get_response_rule", readArgs), &item); err != nil || item.Version != 4 || item.RevisionID != detail.Draft.ID || !reflect.DeepEqual(item.Rule.Nodes, nodes) || !reflect.DeepEqual(item.Rule.Edges, edges) {
		t.Fatalf("read graph differs: %+v %v", item, err)
	}
	if err := json.Unmarshal(call("list_response_rules", map[string]any{"designId": designID}), &list); err != nil || len(list.Rules) != 1 || !reflect.DeepEqual(list.Rules[0], item.Rule) {
		t.Fatalf("list differs: %+v %v", list, err)
	}
	var validation struct {
		responserules.Validation
		Source apidesign.ResponseRuleSource `json:"source"`
	}
	if err := json.Unmarshal(call("validate_response_rule", readArgs), &validation); err != nil || !validation.Valid || len(validation.Diagnostics) != 0 {
		t.Fatalf("validation: %+v %v", validation, err)
	}
	if validation.Source.Kind != "saved" || validation.Source.Version == nil || *validation.Source.Version != 4 || validation.Source.RevisionID == nil || *validation.Source.RevisionID != detail.Draft.ID || validation.Source.DocumentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(detail.Draft.Document))) {
		t.Fatalf("saved identity: %+v", validation.Source)
	}

	for _, tc := range []struct {
		name           string
		headers        []responserules.Field
		terminal, body string
		delay          int
		matched        bool
		trace          []string
	}{
		{"missing", []responserules.Field{}, "missing", missingBody, 0, false, []string{"start", "auth", "missing"}},
		{"case variants preserve first", []responserules.Field{{Name: "authorization", Value: "Bearer fixture"}, {Name: "Authorization", Value: ""}}, "allowed", allowedBody, 250, true, []string{"start", "auth", "slow", "allowed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := responserules.Request{Query: []responserules.Field{}, Headers: tc.headers}
			out := call("simulate_response_rule", map[string]any{"designId": designID, "ruleId": "auth", "request": request})
			var run responseRuleIntegrationResult
			if err := json.Unmarshal(out, &run); err != nil {
				t.Fatal(err)
			}
			if !run.Valid || run.Outcome != "response" || run.TerminalNodeID != tc.terminal || run.Response == nil || run.Response.Status != 200 || run.Response.BodyJSON == nil || *run.Response.BodyJSON != tc.body || run.TotalDelayMs == nil || *run.TotalDelayMs != tc.delay {
				t.Fatalf("branch: %s", out)
			}
			trace := make([]string, len(run.Trace))
			for i, step := range run.Trace {
				trace[i] = step.NodeID
			}
			if !reflect.DeepEqual(trace, tc.trace) || run.Trace[1].Matched == nil || *run.Trace[1].Matched != tc.matched || run.Trace[0].EdgeID != "e1" {
				t.Fatalf("trace: %+v", run.Trace)
			}
			if strings.Contains(string(out), "Bearer fixture") {
				t.Fatal("trace disclosed fixture")
			}
			if !reflect.DeepEqual(run.Source, validation.Source) {
				t.Fatalf("source differs: %+v", run.Source)
			}
			status, direct, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", fmt.Sprintf("/api/designs/%d/response-rules/auth/simulate", designID), []byte(responseRuleIntegrationJSON(t, map[string]any{"request": request})))
			if err != nil || status != 200 || !reflect.DeepEqual(responseRuleIntegrationValue(t, out), responseRuleIntegrationValue(t, direct)) {
				t.Fatalf("HTTP/MCP differ: status=%d\nMCP=%s\nHTTP=%s\n%v", status, out, direct, err)
			}
		})
	}

	_, message := fixture.Call(t, "delete_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": designID, "ruleId": "auth", "expectedVersion": 3}))
	if !strings.Contains(message, "409") || !strings.Contains(message, `"version":4`) {
		t.Fatalf("stale conflict: %s", message)
	}
	_, message = fixture.Call(t, "apply_response_rule_commands", responseRuleIntegrationJSON(t, map[string]any{"designId": designID, "ruleId": "auth", "expectedVersion": 4, "commands": []any{map[string]any{"type": "set_rule", "name": "ROLLBACK"}, map[string]any{"type": "remove_edge", "edgeId": "missing"}}}))
	if !strings.Contains(message, "400") {
		t.Fatalf("failed batch accepted: %s", message)
	}
	unchanged := decodeDetail(call("get_api_design", map[string]any{"designId": designID}))
	if !reflect.DeepEqual(unchanged, detail) {
		t.Fatal("evaluation, conflict or failed batch changed API state")
	}

	// A proposal may name an unsaved rule and retains exact entire-document bytes.
	var proposedRoot map[string]json.RawMessage
	if err := json.Unmarshal([]byte(detail.Draft.Document), &proposedRoot); err != nil {
		t.Fatal(err)
	}
	proposed := item.Rule
	proposed.ID = "unsaved"
	proposedRoot[responserules.Extension] = json.RawMessage(responseRuleIntegrationJSON(t, responserules.Envelope{FormatVersion: 1, Rules: []responserules.Rule{proposed}}))
	proposal := responseRuleIntegrationJSON(t, proposedRoot) + "\n "
	request := responserules.Request{Query: []responserules.Field{}, Headers: []responserules.Field{}}
	var run responseRuleIntegrationResult
	raw := call("simulate_response_rule", map[string]any{"designId": designID, "ruleId": "unsaved", "document": proposal, "request": request})
	if err := json.Unmarshal(raw, &run); err != nil || !run.Valid || run.Source.Kind != "proposal" || run.Source.RuleID != "unsaved" || run.Source.Version != nil || run.Source.RevisionID != nil || run.Source.DocumentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(proposal))) {
		t.Fatalf("proposal provenance: %s %v", raw, err)
	}
	if !strings.Contains(proposal, "9007199254740993") {
		t.Fatal("proposal rounded example")
	}
	_, message = fixture.Call(t, "get_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": designID, "ruleId": "unsaved"}))
	if !strings.Contains(message, "404") {
		t.Fatalf("proposal persisted: %s", message)
	}

	// Duplicate binding is diagnosed through the same complete proposal envelope.
	proposedRoot[responserules.Extension] = json.RawMessage(responseRuleIntegrationJSON(t, responserules.Envelope{FormatVersion: 1, Rules: []responserules.Rule{item.Rule, proposed}}))
	duplicate := responseRuleIntegrationJSON(t, proposedRoot)
	raw = call("simulate_response_rule", map[string]any{"designId": designID, "ruleId": "unsaved", "document": duplicate, "request": request})
	var invalid responseRuleIntegrationResult
	if err := json.Unmarshal(raw, &invalid); err != nil || invalid.Valid || invalid.Outcome != "invalid" || len(invalid.Trace) != 0 || invalid.Response != nil || invalid.TotalDelayMs != nil {
		t.Fatalf("duplicate binding simulation: %s %v", raw, err)
	}
	found := false
	for _, diagnostic := range invalid.Diagnostics {
		if diagnostic.Code == "duplicate_binding" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing duplicate binding diagnostic: %s", raw)
	}
	unchanged = decodeDetail(call("get_api_design", map[string]any{"designId": designID}))
	if !reflect.DeepEqual(unchanged, detail) {
		t.Fatal("proposal evaluation changed saved API")
	}
	detail = decodeDetail(call("delete_response_rule", map[string]any{"designId": designID, "ruleId": "auth", "expectedVersion": 4}))
	if detail.Design.Version != 5 {
		t.Fatalf("delete version: %d", detail.Design.Version)
	}
	if err := json.Unmarshal(call("list_response_rules", map[string]any{"designId": designID}), &list); err != nil || len(list.Rules) != 0 || list.Version != 5 {
		t.Fatalf("delete did not persist: %+v %v", list, err)
	}
}

func TestResponseRuleHTTPContractAdmitsIncompleteConditions(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var api any
	if err := json.Unmarshal(raw, &api); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/response-rule-contract.json"
	if err := compiler.AddResource(uri, api); err != nil {
		t.Fatal(err)
	}
	readSchema, err := compiler.Compile(uri + "#/components/schemas/ResponseRuleDetail")
	if err != nil {
		t.Fatal(err)
	}
	conditionSchema, err := compiler.Compile(uri + "#/components/schemas/ResponseRuleCondition")
	if err != nil {
		t.Fatal(err)
	}
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	fixture := newToolFixture(server)
	raw, message := fixture.Call(t, "create_api_design", `{"name":"Incomplete conditions"}`)
	if message != "" {
		t.Fatal(message)
	}
	var detail apidesign.Detail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	for i, condition := range []overrides.Condition{
		{In: "query", Name: "x", Op: "equals"},
		{In: "header", Name: "x", Op: "contains", Value: ""},
		{In: "", Name: "", Op: ""},
		{In: "unfinished", Name: "x", Op: "unfinished"},
		{In: "body", Name: "x", Op: "exists"},
	} {
		t.Run(fmt.Sprintf("condition%d", i), func(t *testing.T) {
			id := fmt.Sprintf("rule%d", i)
			rule := responserules.Rule{ID: id, Name: "Incomplete", Nodes: []responserules.Node{{ID: "condition", Type: "condition", Name: "Condition", Condition: &condition}}, Edges: []responserules.Edge{}}
			out, message := fixture.Call(t, "create_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": detail.Design.ID, "expectedVersion": detail.Design.Version, "rule": rule}))
			if message != "" {
				t.Fatalf("server rejected authoring condition: %s", message)
			}
			if err := json.Unmarshal(out, &detail); err != nil {
				t.Fatal(err)
			}
			out, message = fixture.Call(t, "get_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": detail.Design.ID, "ruleId": id}))
			if message != "" {
				t.Fatal(message)
			}
			var read any
			if err := json.Unmarshal(out, &read); err != nil {
				t.Fatal(err)
			}
			if err := readSchema.Validate(read); err != nil {
				t.Fatalf("server read violates HTTP schema: %s\n%v", out, err)
			}
		})
	}
	// The one structural predicate restriction remains part of the contract.
	if err := conditionSchema.Validate(map[string]any{"in": "query", "name": "x", "op": "exists", "value": ""}); err == nil {
		t.Fatal("HTTP schema permits a supplied value on exists")
	}
}
