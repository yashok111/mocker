package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendSavedViewTools(s *sdk.Server, lb *loopback) {
	const base = "/api/backend-projects/{id}/saved-views"
	for _, spec := range []struct {
		name, route, contract, description string
		readOnly                           bool
	}{
		{"list_backend_saved_views", "GET " + base, "", "Lists current saved view summaries with project/kind/limit-bound paging. Names and versions may advance between pages.", true},
		{"get_backend_saved_view", "GET " + base + "/{vid}", "", "Reads one immutable saved presentation at an exact version, or resolves latest once when version is omitted. Use its exact pins; reopening starts at the first page.", true},
		{"create_backend_saved_view", "POST " + base, "CreateBackendSavedViewRequest", "Creates a saved-view-v1 presentation bound to an exact source or database proposal revision. No source or proposal semantics change. Retry uncertain results with the identical request and key.", false},
		{"save_backend_saved_view", "POST " + base + "/{vid}/save", "SaveBackendSavedViewRequest", "Appends a presentation version using saved view CAS. Target and kind are immutable. Retry uncertain results with the identical request/key; on conflict reload or save as new.", false},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var err error
			schema, err = api.BackendSchema(spec.contract)
			if err != nil {
				panic(err)
			}
			ids := []string{"projectId"}
			if spec.name == "save_backend_saved_view" {
				ids = append(ids, "viewId")
			}
			backendToolPathSchema(schema, ids)
		} else {
			id := map[string]any{"type": "string", "format": "uuid"}
			props := map[string]any{"projectId": id}
			required := []string{"projectId"}
			if spec.name == "get_backend_saved_view" {
				required = append(required, "viewId")
				props["viewId"] = id
				props["version"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1, "maximum": int64(9223372036854775807)}
			} else {
				props["kind"] = map[string]any{"type": "string", "enum": []string{"flow", "database"}}
				props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
				props["cursor"] = map[string]any{"type": "string", "maxLength": 1024}
			}
			schema = designScenarioSchemaObject(required, props)
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: spec.description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.readOnly, IdempotentHint: true}}, spec.route)
	}
}
