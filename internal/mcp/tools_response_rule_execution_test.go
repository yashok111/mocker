package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

func TestResponseRuleExecutionToolRoutes(t *testing.T) {
	for _, tc := range []struct{ name, method, path, input string }{
		{"get_response_rule_execution", "GET", "/api/designs/7/response-rule-execution", `{"designId":7}`},
		{"apply_response_rule", "PUT", "/api/designs/7/response-rules/r/execution", `{"designId":7,"ruleId":"r","expectedVersion":9007199254740993}`},
		{"unapply_response_rule", "DELETE", "/api/designs/7/response-rules/r/execution", `{"designId":7,"ruleId":"r","expectedVersion":9007199254740993}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":9007199254740993}`)}
			result, message := callTool(t, caller, tc.name, tc.input)
			if message != "" || caller.method != tc.method || caller.path != tc.path {
				t.Fatalf("%s %s: %s", caller.method, caller.path, message)
			}
			if !strings.Contains(string(result), "9007199254740993") {
				t.Fatalf("rounded output %s", result)
			}
			if tc.method != "GET" && string(caller.sent) != `{"expectedVersion":9007199254740993}` {
				t.Fatalf("invalid body %s", caller.sent)
			}
		})
	}
}

func TestResponseRuleExecutionToolsRejectInvalidInput(t *testing.T) {
	for _, input := range []string{
		`{"designId":7,"ruleId":"r"}`,
		`{"designId":7,"ruleId":"r","expectedVersion":null}`,
		`{"designId":7,"ruleId":"r","expectedVersion":0}`,
		`{"designId":7,"ruleId":"../r","expectedVersion":1}`,
		`{"designId":7,"ruleId":"r","expectedVersion":1,"document":"{}"}`,
	} {
		caller := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
		_, message := callTool(t, caller, "apply_response_rule", input)
		if message == "" || caller.method != "" {
			t.Fatalf("invalid input reached handler: %s", input)
		}
	}
}

func TestResponseRuleExecutionMCPStoredLifecycle(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	call := func(name string, args any) jsonx.RawMessage {
		t.Helper()
		input, err := jsonx.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		result, message := callTool(t, server, name, string(input))
		if message != "" {
			t.Fatalf("%s: %s", name, message)
		}
		return result
	}
	document := `{"openapi":"3.1.0","info":{"title":"Execution","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK"}}}}},"x-mocker-response-rules":{"formatVersion":1,"rules":[{"id":"r","name":"Rule","binding":{"method":"GET","path":"/orders"},"nodes":[{"id":"s","type":"start","name":"Start","x":0,"y":0},{"id":"f","type":"fallback","name":"Default","x":300,"y":0}],"edges":[{"id":"e","from":"s","port":"next","to":"f"}]}]},"x-exact":9007199254740993}`
	var detail apidesign.Detail
	if err := jsonx.Unmarshal(call("create_api_design", map[string]any{"name": "Execution", "document": document}), &detail); err != nil {
		t.Fatal(err)
	}
	id := detail.Design.ID
	read := func() apidesign.ResponseRuleExecution {
		t.Helper()
		var status apidesign.ResponseRuleExecution
		if err := jsonx.Unmarshal(call("get_response_rule_execution", map[string]any{"designId": id}), &status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	if initial := read(); len(initial.Rules) != 0 {
		t.Fatalf("passive source activated: %+v", initial)
	}
	if err := jsonx.Unmarshal(call("apply_response_rule", map[string]any{"designId": id, "ruleId": "r", "expectedVersion": 1}), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Design.Version != 2 || !strings.Contains(detail.Draft.Document, responserules.ExecutionExtension) || !strings.Contains(detail.Draft.Document, "9007199254740993") {
		t.Fatalf("invalid applied draft: %+v", detail)
	}
	if status := read(); len(status.Rules) != 1 || status.Rules[0].State != "current" {
		t.Fatalf("status: %+v", status)
	}
	_, message := callTool(t, server, "unapply_response_rule", responseRuleIntegrationJSON(t, map[string]any{"designId": id, "ruleId": "r", "expectedVersion": 1}))
	if !strings.Contains(message, "409") || read().Version != 2 {
		t.Fatalf("stale fence: %s", message)
	}
	if err := jsonx.Unmarshal(call("unapply_response_rule", map[string]any{"designId": id, "ruleId": "r", "expectedVersion": 2}), &detail); err != nil {
		t.Fatal(err)
	}
	if status := read(); len(status.Rules) != 0 || status.Version != 3 {
		t.Fatalf("remove: %+v", status)
	}
	if !strings.Contains(detail.Draft.Document, `"id": "r"`) || !strings.Contains(detail.Draft.Document, "9007199254740993") {
		t.Fatal("remove lost authoring/unrelated data")
	}
}
