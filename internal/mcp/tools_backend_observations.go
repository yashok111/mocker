package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
	"strings"
)

// backendObservationFamily follows each observation tool's own summary
// sentence; see backendAnalysisFamily for why the two are separate.
const backendObservationFamily = "Immutable observations and exact source/diagram correlation. Preserve full context, set/version/hash and source pins. Adapt is pure and does not collect or execute. Unknown build, missing data, associations and authored intent are not runtime proof. Replay exact idempotency keys after uncertain writes."

func addBackendObservationTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract, summary string
		read                           bool
	}{
		{"import_backend_observations", "POST /api/backend-projects/{id}/observations", "ObservationImportInput", "Imports adapted records as a new observation set or appends them as the next immutable version of an existing set.", false},
		{"list_backend_observations", "GET /api/backend-projects/{id}/observations", "", "Lists one page of every observation set version in the project with its receipt, name and context.", true},
		{"get_backend_observation_version", "GET /api/backend-projects/{id}/observations/{sid}/versions/{v}", "", "Reads one exact observation set version's receipt, name and context, without its records.", true},
		{"get_backend_observation_records", "GET /api/backend-projects/{id}/observations/{sid}/versions/{v}/records", "", "Reads one page of the records of one exact observation set version.", true},
		{"adapt_backend_observations", "POST /api/backend-projects/{id}/observations/adapt", "ObservationAdaptInput", "Converts an already-produced OTLP trace, JUnit summary or Orders replay report into reviewable records, storing nothing.", true},
		{"correlate_backend_observations", "POST /api/backend-projects/{id}/observations/{sid}/correlations", "ObservationCorrelateInput", "Correlates one exact observation set version with a source revision graph and saves the result as an immutable correlation version.", false},
		{"get_backend_observation_correlation", "GET /api/backend-projects/{id}/observations/{sid}/correlations/{v}", "", "Reads one page of the rows of one saved observation correlation version.", true},
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
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: spec.summary + " " + backendObservationFamily, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
