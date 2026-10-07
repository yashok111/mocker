package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

// backendMaterializationFamily follows each materialization tool's own
// summary sentence; see backendAnalysisFamily for why the two are separate.
const backendMaterializationFamily = "Explicit backend-http-draft-v1 translation at exact full proposal and owner pins. Preview is pure and lists linked draft mock effects and translated/excluded/unsupported scope. Apply uses one transaction and durable receipt; retry the identical key/body after uncertainty. No publication, source execution or behavioral equivalence claim."

func addBackendMaterializationTools(s *sdk.Server, lb *loopback) {
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
