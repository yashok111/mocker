// tools_stream.go registers P6a's one MCP tool (decisions.md mocker-p6a-sse
// D16): get_stream_stats, an adapter over GET /api/stream/stats — the
// process-wide streaming health of internal/stream's registry
// (internal/admin/stream_handlers.go). Tool forty-seven.
//
// Like every other tool in this package it is an ADAPTER: it decodes the
// JSON the handler actually writes and reaches the domain only through
// loopback.call. No confirmSlug — it destroys nothing — and no
// workspaceId: D8's cap and D15's counters are properties of the process,
// not of any one workspace, which is also why the route it wraps is not
// workspace-scoped. The stream route itself has no tool: an in-process
// loopback response cannot take a write deadline (D9's exact refusal), and
// a stream is not a value a tool call returns.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func addStreamTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_stream_stats",
		Description: "Reports the process-wide health of the admin plane's Server-Sent Events traffic feed " +
			"(GET /api/workspaces/{id}/traffic/stream): open is the number of live stream connections " +
			"across every workspace, cap the fixed process-wide ceiling (a handshake over it is refused " +
			"with 503), refusedCap and refusedUnsupported the two refusal counters since the process " +
			"started (over the cap, and a response writer that could not stream at all, answered 501), " +
			"coalescedNudges how many wake-ups were dropped because a connection was already reading " +
			"(no row is lost by that — the read in flight returns everything the dropped nudge " +
			"announced), and byWorkspace one row per workspace holding at least one live connection. " +
			"Read-only; an agent that wants the rows themselves calls list_traffic with a since cursor.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetStreamStats(lb))
}

// ---- get_stream_stats ----

// GetStreamStatsInput is get_stream_stats's input: nothing, the route is
// not workspace-scoped.
type GetStreamStatsInput struct{}

// streamWorkspaceStats is one row of GetStreamStatsOutput.ByWorkspace.
type streamWorkspaceStats struct {
	WorkspaceID int64 `json:"workspaceId"`
	Open        int   `json:"open"`
}

// GetStreamStatsOutput is get_stream_stats's declared output schema,
// projected field-for-field from stream.Stats (internal/stream/registry.go),
// which the handler marshals as-is.
type GetStreamStatsOutput struct {
	Open               int                    `json:"open"`
	Cap                int                    `json:"cap"`
	RefusedCap         int64                  `json:"refusedCap"`
	RefusedUnsupported int64                  `json:"refusedUnsupported"`
	CoalescedNudges    int64                  `json:"coalescedNudges"`
	ByWorkspace        []streamWorkspaceStats `json:"byWorkspace"`
}

func handleGetStreamStats(lb *loopback) sdk.ToolHandlerFor[GetStreamStatsInput, GetStreamStatsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, _ GetStreamStatsInput) (*sdk.CallToolResult, GetStreamStatsOutput, error) {
		var out GetStreamStatsOutput
		method, path := toolPath("get_stream_stats", "GET /api/stream/stats")
		if err := lb.call(ctx, method, path, nil, &out); err != nil {
			return nil, GetStreamStatsOutput{}, err
		}
		if out.ByWorkspace == nil {
			out.ByWorkspace = []streamWorkspaceStats{}
		}
		return nil, out, nil
	}
}
