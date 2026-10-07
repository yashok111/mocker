package mcp

import (
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

// backendDiagramFamily follows each diagram tool's own summary sentence; see
// backendAnalysisFamily for why the two are separate.
const backendDiagramFamily = "Exact architecture/interactions/lifecycle/business_map companion mapping or pinned diagram-view-v1. Query/compare are read-only. Authored intent is not source proof. No-op saves retain versions; replay identical keys before CAS. A new semantic pin requires a new saved view."

func addBackendDiagramTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract, summary string
		read                           bool
	}{
		{"build_backend_interactions", "POST /api/backend-projects/{id}/diagrams/interactions/build", "BuildBackendInteractionsRequest", "Builds an unsaved interactions diagram candidate from one exact source entrypoint, writing nothing.", true},
		{"build_backend_lifecycle", "POST /api/backend-projects/{id}/diagrams/lifecycle/build", "BuildBackendLifecycleRequest", "Builds an unsaved lifecycle diagram candidate from one pinned state-diagram artifact row, writing nothing.", true},
		{"create_backend_diagram", "POST /api/backend-projects/{id}/diagrams", "CreateBackendDiagramRequest", "Creates a new architecture, interactions, lifecycle or business_map diagram at version 1 from an explicit document.", false},
		{"save_backend_diagram", "POST /api/backend-projects/{id}/diagrams/{did}/save", "SaveBackendDiagramRequest", "Saves an edited document as the next semantic version of an existing diagram, under expectedVersion CAS.", false},
		{"fork_backend_diagram", "POST /api/backend-projects/{id}/diagrams/fork", "ForkBackendDiagramRequest", "Forks a pinned diagram onto a new target as a new diagram with its own ID, keeping fork provenance.", false},
		{"list_backend_diagrams", "GET /api/backend-projects/{id}/diagrams", "", "Lists one page of the project's diagrams, optionally filtered by kind.", true},
		{"get_backend_diagram", "GET /api/backend-projects/{id}/diagrams/{did}/versions/{v}", "", "Reads one exact diagram version by diagramId, version and content hash.", true},
		{"query_backend_diagram", "POST /api/backend-projects/{id}/diagrams/query", "QueryBackendDiagramRequest", "Queries one paged section (elements, links, members or gaps) of a pinned diagram version's projection.", true},
		{"compare_backend_diagrams", "POST /api/backend-projects/{id}/diagrams/compare", "CompareBackendDiagramsRequest", "Compares two pinned diagram versions by stable ID, listing added, removed and changed fields.", true},
		{"create_backend_diagram_view", "POST /api/backend-projects/{id}/diagram-views", "CreateBackendDiagramViewRequest", "Creates a saved diagram view (level, root, filters, layout) pinned to one exact diagram version.", false},
		{"save_backend_diagram_view", "POST /api/backend-projects/{id}/diagram-views/{vid}/save", "SaveBackendDiagramViewRequest", "Saves the next version of an existing diagram view's state and layout; its diagram pin cannot change.", false},
		{"list_backend_diagram_views", "GET /api/backend-projects/{id}/diagram-views", "", "Lists one page of the project's saved diagram views, optionally filtered by kind.", true},
		{"get_backend_diagram_view", "GET /api/backend-projects/{id}/diagram-views/{vid}/versions/{v}", "", "Reads one exact diagram view version, including the diagram pin it was created with.", true},
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
				props["kind"] = map[string]any{"type": "string", "enum": []string{"architecture", "interactions", "lifecycle", "business_map"}}
				props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 500}
				props["cursor"] = map[string]any{"type": "string", "maxLength": 4096, "description": "nextCursor of the previous page; it binds the filters and the limit, so keep the same limit"}
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
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: spec.summary + " " + backendDiagramFamily, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
