package mcp

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestResponseRuleToolsUseSharedAdminRoutes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, method, suffix, args string }{
		{"list_response_rules", "GET", "", `{"designId":7}`},
		{"get_response_rule", "GET", "/rule", `{"designId":7,"ruleId":"rule"}`},
		{"create_response_rule", "POST", "", `{"designId":7,"expectedVersion":9007199254740993,"rule":{"id":"rule","name":"Rule","nodes":[],"edges":[]}}`},
		{"save_response_rule", "PUT", "/rule", `{"designId":7,"ruleId":"rule","expectedVersion":9007199254740993,"rule":{"id":"rule","name":"Rule","nodes":[],"edges":[]}}`},
		{"delete_response_rule", "DELETE", "/rule", `{"designId":7,"ruleId":"rule","expectedVersion":9007199254740993}`},
		{"apply_response_rule_commands", "POST", "/rule/commands", `{"designId":7,"ruleId":"rule","expectedVersion":9007199254740993,"commands":[{"type":"set_rule","name":"New"}]}`},
		{"validate_response_rule", "POST", "/rule/validate", `{"designId":7,"ruleId":"rule","document":"{\"example\":9007199254740993}"}`},
		{"simulate_response_rule", "POST", "/rule/simulate", `{"designId":7,"ruleId":"rule","request":{"query":[],"headers":[],"bodyJSON":"{\"n\":9007199254740993}"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":9007199254740993,"trace":[]}`)}
			raw, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" || calls.method != tt.method || calls.path != "/api/designs/7/response-rules"+tt.suffix {
				t.Fatalf("route %s %s; error %s", calls.method, calls.path, msg)
			}
			if !strings.Contains(string(raw), "9007199254740993") {
				t.Fatalf("response number rounded: %s", raw)
			}
			if strings.Contains(string(calls.sent), `"designId"`) || strings.Contains(string(calls.sent), `"ruleId"`) {
				t.Fatalf("path identifiers leaked to body: %s", calls.sent)
			}
			if strings.Contains(tt.args, "9007199254740993") && !strings.Contains(string(calls.sent), "9007199254740993") {
				t.Fatalf("request number rounded or field dropped: %s", calls.sent)
			}
		})
	}
}

func TestResponseRuleToolsRejectMalformedInput(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args string }{
		{"list_response_rules", `{"designId":0}`},
		{"get_response_rule", `{"designId":7,"ruleId":"../elsewhere"}`},
		{"get_response_rule", `{"designId":7,"ruleId":"rule","ignored":true}`},
		{"delete_response_rule", `{"designId":7,"ruleId":"rule"}`},
		{"delete_response_rule", `{"designId":7,"ruleId":"rule","expectedVersion":null}`},
		{"save_response_rule", `{"designId":7,"ruleId":"rule","expectedVersion":1}`},
		{"create_response_rule", `{"designId":7,"expectedVersion":1,"rule":{"id":"rule","name":"Rule","nodes":[{"id":"s","type":"start","name":"Start","y":0}],"edges":[]}}`},
		{"apply_response_rule_commands", `{"designId":7,"ruleId":"rule","expectedVersion":1,"commands":[{"type":"unknown"}]}`},
		{"apply_response_rule_commands", `{"designId":7,"ruleId":"rule","expectedVersion":1,"commands":[{"type":"move_nodes","positions":[{"nodeId":"s","x":0}]}]}`},
		{"apply_response_rule_commands", `{"designId":7,"ruleId":"rule","expectedVersion":1,"commands":[{"type":"remove_node","nodeId":"s","name":"ignored"}]}`},
		{"validate_response_rule", `{"designId":7,"ruleId":"rule","document":null}`},
		{"simulate_response_rule", `{"designId":7,"ruleId":"rule"}`},
		{"simulate_response_rule", `{"designId":7,"ruleId":"rule","request":{"query":[],"headers":[],"bodyJSON":null}}`},
	} {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			_, msg := callTool(t, calls, tt.name, tt.args)
			if msg == "" || calls.method != "" {
				t.Fatalf("invalid input reached admin: %s; error=%q", calls.sent, msg)
			}
		})
	}
}

func TestResponseRuleToolsAdvertiseTypedSchemas(t *testing.T) {
	t.Parallel()
	rec := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Required   []string                   `json:"required"`
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"inputSchema"`
				Annotations struct {
					ReadOnly    bool  `json:"readOnlyHint"`
					Destructive *bool `json:"destructiveHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tool := range env.Result.Tools {
		if !strings.Contains(strings.Join(toolRoutes[tool.Name], " "), "/response-rules") || tool.Name == "apply_response_rule" || tool.Name == "unapply_response_rule" {
			continue
		}
		seen++
		readOnly := slices.Contains([]string{"list_response_rules", "get_response_rule", "validate_response_rule", "simulate_response_rule"}, tool.Name)
		if tool.Annotations.ReadOnly != readOnly || slices.Contains(tool.InputSchema.Required, "expectedVersion") == readOnly {
			t.Errorf("%s: wrong read-only/version contract", tool.Name)
		}
		if tool.Name == "delete_response_rule" && (tool.Annotations.Destructive == nil || !*tool.Annotations.Destructive) {
			t.Error("delete lacks destructive annotation")
		}
		if p := tool.InputSchema.Properties["commands"]; p != nil && (!strings.Contains(string(p), `"oneOf"`) || !strings.Contains(string(p), `"maxItems":200`)) {
			t.Errorf("commands lack closed union and bounds: %s", p)
		}
	}
	if seen != 8 {
		t.Fatalf("found %d response-rule tools, want 8", seen)
	}
}

func TestResponseRuleToolsPreserveConflict(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusConflict, body: []byte(`{"error":{"code":"design_conflict","message":"draft changed","details":{"version":9,"draftRevisionId":23}}}`)}
	_, msg := callTool(t, calls, "delete_response_rule", `{"designId":7,"ruleId":"rule","expectedVersion":3}`)
	if !strings.Contains(msg, "409") || !strings.Contains(msg, `"version":9`) {
		t.Fatalf("missing conflict details: %s", msg)
	}
}

func TestResponseRuleToolAcceptsBoundedParameterizedJSONMedia(t *testing.T) {
	t.Parallel()
	media := `application/json; profile="` + strings.Repeat("a", 300) + `"`
	args, err := json.Marshal(map[string]any{
		"designId": 7, "expectedVersion": 1,
		"rule": map[string]any{"id": "rule", "name": "Rule", "edges": []any{}, "nodes": []any{
			map[string]any{"id": "r", "type": "response", "name": "Response", "x": 0, "y": 0,
				"response": map[string]any{"status": 200, "mediaType": media, "headers": []any{}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := &recordingCaller{status: http.StatusCreated, body: []byte(`{"version":2}`)}
	_, msg := callTool(t, calls, "create_response_rule", string(args))
	if msg != "" || calls.method != "POST" {
		t.Fatalf("valid media rejected: %s", msg)
	}
	var sent struct {
		Rule struct {
			Nodes []struct {
				Response struct {
					MediaType string `json:"mediaType"`
				} `json:"response"`
			} `json:"nodes"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(calls.sent, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Rule.Nodes[0].Response.MediaType != media {
		t.Fatal("media parameters changed")
	}
}
