package mcp

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// addDesignTools registers P7a's one tool: the workspace as an OpenAPI
// document (DESIGN §34.4). It is the deliverable an agent hands to the
// backend team, and the input `import_spec` takes back when the design is
// accepted as the next base.
func addDesignTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "export_openapi",
		Description: "Composes the workspace into ONE OpenAPI 3.1 document: the bound spec as the base " +
			"(an empty 3.1 skeleton when none is bound — a design from nothing), the workspace layer as " +
			"the delta over it. A custom endpoint at a new shape becomes an operation (its response " +
			"schemas, its summary/description/tags/operationId/parameters, its reqSchema as requestBody, " +
			"a pinned body as an example); one at a base operation's canonical shape REPLACES that " +
			"operation; an override's schemaPatch is written inline on the response schema and a pinned " +
			"override body becomes an example; routeOff marks the operation deprecated: true rather than " +
			"deleting it (the base is the contract the backend holds — a removal is a proposal, and the " +
			"reader must see it); an endpoint with overrideOn:false is omitted. info.version carries " +
			"-draft.<revision>. The document RE-IMPORTS: import_spec of it, then update_workspace with " +
			"the new specId, is how a design becomes the next base — then read get_workspace_drift and " +
			"DELETE the delta rows it names, because a schemaPatch applied a second time over a base " +
			"that already carries the patched schema fails and that variant then serves unpatched. " +
			"Scenarios, entity rows, assets and live state are never carried: none of them is contract. " +
			"get_guide {topic: \"design\"} is the whole workflow.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleExportOpenAPI(lb))
}

// ExportOpenAPIInput is export_openapi's input.
type ExportOpenAPIInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// ExportOpenAPIOutput carries the document as ONE JSON STRING — the bytes
// the route served, untouched (decisions.md D7): that is the shape
// import_spec takes, so the accept step passes it straight back, and the
// spec's hash (import_spec deduplicates by it) is over exactly these bytes
// rather than over whatever an agent's own encoder would re-serialize.
type ExportOpenAPIOutput struct {
	Document string `json:"document" jsonschema:"the OpenAPI 3.1 document as one JSON string, exactly as served; pass it to import_spec unchanged"`
}

func handleExportOpenAPI(lb *loopback) sdk.ToolHandlerFor[ExportOpenAPIInput, ExportOpenAPIOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ExportOpenAPIInput) (*sdk.CallToolResult, ExportOpenAPIOutput, error) {
		if in.WorkspaceID <= 0 {
			return nil, ExportOpenAPIOutput{}, errors.New("export_openapi: workspaceId is required")
		}
		method, path := toolPath("export_openapi", "GET /api/workspaces/{id}/openapi.json", in.WorkspaceID)
		status, body, err := lb.do(ctx, method, path, nil)
		if err != nil {
			return nil, ExportOpenAPIOutput{}, fmt.Errorf("export_openapi: %w", err)
		}
		if status < 200 || status >= 300 {
			return nil, ExportOpenAPIOutput{}, fmt.Errorf("export_openapi: %w", toolErr(status, body))
		}
		return nil, ExportOpenAPIOutput{Document: string(body)}, nil
	}
}
