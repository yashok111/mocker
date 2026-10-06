package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendMaterializationTools(s *sdk.Server, lb *loopback) {
	for _, entry := range []struct {
		name, schema, route string
		read                bool
	}{
		{"preview_backend_materialization", "PreviewBackendMaterializationRequest", "POST /api/backend-projects/{id}/materializations/preview", true},
		{"apply_backend_materialization", "ApplyBackendMaterializationRequest", "POST /api/backend-projects/{id}/materializations/apply", false},
	} {
		schema, err := api.BackendSchema(entry.schema)
		if err != nil {
			panic(err)
		}
		backendToolPathSchema(schema, []string{"projectId"})
		addBackendImportTool(s, lb, &sdk.Tool{Name: entry.name, Description: "Explicit backend-http-draft-v1 translation at exact full proposal and owner pins. Preview is pure and lists linked draft mock effects and translated/excluded/unsupported scope. Apply uses one transaction and durable receipt; retry the identical key/body after uncertainty. No publication, source execution or behavioral equivalence claim.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: entry.read, IdempotentHint: true}}, entry.route)
	}
}
