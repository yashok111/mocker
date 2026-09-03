// tools_stream_preview.go registers P6b's one MCP tool (decisions.md
// mocker-p6b-sse-mock D13): preview_endpoint, an adapter over
// POST /api/workspaces/{id}/endpoints/preview — the first frames a stream
// draft would send, with no row written. Tool forty-eight. Like every other
// tool here it is an ADAPTER reaching the domain only through loopback.call;
// no confirmSlug, nothing is destroyed or even stored.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

func addStreamPreviewTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "preview_endpoint",
		Description: "Lays out the first frames (at most 50) a Server-Sent Events endpoint DRAFT would send, " +
			"on one time axis, without saving it: each frame's offset from the handshake in milliseconds, " +
			"its event name and its data — timeline frames as authored, tick frames generated from the " +
			"draft's inline JSON Schema with the workspace's own seed (a draft previews with endpoint id 0, " +
			"so a saved row's tick bodies differ from the preview's while agreeing in shape). truncated is " +
			"true when the stream would send more than the preview shows (a looping timeline or a tick " +
			"always does). maxBytesPerSec is an ESTIMATE of the connection's peak outbound rate — an " +
			"authored stream is an amplifier an unauthenticated client can trigger with one request, so " +
			"read it before saving. The draft is validated exactly as create_endpoint would validate it: a " +
			"tick interval below 100 ms, more than 500 timeline frames, a frame delay over 30000 ms, a " +
			"payload over MOCKER_MAX_RESPONSE or a $ref in the schema is refused here with the same " +
			"message. kind \"sse\" and kind \"ws\" preview (P6d); method must be GET. For a ws draft the " +
			"timeline and tick are laid out exactly as for sse, and rules (the reactive rule count) and " +
			"echo are reported beside the frames — inbound behaviours have no time axis to be laid out on.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handlePreviewEndpoint(lb))
}

// ---- preview_endpoint ----

// PreviewEndpointInput is preview_endpoint's input: the draft in
// create_endpoint's own kind/stream shape.
type PreviewEndpointInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	// Stream is the stream document (see create_endpoint's stream field):
	// {timeline: {frames: [{delayMs, event, data}], loop}, tick: {intervalMs,
	// event, schema}, closeWhenDone}.
	Stream any `json:"stream"`
}

// StreamPreviewFrame is one frame of PreviewEndpointOutput.
type StreamPreviewFrame struct {
	AtMs  int    `json:"atMs"`
	Event string `json:"event,omitempty"`
	Data  any    `json:"data"`
}

// PreviewEndpointOutput is preview_endpoint's declared output schema,
// projected field-for-field from streamPreviewView
// (internal/admin/endpoint_preview_handlers.go).
type PreviewEndpointOutput struct {
	Kind           string               `json:"kind"`
	Frames         []StreamPreviewFrame `json:"frames"`
	Truncated      bool                 `json:"truncated"`
	MaxBytesPerSec int64                `json:"maxBytesPerSec"`
}

func handlePreviewEndpoint(lb *loopback) sdk.ToolHandlerFor[PreviewEndpointInput, PreviewEndpointOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in PreviewEndpointInput) (*sdk.CallToolResult, PreviewEndpointOutput, error) {
		body, err := jsonx.Marshal(struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Stream any    `json:"stream,omitempty"`
		}{Method: in.Method, Path: in.Path, Kind: in.Kind, Stream: in.Stream})
		if err != nil {
			return nil, PreviewEndpointOutput{}, fmt.Errorf("mcp: encode preview_endpoint request: %w", err)
		}
		var out PreviewEndpointOutput
		method, path := toolPath("preview_endpoint", "POST /api/workspaces/{id}/endpoints/preview", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &out); err != nil {
			return nil, PreviewEndpointOutput{}, err
		}
		if out.Frames == nil {
			out.Frames = []StreamPreviewFrame{}
		}
		return nil, out, nil
	}
}
