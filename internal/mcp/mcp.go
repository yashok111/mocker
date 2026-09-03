// Package mcp implements mocker's MCP (Model Context Protocol) endpoint:
// POST /mcp on the admin host, bearer-authenticated, exposing 44
// task-named tools that drive mocker's existing admin API in-process
// (§A6/§B2 of the MCP slice's context document — internal/admin's
// CallAsMCP). It is an ADAPTER, not a second implementation of the domain:
// every tool composes the same 46 admin routes the UI already calls, so
// api/openapi.json stays untouched (§A4/§A5).
//
// The SDK this package wraps, github.com/modelcontextprotocol/go-sdk, is
// also named "mcp". Every file in this package therefore imports it under
// the fixed alias "sdk" (§B4) — two files disagreeing on the alias would be
// its own review round, and three agents write files here across this
// slice.
package mcp

import (
	"context"
	"log/slog"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/guide"
)

// serverVersion is the MCP Implementation.Version reported to every client
// in initialize's result. mocker's binary carries no build-time version
// anywhere else in this tree to read instead — no ldflags-injected value,
// no VERSION file, no existing "Version" identifier anywhere under cmd or
// internal (checked before choosing this route) — so this is a literal,
// bumped by hand alongside this package, not a value read out of the
// binary.
const serverVersion = "0.1.0"

// methodSubscriptionsListen is the SEP-2575 method refuseSubscriptionsListen
// intercepts. The SDK's own name for it, methodSubscriptionsListen
// (mcp/protocol.go:2350), is unexported, so this is a second copy of the
// same literal rather than an import of it.
const methodSubscriptionsListen = "subscriptions/listen"

// Caller is the one thing this package needs from the admin plane: an
// in-process call that dispatches straight to admin.Server's own route mux
// as the MCP identity, bypassing every cookie/CSRF middleware layer by
// construction because the caller here — this package's own bearer check
// (auth.go) — has already made the authorization decision.
//
// It is an interface, not *admin.Server, so the tool tests in this package
// drive a fake and never have to stand up a database just to test a
// projection over a canned response body.
type Caller interface {
	CallAsMCP(ctx context.Context, src *http.Request, method, path string, body []byte) (status int, respBody []byte, err error)
}

// Endpoint is the built /mcp handler: the one *sdk.Server this process ever
// constructs (with every tool already registered), the streamable HTTP
// transport wrapping it, and the bearer key and admin host Handler's guard
// (auth.go) checks every request against.
type Endpoint struct {
	key string
	cfg *config.Config

	// sdkHandler is built once, here in New — never per request. The SDK's
	// own getServer callback (passed to sdk.NewStreamableHTTPHandler below)
	// is invoked PER REQUEST in stateless mode (mcp/streamable.go:400), but
	// it must keep returning this SAME *sdk.Server: AddTool panics on a
	// schema it cannot infer (mcp/server.go:561), and this project's house
	// style is to fail on the ground at startup, not turn a bad schema into
	// a 500 per request through httpx.Recover. Building a server (or
	// registering tools) inside getServer would also re-infer all 42
	// tools' schemas on every single call for no reason.
	sdkHandler http.Handler
}

// New builds the MCP endpoint over calls, which is admin.Server.CallAsMCP.
// key is MOCKER_MCP_KEY; it is never logged, and New panics if it is empty
// — the "unmounted" state §A2 describes ("unset ⇒ the route is not mounted
// at all") is enforced by the caller simply never calling New (and never
// calling admin.Server.SetMCP) when MOCKER_MCP_KEY is unset, exactly as
// config.Load already treats an empty key as "feature off" rather than "a
// door with an empty lock". New enforces that same invariant defensively on
// its own side of the boundary: constructing an Endpoint that would accept
// ANY bearer value is worse than refusing to construct one at all, given
// this package's whole job is guarding admission to an in-process call that
// bypasses every other layer of authorization (§A6).
//
// The *sdk.Server is built HERE, once, with every tool registered — see
// sdkHandler's own comment on why that must never happen inside getServer.
func New(calls Caller, key string, cfg *config.Config, log *slog.Logger) *Endpoint {
	if key == "" {
		panic("mcp.New: MOCKER_MCP_KEY must not be empty — the caller must not build an MCP endpoint at all when the key is unset (§A2)")
	}

	srv := sdk.NewServer(&sdk.Implementation{Name: "mocker", Version: serverVersion}, &sdk.ServerOptions{
		// The orientation every client receives in initialize's result
		// (A7). An MCP host puts it in front of the model on every session,
		// which is why internal/guide keeps it to a few paragraphs and the
		// full text sits behind the get_guide tool instead. This is a FIELD
		// of the initialize result, not a capability: DESIGN §14.2's "only
		// tools, without resources and prompts" still holds.
		Instructions: guide.Instructions(),
	})

	// Refuse subscriptions/listen (SEP-2575) before it ever reaches the
	// SDK's own handler for it. See refuseSubscriptionsListen's own comment
	// for the full argument: JSONResponse below governs the shape of an
	// ORDINARY tool-call response, but the SDK registers this method in its
	// server method table unconditionally, and a server that publishes
	// tools (this one) makes tools_list_changed an allowed subscription for
	// it — so the long-lived, context-parking request this endpoint is
	// otherwise built to avoid is still reachable unless it is refused
	// outright.
	srv.AddReceivingMiddleware(refuseSubscriptionsListen)

	registerTools(srv, newLoopback(calls))
	// A9: the config tool reads cfg itself — no loopback, no route.
	addConfigTools(srv, cfg)

	getServer := func(*http.Request) *sdk.Server { return srv }

	sdkHandler := sdk.NewStreamableHTTPHandler(getServer, &sdk.StreamableHTTPOptions{
		// No session map, no Mcp-Session-Id, no per-session goroutines.
		// goleak (internal/testleak) runs in every package of this repo,
		// including this one; a session registry that outlives a test is a
		// red package. This is also what makes GET/DELETE /mcp answer 405
		// from the SDK itself, rather than this package having to reject
		// them by hand.
		Stateless: true,

		// An ordinary tool call answers application/json rather than
		// text/event-stream. cmd/mocker/main.go sets WriteTimeout: 30s on
		// the http.Server wrapping the whole dispatcher; an SSE stream is
		// cut at 30s, and httpServer.Shutdown would block on a live one for
		// the whole drain window. This flag alone does not make that true
		// of every method — subscriptions/listen still forces SSE
		// regardless (measured, mcp/streamable.go:1650) — which is exactly
		// why it is refused above rather than left to this option.
		JSONResponse: true,

		// The SDK otherwise rejects, with 403, any request arriving from
		// 127.0.0.1/[::1] whose Host header names something else — which is
		// the shape of every developer and smoke-test call this project
		// makes (curl -H 'Host: mocker.local' http://localhost:8080/...).
		// Disabling it is safe here because of what stands in front of
		// this handler in EACH routing mode mocker supports, not one
		// blanket argument:
		//   - in host mode, internal/server's dispatcher hands a request
		//     to the admin plane only when cfg.IsAdminHost(r.Host) is
		//     true, so a rebound DNS name reaches the "unknown host" 404
		//     and never sees /mcp at all — a real second defense besides
		//     the bearer key;
		//   - in MOCKER_ROUTING=path mode there is no host check at all
		//     (internal/server's servePath routes on path alone, and its
		//     default branch hands everything — any Host header included —
		//     to the admin plane), so there the bearer key IS the whole
		//     defense.
		// What holds in BOTH modes regardless: the credential here is an
		// Authorization header, which a cross-origin page cannot attach to
		// a "simple" request at all — unlike the ambient cookie DNS
		// rebinding protection is designed to guard.
		DisableLocalhostProtection: true,

		// A silent transport is undebuggable; mocker's logger is already
		// levelled by MOCKER_LOG_LEVEL.
		Logger: log,

		// Left at zero the SDK assigns itself DefaultMaxRequestBodyBytes
		// (4 MiB) at construction (mcp/streamable.go:225,247) — stricter
		// than MOCKER_MAX_BODY's 10 MB default and applied with its own
		// MaxBytesReader before mocker's own httpx.MaxBody middleware ever
		// gets a say, so the documented limit would quietly not be the
		// real one. cfg.MaxBody is already an int64 (internal/config's own
		// field) — not int64(cfg.MaxBody), which unconvert would reject —
		// so one configured number governs the request body on both
		// planes. A set_operation_response call carrying a large pinned
		// body is the one most likely to meet this limit first.
		MaxRequestBodyBytes: cfg.MaxBody,
	})

	return &Endpoint{key: key, cfg: cfg, sdkHandler: sdkHandler}
}

// refuseSubscriptionsListen answers subscriptions/listen (SEP-2575) with a
// JSON-RPC "method not found" instead of letting the SDK's own handler for
// it park the request until its context is cancelled — exactly the
// long-lived response StreamableHTTPOptions.JSONResponse (New, above) was
// chosen to avoid, and that mocker's 30s WriteTimeout would cut and
// Shutdown would wait on for the whole drain window.
//
// The error value is built from the PUBLIC github.com/modelcontextprotocol/
// go-sdk/jsonrpc package, not the obvious jsonrpc2.ErrMethodNotFound: that
// lives under go-sdk/internal/jsonrpc2 and no code outside the SDK module
// can import it. jsonrpc.Error (a type alias for jsonrpc2.WireError, whose
// Error() method has a POINTER receiver — measured against v1.7.0 by
// building and running a probe) is how the SDK writes its own
// method-not-found errors (mcp/streamable.go:1673); a plain errors.New here
// would put "code":0 on the wire instead of jsonrpc.CodeMethodNotFound
// (-32601), telling a client nothing about why the call was refused.
//
// The refusal comes back as 200 with Content-Type: text/event-stream — an
// SSE-framed JSON-RPC error, not a JSON body — because the SDK forces SSE
// for this one method regardless of JSONResponse (measured,
// mcp/streamable.go:1650: useSSE := !c.jsonResponse || isSubscriptionsListen).
// That is fine: it returns in about a millisecond and nothing parks. See
// mcp_test.go's TestEndpoint_subscriptionsListenIsRefused, which asserts
// both the JSON-RPC error code inside that frame and that the call actually
// RETURNS within a real timeout — a test that only checked the HTTP status
// would pass even if this middleware were never installed, because the
// symptom of a missing refusal is a hang, not a wrong status.
func refuseSubscriptionsListen(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		if method == methodSubscriptionsListen {
			return nil, &jsonrpc.Error{
				Code:    jsonrpc.CodeMethodNotFound,
				Message: "subscriptions/listen is not supported: mocker's MCP endpoint ships tools only, in stateless request/response mode (§0 of the MCP slice's context document)",
			}
		}
		return next(ctx, method, req)
	}
}

// Handler returns the /mcp handler: bearer check, Origin check (both
// guard, auth.go), the inbound request placed on the context, then the
// SDK's streamable HTTP handler.
//
// The SDK does NOT hand the inbound *http.Request to a tool handler at all:
// v1.7.0 gives one only RequestExtra.Header (mcp/shared.go:603), which in
// Go never carries Host, and never RemoteAddr or TLS — the exact three
// fields CallAsMCP (§B2 rule 2) needs to build workspace URLs that match
// what the client actually connected to. So this method puts the inbound
// request on the context itself, via withInboundRequest, before calling the
// SDK handler: the stateless transport connects a session with
// req.Context() (mcp/streamable.go:434), so the value stays visible to
// every tool handler invoked while serving this one call. It is stored on
// the CONTEXT, per call, and never on the Endpoint — the Endpoint is one
// long-lived value shared by every concurrent request this process serves,
// and storing a request there would be shared mutable state racing across
// them.
func (e *Endpoint) Handler() http.Handler {
	withCtx := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.sdkHandler.ServeHTTP(w, r.WithContext(withInboundRequest(r.Context(), r)))
	})
	return e.guard(withCtx)
}

// requestCtxKey is the unexported type behind the inbound *http.Request
// Handler stores on the context — unexported so nothing outside this
// package can collide with, or forge, the key.
type requestCtxKey struct{}

// withInboundRequest returns ctx carrying r for the rest of the MCP call's
// lifetime. See Handler's doc comment for why this lives on the context
// rather than the Endpoint.
func withInboundRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, requestCtxKey{}, r)
}

// inboundRequest returns the /mcp request withInboundRequest attached to
// ctx, or false if none was. That should only happen if a tool handler
// somehow runs outside a call Handler itself dispatched — a bug in this
// package, since Handler always attaches one before calling the SDK
// handler — never something a real client request can trigger.
func inboundRequest(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(requestCtxKey{}).(*http.Request)
	return r, ok
}
