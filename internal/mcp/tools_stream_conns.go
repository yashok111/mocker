// tools_stream_conns.go registers P6c's three MCP tools (decisions.md
// mocker-p6c-live-conns D1, D9): list_stream_connections,
// close_stream_connection and push_stream_frame — adapters over the three
// routes under /api/workspaces/{id}/connections, the live-connection
// surface of the MOCK plane's SSE endpoints (internal/admin/
// connection_handlers.go). Tools forty-nine to fifty-one. Like every other
// tool here they are ADAPTERS reaching the domain only through
// loopback.call; no confirmSlug on any of them — a close destroys a
// connection, never workspace-created data, and a push destroys nothing.
//
// What a caller must know and cannot see from a schema is in each
// description: a connection id is valid only while the process runs; a
// close is followed by the client's own reconnect (a NEW connection with
// a NEW id); a push is delivered or refused, never silently queued.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

func addStreamConnectionTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "list_stream_connections",
		Description: "Lists the live stream connections a workspace's mock endpoints (kind \"sse\" and, since P6d, " +
			"kind \"ws\") currently hold: open is the workspace's live count, cap the per-workspace ceiling " +
			"(MOCKER_STREAM_MAX_CONNS — a handshake over it is refused with 503), and connections one row per " +
			"connection with its id, the endpoint it is on (endpointId, path, kind), the client's address " +
			"(remoteAddr), when it opened, and three live counters: frames written so far (pushed ones " +
			"included), pushed (frames an operator pushed with push_stream_frame), skipped (generated tick " +
			"bodies over MOCKER_MAX_RESPONSE) and framesIn (inbound frames read on a WebSocket connection; 0 " +
			"for SSE). Optional endpointId narrows the rows to one endpoint; open stays " +
			"the workspace's whole count. A connection id is issued by the running process and is valid only " +
			"while it runs — after a restart the same number can name a different connection, so list before " +
			"you close or push. The admin plane's own traffic feed connections are not listed here.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListStreamConnections(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "close_stream_connection",
		Description: "Closes ONE live stream connection (kind \"sse\" or \"ws\") of a workspace's mock endpoint by its id (from " +
			"list_stream_connections). The server cancels the connection: an SSE connection gets no final frame " +
			"(SSE has no close frame), a WebSocket connection gets a closing handshake with code 1001 \"closed by " +
			"operator\"; the socket closes, and the connection's single traffic row is written with " +
			"closed:admin in its notes. A browser EventSource reconnects on its own a few seconds later and " +
			"appears as a NEW connection with a NEW id — closing does not stop a client from coming back; edit " +
			"or delete the endpoint for that. 404 connection_not_found when the id is not a live connection of " +
			"THIS workspace (never issued, already closed, or another workspace's) — list again. No confirmSlug: " +
			"a connection is not workspace data.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: false},
	}, handleCloseStreamConnection(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "push_stream_frame",
		Description: "Pushes ONE frame into ONE live stream connection (kind \"sse\" or \"ws\") of a workspace's mock endpoint (id from " +
			"list_stream_connections) and waits until the connection's own loop has written it: the answer " +
			"carries the frameId the frame went out under (the connection's next `id:`, interleaved with its " +
			"timeline and tick frames). event is the optional SSE event name (at most 64 bytes, no line break; " +
			"empty means the browser's default message event); data is any JSON value, written compact on one " +
			"data: line, at most MOCKER_MAX_RESPONSE bytes — the same rules a timeline frame passes in " +
			"create_endpoint, refused with the same words. On a kind \"ws\" connection the frame goes out as one " +
			"text frame and event must be omitted (a WebSocket frame carries no event name; a non-empty event " +
			"is refused 400 by name). The frame lives in the connection alone: it is never " +
			"stored, never replayed to a later connection, and dies with the connection if it is not written. " +
			"Refusals: 404 connection_not_found (list again); 409 inbox_full (16 frames already queued on this " +
			"connection and not yet written — the frame was NOT queued; wait or close the connection); " +
			"409 connection_closed (the connection ended while the frame was queued — not written); " +
			"504 push_timeout (the loop did not write it within two frame timeouts, MOCKER_STREAM_FRAME_TIMEOUT×2 " +
			"— the frame STAYS queued and may still be written; do not resend blindly). No confirmSlug.",
		Annotations: &sdk.ToolAnnotations{IdempotentHint: false},
	}, handlePushStreamFrame(lb))
}

func ptr[T any](v T) *T { return &v }

// ---- list_stream_connections ----

// ListStreamConnectionsInput is list_stream_connections's input.
type ListStreamConnectionsInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	// EndpointID, when set, narrows the rows to one endpoint's connections.
	EndpointID int64 `json:"endpointId,omitempty"`
}

// StreamConnection is one row of ListStreamConnectionsOutput.Connections,
// projected field-for-field from stream.Snapshot (internal/stream).
type StreamConnection struct {
	ID         int64  `json:"id"`
	EndpointID int64  `json:"endpointId"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	RemoteAddr string `json:"remoteAddr"`
	OpenedAt   string `json:"openedAt"`
	Frames     int64  `json:"frames"`
	Pushed     int64  `json:"pushed"`
	Skipped    int64  `json:"skipped"`
	// FramesIn is P6d's inbound count; 0 on an SSE connection.
	FramesIn int64 `json:"framesIn"`
}

// ListStreamConnectionsOutput is list_stream_connections's declared output
// schema, projected from connectionListView (internal/admin).
type ListStreamConnectionsOutput struct {
	Open        int                `json:"open"`
	Cap         int                `json:"cap"`
	Connections []StreamConnection `json:"connections"`
}

func handleListStreamConnections(lb *loopback) sdk.ToolHandlerFor[ListStreamConnectionsInput, ListStreamConnectionsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListStreamConnectionsInput) (*sdk.CallToolResult, ListStreamConnectionsOutput, error) {
		var out ListStreamConnectionsOutput
		method, path := toolPath("list_stream_connections", "GET /api/workspaces/{id}/connections", in.WorkspaceID)
		if in.EndpointID > 0 {
			path += "?endpointId=" + strconv.FormatInt(in.EndpointID, 10)
		}
		if err := lb.call(ctx, method, path, nil, &out); err != nil {
			return nil, ListStreamConnectionsOutput{}, err
		}
		if out.Connections == nil {
			out.Connections = []StreamConnection{}
		}
		return nil, out, nil
	}
}

// ---- close_stream_connection ----

// CloseStreamConnectionInput is close_stream_connection's input.
type CloseStreamConnectionInput struct {
	WorkspaceID  int64 `json:"workspaceId"`
	ConnectionID int64 `json:"connectionId"`
}

// CloseStreamConnectionOutput is close_stream_connection's declared output
// schema. Closed is always true: lb.call already turns every non-2xx (404
// not found, 503 no registry) into the tool's error.
type CloseStreamConnectionOutput struct {
	ConnectionID int64 `json:"connectionId"`
	Closed       bool  `json:"closed"`
}

func handleCloseStreamConnection(lb *loopback) sdk.ToolHandlerFor[CloseStreamConnectionInput, CloseStreamConnectionOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in CloseStreamConnectionInput) (*sdk.CallToolResult, CloseStreamConnectionOutput, error) {
		method, path := toolPath("close_stream_connection", "DELETE /api/workspaces/{id}/connections/{cid}", in.WorkspaceID, in.ConnectionID)
		if err := lb.call(ctx, method, path, nil, nil); err != nil {
			return nil, CloseStreamConnectionOutput{}, err
		}
		return nil, CloseStreamConnectionOutput{ConnectionID: in.ConnectionID, Closed: true}, nil
	}
}

// ---- push_stream_frame ----

// PushStreamFrameInput is push_stream_frame's input: the connection and
// the frame in a timeline frame's own event/data shape.
type PushStreamFrameInput struct {
	WorkspaceID  int64  `json:"workspaceId"`
	ConnectionID int64  `json:"connectionId"`
	Event        string `json:"event,omitempty"`
	// Data is any JSON value — the frame's payload.
	Data any `json:"data"`
}

// PushStreamFrameOutput is push_stream_frame's declared output schema,
// projected from pushFrameView (internal/admin).
type PushStreamFrameOutput struct {
	ConnectionID int64 `json:"connectionId"`
	FrameID      int64 `json:"frameId"`
}

func handlePushStreamFrame(lb *loopback) sdk.ToolHandlerFor[PushStreamFrameInput, PushStreamFrameOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in PushStreamFrameInput) (*sdk.CallToolResult, PushStreamFrameOutput, error) {
		body, err := jsonx.Marshal(struct {
			Event string `json:"event,omitempty"`
			Data  any    `json:"data"`
		}{Event: in.Event, Data: in.Data})
		if err != nil {
			return nil, PushStreamFrameOutput{}, fmt.Errorf("mcp: encode push_stream_frame request: %w", err)
		}
		var out PushStreamFrameOutput
		method, path := toolPath("push_stream_frame", "POST /api/workspaces/{id}/connections/{cid}/frames", in.WorkspaceID, in.ConnectionID)
		// lb.do rather than lb.call: toolErr drops the envelope's message
		// on every 5xx as an internal detail, and 504 push_timeout's
		// message ("it STAYS queued and may still be written") is the one
		// thing the caller must read before deciding to resend.
		status, respBody, err := lb.do(ctx, method, path, body)
		if err != nil {
			return nil, PushStreamFrameOutput{}, err
		}
		if status == http.StatusGatewayTimeout {
			var env httpx.ErrorBody
			if jsonx.Unmarshal(respBody, &env) == nil && env.Error.Message != "" {
				return nil, PushStreamFrameOutput{}, fmt.Errorf("admin API returned 504 %s: %s", env.Error.Code, env.Error.Message)
			}
		}
		if status < 200 || status >= 300 {
			return nil, PushStreamFrameOutput{}, toolErr(status, respBody)
		}
		if err := jsonx.Unmarshal(respBody, &out); err != nil {
			return nil, PushStreamFrameOutput{}, fmt.Errorf("mcp: decode push_stream_frame response: %w", err)
		}
		return nil, out, nil
	}
}
