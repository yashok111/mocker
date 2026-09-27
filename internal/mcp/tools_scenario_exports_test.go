package mcp

import (
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
	for _, format := range []string{"postman", "curl"} {
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
