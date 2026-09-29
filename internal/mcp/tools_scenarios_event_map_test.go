package mcp

import (
	"strings"
	"testing"
)

func TestEventMapToolsUseAdminRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, args, path string }{
		{"get_design_scenario_event_map", `{"scenarioId":7,"revisionId":11}`, "/api/design-scenarios/7/event-map?revisionId=11"},
		{"analyze_design_scenario_event_map", `{"scenarioId":7,"document":` + designScenarioDocumentFixture + `}`, "/api/design-scenarios/7/event-map"},
		{"apply_design_scenario_event_map_commands", `{"scenarioId":7,"expectedVersion":3,"commands":[{"type":"set_event_failure_routes","contractId":"consumer","id":"receive","failureRoutes":{"retryChannelId":"retry"}}]}`, "/api/design-scenarios/7/commands"},
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{"nodes":[],"edges":[],"diagnostics":[]}`)}
		_, errMsg := callTool(t, calls, tc.name, tc.args)
		if errMsg != "" || calls.path != tc.path {
			t.Fatalf("%s: %s %s", tc.name, calls.path, errMsg)
		}
		if strings.HasPrefix(tc.name, "apply") && !strings.Contains(string(calls.sent), `"retryChannelId":"retry"`) {
			t.Fatalf("lost route: %s", calls.sent)
		}
	}
}

func TestEventMapCommandsRejectMalformedBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		`{"type":"set_title","title":"wrong tool"}`,
		`{"type":"set_event_failure_routes","contractId":"c","id":"o","failureRoutes":null}`,
		`{"type":"set_event_failure_routes","contractId":"c","id":"o","failureRoutes":{"unknown":"x"}}`,
		`{"type":"upsert_event_api_link","contractId":"c","id":"o","apiLink":{"contractId":"api"}}`,
		`{"type":"remove_event_state_link","contractId":"c","id":"o","stateLink":{"contractId":"api","diagramId":"d","transitionId":"t","extra":true}}`,
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, errMsg := callTool(t, calls, "apply_design_scenario_event_map_commands", `{"scenarioId":7,"expectedVersion":3,"commands":[`+command+`]}`)
		if errMsg == "" || calls.method != "" {
			t.Fatalf("dispatched %s: %s", command, errMsg)
		}
	}
}
