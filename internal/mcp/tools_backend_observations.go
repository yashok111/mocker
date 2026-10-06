package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
	"strings"
)

func addBackendObservationTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract string
		read                  bool
	}{
		{"import_backend_observations", "POST /api/backend-projects/{id}/observations", "ObservationImportInput", false},
		{"list_backend_observations", "GET /api/backend-projects/{id}/observations", "", true},
		{"get_backend_observation_version", "GET /api/backend-projects/{id}/observations/{sid}/versions/{v}", "", true},
		{"get_backend_observation_records", "GET /api/backend-projects/{id}/observations/{sid}/versions/{v}/records", "", true},
		{"adapt_backend_observations", "POST /api/backend-projects/{id}/observations/adapt", "ObservationAdaptInput", true},
		{"correlate_backend_observations", "POST /api/backend-projects/{id}/observations/{sid}/correlations", "ObservationCorrelateInput", false},
		{"get_backend_observation_correlation", "GET /api/backend-projects/{id}/observations/{sid}/correlations/{v}", "", true},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var e error
			schema, e = api.BackendSchema(spec.contract)
			if e != nil {
				panic(e)
			}
		} else {
			schema = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}, "required": []any{}}
		}
		ids := []string{"projectId"}
		if strings.Contains(spec.route, "{sid}") {
			ids = append(ids, "observationSetId")
		}
		backendToolPathSchema(schema, ids)
		if spec.contract == "" {
			props := schema["properties"].(map[string]any)
			if strings.Contains(spec.route, "{v}") {
				props["version"] = map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9223372036854775807)}
				schema["required"] = append(schema["required"].([]any), "version")
			}
			if spec.name == "list_backend_observations" || spec.name == "get_backend_observation_records" || spec.name == "get_backend_observation_correlation" {
				props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 500}
				props["cursor"] = map[string]any{"type": "string"}
			}
		}
		schema["type"] = "object"
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: "Immutable observations and exact source/diagram correlation. Preserve full context, set/version/hash and source pins. Adapt is pure and does not collect or execute. Unknown build, missing data, associations and authored intent are not runtime proof. Replay exact idempotency keys after uncertain writes.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
