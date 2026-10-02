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
	tool := &sdk.Tool{Name: "query_backend_flow", Description: "Inspect source3/4 entrypoints, flow steps/transitions or direct/possible data accesses at an exact source revision. Paginate pinned results and inspect per-record evidence. Bounded reachability does not prove execution or transaction atomicity. Explicit source4 field lineage uses query_backend_lineage. Proposal selectors are unsupported.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendImportTool(s, lb, tool, "POST /api/backend-projects/{id}/flow/query")
}
