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
	tool := &sdk.Tool{Name: "query_backend_events", Description: "Inspect source5/source6 routes, jobs and service calls or a full changeProposal with source5/6 baseline. Legacy proposal and importCandidate targets are unsupported. Exact edge tuples preserve producer/message/channel/consumer context. Desired witnesses retain baseline evidence labels. Complete means bounded enumeration, never source coverage, runtime delivery or execution.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}
	addBackendImportTool(s, lb, tool, "POST /api/backend-projects/{id}/events/query")
}
