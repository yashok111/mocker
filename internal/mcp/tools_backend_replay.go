package mcp

import (
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

var replayListPages = map[string]string{
	"list_backend_replay_runs":     "Runs list newest first, 100 per page; pass the last run id as cursor for the next page, and a shorter page is the last.",
	"list_backend_replay_profiles": "Profiles list oldest first, 100 per page; pass the last item's \"id:version\" as cursor, and a shorter page is the last.",
	"list_backend_replay_packages": "Packages list oldest first, 25 per page; pass the last item's \"id:version\" as cursor, and a shorter page is the last.",
}

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
		description := "Trusted Orders replay with exact immutable package/profile/source/build pins. Connect requires explicit allowReset consent. Save/select do not execute. Start queues work; preserve complete request and idempotency key before sending. Never retry uncertain mutations with new keys. Payment is mocked; fixture order persistence is actual. Compare does not execute."
		// The three lists page (review 2026-10-06, F123/F124); the loopback
		// already forwards `cursor` as a query parameter on GET routes.
		if page, paged := replayListPages[spec.name]; paged {
			schema["properties"].(map[string]any)["cursor"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64}
			description += " " + page
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
