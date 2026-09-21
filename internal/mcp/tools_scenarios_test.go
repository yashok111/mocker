package mcp

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const designScenarioDocumentFixture = `{"formatVersion":1,"title":"Checkout","participants":[],"messages":[],"fragments":[],"contracts":[]}`

func TestDesignScenarioToolsUseAdminRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		args   string
		method string
		path   string
	}{
		{name: "list_design_scenarios", args: `{}`, method: "GET", path: "/api/design-scenarios"},
		{name: "create_design_scenario", args: `{"document":` + designScenarioDocumentFixture + `,"formDrafts":{"contract:1":"draft"},"summary":"Start"}`, method: "POST", path: "/api/design-scenarios"},
		{name: "get_design_scenario", args: `{"scenarioId":7}`, method: "GET", path: "/api/design-scenarios/7"},
		{name: "save_design_scenario_draft", args: `{"scenarioId":7,"expectedVersion":3,"document":` + designScenarioDocumentFixture + `,"formDrafts":{},"summary":"Save"}`, method: "PUT", path: "/api/design-scenarios/7/draft"},
		{name: "apply_design_scenario_commands", args: `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"set_title","title":"Changed"}],"summary":"Rename"}`, method: "POST", path: "/api/design-scenarios/7/commands"},
		{name: "get_design_scenario_revision", args: `{"scenarioId":7,"revisionId":8}`, method: "GET", path: "/api/design-scenarios/7/revisions/8"},
		{name: "get_design_scenario_diff", args: `{"scenarioId":7,"fromRevisionId":1,"toRevisionId":8}`, method: "GET", path: "/api/design-scenarios/7/diff?from=1&to=8"},
		{name: "restore_design_scenario_revision", args: `{"scenarioId":7,"revisionId":8,"expectedVersion":3,"summary":"Restore"}`, method: "POST", path: "/api/design-scenarios/7/restore"},
		{name: "validate_design_scenario", args: `{"scenarioId":7,"document":` + designScenarioDocumentFixture + `}`, method: "POST", path: "/api/design-scenarios/7/validate"},
		{name: "execute_design_scenario_step", args: `{"scenarioId":7,"revisionId":8,"messageId":"call","pathParams":{},"query":{},"headers":{},"body":""}`, method: "POST", path: "/api/design-scenarios/7/execute-step"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{
				status: http.StatusOK,
				body:   []byte(`{"id":42,"unknown":{"kept":true}}`),
			}
			raw, errMsg := callTool(t, calls, tt.name, tt.args)
			if errMsg != "" {
				t.Fatalf("tool error: %s", errMsg)
			}
			if calls.method != tt.method || calls.path != tt.path {
				t.Fatalf("called %s %s, want %s %s", calls.method, calls.path, tt.method, tt.path)
			}
			var got map[string]any
			if err := jsonx.Unmarshal(raw, &got); err != nil || got["unknown"] == nil {
				t.Fatalf("response lost fields: %s (%v)", raw, err)
			}
			if tt.method == http.MethodGet {
				return
			}
			var sent map[string]any
			if err := jsonx.Unmarshal(calls.sent, &sent); err != nil {
				t.Fatal(err)
			}
			if sent["scenarioId"] != nil {
				t.Errorf("path identity leaked into body: %s", calls.sent)
			}
			if strings.Contains(tt.args, "expectedVersion") && sent["expectedVersion"] != float64(3) {
				t.Errorf("expected version lost: %s", calls.sent)
			}
		})
	}
}

func TestDesignScenarioToolRejectsMissingVersionWithoutAdminCall(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
	_, errMsg := callTool(t, calls, "apply_design_scenario_commands", `{"scenarioId":7,"expectedVersion":0,"commands":[{"type":"set_title","title":"Changed"}]}`)
	if errMsg == "" || calls.method != "" {
		t.Fatalf("missing CAS reached admin: call=%q error=%q", calls.method, errMsg)
	}
}

func TestDesignScenarioToolsPreserveExecutionConfiguration(t *testing.T) {
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"id":42}`)}
	document := strings.Replace(designScenarioDocumentFixture, `"messages":[]`, `"messages":[{"id":"call","fromId":"api","toId":"api","kind":"request","label":"","description":"","execution":{"enabled":true,"pathParams":{},"query":{},"headers":{},"body":"","assertions":[{"pointer":"/id","equals":9007199254740993}],"extract":[]}}]`, 1)
	document = strings.TrimSuffix(document, "}") + `,"execution":{"variables":{"token":"initial"}}}`
	_, errMsg := callTool(t, calls, "create_design_scenario", `{"document":`+document+`}`)
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if !strings.Contains(string(calls.sent), `"equals":9007199254740993`) || !strings.Contains(string(calls.sent), `"variables":{"token":"initial"}`) {
		t.Fatalf("execution config lost: %s", calls.sent)
	}
}

func TestDesignScenarioToolPreservesConflictDetails(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusConflict,
		body:   []byte(`{"error":{"code":"design_scenario_conflict","message":"draft changed","details":{"version":9,"draftRevisionId":23,"designId":4,"contractId":"orders"}}}`),
	}
	_, errMsg := callTool(t, calls, "save_design_scenario_draft", `{"scenarioId":7,"expectedVersion":3,"document":`+designScenarioDocumentFixture+`}`)
	if !strings.Contains(errMsg, "409") || !strings.Contains(errMsg, `"version":9`) || !strings.Contains(errMsg, `"contractId":"orders"`) {
		t.Fatalf("agent cannot resolve conflict from %q", errMsg)
	}
}

func TestDesignScenarioToolDoesNotDiscloseInternalFailure(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusInternalServerError,
		body:   []byte(`{"error":{"code":"internal","message":"database secret","details":{"secret":"private"}}}`),
	}
	_, errMsg := callTool(t, calls, "get_design_scenario", `{"scenarioId":7}`)
	if !strings.Contains(errMsg, "500") || strings.Contains(errMsg, "secret") || strings.Contains(errMsg, "private") {
		t.Fatalf("internal failure disclosure: %q", errMsg)
	}
}

func TestDesignScenarioToolsListPublishesObjectContractAndCommandUnion(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"Authorization": "Bearer " + testKey,
	})
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}
	byName := map[string]json.RawMessage{}
	for _, tool := range envelope.Result.Tools {
		byName[tool.Name] = tool.InputSchema
	}
	var createSchema struct {
		Properties map[string]struct {
			Properties map[string]struct {
				Items struct {
					Properties map[string]struct {
						Type     string   `json:"type"`
						Required []string `json:"required"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := jsonx.Unmarshal(byName["create_design_scenario"], &createSchema); err != nil {
		t.Fatal(err)
	}
	contractDocumentType := createSchema.Properties["document"].Properties["contracts"].Items.Properties["document"].Type
	if contractDocumentType != "object" {
		t.Fatalf("contracts[].document schema type=%q, want object; schema=%s", contractDocumentType, byName["create_design_scenario"])
	}
	sourceRequired := createSchema.Properties["document"].Properties["contracts"].Items.Properties["source"].Required
	if slices.Contains(sourceRequired, "version") {
		t.Fatalf("copy source unexpectedly requires version; schema=%s", byName["create_design_scenario"])
	}

	var commandSchema struct {
		Properties map[string]struct {
			Items struct {
				OneOf []struct {
					Required   []string `json:"required"`
					Properties map[string]struct {
						Const string `json:"const"`
					} `json:"properties"`
				} `json:"oneOf"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := jsonx.Unmarshal(byName["apply_design_scenario_commands"], &commandSchema); err != nil {
		t.Fatal(err)
	}
	variants := commandSchema.Properties["commands"].Items.OneOf
	if len(variants) != 16 {
		t.Fatalf("command variants=%d, want 16; schema=%s", len(variants), byName["apply_design_scenario_commands"])
	}
	var foundSetTitle bool
	for _, variant := range variants {
		if variant.Properties["type"].Const == "set_title" {
			foundSetTitle = slices.Contains(variant.Required, "title")
		}
	}
	if !foundSetTitle {
		t.Fatalf("set_title does not require title; schema=%s", byName["apply_design_scenario_commands"])
	}
}

func TestCreateDesignScenarioPreservesContractDocumentNumbers(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusCreated, body: []byte(`{"scenario":{"id":1}}`)}
	args := `{"document":{"formatVersion":1,"title":"Checkout","participants":[],"messages":[],"fragments":[],"contracts":[{"id":"orders","name":"Orders","document":{"openapi":"3.1.0","x-big":9007199254740993}}]}}`
	if _, errMsg := callTool(t, calls, "create_design_scenario", args); errMsg != "" {
		t.Fatal(errMsg)
	}
	if !strings.Contains(string(calls.sent), `"x-big":9007199254740993`) {
		t.Fatalf("contract document number changed: %s", calls.sent)
	}
}

func TestApplyDesignScenarioCommandsPreservesNewContractDocumentNumbers(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"scenario":{"id":7}}`)}
	args := `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"create_contract","contract":{"id":"orders","name":"Orders","document":{"openapi":"3.1.0","x-big":9007199254740993}}},{"type":"materialize_contract","contractId":"orders"}]}`
	if _, errMsg := callTool(t, calls, "apply_design_scenario_commands", args); errMsg != "" {
		t.Fatal(errMsg)
	}
	if calls.path != "/api/design-scenarios/7/commands" || !strings.Contains(string(calls.sent), `"x-big":9007199254740993`) {
		t.Fatalf("new contract document changed or wrong endpoint: path=%s body=%s", calls.path, calls.sent)
	}
}

func TestApplyDesignScenarioCommandsRejectsUnknownFieldsBeforeAdminCall(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		`{"type":"create_contract","contract":{"id":"new","name":"API","document":{},"source":null}}`,
		`{"type":"create_contract","contract":{"id":"new","name":"API","document":{},"extra":true}}`,
		`{"type":"create_contract","contract":{"id":"new","name":"API","document":{}},"label":"ignored"}`,
		`{"type":"set_title","title":"new","label":"ignored"}`,
	} {
		calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
		_, errMsg := callTool(t, calls, "apply_design_scenario_commands", `{"scenarioId":7,"expectedVersion":3,"commands":[`+command+`]}`)
		if errMsg == "" || calls.method != "" {
			t.Fatalf("malformed command reached admin: call=%q error=%q command=%s", calls.method, errMsg, command)
		}
	}
}

func TestApplyDesignScenarioCommandsRejectsIncompleteUpsertsBeforeAdminCall(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		`{"type":"upsert_participant","participant":{"id":"api","kind":"service"}}`,
		`{"type":"upsert_participant","participant":{"id":"api","name":null,"kind":"service","description":"kept"}}`,
		`{"type":"upsert_message","message":{"id":"call","fromId":"client","toId":"api","kind":"request"}}`,
		`{"type":"upsert_fragment","fragment":{"id":"loop","kind":"loop","fromMessageId":"call","toMessageId":"call"}}`,
	} {
		calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
		_, errMsg := callTool(t, calls, "apply_design_scenario_commands", `{"scenarioId":7,"expectedVersion":3,"commands":[`+command+`]}`)
		if errMsg == "" || calls.method != "" {
			t.Fatalf("incomplete upsert reached admin and could erase saved fields: call=%q error=%q command=%s", calls.method, errMsg, command)
		}
	}
}

func TestApplyDesignScenarioCommandsAcceptsExplicitEmptyUpsertFields(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
	args := `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"upsert_participant","participant":{"id":"api","name":"","kind":"service","description":""}}]}`
	if _, errMsg := callTool(t, calls, "apply_design_scenario_commands", args); errMsg != "" {
		t.Fatal(errMsg)
	}
	if calls.path != "/api/design-scenarios/7/commands" || !strings.Contains(string(calls.sent), `"name":""`) || !strings.Contains(string(calls.sent), `"description":""`) {
		t.Fatalf("explicit field clearing did not reach admin: path=%s body=%s", calls.path, calls.sent)
	}
}

func TestDesignScenarioToolsPreserveColors(t *testing.T) {
	t.Parallel()
	const participant = `{"id":"api","name":"API","kind":"service","description":"","color":"#aBcDeF"}`
	const message = `{"id":"call","fromId":"api","toId":"api","kind":"request","label":"Call","description":"","color":"#123456","arrowColor":"#FEDCBA"}`
	const document = `{"formatVersion":1,"title":"Colors","participants":[` + participant + `],"messages":[` + message + `],"fragments":[],"contracts":[]}`
	for _, tt := range []struct {
		name string
		args string
	}{
		{"create_design_scenario", `{"document":` + document + `}`},
		{"save_design_scenario_draft", `{"scenarioId":7,"expectedVersion":3,"document":` + document + `}`},
		{"validate_design_scenario", `{"scenarioId":7,"document":` + document + `}`},
		{"apply_design_scenario_commands", `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"upsert_participant","participant":` + participant + `},{"type":"upsert_message","message":` + message + `}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			if _, errMsg := callTool(t, calls, tt.name, tt.args); errMsg != "" {
				t.Fatal(errMsg)
			}
			for _, color := range []string{`"color":"#aBcDeF"`, `"color":"#123456"`, `"arrowColor":"#FEDCBA"`} {
				if !strings.Contains(string(calls.sent), color) {
					t.Fatalf("color not forwarded: %s", calls.sent)
				}
			}
		})
	}
}

func TestDesignScenarioToolsRejectInvalidColorsBeforeAdminCall(t *testing.T) {
	t.Parallel()
	for _, color := range []string{`""`, `"red"`, `"#123"`, `"#12345678"`, `"#abcdez"`, `"#123456\n"`, `null`, `12`} {
		for _, field := range []string{"color", "arrowColor"} {
			message := `{"id":"call","fromId":"api","toId":"api","kind":"request","label":"Call","description":"","` + field + `":` + color + `}`
			for _, tt := range []struct {
				name string
				args string
			}{
				{"create_design_scenario", `{"document":{"formatVersion":1,"title":"Colors","participants":[],"messages":[` + message + `],"fragments":[],"contracts":[]}}`},
				{"apply_design_scenario_commands", `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"upsert_message","message":` + message + `}]}`},
			} {
				t.Run(tt.name+"/"+field+"/"+color, func(t *testing.T) {
					calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
					_, errMsg := callTool(t, calls, tt.name, tt.args)
					if errMsg == "" || calls.method != "" {
						t.Fatalf("invalid color reached admin: call=%q error=%q", calls.method, errMsg)
					}
				})
			}
		}
	}
}
