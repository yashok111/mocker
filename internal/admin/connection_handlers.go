// connection_handlers.go is P6c's live-connection surface (decisions.md
// mocker-p6c-live-conns D1, D2, D4, D5, D6, D8; DESIGN §30.15, §30.16): the
// three routes under /api/workspaces/{id}/connections — list the MOCK
// plane's live SSE connections of one workspace, close one, push one frame
// into one. None of them writes a row, bumps revision or takes an auto
// checkpoint: a connection is RAM the registry holds, and so is a pushed
// frame (D3). Agent-only from P6c to P6e (2026-09-02), when the «Соединения»
// screen (StreamConnectionsPage.tsx) took all three beside
// list_stream_connections, close_stream_connection and push_stream_frame
// (internal/mcp); the EXEMPT entries were withdrawn then.
//
// The registry these read is s.mockStreams — the mock plane's own,
// per-workspace-capped instance P6b wired for GET /api/stream/stats to
// REPORT; this slice is the first to address it. The admin feed's own
// connections (s.streamReg) are the operator's and stay out of every one
// of these (D1).
package admin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/stream"
)

// Error codes of this surface. connection_not_found covers every reason
// Lookup answers nil — never issued, already closed, another workspace's —
// because the repair is "list again" in all three (D8).
const (
	codeConnectionNotFound = "connection_not_found"
	codeInboxFull          = "inbox_full"
	codeConnectionClosed   = "connection_closed"
	codePushTimeout        = "push_timeout"
)

// connectionListView is GET .../connections's envelope (D8): the ceiling
// beside who holds it, in one call.
type connectionListView struct {
	Open        int               `json:"open"`
	Cap         int               `json:"cap"`
	Connections []stream.Snapshot `json:"connections"`
}

// pushFrameRequest is POST .../connections/{cid}/frames's body — the shape
// of a timeline frame (customep.Frame), validated by the same rules (D6).
type pushFrameRequest struct {
	Event string           `json:"event,omitempty"`
	Data  jsonx.RawMessage `json:"data"`
}

// pushFrameView is the push's answer once the loop wrote the frame (D4).
type pushFrameView struct {
	ConnectionID int64 `json:"connectionId"`
	FrameID      int64 `json:"frameId"`
}

// mockRegistry resolves the workspace and the mock registry for the three
// handlers, answering the same 503 the handshake gives without one.
func (s *Server) mockRegistry(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if _, ok := s.requireUser(w, r); !ok {
		return 0, false
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return 0, false
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return 0, false
	}
	if s.mockStreams == nil {
		httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable, "no mock stream registry is wired in this deployment")
		return 0, false
	}
	return ws.ID, true
}

// handleListConnections answers GET /api/workspaces/{id}/connections.
func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	wsID, ok := s.mockRegistry(w, r)
	if !ok {
		return
	}
	rows := s.mockStreams.Snapshot(wsID)
	if raw := r.URL.Query().Get("endpointId"); raw != "" {
		eid, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || eid <= 0 {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid endpointId")
			return
		}
		filtered := rows[:0]
		for _, row := range rows {
			if row.EndpointID == eid {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	// open is the WORKSPACE's live count, filter or not — the number the
	// per-workspace cap is compared against, which is what an operator
	// reading this beside cap needs.
	open := len(s.mockStreams.Snapshot(wsID))
	httpx.JSON(w, http.StatusOK, connectionListView{Open: open, Cap: s.mockStreams.Cap(), Connections: rows})
}

// lookupConnection resolves {cid} against the workspace's own connections.
func (s *Server) lookupConnection(w http.ResponseWriter, r *http.Request, wsID int64) *stream.Conn {
	cid, ok := parsePathInt64(w, r, "cid")
	if !ok {
		return nil
	}
	c := s.mockStreams.Lookup(wsID, cid)
	if c == nil {
		httpx.Err(w, http.StatusNotFound, codeConnectionNotFound, "no live connection with this id in this workspace (it was never issued, already closed, or belongs to another workspace) — list again")
		return nil
	}
	return c
}

// handleCloseConnection answers DELETE /api/workspaces/{id}/connections/{cid}
// (D5): a cancel, nothing more. No confirmSlug — a connection is not
// workspace-created data, and the client's own EventSource reconnects.
func (s *Server) handleCloseConnection(w http.ResponseWriter, r *http.Request) {
	wsID, ok := s.mockRegistry(w, r)
	if !ok {
		return
	}
	cid, ok := parsePathInt64(w, r, "cid")
	if !ok {
		return
	}
	// One registry operation, not Lookup then close (diff-review finding
	// 1): a connection that deregistered in between, or lost the race with
	// another close (round-1 finding 4), gets the same 404 a DELETE after
	// deregistration gets.
	if !s.mockStreams.CloseByAdmin(wsID, cid) {
		httpx.Err(w, http.StatusNotFound, codeConnectionNotFound, "no live connection with this id in this workspace (it was never issued, already closed, or belongs to another workspace) — list again")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePushFrame answers POST /api/workspaces/{id}/connections/{cid}/frames
// (D4, D6): validate the frame as the endpoint writer would, queue it, wait
// two frame timeouts for the loop to write it, answer with the ordinal it
// carried.
func (s *Server) handlePushFrame(w http.ResponseWriter, r *http.Request) {
	wsID, ok := s.mockRegistry(w, r)
	if !ok {
		return
	}
	c := s.lookupConnection(w, r, wsID)
	if c == nil {
		return
	}
	var body pushFrameRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if err := customep.ValidatePushFrame(body.Event, body.Data, s.customepRepo.MaxFrameBytes); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	// P6d (decisions.md mocker-p6d-websocket D9): a WebSocket frame carries
	// no event name; an event on a ws connection is refused by name rather
	// than dropped, the "quietly never fires" rule.
	if body.Event != "" && c.Info().Kind == customep.KindWS {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "event: a WebSocket frame carries no event name; omit it on a kind \"ws\" connection")
		return
	}
	// One data: line, whatever indentation the caller sent — the same
	// compaction the loop applies to a stored timeline frame.
	data := compactFrameData(body.Data)

	// Two frame timeouts (D4): one frame ahead in the queue under its own
	// deadline, plus this one's. The request's own context bounds it too.
	ctx, cancel := context.WithTimeout(r.Context(), 2*s.streamOpts.FrameTimeout)
	defer cancel()
	frameID, err := c.Push(ctx, body.Event, data)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, pushFrameView{ConnectionID: c.ID(), FrameID: frameID})
	case errors.Is(err, stream.ErrInboxFull):
		httpx.Err(w, http.StatusConflict, codeInboxFull, "this connection's inbox is full (16 frames queued and not yet written); the frame was NOT queued")
	case errors.Is(err, stream.ErrPushTimeout):
		httpx.Err(w, http.StatusGatewayTimeout, codePushTimeout, "the loop did not write the frame within two frame timeouts; it STAYS queued and may still be written")
	default:
		// ErrConnClosed, or the write's own failure (the peer went away
		// under this very frame): either way the connection is over and
		// the frame did not reach the wire.
		httpx.Err(w, http.StatusConflict, codeConnectionClosed, "the connection ended before the frame was written")
	}
}

// compactFrameData renders the payload on one line; invalid bytes cannot
// reach here (ValidatePushFrame ran), so the fallback is the bytes as sent.
func compactFrameData(raw jsonx.RawMessage) []byte {
	// Compact, never decode-then-marshal: a round trip through `any` turns
	// 9007199254740993 into ...992 and "<b>" into "\u003cb\u003e", and
	// the frame the peer gets would not be the one ValidatePushFrame
	// accepted.
	var buf bytes.Buffer
	if err := jsonx.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}
