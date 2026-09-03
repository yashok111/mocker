package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
)

// testKey is a MOCKER_MCP_KEY-shaped value: 33 bytes, comfortably over
// config.minMCPKeyLen (32) even though this package never imports config's
// unexported constant itself — that floor is config.Load's own job (§A2),
// not this package's, so tests here only need a key realistic enough that
// nothing about its SHAPE (as opposed to its correctness) trips a check.
const testKey = "0123456789abcdef0123456789abcdef"

// testConfig returns the minimum config.Config every Endpoint test needs:
// an AdminHost for the Origin check (auth.go) and a MaxBody the streamable
// transport's MaxRequestBodyBytes reads directly (mcp.go's New).
func testConfig() *config.Config {
	return &config.Config{
		AdminHost: "mocker.local",
		MaxBody:   10 << 20,
	}
}

// fakeCaller is a Caller that never has to stand up a database: every tool
// in this run's registry is still a no-op stub (tools_ops.go/
// tools_traffic.go), so nothing in this package's own tests ever drives it
// through an actual tool call — it exists only to satisfy mcp.New's
// parameter type. Tools A/Tools B give it real behavior in their own test
// files.
type fakeCaller struct {
	status int
	body   []byte
	err    error
}

func (f *fakeCaller) CallAsMCP(_ context.Context, _ *http.Request, _, _ string, _ []byte) (int, []byte, error) {
	if f.err != nil {
		return 0, nil, f.err
	}
	return f.status, f.body, nil
}

// newTestEndpoint builds an Endpoint over a fakeCaller and testKey/
// testConfig — the shared starting point every test in this package needs.
func newTestEndpoint(t *testing.T) *Endpoint {
	t.Helper()
	return New(&fakeCaller{status: http.StatusOK, body: []byte(`{}`)}, testKey, testConfig(), nil)
}

// doMCP issues one /mcp request against h with the two headers every
// legitimate call needs (Content-Type and an Accept naming BOTH
// application/json and text/event-stream — the SDK answers 400 before
// anything else if Accept is missing either, measured against v1.7.0, §A1),
// plus whatever extra headers the caller supplies.
func doMCP(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestNew_emptyKeyPanics is the construction-time refusal §A2 describes for
// "MOCKER_MCP_KEY unset": New must never hand back an Endpoint that would
// accept any bearer value. The caller (cmd/mocker/main.go, Wiring's own
// file) enforces the OTHER half of "not mounted at all" by simply never
// calling New (and never calling admin.Server.SetMCP) when the key is
// empty — this test pins New's own side of that contract.
func TestNew_emptyKeyPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("New(key=\"\") did not panic — an empty MOCKER_MCP_KEY must never build a usable Endpoint")
		}
	}()
	New(&fakeCaller{}, "", testConfig(), nil)
}

// TestEndpoint_initializeSucceeds is the handshake every real MCP client
// sends first (observation 3 of §G's smoke script has the same shape): a
// well-formed initialize call, with the right key and both Accept types,
// must answer 200 with a JSON-RPC result naming this server "mocker".
func TestEndpoint_initializeSucceeds(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`,
		map[string]string{"Authorization": "Bearer " + testKey})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result *struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
			Instructions string `json:"instructions"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if env.Error != nil {
		t.Fatalf("initialize returned a JSON-RPC error: %s", env.Error.Message)
	}
	if env.Result == nil || env.Result.ServerInfo.Name != "mocker" {
		t.Fatalf("result.serverInfo.name = %+v, want %q; body=%s", env.Result, "mocker", rec.Body.String())
	}
	// A7: the orientation rides in initialize itself, so a client that
	// never calls get_guide still learns where the guide is.
	if !strings.Contains(env.Result.Instructions, "get_guide") {
		t.Errorf("result.instructions does not name get_guide: %q", env.Result.Instructions)
	}
}

// TestEndpoint_toolsListRoundTrip drives a real tools/list call all the way
// through the guard and the SDK's own dispatch — the "full tools/list
// round trip over the handler" the Server agent's own tests must cover.
// It asserts a well-formed, error-free JSON-RPC result carrying a tools
// array, deliberately NOT a specific count: with the stub tools_ops.go/
// tools_traffic.go in place the registry (tools.go) adds zero tools, and
// this test's job is proving the ROUND TRIP — auth, transport, the SDK's
// own tools/list dispatch — works for however many tools
// addOperationTools/addTrafficTools actually register, unchanged once
// Tools A/Tools B fill those files in (§F: this file stays the Server
// agent's own, across every later phase).
//
// No initialize is sent first — measured against v1.7.0: tools/list works
// without one in stateless mode.
func TestEndpoint_toolsListRoundTrip(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result *struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if env.Error != nil {
		t.Fatalf("tools/list returned a JSON-RPC error: %s", env.Error.Message)
	}
	if env.Result == nil {
		t.Fatalf("tools/list returned no result; body=%s", rec.Body.String())
	}
	t.Logf("registerTools currently adds %d tool(s)", len(env.Result.Tools))
}

// TestEndpoint_subscriptionsListenIsRefused is §A1's hardest case to get
// right: with no middleware refusing it, the SDK parks a
// subscriptions/listen request until its own context is cancelled — the
// exact long-lived response JSONResponse: true (mcp.go's New) was chosen to
// avoid, and the one mocker's 30s WriteTimeout would cut and Shutdown would
// wait on for the whole drain window. A status check alone would not catch
// a MISSING refusal — the symptom is a hang, not a wrong status — so this
// bounds the call two ways: a context timeout on the request itself (so a
// bug here cannot leak a parked goroutine past this test — goleak runs in
// this package's TestMain, main_test.go) and a wall-clock bound on the test
// so a bug fails fast with a clear message instead of the test silently
// riding out the context timeout to a slow, unexplained pass/fail.
func TestEndpoint_subscriptionsListenIsRefused(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// notifications must be present (an absent one fails SDK param
	// validation with -32602 before reaching the method at all — a false
	// pass for this test, since -32602 is not the -32601 refusal being
	// asserted below). With the stub tools_ops.go/tools_traffic.go in
	// place (zero tools registered), the SDK's own subscriptionsListen
	// would itself return immediately even without this package's
	// middleware — it only parks on ctx.Done() when at least one requested
	// notification is actually allowed (mcp/server.go:1187), and
	// "toolsListChanged" is allowed only once the server's capabilities
	// advertise tools (mcp/server.go's capabilities(), s.tools.len() > 0) —
	// so this test cannot exercise the PARK itself yet. What it verified,
	// by temporarily removing the AddReceivingMiddleware call in mcp.go's
	// New and re-running: without the middleware this exact request comes
	// back as a plain JSON-RPC SUCCESS (no error at all), so the
	// "code":-32601 assertion below is a real, working regression guard
	// for the refusal today, and will additionally start catching an
	// actual park once Tools A/Tools B register real tools.
	req := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":7,"method":"subscriptions/listen","params":{"notifications":{"toolsListChanged":true}}}`)))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+testKey)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ServeHTTP did not return within 3s — subscriptions/listen was not refused and is parking the request, exactly the hang §A1's middleware exists to prevent")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ServeHTTP took %s to refuse subscriptions/listen (measured: a real refusal returns in about a millisecond) — this smells like the request parked and was only released by the request's own context timeout, not by the refusal middleware", elapsed)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the SDK forces SSE with a 200 for this method regardless of JSONResponse — measured, mcp/streamable.go:1650); body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), `"code":-32601`) {
		t.Errorf("body does not contain the JSON-RPC method-not-found code -32601: %s", rec.Body.String())
	}
}

// The tests below exercise loopback (loopback_client.go) directly — the
// "thin layer every tool uses" this file's own package doc points at, and
// the piece Tools A/Tools B use verbatim once tools_ops.go/
// tools_traffic.go stop being stubs. Driving it here, through withInboundRequest
// rather than a live Handler() round trip, is what proves do/call/toolErr
// work in isolation from the SDK transport around them.

// TestLoopback_doPassesThroughStatusAndBody proves do is a plain
// passthrough onto Caller.CallAsMCP, using the inbound request
// withInboundRequest placed on the context — the exact mechanism Handler
// uses for every real call.
func TestLoopback_doPassesThroughStatusAndBody(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{status: http.StatusCreated, body: []byte(`{"id":7}`)}
	lb := newLoopback(fc)

	src := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil)
	ctx := withInboundRequest(context.Background(), src)

	status, body, err := lb.do(ctx, http.MethodPost, "/api/workspaces", []byte(`{"name":"x"}`))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d, want %d", status, http.StatusCreated)
	}
	if string(body) != `{"id":7}` {
		t.Errorf("body = %s, want {\"id\":7}", body)
	}
}

// TestLoopback_doWithoutInboundRequestErrors is the defensive path do's own
// comment describes: a tool handler running outside Handler's own call — a
// bug in this package — must fail loudly rather than dispatching with a
// nil request.
func TestLoopback_doWithoutInboundRequestErrors(t *testing.T) {
	t.Parallel()
	lb := newLoopback(&fakeCaller{status: http.StatusOK})
	if _, _, err := lb.do(context.Background(), http.MethodGet, "/api/workspaces", nil); err == nil {
		t.Fatal("do() with no inbound request on context returned no error")
	}
}

// TestLoopback_callDecodesOn2xx is call's ordinary path: a 2xx body is
// decoded into out.
func TestLoopback_callDecodesOn2xx(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{status: http.StatusOK, body: []byte(`{"slug":"demo"}`)}
	lb := newLoopback(fc)
	ctx := withInboundRequest(context.Background(), httptest.NewRequest(http.MethodGet, "http://mocker.local/mcp", nil))

	var out struct {
		Slug string `json:"slug"`
	}
	if err := lb.call(ctx, http.MethodGet, "/api/workspaces", nil, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Slug != "demo" {
		t.Errorf("out.Slug = %q, want %q", out.Slug, "demo")
	}
}

// TestLoopback_callTurnsNon2xxIntoToolError is call's error path: any
// status outside 2xx becomes a plain Go error — never a hand-built
// CallToolResult (see call's own comment on why ToolHandlerFor's automatic
// packing is what actually turns this into the "tool error" §I requires).
func TestLoopback_callTurnsNon2xxIntoToolError(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{status: http.StatusNotFound, body: []byte(`{"error":{"code":"not_found","message":"workspace 9 not found"}}`)}
	lb := newLoopback(fc)
	ctx := withInboundRequest(context.Background(), httptest.NewRequest(http.MethodGet, "http://mocker.local/mcp", nil))

	err := lb.call(ctx, http.MethodGet, "/api/workspaces/9", nil, nil)
	if err == nil {
		t.Fatal("call() with a 404 returned no error")
	}
	if !strings.Contains(err.Error(), "workspace 9 not found") {
		t.Errorf("error = %q, want it to carry the admin plane's own message", err.Error())
	}
}

// TestToolErr_5xxDoesNotLeakBody is §I's other half of toolErr: a 5xx is
// not a mistake the model's own arguments could have caused, so nothing
// past its status is surfaced — an internal error message must never reach
// the model verbatim.
func TestToolErr_5xxDoesNotLeakBody(t *testing.T) {
	t.Parallel()
	err := toolErr(http.StatusInternalServerError, []byte(`{"error":{"code":"internal","message":"sqlite: database is locked, dsn=/data/mocker.db"}}`))
	if err == nil {
		t.Fatal("toolErr(500, ...) returned nil")
	}
	if strings.Contains(err.Error(), "sqlite") || strings.Contains(err.Error(), "/data/mocker.db") {
		t.Errorf("error = %q, leaks internal detail past the 5xx status", err.Error())
	}
}

// TestToolErr_4xxCarriesAdminMessage is toolErr's positive control: a 4xx
// is the caller's own mistake, so its message DOES carry the admin plane's
// wording through, unwrapped from its {"error":{...}} envelope.
func TestToolErr_4xxCarriesAdminMessage(t *testing.T) {
	t.Parallel()
	err := toolErr(http.StatusBadRequest, []byte(`{"error":{"code":"bad_request","message":"decode directive: unexpected trailing data after JSON body"}}`))
	if err == nil {
		t.Fatal("toolErr(400, ...) returned nil")
	}
	if !strings.Contains(err.Error(), "unexpected trailing data after JSON body") {
		t.Errorf("error = %q, want the admin plane's own message", err.Error())
	}
}
