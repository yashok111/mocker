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
	tool := &sdk.Tool{Name: "query_backend_lineage", Description: "Inspect explicit source4/5 value dependencies at an exact revision and full seed address. Returns entire multi-source mappings, evidence status, unknown boundaries and bounded witnesses. Static dependencies do not prove execution or complete coverage. Proposal selectors are unsupported.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendImportTool(s, lb, tool, "POST /api/backend-projects/{id}/lineage/query")
}
