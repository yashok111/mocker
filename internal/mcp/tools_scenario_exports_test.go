package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestScenarioExportToolsUseImmutableReadRoutes(t *testing.T) {
	for _, tt := range []struct{ name, args, path string }{
		{"get_design_scenario_export_options", `{"scenarioId":7,"revisionId":11}`, "/api/design-scenarios/7/revisions/11/export-options"},
		{"export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"openapi-json","contractId":"a&b"}`, "/api/design-scenarios/7/revisions/11/exports/openapi-json?contractId=a%26b"},
		{"export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"postman"}`, "/api/design-scenarios/7/revisions/11/exports/postman"},
		{"export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"curl"}`, "/api/design-scenarios/7/revisions/11/exports/curl"},
		{"export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"markdown"}`, "/api/design-scenarios/7/revisions/11/exports/markdown"},
		{"export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"html"}`, "/api/design-scenarios/7/revisions/11/exports/html"},
	} {
		calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"revisionId":11,"content":"9007199254740993"}`)}
		raw, errMsg := callTool(t, calls, tt.name, tt.args)
		if errMsg != "" || calls.method != "GET" || calls.path != tt.path {
			t.Fatalf("%s: %s %s %s", tt.name, errMsg, calls.method, calls.path)
		}
		if !strings.Contains(string(raw), "9007199254740993") {
			t.Fatal("content changed")
		}
	}
}

func TestHTTPExportToolRejectsContractFilter(t *testing.T) {
	for _, format := range []string{"postman", "curl", "markdown", "html"} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, "export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"`+format+`","contractId":"api"}`)
		if !strings.Contains(message, "contractId") || calls.method != "" {
			t.Fatalf("contract filter accepted for %s: %s", format, message)
		}
	}
}

func TestScenarioExportToolRejectsUnknownFormatBeforeRequest(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	_, errMsg := callTool(t, calls, "export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"../../secrets"}`)
	if errMsg == "" || calls.method != "" {
		t.Fatalf("invalid format reached server: %s %s", errMsg, calls.method)
	}
}

func TestAsyncAPIExportToolRequiresContractAndUsesPinnedRevision(t *testing.T) {
	for _, format := range []string{"asyncapi-json", "asyncapi-yaml"} {
		t.Run(format, func(t *testing.T) {
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"revisionId":11,"content":"9007199254740993"}`)}
			args := `{"scenarioId":7,"revisionId":11,"format":"` + format + `","contractId":"orders"}`
			raw, message := callTool(t, calls, "export_design_scenario", args)
			wantPath := "/api/design-scenarios/7/revisions/11/exports/" + format + "?contractId=orders"
			if message != "" || calls.method != http.MethodGet || calls.path != wantPath {
				t.Fatalf("AsyncAPI export did not reach the saved revision: %s %s %s", message, calls.method, calls.path)
			}
			if !strings.Contains(string(raw), "9007199254740993") {
				t.Fatal("export content changed")
			}
			missing := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			_, message = callTool(t, missing, "export_design_scenario", `{"scenarioId":7,"revisionId":11,"format":"`+format+`"}`)
			if !strings.Contains(message, "contractId") || missing.method != "" {
				t.Fatalf("missing contract reached server: %s %s", message, missing.method)
			}
		})
	}
}

func TestScenarioArchiveToolUsesReadOnlyPost(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{"contentBase64":"UEs=","manifest":{"schemaVersion":1}}`)}
	raw, message := callTool(t, calls, "export_design_scenario_archive", `{"scenarioId":7,"revisionId":11,"items":[{"format":"mermaid"},{"format":"openapi-json","contractId":"a&b"}]}`)
	if message != "" || calls.method != "POST" || calls.path != "/api/design-scenarios/7/revisions/11/archive" || !strings.Contains(string(raw), `"contentBase64":"UEs="`) {
		t.Fatalf("archive route: %s %s %s %s", message, calls.method, calls.path, raw)
	}
}

func TestScenarioArchiveToolRejectsInvalidItemsBeforeRequest(t *testing.T) {
	for _, items := range []string{`[]`, `[{"format":"png"}]`, `[{"format":"mermaid"},{"format":"mermaid"}]`, `[{"format":"openapi-json"}]`, `[{"format":"markdown","contractId":"a"}]`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, "export_design_scenario_archive", `{"scenarioId":7,"revisionId":11,"items":`+items+`}`)
		if message == "" || calls.method != "" {
			t.Fatalf("invalid selection reached REST: %s %s", message, calls.method)
		}
	}
}

func TestScenarioArchiveToolReadOnlyAnnotations(t *testing.T) {
	t.Parallel()
	handler := New(&recordingCaller{}, testKey, testConfig(), nil).Handler()
	response := doMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var body struct {
		Result struct {
			Tools []struct {
				Name        string
				Annotations struct {
					ReadOnlyHint   bool
					IdempotentHint bool
				}
			}
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, tool := range body.Result.Tools {
		if tool.Name == "export_design_scenario_archive" {
			if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
				t.Fatalf("archive annotations: %+v", tool.Annotations)
			}
			return
		}
	}
	t.Fatal("archive tool not registered")
}

func TestScenarioTransferToolsRoutePackages(t *testing.T) {
	for _, tc := range []struct{ name, args, path string }{
		{"export_design_scenarios_file", `{"scenarioIds":[1,2],"includeHistory":true}`, "/api/design-scenarios/transfer-export"},
		{"import_design_scenarios_file", `{"bundle":{"kind":"mocker.scenarios","formatVersion":1,"scenarios":[]},"relink":true}`, "/api/design-scenarios/transfer-import"},
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, tc.name, tc.args)
		if message != "" || calls.method != "POST" || calls.path != tc.path {
			t.Fatalf("%s: %s %s %s", tc.name, message, calls.method, calls.path)
		}
	}
}
