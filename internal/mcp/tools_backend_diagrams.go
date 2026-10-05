package mcp

import (
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendDiagramTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract string
		read                  bool
	}{
		{"build_backend_interactions", "POST /api/backend-projects/{id}/diagrams/interactions/build", "BuildBackendInteractionsRequest", true},
		{"create_backend_diagram", "POST /api/backend-projects/{id}/diagrams", "CreateBackendDiagramRequest", false},
		{"save_backend_diagram", "POST /api/backend-projects/{id}/diagrams/{did}/save", "SaveBackendDiagramRequest", false},
		{"fork_backend_diagram", "POST /api/backend-projects/{id}/diagrams/fork", "ForkBackendDiagramRequest", false},
		{"list_backend_diagrams", "GET /api/backend-projects/{id}/diagrams", "", true},
		{"get_backend_diagram", "GET /api/backend-projects/{id}/diagrams/{did}/versions/{v}", "", true},
		{"query_backend_diagram", "POST /api/backend-projects/{id}/diagrams/query", "QueryBackendDiagramRequest", true},
		{"compare_backend_diagrams", "POST /api/backend-projects/{id}/diagrams/compare", "CompareBackendDiagramsRequest", true},
		{"create_backend_diagram_view", "POST /api/backend-projects/{id}/diagram-views", "CreateBackendDiagramViewRequest", false},
		{"save_backend_diagram_view", "POST /api/backend-projects/{id}/diagram-views/{vid}/save", "SaveBackendDiagramViewRequest", false},
		{"list_backend_diagram_views", "GET /api/backend-projects/{id}/diagram-views", "", true},
		{"get_backend_diagram_view", "GET /api/backend-projects/{id}/diagram-views/{vid}/versions/{v}", "", true},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var err error
			schema, err = api.BackendSchema(spec.contract)
			if err != nil {
				panic(err)
			}
			ids := []string{"projectId"}
			if strings.Contains(spec.route, "{did}") {
				ids = append(ids, "diagramId")
			}
			if strings.Contains(spec.route, "{vid}") {
				ids = append(ids, "viewId")
			}
			backendToolPathSchema(schema, ids)
		} else {
			id := map[string]any{"type": "string", "format": "uuid"}
			props := map[string]any{"projectId": id}
			required := []string{"projectId"}
			if strings.HasPrefix(spec.name, "list_") {
				props["kind"] = map[string]any{"type": "string", "enum": []string{"architecture", "interactions"}}
				props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 500}
				props["cursor"] = map[string]any{"type": "string", "maxLength": 4096}
			} else {
				field := "diagramId"
				if spec.name == "get_backend_diagram_view" {
					field = "viewId"
				}
				props[field] = id
				props["version"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1, "maximum": int64(9223372036854775807)}
				required = append(required, field, "version")
				if spec.name == "get_backend_diagram" {
					props["hash"] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
					required = append(required, "hash")
				}
			}
			schema = designScenarioSchemaObject(required, props)
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: "Exact architecture/interactions companion mapping or pinned diagram-view-v1. Query/compare are read-only. Authored intent is not source proof. No-op saves retain versions; replay identical keys before CAS. A new semantic pin requires a new saved view.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
