// Loopback implements the seam §A6/§B2 of the MCP slice's context document
// calls the single highest-risk object in the slice: an in-process admin-API
// call that authenticates a request WITHOUT a session. internal/mcp's tools
// reach this API through exactly this file and nothing else — never
// around it into overrides.Repo, workspaces.Repo or any other repository
// directly — so every tool goes through the SAME handlers, validation and
// error envelopes the admin UI does.
//
// Only the MCP endpoint (internal/mcp, via the Caller interface it defines
// over CallAsMCP) may call CallAsMCP. It bypasses every middleware layer
// Handler() adds — attachSession, enforceCSRF, rateLimitLogin — by
// construction: it dispatches straight to the route mux, so the caller must
// already have made the authorization decision before reaching here. The
// MCP endpoint makes that decision with its own bearer-key check
// (internal/mcp), exactly once, before ever calling CallAsMCP.
package admin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"

	"github.com/yashok111/mocker/internal/auth"
)

// mcpIdentityName and mcpIdentityRole are the fixed users.name/role
// [Server.mcpIdentity] resolves via [auth.Manager.EnsureUser] for every MCP
// call. The MCP endpoint has no logged-in person behind it — its identity is
// a key in the environment — so every tool call this process ever makes is
// attributed to this one well-known row. See [auth.Manager.EnsureUser]'s own
// doc comment for why a real row and not a synthetic zero id.
const (
	mcpIdentityName = "mcp"
	mcpIdentityRole = "member"
)

// mcpAllowedRoutes is the ALLOWLIST OF ROUTE TEMPLATES [CallAsMCP] may
// dispatch to (§B2 rule 8 of the MCP slice's context document) — the
// fifty-six admin routes the MCP tools compose between them. It held
// twelve until slice A2 of the mocker-a-mcp gate document (D5, D6, D7) grew
// it to forty; slice P3b of the mocker-p3b-resources gate document (D7)
// grew it again, forty to forty-four, for the four resource tools that
// slice's own internal/mcp/tools_resources.go adds; P3f's rederive and
// P4a's drift added one route each, forty-four to forty-six; A4
// (decisions.md mocker-a4-mcp-reach D9) grew it once more, forty-six to
// forty-eight, for the pre-existing probe route (D5, newly allowed rather
// than newly created) and the one new entities route (D4); P6a
// (decisions.md mocker-p6a-sse D16) grew it to forty-nine for GET
// /api/stream/stats, wrapped by get_stream_stats — the stream route itself
// is NOT here: an in-process loopback response cannot take a write deadline
// (D9's exact refusal), and a stream is not a call an MCP tool returns
// from.
//
// What may enter it is not a judgement made at a call site. That document
// enumerates both the tools and the routes each one wraps, and — in D12 —
// the routes deliberately kept OUT, each with its reason: POST
// /api/auth/login (allowlisting it would hand an unthrottled credential
// oracle to anything holding the bearer key), POST /api/auth/logout and GET
// /api/me (this endpoint has no session and a fixed identity), POST
// /api/specs and DELETE /api/specs/{id} (mocker-a4-mcp-reach D3: the
// screen that imports a spec already works and stays the only way in), and
// the two infrastructure probes GET /healthz and GET /readyz. POST
// .../probe was on this list of exclusions before A4 (mocker-a-mcp D12)
// and is not any more (mocker-a4-mcp-reach D5): a probe destroys nothing
// and reaches exactly the hosts an operator's own «Проверить» click can
// reach, so the reason the original six D12 exclusions exist does not
// apply to it (A6 brought it to fifty-six). A tool that needs a fifty-seventh route is that document's
// decision to make, not something this list grows to accommodate on its
// own — CallAsMCP bypasses attachSession, enforceCSRF and rateLimitLogin
// by construction, so this list is the whole of what stands between a
// bearer key and the admin plane.
//
// [MCPAllowedRoutes] hands a copy of it to internal/mcp's own test, which
// asserts in BOTH directions that this list and that package's
// tool-to-route table describe the same set: a template here with no tool
// behind it is reach nobody asked for, and a tool route missing from here
// is a 404 that shows up only at run time.
var mcpAllowedRoutes = []string{
	"GET /api/workspaces",
	"POST /api/workspaces",
	"GET /api/workspaces/{id}",
	"PATCH /api/workspaces/{id}",
	"DELETE /api/workspaces/{id}",
	// P4b: export_workspace, import_workspace, fork_workspace.
	"GET /api/workspaces/{id}/export",
	// P7a: export_openapi.
	"GET /api/workspaces/{id}/openapi.json",
	"POST /api/workspaces/import",
	"POST /api/workspaces/{id}/fork",

	"GET /api/specs",
	// A8 (2026-09-02): POST /api/specs joins the list — mocker-a4-mcp-reach
	// D3 kept it out because the /specs screen "already works and stays the
	// only way in"; the owner reversed that so an agent holding the spec
	// file in its own repository needs no human to paste it. DELETE
	// /api/specs/{id} stays out: it cascades across every bound workspace.
	"POST /api/specs",
	"GET /api/specs/{id}",
	"GET /api/specs/{id}/report",
	"GET /api/specs/{id}/operations",

	"GET /api/workspaces/{id}/operations",
	"GET /api/workspaces/{id}/operations/{opKey}",
	"PUT /api/workspaces/{id}/operations/{opKey}",
	"DELETE /api/workspaces/{id}/operations/{opKey}",
	"GET /api/workspaces/{id}/auth-preset",
	"POST /api/workspaces/{id}/auth-preset",

	"POST /api/workspaces/{id}/preview",

	"GET /api/workspaces/{id}/session",
	"POST /api/workspaces/{id}/session",
	"DELETE /api/workspaces/{id}/session",

	"GET /api/workspaces/{id}/traffic",
	"GET /api/workspaces/{id}/traffic/poll",
	"DELETE /api/workspaces/{id}/traffic",

	"GET /api/workspaces/{id}/endpoints",
	"POST /api/workspaces/{id}/endpoints",
	"PUT /api/workspaces/{id}/endpoints/{eid}",
	"DELETE /api/workspaces/{id}/endpoints/{eid}",

	"POST /api/workspaces/{id}/traffic/{tid}/to-override",
	"POST /api/workspaces/{id}/traffic/{tid}/to-endpoint",

	"GET /api/workspaces/{id}/scenarios",
	"POST /api/workspaces/{id}/scenarios",
	"GET /api/workspaces/{id}/scenarios/{sid}",
	"PUT /api/workspaces/{id}/scenarios/{sid}",
	"DELETE /api/workspaces/{id}/scenarios/{sid}",
	"POST /api/workspaces/{id}/scenarios/{sid}/activate",
	"POST /api/workspaces/{id}/scenarios/deactivate",

	"GET /api/workspaces/{id}/checkpoints",
	"POST /api/workspaces/{id}/checkpoints",
	"DELETE /api/workspaces/{id}/checkpoints/{cid}",
	"POST /api/workspaces/{id}/rollback/{cid}",
	"POST /api/workspaces/{id}/reset-overrides",

	// P3b (mocker-p3b-resources D7): the three resource routes P3a shipped
	// without an MCP tool (list_resource_suggestions, list_resources,
	// decide_resource) plus the one route P3b itself adds
	// (reset_resource_data) — forty-one through forty-four.
	"GET /api/specs/{id}/resource-suggestions",
	"GET /api/workspaces/{id}/resources",
	"POST /api/workspaces/{id}/resource-decisions",
	"POST /api/workspaces/{id}/reset-data",

	// P3f (decisions.md mocker-p3f-rederive, D8.3): the new spec-scoped
	// rederive verb, forty-fifth — rederive_suggestions.
	"POST /api/specs/{id}/rederive",

	// A6 (decisions.md mocker-a6-assets D8): the three asset routes —
	// upload_asset, list_assets, delete_asset.
	"PUT /api/workspaces/{id}/assets/{name}",
	"GET /api/workspaces/{id}/assets",
	"DELETE /api/workspaces/{id}/assets/{name}",

	// P4a (decisions.md mocker-p4a-triage, D7): the one route this slice
	// adds, wrapped by get_workspace_drift.
	"GET /api/workspaces/{id}/drift",

	// A4 (decisions.md mocker-a4-mcp-reach, D9): forty-seven and
	// forty-eight. POST .../probe already existed (probe_handlers.go) and
	// is newly allowed here (D5) — probe_workspace is its tool. GET
	// .../resources/{family}/entities is the one route this slice adds
	// (D4) — list_resource_entities is its tool.
	"POST /api/workspaces/{id}/probe",
	"GET /api/workspaces/{id}/resources/{family}/entities",
	// A11: the read's two write siblings — set_resource_entity and
	// delete_resource_entity are their tools.
	"PUT /api/workspaces/{id}/resources/{family}/entities/{key}",
	"DELETE /api/workspaces/{id}/resources/{family}/entities/{key}",

	// P6a (decisions.md mocker-p6a-sse D15, D16): forty-ninth — the
	// process-wide streaming health, wrapped by get_stream_stats.
	"GET /api/stream/stats",

	// P6b (decisions.md mocker-p6b-sse-mock D13): fiftieth — a stream
	// draft's first frames, wrapped by preview_endpoint. Writes nothing.
	"POST /api/workspaces/{id}/endpoints/preview",

	// P6c (decisions.md mocker-p6c-live-conns D1, D9): fifty-first to
	// fifty-third — the live-connection surface, wrapped by
	// list_stream_connections, close_stream_connection and
	// push_stream_frame. None writes a row; the push waits on a live
	// connection's loop, which the loopback's in-process call can do
	// because the STREAM is on the mock plane's own socket, not on this
	// response.
	"GET /api/workspaces/{id}/connections",
	"DELETE /api/workspaces/{id}/connections/{cid}",
	"POST /api/workspaces/{id}/connections/{cid}/frames",
}

// MCPAllowedRoutes returns the route-template allowlist [CallAsMCP] enforces.
//
// It exists for exactly one caller: internal/mcp's test that its
// tool-to-route table and this list agree in both directions. Without an
// accessor that test would have to hand-copy forty-four templates, and a second
// copy of a security allowlist is free to drift from the first — at which
// point the test passes over a statement of the seam nobody enforces.
//
// The returned slice is a COPY. Handing out the package var's own backing
// array would let any caller rewrite the allowlist in place, which is the
// one mutation this file exists to make impossible.
func MCPAllowedRoutes() []string {
	return slices.Clone(mcpAllowedRoutes)
}

// mcpAllowlistMux matches a request against mcpAllowedRoutes using the SAME
// segment-decoding semantics as the real route mux ([Server.routeMux]): a
// second, throwaway http.ServeMux registering a no-op handler per template.
// Built once and shared for the whole process via sync.OnceValue — the
// template list above is static and carries no per-Server state, so there is
// nothing here a second Server instance would ever need to see differently.
//
// This exists because a prefix check on the raw path STRING is bypassable,
// and was a blocker against an earlier revision of this slice: Go's
// http.ServeMux decodes segments before matching, so
// "POST /api/%61uth/login" does not start with "/api/auth/" as a string —
// yet reaches the login handler, and with it the login rate limiter, a
// middleware CallAsMCP already bypasses by construction. Using the mux
// itself to answer "does this match" means a decoded bypass simply does not
// match any of the registered templates — measured against this
// exact mechanism: POST /api/%61uth/login → no match, GET /api/workspaces
// against a registered POST /api/workspaces → no match (method mismatch),
// /healthz → no match, /mcp → no match.
var mcpAllowlistMux = sync.OnceValue(func() *http.ServeMux {
	mux := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, pattern := range mcpAllowedRoutes {
		mux.HandleFunc(pattern, noop)
	}
	return mux
})

// mcpPathAllowed reports whether req matches one of the templates in
// mcpAllowedRoutes.
//
// It is asked with the EXACT *http.Request CallAsMCP is about to dispatch
// for real, not a second one built just to ask: http.ServeMux.Handler only
// READS req.Method/req.URL/req.Host to compute a match and does not mutate
// req, so asking the allowlist mux first and then handing the same request
// to the real dispatch mux afterwards is safe — verified with a standalone
// probe against this Go version, not assumed. A query string on req.URL is
// ignored by mux matching, exactly like the real dispatch; and a "%2F"
// inside an opKey segment stays inside that one segment here exactly as it
// does on the real mux, because both ask the identical question of the
// identical *url.URL.
func mcpPathAllowed(req *http.Request) bool {
	_, pattern := mcpAllowlistMux().Handler(req)
	return pattern != ""
}

// buildLoopbackRequest constructs the outbound *http.Request [CallAsMCP]
// dispatches: everything §B2 rules 1-4 of the MCP slice's context document
// require, and nothing else. Factored out of CallAsMCP so each rule is a
// direct, database-free unit test instead of a full round trip through the
// route mux and a real users row.
func buildLoopbackRequest(ctx context.Context, src *http.Request, method, path string, body []byte, contentType string) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	// §B2 rule 1: from the ESCAPED PATH STRING, via
	// http.NewRequestWithContext — never a url.URL literal. overrides.OpKey
	// (internal/overrides/opkey.go) percent-encodes "METHOD /path" with
	// url.PathEscape so it fits in one path segment, and this plane's own
	// opKeyFromPath (override_handlers.go) reverses that by re-escaping
	// r.PathValue("opKey") before handing it to overrides.ParseOpKey. Both
	// directions depend on the request's EscapedPath() carrying the
	// ORIGINAL encoding through unchanged. Assigning path into a
	// &url.URL{Path: path} literal instead would treat it as an
	// ALREADY-DECODED path and re-escape it when the request is written —
	// double-encoding every '%' in the process — so a "%2F" inside an opKey
	// would arrive as a literal three-character "%2F" instead of surviving
	// as the path separator ServeMux is meant to see it as. Building from
	// the string, as done here, is what makes the "%2F"-in-an-opKey test in
	// loopback_test.go pass.
	req, err := http.NewRequestWithContext(ctx, method, path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("admin: mcp loopback: build request: %w", err)
	}
	// net/http's SERVER contract says "Body is always non-nil" — a handler
	// may read it without a nil check, and A13's DELETE .../session does
	// (io.ReadAll over http.MaxBytesReader). http.NewRequestWithContext is
	// the CLIENT constructor and leaves Body nil for a nil reader, so a
	// bodiless loopback call reached that handler with a nil Body and the
	// process died on the first `set_session_directive {clear: true}`
	// (`make smoke`, MCP observation 7, 2026-09-03). http.NoBody is what a
	// real server hands a handler for an empty body.
	if req.Body == nil {
		req.Body = http.NoBody
	}

	// §B2 rule 2: carried over from src. httpx.ForwardedProto
	// (internal/admin/workspace_handlers.go's workspaceURL, called from
	// every route that returns a workspaceView) trusts a forwarded scheme
	// only when r.TLS != nil OR the peer resolved from r.RemoteAddr sits
	// inside a trusted CIDR — leaving either one zero silently turns every
	// workspace URL an MCP tool sees into http:// behind a
	// TLS-terminating proxy.
	req.Host = src.Host
	req.RemoteAddr = src.RemoteAddr
	req.TLS = src.TLS
	if proto := src.Header.Get("X-Forwarded-Proto"); proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}

	// §B2 rule 3: Cookie and Authorization are deliberately NOT copied. req
	// started from http.NewRequestWithContext with an empty header set —
	// nothing above (or below) copies src.Header wholesale — so "not
	// carried over" is simply everything this function does not explicitly
	// set. There is no separate strip step because there is nothing here to
	// strip in the first place.

	// §B2 rule 4 — and A6: the one caller with a body that is not JSON
	// (upload_asset, through CallAsMCPRaw) names its own type; "" is the
	// JSON default every other tool has always had.
	if body != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}

	return req, nil
}

// captureResponse is CallAsMCP's own http.ResponseWriter (§B2 rule 6: do NOT
// use net/http/httptest in production code). It is httptest.ResponseRecorder
// written by hand, sized for exactly what CallAsMCP needs — a status and a
// body — and nothing a real network listener would: no Flush, no Hijack, no
// streaming.
type captureResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

// newCaptureResponse returns a captureResponse ready to hand to
// http.Handler.ServeHTTP. status starts at 200 to match net/http's own
// convention: a handler that calls Write without ever calling WriteHeader
// gets an implicit 200, and this writer must agree with that.
func newCaptureResponse() *captureResponse {
	return &captureResponse{header: make(http.Header), status: http.StatusOK}
}

func (c *captureResponse) Header() http.Header { return c.header }

// WriteHeader records status once, matching net/http's own behavior of
// ignoring repeat calls (see httpx.StatusRecorder for the same rule on the
// real response path).
func (c *captureResponse) WriteHeader(status int) {
	if c.wrote {
		return
	}
	c.wrote = true
	c.status = status
}

func (c *captureResponse) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.body.Write(b)
}

// mcpIdentity resolves — and caches — the users row every MCP-driven admin
// call is attributed to.
//
// §B2 rule 5: cache only a SUCCESSFUL resolution — a plain mutex plus a nil
// check, not sync.Once with a stored error. A cancelled first request, or
// one transient database error on the very first MCP call this process ever
// serves, must not wedge the MCP endpoint dead for the rest of the
// process's life; the next call simply tries EnsureUser again.
func (s *Server) mcpIdentity(ctx context.Context) (*auth.User, error) {
	s.mcpUserMu.Lock()
	defer s.mcpUserMu.Unlock()
	if s.mcpUser != nil {
		return s.mcpUser, nil
	}
	user, err := s.sessions.EnsureUser(ctx, mcpIdentityName, mcpIdentityRole)
	if err != nil {
		return nil, err
	}
	s.mcpUser = user
	return user, nil
}

// CallAsMCP dispatches an in-process admin-API request as the MCP identity
// and returns the status and body the admin plane produced.
//
// It is the ONLY way internal/mcp reaches this API, and it exists so the
// tools go through exactly the handlers, validation and error envelopes the
// UI does — never around them into the repositories.
//
// src is the INBOUND /mcp request. Its Host, RemoteAddr, TLS state and
// forwarded-proto header are carried over because admin responses embed
// workspace URLs built from them (see newWorkspaceView); its cookies,
// Authorization header and body are NOT.
//
// Every middleware layer Handler() adds is bypassed by construction — this
// dispatches straight to the route mux — so the authorization decision has
// already been made by the caller. Nothing but the MCP endpoint may call it.
func (s *Server) CallAsMCP(ctx context.Context, src *http.Request, method, path string, body []byte) (status int, respBody []byte, err error) {
	return s.CallAsMCPRaw(ctx, src, method, path, "", body)
}

// CallAsMCPRaw is CallAsMCP with the body's content type named by the
// caller (A6, decisions.md mocker-a6-assets D8): upload_asset PUTs a file
// whose type is the file's own. A second method rather than a widened
// mcp.Caller, because that interface is the seam all fifty-one earlier
// tools and every test double dispatch through; internal/mcp asserts this
// method through its own one-method interface only where it needs it. ""
// means application/json, exactly as CallAsMCP has always sent.
func (s *Server) CallAsMCPRaw(ctx context.Context, src *http.Request, method, path, contentType string, body []byte) (status int, respBody []byte, err error) {
	// err is non-nil ONLY for a failure to MAKE the call (§B2 rule 7) — in
	// exactly this order: identity resolution, request construction, then
	// the allowlist. A 4xx/5xx a real handler answers below is a normal
	// return with that status; the caller (internal/mcp) turns it into a
	// tool error, never a Go error.
	user, err := s.mcpIdentity(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("admin: mcp loopback: identity: %w", err)
	}

	req, err := buildLoopbackRequest(ctx, src, method, path, body, contentType)
	if err != nil {
		return 0, nil, err
	}

	if !mcpPathAllowed(req) {
		return 0, nil, fmt.Errorf("admin: mcp loopback: %s %s is not an allowed route", method, path)
	}

	// withAuthContext/ctxKey (security.go) are unexported but in-package:
	// this file lives in package admin specifically so it may attach the
	// resolved identity the same way attachSession does for a real session,
	// without exporting a second, wider hook that any other package could
	// then use to forge an identity of its own. sess is nil — enforceCSRF is
	// the only reader of the session half of authContext, and this call
	// path never reaches enforceCSRF.
	//
	// withAuthContext(ctx, ...) — the function's OWN ctx parameter, not
	// req.Context() — even though the two hold the same value at this point
	// (buildLoopbackRequest built req from ctx and nothing since has
	// changed it): contextcheck's static analysis only recognizes context
	// inheritance through the literal parameter, and req.Context() reads as
	// a fresh, non-inherited context to it.
	req = req.WithContext(withAuthContext(ctx, nil, user))

	rw := newCaptureResponse()
	// §B2 rule 9: routeMux(), not a freshly built mux — see its own comment
	// for why CallAsMCP and Handler() must share exactly one.
	s.routeMux().ServeHTTP(rw, req)
	return rw.status, rw.body.Bytes(), nil
}
