package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/api"
)

func addBackendFindingTools(s *sdk.Server, lb *loopback) {
	hash := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	schema, err := api.BackendSchema("ReviewBackendFindingRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(schema, []string{"projectId"})
	schema["properties"].(map[string]any)["fingerprint"] = hash
	// The schema loader retains required as []any.
	switch required := schema["required"].(type) {
	case []any:
		schema["required"] = append(required, "fingerprint")
	case []string:
		schema["required"] = append(required, "fingerprint")
	}
	addBackendImportTool(s, lb, &sdk.Tool{Name: "review_backend_finding", Description: "Review exact diagnostic basis with CAS and immutable history. Resolved requires a completed scoped recheck with sufficient coverage. Review never proves runtime behavior. Replay the same key and body after a lost response.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true}}, "PUT /api/backend-projects/{id}/findings/{fingerprint}/review")
	props := map[string]any{"projectId": map[string]any{"type": "string", "format": "uuid"}, "jobId": map[string]any{"type": "string", "format": "uuid"}, "resultVersion": map[string]any{"type": "integer", "minimum": 1, "maximum": int64(9223372036854775807)}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "cursor": hash}
	addBackendImportTool(s, lb, &sdk.Tool{Name: "list_backend_findings", Description: "Read immutable diagnostic occurrences at exact job/resultVersion alongside live review history. Cursor is the last fingerprint.", InputSchema: designScenarioSchemaObject([]string{"projectId", "jobId", "resultVersion"}, props), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "GET /api/backend-projects/{id}/findings")
}
