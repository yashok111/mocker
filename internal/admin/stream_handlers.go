// Stream handlers implement P6a's two routes (decisions.md mocker-p6a-sse,
// DESIGN §30.10): GET /api/workspaces/{id}/traffic/stream, the admin traffic
// feed over Server-Sent Events, and GET /api/stream/stats, the process-wide
// streaming health an agent reads through get_stream_stats.
//
// The split with internal/stream is D4's: that package owns the registry,
// the SSE wire, the write-deadline exemption, the per-frame deadline, the
// ping ticker and the refusal path, and knows nothing about sessions,
// workspaces or repositories. THIS file resolves the session and the
// workspace, reads traffic.Repo.Since, builds the same trafficPollView the
// poll route already answers with, and hands frames to the package through
// two callbacks. The registry arrives via [Server.SetStream], the same setter
// shape SetTraffic already has and for the same reason (New's signature is
// shared with cmd/mocker/main.go).
//
// The transport is SSE and not WebSocket on purpose (D2): the CSRF guard
// runs only on state-changing methods, a WebSocket handshake is a GET that
// CORS does not cover, and §15 already records that SameSite=Lax does not
// protect against a neighbouring-subdomain page — a WebSocket feed would be
// readable, session-authenticated, by any page in the contour. EventSource
// is an ordinary GET and this plane emits no cross-origin allowance, so the
// browser blocks the cross-origin read; A12 asserts that no Access-Control
// header ever appears on this route's response.
package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/workspaces"
)

// StreamOptions is D12's three variables, already converted from the
// integer seconds internal/config reads to durations — cmd/mocker/main.go
// does that conversion at the package boundary, so neither this package nor
// internal/stream ever reads the environment.
type StreamOptions struct {
	Ping           time.Duration // MOCKER_STREAM_PING
	FrameTimeout   time.Duration // MOCKER_STREAM_FRAME_TIMEOUT
	SessionRecheck time.Duration // MOCKER_STREAM_SESSION_RECHECK
}

// SetStream wires the process's one [stream.Registry] and the timings its
// connections run with. Like SetLiveState and SetTraffic this is a setter
// rather than a New parameter (New's signature is shared with
// cmd/mocker/main.go), and like them it is the seam a green test suite has
// twice hidden a dead feature behind: a Server whose SetStream is never
// called still registers both routes and answers 503 service_unavailable on
// each — the same "no dependency wired" code the session routes already use
// — rather than 404, so the contract test still sees the route and an
// operator sees WHY nothing streams.
//
// mock is the MOCK plane's own registry (P6b D9/D10, a separate,
// per-workspace-capped instance) and is held here only to be REPORTED by
// GET /api/stream/stats; nil (a test harness with no mock plane) leaves the
// `mock` object out of the stats document.
func (s *Server) SetStream(reg, mock *stream.Registry, opts StreamOptions) {
	s.streamReg = reg
	s.mockStreams = mock
	s.streamOpts = opts
}

// streamStatsView is GET /api/stream/stats's wire shape since P6b (D10):
// the admin feed's six fields exactly as P6a defined them, plus `mock` —
// the mock plane's registry, whose `cap` is PER WORKSPACE and whose
// `open`/`byWorkspace` count SSE mock connections.
type streamStatsView struct {
	stream.Stats
	Mock *stream.Stats `json:"mock,omitempty"`
}

// codeStreamingUnsupported is D9's refusal: the response writer this request
// arrived on cannot take a write deadline or flush, so the handler answers
// 501 with this code BEFORE a single frame is written, never a buffered
// fallthrough that looks like a stream and is not one. Distinct from D8's
// 503 by design — two refusals sharing one status would both pass a check
// that the other feature had been deleted.
const codeStreamingUnsupported = "streaming_unsupported"

// errWorkspaceReplaced is the recheck's own refusal (D11): the workspace row
// the handshake resolved is gone, or a different row now carries the same
// id. workspaces.id is INTEGER PRIMARY KEY and therefore reusable, so an
// id-only recheck would keep serving a DELETED workspace's connection with
// whatever workspace a later POST /api/workspaces was given that id — the
// (created_at, slug) pair is the identity internal/checkpoints and
// internal/resources already fence on, and it is the one this recheck uses.
var errWorkspaceReplaced = errors.New("workspace deleted or replaced since the handshake")

// handleStreamTraffic answers GET /api/workspaces/{id}/traffic/stream. Every
// refusal here is written BEFORE the SSE handshake and carries the standard
// error envelope: 401 (no session), 404 (no such workspace), 503 (no
// registry wired; D8's cap; the registry closing for shutdown) and — the one
// refusal decided inside internal/stream — 501 streaming_unsupported (D9).
// Once the handshake has gone out the connection lives until the client
// leaves, the session or the workspace fails its recheck (D11), the D10
// lifetime expires, or a frame write misses its deadline (D12); none of
// those is an error a client can be told about, so nothing is written for
// them here beyond a log line for the recheck case.
func (s *Server) handleStreamTraffic(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	sess, ok := sessionFrom(r.Context())
	if !ok {
		// requireUser passing without a session in context cannot happen
		// through attachSession; guarded anyway, because D11's recheck
		// needs the session ID and a nil here would be a panic on a route
		// that holds a connection open.
		httpx.Err(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	if s.streamReg == nil {
		httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable, "no stream registry is wired in this deployment; use GET .../traffic/poll")
		return
	}

	conn, err := s.streamReg.Open(r.Context(), ws.ID)
	if err != nil {
		switch {
		case errors.Is(err, stream.ErrCapExceeded):
			httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable, "the process-wide stream connection cap is reached; retry later or use GET .../traffic/poll")
		case errors.Is(err, stream.ErrClosed):
			httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable, "the server is shutting down")
		default:
			s.log.Error("stream traffic: open", "workspace", ws.Slug, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to open the stream")
		}
		return
	}

	identity := s.streamWorkspaceIdentityFunc(ws)
	err = conn.Serve(w, stream.ServeConfig{
		Since:          streamCursor(r),
		Read:           s.streamReadFunc(ws.ID, identity),
		Recheck:        s.streamRecheckFunc(sess.ID, identity),
		Ping:           s.streamOpts.Ping,
		FrameTimeout:   s.streamOpts.FrameTimeout,
		SessionRecheck: s.streamOpts.SessionRecheck,
	})
	switch {
	case err == nil, errors.Is(err, stream.ErrPeerGone):
		// The ordinary ends: client gone, lifetime expired, shutdown, a
		// stalled peer cut by the frame deadline. Nothing to write, nothing
		// worth a log line at error level on a plane one browser tab
		// reconnects to every fifteen minutes.
	case errors.Is(err, stream.ErrUnsupported):
		// D9: nothing has been written yet, so the envelope still goes out
		// as an ordinary JSON response.
		httpx.Err(w, http.StatusNotImplemented, codeStreamingUnsupported, "this response writer cannot stream (no write deadline or flush); use GET .../traffic/poll")
	default:
		s.log.Info("stream traffic: connection closed by recheck", "workspace", ws.Slug, "reason", err)
	}
}

// streamCursor is D7: Last-Event-ID wins whenever it is present and parses
// as a positive integer (the browser's own reconnect replays the URL and
// adds the header, so honouring only ?since= would re-deliver or skip),
// ?since= otherwise (the first open, and curl, have no header to send), and
// neither means zero — treated exactly as the poll route treats since=0.
func streamCursor(r *http.Request) int64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return parseTrafficSince(r)
}

// streamReadFunc builds D5's read half for one workspace: one Repo.Since of
// at most stream.MaxFrameRows rows (200 — the number the screen already
// asks the poll for, never the poll route's own default or ceiling, which D3
// leaves untouched), marshalled as the SAME trafficPollView the poll route
// answers with (D6: one event name, the poll view as its payload, `dropped`
// reused rather than a second indicator). lastId echoes since when nothing
// is new, exactly as handlePollTraffic does — a zero would make a client
// resuming on Last-Event-ID replay the whole table.
//
// Before every read it re-checks the WORKSPACE's identity the same way the
// timer does (streamRecheckFunc) — one primary-key read on the reader pool
// — and refuses the connection (stream.ErrRefused) on a mismatch. The timer
// alone is not enough for the reissued-id case D11 describes: a workspace
// deleted and its id reissued INSIDE one recheck interval gets a nudge for
// the new workspace's first batch, and a read that trusted the handshake
// would serve that batch to a client that asked for the deleted one before
// the next tick ever fired. The package's own test for exactly that
// sequence delivered the impostor's row on the first draft of this
// function; the per-read check is what closed it. The timer still owns the
// SESSION half and the idle case, where no nudge arrives to trigger this.
func (s *Server) streamReadFunc(workspaceID int64, identity stream.RecheckFunc) stream.ReadFunc {
	return func(ctx context.Context, since int64) ([]byte, int64, int, error) {
		if err := identity(ctx); err != nil {
			return nil, 0, 0, fmt.Errorf("%w: %w", stream.ErrRefused, err)
		}
		rows, err := s.trafficRepo.Since(ctx, workspaceID, since, stream.MaxFrameRows)
		if err != nil {
			// internal/stream has no logger by design; this is where D5's
			// "the error is logged and the connection waits for the next
			// nudge" is logged.
			s.log.Error("stream traffic: read since", "workspace_id", workspaceID, "since", since, "err", err)
			return nil, 0, 0, err
		}
		// And AGAIN after the read: the two are separate statements on the
		// reader pool, not one snapshot, so a delete-and-recreate landing
		// between them would have the first check pass and the read return
		// the replacement's rows. A row set that passes both checks was
		// read while the handshake's workspace existed on both sides of
		// it, which is the most two reads can promise; the second read is
		// one primary-key lookup per nudge. (Second-reader finding, triaged
		// as real.)
		if err := identity(ctx); err != nil {
			return nil, 0, 0, fmt.Errorf("%w: %w", stream.ErrRefused, err)
		}
		lastID := since
		if n := len(rows); n > 0 {
			lastID = rows[n-1].ID
		}
		data, err := jsonx.Marshal(trafficPollView{Rows: rows, LastID: lastID, Dropped: s.trafficDropped()})
		if err != nil {
			s.log.Error("stream traffic: marshal frame", "workspace_id", workspaceID, "err", err)
			return nil, 0, 0, err
		}
		return data, lastID, len(rows), nil
	}
}

// streamRecheckFunc is D11 in full: on every MOCKER_STREAM_SESSION_RECHECK
// tick the session is looked up in the store the way the SPA's own route
// guard does — never a value cached at the handshake — and the WORKSPACE is
// re-read and compared by (created_at, slug), not by id (see
// errWorkspaceReplaced). Either failure closes the connection; the error is
// what the handler logs.
//
// The pair is an identity only to within a second (created_at is stored as
// Unix seconds), so a workspace deleted and recreated with the SAME slug in
// the same second still matches. That residual is accepted rather than
// hidden — it is the identical one internal/checkpoints has fenced on since
// P2c, one second wide, needing the same slug and an already-authenticated
// operator on a plane where every workspace is that operator's own.
func (s *Server) streamRecheckFunc(sessionID string, identity stream.RecheckFunc) stream.RecheckFunc {
	return func(ctx context.Context) error {
		if _, _, err := s.sessions.Lookup(ctx, sessionID); err != nil {
			return fmt.Errorf("session recheck: %w", err)
		}
		return identity(ctx)
	}
}

// streamWorkspaceIdentityFunc captures the (created_at, slug) pair the
// handshake resolved and returns the check both the timer (via
// streamRecheckFunc) and every read (via streamReadFunc) run: the row must
// still exist and still carry that pair, or the connection is refused with
// errWorkspaceReplaced. One function, two callers, so the two cannot
// disagree about what "the same workspace" means.
func (s *Server) streamWorkspaceIdentityFunc(ws *workspaces.Workspace) stream.RecheckFunc {
	id, createdAt, slug := ws.ID, ws.CreatedAt, ws.Slug
	return func(ctx context.Context) error {
		cur, err := s.ws.ByID(ctx, id)
		if err != nil {
			return fmt.Errorf("workspace recheck: %w", err)
		}
		if !cur.CreatedAt.Equal(createdAt) || cur.Slug != slug {
			return errWorkspaceReplaced
		}
		return nil
	}
}

// handleStreamStats answers GET /api/stream/stats (D15): the registry's own
// counters, process-wide because D8's cap is — open connections, the cap,
// the two refusal counters kept apart because D8 and D9 are different
// failures with different repairs, D5's coalesced-nudge total, and one row
// per workspace that currently holds at least one live connection. No
// screen calls it (D16); get_stream_stats is its only caller and
// web/src/api/coverage.test.ts's EXEMPT entry says so.
func (s *Server) handleStreamStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if s.streamReg == nil {
		httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable, "no stream registry is wired in this deployment")
		return
	}
	view := streamStatsView{Stats: s.streamReg.Stats()}
	if s.mockStreams != nil {
		m := s.mockStreams.Stats()
		view.Mock = &m
	}
	httpx.JSON(w, http.StatusOK, view)
}
