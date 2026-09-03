// tools_drift.go registers P4a's one MCP tool (decisions.md
// mocker-p4a-triage D7): get_workspace_drift, an adapter over
// GET /api/workspaces/{id}/drift — the read-only route that names the
// three things a spec re-bind or a rederive can silently strand
// (internal/admin/drift_handlers.go).
//
// Like every other tool in this package it is an ADAPTER: it decodes the
// JSON the handler actually writes, through a PRIVATE wire struct that
// mirrors driftReportView (unexported, in package admin, and this package
// cannot import it — §A6), and reaches the domain only through
// loopback.call. No confirmSlug: the route it wraps writes no workspace
// row but the resource_suggestions lazy backfill D4.4 permits (D7's own
// "it wraps a GET that writes no workspace row").
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func addDriftTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_workspace_drift",
		Description: "Reports what has silently stopped working since a workspace was last re-bound " +
			"to a different spec, or since a rederive dropped a family from the bound spec's newest " +
			"suggestion generation: an orphaned operation override (a pinned response the bound spec no " +
			"longer has an operation for), an orphaned confirmed resource family (one the bound spec's " +
			"newest suggestion generation no longer names), and a shadowing custom endpoint (one whose " +
			"method and canonical path a spec operation now also declares, so the spec's own operation " +
			"is unreachable behind it). hasDrift is true when any of the three arrays is non-empty. This " +
			"tool only REPORTS — repairing what it names means calling one of three existing verbs, and " +
			"each one DESTROYS something. DELETE /api/workspaces/{id}/operations/{opKey} drops that " +
			"override's pinned body, its recipes, its when[] conditions and its forced status. " +
			"DELETE /api/workspaces/{id}/endpoints/{eid} drops the custom endpoint's own authored body. " +
			"POST /api/workspaces/{id}/resource-decisions with state \"declined\" deletes the family's " +
			"entity rows. Read a row here before deleting it — there is no undo short of a checkpoint " +
			"taken beforehand.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetWorkspaceDrift(lb))
}

// ---- get_workspace_drift ----

// GetWorkspaceDriftInput is get_workspace_drift's input.
type GetWorkspaceDriftInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// driftOrphanedOverride is one row of GetWorkspaceDriftOutput.OrphanedOverrides.
type driftOrphanedOverride struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	OpKey  string `json:"opKey"`
}

// driftOrphanedResource is one row of GetWorkspaceDriftOutput.OrphanedResources.
type driftOrphanedResource struct {
	RouteFamily string `json:"routeFamily"`
	Name        string `json:"name"`
	ResourceID  int64  `json:"resourceId"`
	EntityCount int64  `json:"entityCount"`
}

// driftShadowedEndpoint is one row of GetWorkspaceDriftOutput.ShadowedEndpoints.
type driftShadowedEndpoint struct {
	EndpointID    int64  `json:"endpointId"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	CanonicalPath string `json:"canonicalPath"`
	PrecededSpec  bool   `json:"precededSpec"`
}

// GetWorkspaceDriftOutput is get_workspace_drift's declared output schema,
// projected field-for-field from driftReportView
// (internal/admin/drift_handlers.go) — three typed arrays and the boolean
// derived from them, never a fourth "remedy" field (D4.1, D9): the three
// repair verbs live in this tool's own description, not on the wire.
type GetWorkspaceDriftOutput struct {
	HasDrift          bool                    `json:"hasDrift"`
	OrphanedOverrides []driftOrphanedOverride `json:"orphanedOverrides"`
	OrphanedResources []driftOrphanedResource `json:"orphanedResources"`
	ShadowedEndpoints []driftShadowedEndpoint `json:"shadowedEndpoints"`
}

// driftReportWire decodes driftReportView's whole body — GET
// /api/workspaces/{id}/drift's own 200 response.
type driftReportWire struct {
	HasDrift          bool                    `json:"hasDrift"`
	OrphanedOverrides []driftOrphanedOverride `json:"orphanedOverrides"`
	OrphanedResources []driftOrphanedResource `json:"orphanedResources"`
	ShadowedEndpoints []driftShadowedEndpoint `json:"shadowedEndpoints"`
}

func handleGetWorkspaceDrift(lb *loopback) sdk.ToolHandlerFor[GetWorkspaceDriftInput, GetWorkspaceDriftOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetWorkspaceDriftInput) (*sdk.CallToolResult, GetWorkspaceDriftOutput, error) {
		var wire driftReportWire
		method, path := toolPath("get_workspace_drift", "GET /api/workspaces/{id}/drift", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, GetWorkspaceDriftOutput{}, err
		}
		return nil, GetWorkspaceDriftOutput(wire), nil
	}
}
