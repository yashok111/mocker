package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendFlowTools(s *sdk.Server, lb *loopback) {
	schema, err := api.BackendSchema("QueryBackendFlowRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(schema, []string{"projectId"})
	schema["type"] = "object"
	tool := &sdk.Tool{Name: "query_backend_flow", Description: "Inspect source3 through source6 entrypoints, steps/transitions or data accesses, or a full changeProposal with source5/6 baseline. Legacy proposal and importCandidate targets are unsupported. Keep exact target/pins on all pages; desired structure and baseline proof stay separate. Bounded reachability proves neither execution nor transaction atomicity.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendImportTool(s, lb, tool, "POST /api/backend-projects/{id}/flow/query")
}
