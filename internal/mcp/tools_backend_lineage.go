package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendLineageTools(s *sdk.Server, lb *loopback) {
	schema, err := api.BackendSchema("QueryBackendLineageRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(schema, []string{"projectId"})
	schema["type"] = "object"
	tool := &sdk.Tool{Name: "query_backend_lineage", Description: "Inspect source4 through source6 value dependencies or a full changeProposal with source5/6 baseline, using the complete seed address including facet/port/event context. Legacy proposal and importCandidate targets are unsupported. Returns whole multi-source mappings, source/desired status, unknown boundaries and bounded witnesses; never claims execution.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendImportTool(s, lb, tool, "POST /api/backend-projects/{id}/lineage/query")
}
