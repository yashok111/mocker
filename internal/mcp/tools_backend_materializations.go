package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/api"
)

// backendMaterializationFamily follows each materialization tool's own
// summary sentence; see backendAnalysisFamily for why the two are separate.
const backendMaterializationFamily = "Explicit backend-http-draft-v1 translation at exact full proposal and owner pins. Preview is pure and lists linked draft mock effects and translated/excluded/unsupported scope. Apply uses one transaction and durable receipt; retry the identical key/body after uncertainty. No publication, source execution or behavioral equivalence claim."

func addBackendMaterializationTools(s *sdk.Server, lb *loopback) {
	schema, err := api.BackendSchema("QueryBackendExploreRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(schema, []string{"projectId"})
	addBackendImportTool(s, lb, &sdk.Tool{Name: "query_backend_explore", Description: "Read-only bounded source overview, navigational API collections and object neighborhoods at an exact target. Collections are not architectural claims. No source manifests or execution.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "POST /api/backend-projects/{id}/explore/query")
	for _, entry := range []struct{ name, route string }{{"list_backend_import_summaries", "GET /api/backend-projects/{id}/import-summaries"}, {"list_backend_materializations", "GET /api/backend-projects/{id}/materializations"}, {"get_backend_materialization", "GET /api/backend-projects/{id}/materializations/{mid}"}} {
		props := map[string]any{"projectId": map[string]any{"type": "string", "format": "uuid"}}
		required := []string{"projectId"}
		if entry.name == "get_backend_materialization" {
			props["materializationId"] = map[string]any{"type": "string", "format": "uuid"}
			required = append(required, "materializationId")
		} else {
			props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
			props["cursor"] = map[string]any{"type": "string", "maxLength": 1024}
		}
		description := "Read one persisted applied materialization plan and receipt with exact artifact pins. No preview/apply, writes, repair or owner head substitution."
		switch entry.name {
		case "list_backend_import_summaries":
			description = "Read newest project import status summaries without source file manifests. Project-bound cursor; no import, preview, commit or retry."
		case "list_backend_materializations":
			description = "List persisted materialization result summaries for a project, newest first. Project-bound cursor; no preview, apply or owner head substitution."
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: entry.name, Description: description, InputSchema: designScenarioSchemaObject(required, props), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, entry.route)
	}

	for _, entry := range []struct {
		name, schema, route, summary string
		read                         bool
	}{
		{"preview_backend_materialization", "PreviewBackendMaterializationRequest", "POST /api/backend-projects/{id}/materializations/preview", "Previews translating a full change proposal into linked draft mock edits and returns the plan and candidateHash without writing.", true},
		{"apply_backend_materialization", "ApplyBackendMaterializationRequest", "POST /api/backend-projects/{id}/materializations/apply", "Applies a previewed materialization (the identical preview input plus candidateHash) to every linked draft mock in one transaction.", false},
	} {
		schema, err := api.BackendSchema(entry.schema)
		if err != nil {
			panic(err)
		}
		backendToolPathSchema(schema, []string{"projectId"})
		addBackendImportTool(s, lb, &sdk.Tool{Name: entry.name, Description: entry.summary + " " + backendMaterializationFamily, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: entry.read, IdempotentHint: true}}, entry.route)
	}
}
