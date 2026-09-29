package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const dataBindingFixture = `{"id":"order-id","sourceMessageId":"create","sourcePointer":"/id","target":{"kind":"body","pointer":""}}`

func TestDataBindingTransformsRoundTripThroughTools(t *testing.T) {
	t.Parallel()
	chain := `[{"kind":"trim"},{"kind":"to_integer"},{"kind":"to_string"}]`
	binding := strings.TrimSuffix(dataBindingFixture, "}") + `,"transforms":` + chain + `}`
	for _, name := range []string{"upsert_design_scenario_data_binding", "apply_design_scenario_commands"} {
		args := `{"scenarioId":7,"expectedVersion":3,"messageId":"get","binding":` + binding + `}`
		if name == "apply_design_scenario_commands" {
			args = `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"upsert_data_binding","messageId":"get","binding":` + binding + `}]}`
		}
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, errMsg := callTool(t, calls, name, args)
		if errMsg != "" || !strings.Contains(string(calls.sent), `"transforms":`+chain) {
			t.Fatalf("%s lost ordered transforms: %s error=%s", name, calls.sent, errMsg)
		}
	}
}

func TestDataBindingTransformSchemasRejectMalformedBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, chain := range []string{`null`, `[null]`, `[{}]`, `[{"kind":"eval"}]`, `[{"kind":"trim","fallback":"x"}]`, `[` + strings.TrimSuffix(strings.Repeat(`{"kind":"trim"},`, 9), ",") + `]`} {
		binding := strings.TrimSuffix(dataBindingFixture, "}") + `,"transforms":` + chain + `}`
		for _, name := range []string{"upsert_design_scenario_data_binding", "apply_design_scenario_commands"} {
			args := `{"scenarioId":7,"expectedVersion":3,"messageId":"get","binding":` + binding + `}`
			if name == "apply_design_scenario_commands" {
				args = `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"upsert_data_binding","messageId":"get","binding":` + binding + `}]}`
			}
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, errMsg := callTool(t, calls, name, args)
			if errMsg == "" || calls.method != "" {
				t.Fatalf("%s dispatched malformed transforms %s: %s", name, chain, errMsg)
			}
		}
	}
}

func TestDataFlowToolsRoutesAndCommands(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args, path, command string }{
		{"get_design_scenario_data_flow", `{"scenarioId":7,"revisionId":8}`, "/api/design-scenarios/7/data-flow?revisionId=8", ""},
		{"analyze_design_scenario_data_flow", `{"scenarioId":7,"document":` + designScenarioDocumentFixture + `}`, "/api/design-scenarios/7/data-flow", ""},
		{"upsert_design_scenario_data_binding", `{"scenarioId":7,"expectedVersion":3,"messageId":"get","binding":` + dataBindingFixture + `}`, "/api/design-scenarios/7/commands", "upsert_data_binding"},
		{"remove_design_scenario_data_binding", `{"scenarioId":7,"expectedVersion":3,"messageId":"get","id":"order-id"}`, "/api/design-scenarios/7/commands", "remove_data_binding"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: 200, body: []byte(`{"messages":[],"bindings":[],"diagnostics":[]}`)}
			_, errMsg := callTool(t, calls, tt.name, tt.args)
			if errMsg != "" || calls.path != tt.path {
				t.Fatalf("path=%s error=%s", calls.path, errMsg)
			}
			if tt.command == "" {
				return
			}
			var body struct {
				ExpectedVersion int64            `json:"expectedVersion"`
				Commands        []map[string]any `json:"commands"`
			}
			if err := jsonx.Unmarshal(calls.sent, &body); err != nil {
				t.Fatal(err)
			}
			if body.ExpectedVersion != 3 || len(body.Commands) != 1 || body.Commands[0]["type"] != tt.command || body.Commands[0]["messageId"] != "get" {
				t.Fatalf("wrong command: %s", calls.sent)
			}
			if tt.command == "upsert_data_binding" && !strings.Contains(string(calls.sent), `"pointer":""`) {
				t.Fatalf("whole-body pointer lost: %s", calls.sent)
			}
		})
	}
}

func TestDataBindingSchemasRejectMalformedBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, binding := range []string{
		`null`, strings.Replace(dataBindingFixture, `"pointer":""`, `"name":"id"`, 1),
		strings.Replace(dataBindingFixture, `"kind":"body"`, `"kind":"path"`, 1),
		strings.Replace(dataBindingFixture, `"id":"order-id"`, `"id":"bad space"`, 1),
		strings.Replace(dataBindingFixture, `"sourcePointer":"/id"`, `"sourcePointer":"/bad~2"`, 1),
		strings.Replace(dataBindingFixture, `"pointer":""`, `"pointer":"","name":"extra"`, 1),
	} {
		for _, name := range []string{"upsert_design_scenario_data_binding", "apply_design_scenario_commands"} {
			args := `{"scenarioId":7,"expectedVersion":3,"messageId":"get","binding":` + binding + `}`
			if name == "apply_design_scenario_commands" {
				args = `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"upsert_data_binding","messageId":"get","binding":` + binding + `}]}`
			}
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, errMsg := callTool(t, calls, name, args)
			if errMsg == "" || calls.method != "" {
				t.Fatalf("malformed binding dispatched via %s: %s error=%s", name, binding, errMsg)
			}
		}
	}
}

func TestDataBindingToolsRequireVersionAndPreserveConflict(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"upsert_design_scenario_data_binding", "remove_design_scenario_data_binding"} {
		tail := `,"id":"order-id"}`
		if strings.HasPrefix(name, "upsert") {
			tail = `,"binding":` + dataBindingFixture + `}`
		}
		calls := &recordingCaller{status: http.StatusConflict, body: []byte(`{"error":{"code":"design_scenario_conflict","message":"changed","details":{"version":9,"draftRevisionId":23}}}`)}
		_, errMsg := callTool(t, calls, name, `{"scenarioId":7,"messageId":"get"`+tail)
		if errMsg == "" || calls.method != "" {
			t.Fatalf("missing version dispatched: %s", errMsg)
		}
		_, errMsg = callTool(t, calls, name, `{"scenarioId":7,"expectedVersion":3,"messageId":"get"`+tail)
		if !strings.Contains(errMsg, "409") || !strings.Contains(errMsg, `"version":9`) {
			t.Fatalf("conflict lost: %s", errMsg)
		}
	}
}

func TestDataFlowToolAnnotationsAndSchemas(t *testing.T) {
	t.Parallel()
	rec := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnly bool `json:"readOnlyHint"`
				} `json:"annotations"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range envelope.Result.Tools {
		switch tool.Name {
		case "get_design_scenario_data_flow", "analyze_design_scenario_data_flow":
			found++
			if !tool.Annotations.ReadOnly {
				t.Errorf("%s must be readonly", tool.Name)
			}
		case "upsert_design_scenario_data_binding", "remove_design_scenario_data_binding":
			found++
			if tool.Annotations.ReadOnly {
				t.Errorf("%s must mutate", tool.Name)
			}
			properties := tool.InputSchema["properties"].(map[string]any)
			if properties["expectedVersion"] == nil {
				t.Errorf("missing version schema: %s", tool.Name)
			}
		}
	}
	if found != 4 {
		t.Fatalf("found %d binding tools", found)
	}
}

func TestDataBindingFullDocumentSchemasPreserveAndRejectNull(t *testing.T) {
	t.Parallel()
	execution := `{"enabled":true,"pathParams":{},"query":{},"headers":{},"body":"{}","assertions":[],"extract":[],"bindings":[` + dataBindingFixture + `]}`
	document := strings.Replace(designScenarioDocumentFixture, `"messages":[]`, `"messages":[{"id":"get","fromId":"api","toId":"api","kind":"request","label":"","description":"","execution":`+execution+`}]`, 1)
	for _, name := range []string{"create_design_scenario", "save_design_scenario_draft", "analyze_design_scenario_data_flow", "validate_design_scenario"} {
		prefix := `{"document":`
		if name != "create_design_scenario" {
			prefix = `{"scenarioId":7,"document":`
		}
		if name == "save_design_scenario_draft" {
			prefix = `{"scenarioId":7,"expectedVersion":3,"document":`
		}
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, errMsg := callTool(t, calls, name, prefix+document+`}`)
		if errMsg != "" || !strings.Contains(string(calls.sent), `"bindings":[`) || !strings.Contains(string(calls.sent), `"pointer":""`) {
			t.Fatalf("%s lost binding: %s error=%s", name, calls.sent, errMsg)
		}
		calls = &recordingCaller{status: 200, body: []byte(`{}`)}
		_, errMsg = callTool(t, calls, name, prefix+strings.Replace(document, `"bindings":[`+dataBindingFixture+`]`, `"bindings":null`, 1)+`}`)
		if errMsg == "" || calls.method != "" {
			t.Fatalf("%s accepted null bindings: %s", name, errMsg)
		}
	}
}
