package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendEventsTools(s *sdk.Server, lb *loopback) {
	schema, err := api.BackendSchema("QueryBackendEventsRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(schema, []string{"projectId"})
	schema["type"] = "object"
	tool := &sdk.Tool{Name: "query_backend_events", Description: "Inspect explicit source5 event routes, static jobs or service calls at an exact immutable revision. Exact edge pairs preserve producer/message/channel/consumer context, evidence, dispatch and boundaries. Complete means bounded enumeration, never source coverage or delivery. Reports complete-scan admission and item/witness/auxiliary limits; no execution or history. Proposal selectors are unsupported.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendImportTool(s, lb, tool, "POST /api/backend-projects/{id}/events/query")
}
