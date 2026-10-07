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

// backendReplayFamily follows each replay tool's own summary sentence; see
// backendAnalysisFamily for why the two are separate.
const backendReplayFamily = "Trusted Orders replay with exact immutable package/profile/source/build pins. Connect requires explicit allowReset consent. Save/select do not execute. Start queues work; preserve complete request and idempotency key before sending. Never retry uncertain mutations with new keys. Payment is mocked; fixture order persistence is actual. Compare does not execute."

func addBackendReplayTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract, summary string
		read                           bool
	}{
		// No summary: this pure read carries its own whole description below.
		{"resolve_backend_diagram_scope", "POST /api/backend-projects/{id}/diagrams/resolve-scope", "BackendDiagramScopeInput", "", true},
		{"list_backend_replay_targets", "GET /api/backend-projects/{id}/replay/targets", "", "Lists the operator-configured replay fixture targets by their public IDs and config versions.", true},
		{"get_backend_replay_template", "GET /api/backend-projects/{id}/replay/template", "", "Reads the fixed Orders replay program template (steps, assertions, fixtureHash) a package is filled from.", true},
		{"connect_backend_replay_profile", "POST /api/backend-projects/{id}/replay/profiles", "ConnectBackendReplayProfileRequest", "Connects a configured target as an immutable replay profile, recording the explicit reset consent without resetting anything.", false},
		{"list_backend_replay_profiles", "GET /api/backend-projects/{id}/replay/profiles", "", "Lists the project's connected replay profiles.", true},
		{"revoke_backend_replay_authorization", "POST /api/backend-projects/{id}/replay/revoke", "RevokeBackendReplayAuthorizationRequest", "Revokes one exact profile's reset authorization, blocking every future replay mutation under it.", false},
		{"save_backend_replay_package", "POST /api/backend-projects/{id}/replay/packages", "SaveBackendReplayPackageRequest", "Saves a replay package version pinning the exact target, profile and build provenance; saving never starts a run.", false},
		{"list_backend_replay_packages", "GET /api/backend-projects/{id}/replay/packages", "", "Lists the project's saved replay packages.", true},
		{"get_backend_replay_profile", "GET /api/backend-projects/{id}/replay/profiles/{item}/versions/{v}", "", "Reads one exact replay profile version by replayItemId and version.", true},
		{"get_backend_replay_package", "GET /api/backend-projects/{id}/replay/packages/{item}/versions/{v}", "", "Reads one exact replay package version by replayItemId and version.", true},
		{"start_backend_replay", "POST /api/backend-projects/{id}/replay/runs", "StartBackendReplayRequest", "Queues a replay run of an exact package and profile against the trusted fixture, the only tool that executes it.", false},
		{"list_backend_replay_runs", "GET /api/backend-projects/{id}/replay/runs", "", "Lists the project's replay runs with their status, for polling.", true},
		{"get_backend_replay_run", "GET /api/backend-projects/{id}/replay/runs/{rid}", "", "Reads one replay run with its status, ordered receipts and local assertion report.", true},
		{"cancel_backend_replay_run", "POST /api/backend-projects/{id}/replay/runs/{rid}/cancel", "CancelBackendReplayRunRequest", "Cancels a replay run's future dispatch; a fixture request already in flight may still take effect.", false},
		{"reconcile_backend_replay_run", "POST /api/backend-projects/{id}/replay/runs/{rid}/reconcile", "ReconcileBackendReplayRunRequest", "Reconciles an uncertain replay run by reading fixture identity and journal and appending recovery evidence, without resetting or ordering.", false},
		{"compare_backend_replay_runs", "POST /api/backend-projects/{id}/replay/compare", "CompareBackendReplayRunsRequest", "Compares the reports of two replay runs of the same program, fixture and assertions without executing anything.", true},
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
		description := spec.summary + " " + backendReplayFamily
		if spec.name == "resolve_backend_diagram_scope" {
			// Review 2026-10-06, F31/F132: the replay text above (consent,
			// queued work, mocked payment) was published for this pure read
			// too, so an agent could skip it or ask for a consent it does not
			// need; it is also the step before a correlation diagramScope.
			description = "Pure read: resolves a diagram pin and semantic selectors to the exact target, targetHash, scopeHash, members, gaps and truncation. Never connects to a fixture or starts work. Resolve before save_backend_replay_package or correlate_backend_observations with a diagramScope; save_backend_replay_package refuses a truncated scope or one with gaps, so narrow the selectors first."
		}
		// The three lists page (review 2026-10-06, F123/F124); the loopback
		// already forwards `cursor` as a query parameter on GET routes.
		if page, paged := replayListPages[spec.name]; paged {
			schema["properties"].(map[string]any)["cursor"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64}
			description += " " + page
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
