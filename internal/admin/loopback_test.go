package admin

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// loopbackTestConfig returns a *config.Config with every field the shared
// TEST CONFIG contract needs — the same literal admin_test.go's testConfig
// uses, spelled out again here because that one lives in package
// admin_test, a different Go package this internal test file cannot reach
// into (see the task note on why: withAuthContext/ctxKey are unexported and
// in-package, which is the whole reason loopback.go — and this file — are
// package admin rather than admin_test).
func loopbackTestConfig(t *testing.T) *config.Config {
	t.Helper()
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}
	return &config.Config{
		BaseDomain:          "mock.local",
		AdminHost:           "mocker.local",
		Routing:             config.RoutingHost,
		ReservedPrefix:      "/__mocker",
		AuthMode:            config.AuthShared,
		SharedPasswordHash:  hash,
		DataDir:             t.TempDir(),
		MaxBody:             10 << 20,
		MaxResponse:         4 << 20,
		MaxEntities:         1000, // D11's own default (config.Load's, spelled out here since this fixture bypasses Load)
		RuntimeCache:        32,
		Dev:                 true,
		CheckpointRetention: 20,
	}
}

// loopbackTestServer builds a fully wired *Server over a fresh, migrated
// SQLite database. mutate, when non-nil, is applied to the config before
// anything else is built — the one caller that needs it configures
// MOCKER_TRUST_PROXY.
func loopbackTestServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := loopbackTestConfig(t)
	if mutate != nil {
		mutate(cfg)
	}
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, sessions, ws, db, log)
}

// loopbackTestSrc returns a plausible inbound /mcp request: enough for
// buildLoopbackRequest/CallAsMCP to work from, with none of the fields an
// individual test wants to control already set.
func loopbackTestSrc() *http.Request {
	return httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil)
}

// TestBuildLoopbackRequest_opKeyWithSlashSurvivesEscaping is §B2 rule 1's own
// test: an opKey containing "%2F" must reach a mux's PathValue exactly as it
// would from a real HTTP client hitting the same URL, which is only true
// when the request is built from the escaped path STRING via
// http.NewRequestWithContext rather than a url.URL literal.
func TestBuildLoopbackRequest_opKeyWithSlashSurvivesEscaping(t *testing.T) {
	t.Parallel()
	opKey := overrides.OpKey(http.MethodGet, "/widgets/sub")
	if !strings.Contains(opKey, "%2F") {
		t.Fatalf("test precondition broken: opKey %q does not contain %%2F", opKey)
	}
	path := "/api/workspaces/7/operations/" + opKey

	req, err := buildLoopbackRequest(t.Context(), loopbackTestSrc(), http.MethodPut, path, nil, "")
	if err != nil {
		t.Fatalf("buildLoopbackRequest(): %v", err)
	}

	// Dispatch through a throwaway mux registered with the SAME pattern
	// override_handlers.go uses, and read PathValue back exactly as
	// opKeyFromPath does. A url.URL{Path: path} literal would have
	// double-escaped every '%' on the way out, and this would come back
	// mangled instead of the original "GET /widgets/sub".
	var gotOpKey string
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/workspaces/{id}/operations/{opKey}", func(_ http.ResponseWriter, r *http.Request) {
		gotOpKey = r.PathValue("opKey")
	})
	mux.ServeHTTP(httptest.NewRecorder(), req)

	const want = "GET /widgets/sub"
	if gotOpKey != want {
		t.Errorf("PathValue(opKey) = %q, want %q (opKey on the wire was %q)", gotOpKey, want, opKey)
	}
}

// TestBuildLoopbackRequest_carriesHostAndTLS is half of §B2 rule 2: Host and
// TLS must reach the outbound request unchanged, or httpx.ForwardedProto
// reads a nil r.TLS and every workspace URL in an MCP response comes back
// http:// even when the inbound /mcp request arrived over real TLS.
func TestBuildLoopbackRequest_carriesHostAndTLS(t *testing.T) {
	t.Parallel()
	src := loopbackTestSrc()
	src.Host = "mocker.local:9443"
	src.TLS = &tls.ConnectionState{}

	req, err := buildLoopbackRequest(t.Context(), src, http.MethodGet, "/api/workspaces", nil, "")
	if err != nil {
		t.Fatalf("buildLoopbackRequest(): %v", err)
	}
	if req.Host != src.Host {
		t.Errorf("Host = %q, want %q", req.Host, src.Host)
	}
	if req.TLS == nil {
		t.Error("TLS not carried over, want src's non-nil *tls.ConnectionState")
	}
}

// TestBuildLoopbackRequest_carriesRemoteAddrAndForwardedProto is the other
// half of §B2 rule 2: RemoteAddr and X-Forwarded-Proto must reach the
// outbound request unchanged too — httpx.ForwardedProto trusts a forwarded
// scheme only when the peer resolved from RemoteAddr sits inside a trusted
// CIDR, and a loopback call with an empty RemoteAddr behind a
// TLS-terminating proxy silently loses that trust.
func TestBuildLoopbackRequest_carriesRemoteAddrAndForwardedProto(t *testing.T) {
	t.Parallel()
	src := loopbackTestSrc()
	src.RemoteAddr = "10.0.0.5:54321"
	src.Header.Set("X-Forwarded-Proto", "https")

	req, err := buildLoopbackRequest(t.Context(), src, http.MethodGet, "/api/workspaces", nil, "")
	if err != nil {
		t.Fatalf("buildLoopbackRequest(): %v", err)
	}
	if req.RemoteAddr != src.RemoteAddr {
		t.Errorf("RemoteAddr = %q, want %q", req.RemoteAddr, src.RemoteAddr)
	}
	if got := req.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want %q", got, "https")
	}
}

// TestCallAsMCP_workspaceURLHonoursForwardedProtoBehindTrustedProxy is §B2
// rule 2's own end-to-end assertion: "one test asserts https under a
// trusted-proxy config". A workspace created through CallAsMCP behind a
// trusted reverse proxy must come back with an https:// URL — which is only
// possible if RemoteAddr AND X-Forwarded-Proto both survived the loopback
// call intact.
func TestCallAsMCP_workspaceURLHonoursForwardedProtoBehindTrustedProxy(t *testing.T) {
	t.Parallel()
	const trustedPeer = "203.0.113.9"
	srv := loopbackTestServer(t, func(cfg *config.Config) {
		cfg.TrustProxy = config.TrustProxy{
			Enabled: true,
			CIDRs:   []netip.Prefix{netip.MustParsePrefix(trustedPeer + "/32")},
		}
	})

	src := loopbackTestSrc()
	src.Host = "mocker.local"
	src.RemoteAddr = trustedPeer + ":12345"
	src.Header.Set("X-Forwarded-Proto", "https")

	status, body, err := srv.CallAsMCP(t.Context(), src, http.MethodPost, "/api/workspaces", []byte(`{"name":"proxied"}`))
	if err != nil {
		t.Fatalf("CallAsMCP(): %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}

	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode create-workspace response: %v; body=%s", err, body)
	}
	if !strings.HasPrefix(resp.URL, "https://") {
		t.Errorf("workspace url = %q, want an https:// URL — RemoteAddr/X-Forwarded-Proto were not carried over correctly", resp.URL)
	}
}

// TestBuildLoopbackRequest_dropsCookieAndAuthorization is §B2 rule 3: a
// Cookie or Authorization header on the inbound /mcp request must never
// reach the outbound admin-API call. The request is built fresh rather than
// cloned from src, so this test's real job is proving nothing ELSE in
// buildLoopbackRequest accidentally copies src.Header wholesale.
func TestBuildLoopbackRequest_dropsCookieAndAuthorization(t *testing.T) {
	t.Parallel()
	src := loopbackTestSrc()
	src.Header.Set("Cookie", "mocker_session=someone-elses-session")
	src.Header.Set("Authorization", "Bearer the-mcp-key-itself")

	req, err := buildLoopbackRequest(t.Context(), src, http.MethodGet, "/api/workspaces", nil, "")
	if err != nil {
		t.Fatalf("buildLoopbackRequest(): %v", err)
	}
	if got := req.Header.Get("Cookie"); got != "" {
		t.Errorf("Cookie leaked into the loopback request: %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization leaked into the loopback request: %q", got)
	}
}

// TestBuildLoopbackRequest_contentTypeOnlyWithBody is §B2 rule 4.
func TestBuildLoopbackRequest_contentTypeOnlyWithBody(t *testing.T) {
	t.Parallel()
	src := loopbackTestSrc()

	noBody, err := buildLoopbackRequest(t.Context(), src, http.MethodGet, "/api/workspaces", nil, "")
	if err != nil {
		t.Fatalf("buildLoopbackRequest() with nil body: %v", err)
	}
	if got := noBody.Header.Get("Content-Type"); got != "" {
		t.Errorf("Content-Type = %q with a nil body, want unset", got)
	}
	// The server contract: Body is never nil, so a handler that reads it
	// unguarded (DELETE .../session since A13) does not dereference nil.
	if noBody.Body == nil {
		t.Errorf("Body = nil with a nil body, want http.NoBody — a handler reading r.Body panics on nil")
	}

	withBody, err := buildLoopbackRequest(t.Context(), src, http.MethodPost, "/api/workspaces", []byte(`{"name":"x"}`), "")
	if err != nil {
		t.Fatalf("buildLoopbackRequest() with a body: %v", err)
	}
	if got := withBody.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q with a non-nil body, want application/json", got)
	}
}

// TestMcpPathAllowed is §B2 rule 8: the allowlist matches by ROUTE
// TEMPLATE, through the same ServeMux matching semantics the real dispatch
// mux uses — not a string prefix, which is bypassable exactly the way the
// "%61uth" case below demonstrates.
func TestMcpPathAllowed(t *testing.T) {
	t.Parallel()
	opKey := overrides.OpKey(http.MethodGet, "/widgets/sub")

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"list workspaces", http.MethodGet, "/api/workspaces", true},
		{"create workspace", http.MethodPost, "/api/workspaces", true},
		{"get operation, opKey containing %2F", http.MethodGet, "/api/workspaces/7/operations/" + opKey, true},
		{"put operation, opKey containing %2F", http.MethodPut, "/api/workspaces/7/operations/" + opKey, true},
		{"post session directive", http.MethodPost, "/api/workspaces/7/session", true},
		{"delete session directive", http.MethodDelete, "/api/workspaces/7/session", true},
		{"traffic poll with a query string", http.MethodGet, "/api/workspaces/7/traffic/poll?since=3&limit=1", true},

		// A2 (mocker-a-mcp gate document, D5) put all three routes that
		// used to stand here — DELETE and PATCH /api/workspaces/{id}, and
		// GET .../session — IN scope, so the negative rows below are drawn
		// from D12's exclusion list instead: routes kept out permanently and
		// by name, which is what a want-false row needs to stay meaningful.
		{"login, exact method and path", http.MethodPost, "/api/auth/login", false},
		{"logout: the mcp endpoint has no session to end", http.MethodPost, "/api/auth/logout", false},
		{"delete spec: excluded by name (D6)", http.MethodDelete, "/api/specs/1", false},
		{"login via a decoded-segment bypass (%61uth)", http.MethodPost, "/api/%61uth/login", false},
		{"healthz", http.MethodGet, "/healthz", false},
		{"mcp itself", http.MethodPost, "/mcp", false},
		{"method mismatch on an otherwise-allowed template", http.MethodDelete, "/api/workspaces", false},

		// A4 (mocker-a4-mcp-reach, D5, D9): probe moved OUT of the
		// exclusion list above and into the allowlist — it used to be a
		// want-false row here (excluded by mocker-a-mcp's own D12) and is
		// now the opposite: a probe destroys nothing and reaches exactly
		// the hosts an operator's own «Проверить» click can reach.
		{"probe: newly allowed (A4 D5, D9)", http.MethodPost, "/api/workspaces/7/probe", true},
		// A4 (D4, D9): the one new route this slice adds, addressed by a
		// route_family segment that itself carries a literal "/" — proof
		// the allowlist mux matches this template the same way it already
		// matches an opKey containing "%2F" above.
		{"resource entities, family segment containing a literal /", http.MethodGet, "/api/workspaces/7/resources/%2Fwidgets/entities", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
			if err != nil {
				t.Fatalf("NewRequestWithContext(): %v", err)
			}
			if got := mcpPathAllowed(req); got != tt.want {
				t.Errorf("mcpPathAllowed(%s %s) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// TestCaptureResponse is §B2 rule 6's own writer, tested the same way any
// http.ResponseWriter contract would be: a Write with no prior WriteHeader
// gets the implicit 200 net/http itself grants, and a second WriteHeader
// call — whether "late" (after a Write) or simply repeated — is ignored,
// keeping the FIRST status.
func TestCaptureResponse(t *testing.T) {
	t.Parallel()

	rw := newCaptureResponse()
	if _, err := rw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if rw.status != http.StatusOK {
		t.Errorf("status after Write with no prior WriteHeader = %d, want %d", rw.status, http.StatusOK)
	}
	rw.WriteHeader(http.StatusTeapot) // late; must be ignored like net/http's own recorder
	if rw.status != http.StatusOK {
		t.Errorf("status after a late WriteHeader = %d, want it to stay %d", rw.status, http.StatusOK)
	}
	if got := rw.body.String(); got != "hello" {
		t.Errorf("body = %q, want %q", got, "hello")
	}

	rw2 := newCaptureResponse()
	rw2.WriteHeader(http.StatusNotFound)
	rw2.WriteHeader(http.StatusInternalServerError) // repeated; must be ignored too
	if rw2.status != http.StatusNotFound {
		t.Errorf("status after two WriteHeader calls = %d, want the FIRST one, %d", rw2.status, http.StatusNotFound)
	}
}

// TestMcpIdentity_cachesOnlySuccessfulResolution is §B2 rule 5's "cache
// only a SUCCESSFUL resolution" half: a second call must return the SAME
// *auth.User (pointer identity, not merely the same id — a fresh
// EnsureUser call would also produce the same id, by design, so only
// pointer identity actually distinguishes "cached" from "re-resolved and
// happened to agree").
func TestMcpIdentity_cachesOnlySuccessfulResolution(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)

	user1, err := srv.mcpIdentity(t.Context())
	if err != nil {
		t.Fatalf("first mcpIdentity(): %v", err)
	}
	if user1.Name != mcpIdentityName || user1.Role != mcpIdentityRole {
		t.Errorf("identity = {%q,%q}, want {%q,%q}", user1.Name, user1.Role, mcpIdentityName, mcpIdentityRole)
	}

	user2, err := srv.mcpIdentity(t.Context())
	if err != nil {
		t.Fatalf("second mcpIdentity(): %v", err)
	}
	if user2 != user1 {
		t.Errorf("second mcpIdentity() returned a different *auth.User (%p vs %p), want the cached one", user2, user1)
	}
}

// TestMcpIdentity_retriesAfterAFailedResolution is §B2 rule 5's other half:
// a FAILED resolution must not be cached. The failure is forced
// deterministically (a query against a users table that does not exist yet,
// rather than racing a context cancellation against a real database write,
// which would be flaky) — a plain mutex plus a nil check must let the next
// call try EnsureUser again once the database is actually usable.
func TestMcpIdentity_retriesAfterAFailedResolution(t *testing.T) {
	t.Parallel()
	cfg := loopbackTestConfig(t)
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Deliberately NOT migrated yet.
	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg, sessions, ws, db, log)

	if _, err := srv.mcpIdentity(t.Context()); err == nil {
		t.Fatal("mcpIdentity() against an unmigrated database succeeded, want an error (test precondition broken)")
	}

	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	user, err := srv.mcpIdentity(t.Context())
	if err != nil {
		t.Fatalf("mcpIdentity() after migrating = %v, want success — a failed first resolution must not be cached forever", err)
	}
	if user.Name != mcpIdentityName {
		t.Errorf("Name = %q, want %q", user.Name, mcpIdentityName)
	}
}

// TestCallAsMCP_passesThroughHandlerStatusWithoutError is §B2 rule 7's
// "normal return" half: a 4xx/5xx a real handler answers is not a CallAsMCP
// failure.
func TestCallAsMCP_passesThroughHandlerStatusWithoutError(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)

	status, body, err := srv.CallAsMCP(t.Context(), loopbackTestSrc(), http.MethodGet, "/api/workspaces/999999/operations", nil)
	if err != nil {
		t.Fatalf("CallAsMCP() error = %v, want nil (a 404 from the real handler is not a CallAsMCP failure)", err)
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", status, body)
	}
}

// TestCallAsMCP_refusesARouteOutsideTheAllowlist is §B2 rule 7's "failure to
// make the call" half, exercised via the allowlist specifically: POST
// /api/auth/login is a REAL registered admin route, just not one the MCP
// tools may compose, and CallAsMCP must refuse it rather than dispatch it
// and hand back whatever status the real handler would answer.
//
// Its subject used to be DELETE /api/workspaces/{id}, which A2 put in scope
// (delete_workspace). The replacement is login on purpose: D12 excludes it
// BY NAME and permanently — CallAsMCP bypasses rateLimitLogin by
// construction, so allowlisting it would hand an unthrottled credential
// oracle to anything holding the bearer key — so this test's population
// cannot go empty the way it just nearly did.
func TestCallAsMCP_refusesARouteOutsideTheAllowlist(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)

	status, body, err := srv.CallAsMCP(t.Context(), loopbackTestSrc(), http.MethodPost, loginPath, []byte(`{"password":"x"}`))
	if err == nil {
		t.Fatalf("CallAsMCP() error = nil, want a refusal; status=%d body=%s", status, body)
	}
	if status != 0 || body != nil {
		t.Errorf("on a refused call: status=%d, body=%q, want the zero values", status, body)
	}
}

// TestCallAsMCP_worksBeforeHandlerIsEverBuilt is §B2 rule 9's own
// precondition-free requirement: CallAsMCP must work on a Server whose
// Handler() was never invoked — the shape an in-process caller with no
// listener naturally has no reason to force otherwise.
func TestCallAsMCP_worksBeforeHandlerIsEverBuilt(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil) // Handler() deliberately never called

	status, body, err := srv.CallAsMCP(t.Context(), loopbackTestSrc(), http.MethodPost, "/api/workspaces", []byte(`{"name":"no-handler-yet"}`))
	if err != nil {
		t.Fatalf("CallAsMCP() on a Server whose Handler() was never called: %v", err)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d, want 201; body=%s", status, body)
	}
}

// TestRouteMux_sharedBetweenHandlerAndCallAsMCP is §B2 rule 9's other half:
// Handler() must not build a second, independent mux once routeMux() has
// already been asked for one (directly, or via a prior CallAsMCP call).
func TestRouteMux_sharedBetweenHandlerAndCallAsMCP(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)

	before := srv.routeMux()
	srv.Handler()
	after := srv.routeMux()

	if before != after {
		t.Error("routeMux() returned different *http.ServeMux pointers across calls and after Handler(), want exactly one shared mux")
	}
}

// TestCallAsMCP_opKeyWithSlashRoundTripsThroughRealHandlers is §B2 rule 1's
// end-to-end proof, through the real handlers rather than a throwaway mux:
// a PUT storing an override under an opKey containing "%2F", followed by a
// GET on the SAME opKey, must see the SAME (method, path) come back. A
// mis-escaped request would have stored the override under a different,
// mangled key, and the GET would 404 instead.
func TestCallAsMCP_opKeyWithSlashRoundTripsThroughRealHandlers(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	src := loopbackTestSrc()

	status, body, err := srv.CallAsMCP(t.Context(), src, http.MethodPost, "/api/workspaces", []byte(`{"name":"opkey-roundtrip"}`))
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create workspace: status=%d err=%v body=%s", status, err, body)
	}
	var ws struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatalf("decode create-workspace response: %v", err)
	}

	opKey := overrides.OpKey(http.MethodGet, "/widgets/sub")
	if !strings.Contains(opKey, "%2F") {
		t.Fatalf("test precondition broken: opKey %q has no %%2F", opKey)
	}
	opPath := fmt.Sprintf("/api/workspaces/%d/operations/%s", ws.ID, opKey)

	putBody := []byte(`{"overrideOn":true,"routeOff":false,"responses":{},"editVersion":0}`)
	status, body, err = srv.CallAsMCP(t.Context(), src, http.MethodPut, opPath, putBody)
	if err != nil {
		t.Fatalf("PUT operation: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("PUT operation status = %d, want 200; body=%s", status, body)
	}

	status, body, err = srv.CallAsMCP(t.Context(), src, http.MethodGet, opPath, nil)
	if err != nil {
		t.Fatalf("GET operation back: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("GET operation status = %d, want 200 — a mis-escaped PUT stores under a different key; body=%s", status, body)
	}
	var doc struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode override doc: %v", err)
	}
	if doc.Path != "/widgets/sub" {
		t.Errorf("stored override path = %q, want %q — the opKey did not survive CallAsMCP intact", doc.Path, "/widgets/sub")
	}
}
