package mcp

import (
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendReplayTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract string
		read                  bool
	}{
		{"resolve_backend_diagram_scope", "POST /api/backend-projects/{id}/diagrams/resolve-scope", "BackendDiagramScopeInput", true},
		{"list_backend_replay_targets", "GET /api/backend-projects/{id}/replay/targets", "", true},
		{"get_backend_replay_template", "GET /api/backend-projects/{id}/replay/template", "", true},
		{"connect_backend_replay_profile", "POST /api/backend-projects/{id}/replay/profiles", "ConnectBackendReplayProfileRequest", false},
		{"list_backend_replay_profiles", "GET /api/backend-projects/{id}/replay/profiles", "", true},
		{"revoke_backend_replay_authorization", "POST /api/backend-projects/{id}/replay/revoke", "RevokeBackendReplayAuthorizationRequest", false},
		{"save_backend_replay_package", "POST /api/backend-projects/{id}/replay/packages", "SaveBackendReplayPackageRequest", false},
		{"list_backend_replay_packages", "GET /api/backend-projects/{id}/replay/packages", "", true},
		{"get_backend_replay_profile", "GET /api/backend-projects/{id}/replay/profiles/{item}/versions/{v}", "", true},
		{"get_backend_replay_package", "GET /api/backend-projects/{id}/replay/packages/{item}/versions/{v}", "", true},
		{"start_backend_replay", "POST /api/backend-projects/{id}/replay/runs", "StartBackendReplayRequest", false},
		{"list_backend_replay_runs", "GET /api/backend-projects/{id}/replay/runs", "", true},
		{"get_backend_replay_run", "GET /api/backend-projects/{id}/replay/runs/{rid}", "", true},
		{"cancel_backend_replay_run", "POST /api/backend-projects/{id}/replay/runs/{rid}/cancel", "CancelBackendReplayRunRequest", false},
		{"reconcile_backend_replay_run", "POST /api/backend-projects/{id}/replay/runs/{rid}/reconcile", "ReconcileBackendReplayRunRequest", false},
		{"compare_backend_replay_runs", "POST /api/backend-projects/{id}/replay/compare", "CompareBackendReplayRunsRequest", true},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var err error
			schema, err = api.BackendSchema(spec.contract)
			if err != nil {
				panic(err)
			}
		} else {
			schema = map[string]any{"type": "object", "additionalProperties": false, "required": []any{}, "properties": map[string]any{}}
		}
		ids := []string{"projectId"}
		if strings.Contains(spec.route, "{rid}") {
			ids = append(ids, "replayRunId")
		}
		if strings.Contains(spec.route, "{item}") {
			ids = append(ids, "replayItemId")
		}
		backendToolPathSchema(schema, ids)
		if strings.Contains(spec.route, "{v}") {
			schema["properties"].(map[string]any)["version"] = map[string]any{"type": "integer", "minimum": 1}
			schema["required"] = append(schema["required"].([]any), "version")
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: "Trusted Orders replay with exact immutable package/profile/source/build pins. Connect requires explicit allowReset consent. Save/select do not execute. Start queues work; preserve complete request and idempotency key before sending. Never retry uncertain mutations with new keys. Payment is mocked; fixture order persistence is actual. Compare does not execute.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
