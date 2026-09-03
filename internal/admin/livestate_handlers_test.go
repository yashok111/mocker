package admin_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// p1c2TestServer mirrors admin_test.go's own testServer, plus the two P1c2
// setters that harness cannot reach: newTestServer returns only a handler,
// never the *admin.Server (admin_test.go's own doc comment on why), so
// anything needing SetLiveState/SetTraffic writes its own builder rather
// than editing that file. Every test in this file and in
// traffic_handlers_test.go uses THIS builder, never admin_test.go's
// newTestServer, so a live-state or traffic call never 503s by accident.
type p1c2TestServer struct {
	handler   http.Handler
	db        *store.DB
	cfg       *config.Config
	liveState *livestate.Store
	recorder  *traffic.Recorder
}

// p1c2NewTestServer builds a fully wired admin.Server, in host-routing mode
// (testConfig's own default), with both P1c2 setters called.
func p1c2NewTestServer(t *testing.T) *p1c2TestServer {
	t.Helper()
	return p1c2NewTestServerWithConfig(t, testConfig(t))
}

// p1c2NewTestServerWithConfig is p1c2NewTestServer parameterized on cfg, for
// the one test (workspace URL) that needs path-routing mode instead of
// testConfig's host-routing default.
func p1c2NewTestServerWithConfig(t *testing.T, cfg *config.Config) *p1c2TestServer {
	t.Helper()
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

	srv := admin.New(cfg, sessions, ws, db, log)
	liveState := livestate.NewStore(0, nil)
	srv.SetLiveState(liveState)
	rec := traffic.NewRecorder(db, log, traffic.Options{})
	srv.SetTraffic(rec)

	return &p1c2TestServer{handler: srv.Handler(), db: db, cfg: cfg, liveState: liveState, recorder: rec}
}

// p1c2NewTestServerNoLiveState builds a Server with SetTraffic wired but
// SetLiveState never called — the shape TestP1c2Session_noLiveStateSource
// needs to prove 503, not a panic, on a nil LiveStateSource.
func p1c2NewTestServerNoLiveState(t *testing.T) *p1c2TestServer {
	t.Helper()
	cfg := testConfig(t)
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

	srv := admin.New(cfg, sessions, ws, db, log)
	return &p1c2TestServer{handler: srv.Handler(), db: db, cfg: cfg}
}

func (ts *p1c2TestServer) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	return rec
}

// p1c2Login mirrors admin_test.go's testServer.login on the p1c2TestServer
// type — the logic is identical, but it is a method on a different receiver
// type, so it cannot simply be reused.
func (ts *p1c2TestServer) p1c2Login(t *testing.T, name string) (*http.Cookie, string) {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://"+ts.cfg.AdminHost+"/api/auth/login",
		map[string]string{"name": name, "password": testPassword}, nil, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login: wrote %d cookies, want 1", len(cookies))
	}
	var resp struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return cookies[0], resp.CSRFToken
}

// p1c2CreateWorkspace mirrors admin_test.go's testServer.createWorkspace on
// the p1c2TestServer type, for the same reason p1c2Login does.
func (ts *p1c2TestServer) p1c2CreateWorkspace(t *testing.T, userName, wsName string) (cookie *http.Cookie, csrfToken string, id int64, slug string) {
	t.Helper()
	cookie, csrfToken = ts.p1c2Login(t, userName)
	req := jsonRequest(t, http.MethodPost, "http://"+ts.cfg.AdminHost+"/api/workspaces",
		map[string]string{"name": wsName}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}
	return cookie, csrfToken, got.ID, got.Slug
}

func p1c2SessionURL(ts *p1c2TestServer, id int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/session", ts.cfg.AdminHost, id)
}

// TestP1c2Session_unauthenticated covers every session route: no session
// cookie at all must answer 401, regardless of method.
func TestP1c2Session_unauthenticated(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	_, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			// jsonRequest, not a bare httptest.NewRequest, for POST/DELETE:
			// enforceCSRF's Content-Type/Origin checks run BEFORE
			// requireUser in the middleware chain, so a state-changing
			// request missing either would answer 415/403 first and never
			// exercise the 401 this test is actually about. A cookie-less
			// request that DOES carry the right headers still reaches
			// requireUser (enforceCSRF skips its own token check when there
			// is no session at all — see that method's doc comment).
			req := jsonRequest(t, method, target, map[string]any{"target": "*", "action": "status", "status": 503}, nil, "")
			rec := ts.do(req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without a session: status = %d, want 401; body = %s",
					method, target, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestP1c2Session_missingCSRF covers the two state-changing session routes:
// a valid session but no CSRF token must answer 403.
func TestP1c2Session_missingCSRF(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			req := jsonRequest(t, method, target, map[string]any{
				"target": "*", "action": "status", "status": 503,
			}, cookie, "")
			rec := ts.do(req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with no CSRF token: status = %d, want 403; body = %s",
					method, target, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestP1c2Session_setGetClear is the core round trip: POST a status
// directive, GET it back with the remainder, DELETE it and get an empty
// list.
func TestP1c2Session_setGetClear(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	post := jsonRequest(t, http.MethodPost, target, map[string]any{
		"target": map[string]string{"method": "POST", "path": "/auth/login"},
		"action": "fail", "status": 503, "n": 2,
	}, cookie, csrfToken)
	rec := ts.do(post)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST session: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var posted struct {
		Directives []struct {
			Status int `json:"status"`
			N      int `json:"n"`
		} `json:"directives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &posted); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if len(posted.Directives) != 1 || posted.Directives[0].N != 2 {
		t.Fatalf("POST session directives = %+v, want exactly one with n=2", posted.Directives)
	}

	get := httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	rec = ts.do(get)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET session: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Directives []struct {
			Status int `json:"status"`
			N      int `json:"n"`
		} `json:"directives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if len(got.Directives) != 1 || got.Directives[0].Status != 503 || got.Directives[0].N != 2 {
		t.Fatalf("GET session directives = %+v, want exactly one {status:503,n:2}", got.Directives)
	}

	del := jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken)
	rec = ts.do(del)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE session: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var cleared struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatalf("decode DELETE response: %v", err)
	}
	if cleared.Cleared != 1 {
		t.Errorf("DELETE session cleared = %d, want 1", cleared.Cleared)
	}

	get2 := httptest.NewRequest(http.MethodGet, target, nil)
	get2.AddCookie(cookie)
	rec = ts.do(get2)
	var after struct {
		Directives []json.RawMessage `json:"directives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode GET-after-clear response: %v", err)
	}
	if len(after.Directives) != 0 {
		t.Errorf("GET session after DELETE = %d directives, want 0", len(after.Directives))
	}
}

// TestP1c2Session_scenarioAnswers400 replaces the old
// TestP1c2Session_scenarioStillNotImplemented: DESIGN §12's scenario switch
// used to be the one action still named 501 rather than silently rejected
// as "unknown action" or accepted — that stopped being true in P2b (A17),
// the moment scenarios got their own dedicated activate/deactivate routes
// (scenario_handlers.go). The session route now answers a plain 400 for a
// "scenario" key, naming the routes to use instead.
//
// Asserting the wire CODE, not just the 400 status, is the point (§G
// observation 14's own reasoning, mirrored here at the unit level): a
// regression that kept answering 400 but left the OLD "not_implemented_yet"
// code in the body would pass a status-only check while quietly re-lying
// about whether the feature exists.
func TestP1c2Session_scenarioAnswers400(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	req := jsonRequest(t, http.MethodPost, target, map[string]any{
		"target": "*", "action": "status", "status": 200, "scenario": map[string]any{"name": "x"},
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST session scenario: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "bad_request" {
		t.Errorf("POST session scenario: wire code = %q, want %q (NOT not_implemented_yet — A17)",
			body.Error.Code, "bad_request")
	}
}

// TestP1c2Session_delayAndPauseAccepted pins P2a's actual change:
// handlePostSession no longer hard-codes 501 for "delay" or "pause" before
// ever reaching the Store (TestP1c2Session_scenarioAnswers400 above covers
// the one key this route still refuses outright — "scenario", now a 400,
// not a 501; see A17). Both directives now reach [livestate.Store.Set] and
// come back out of GET — a second,
// independent request against the SAME Store, not just an echo of the POST
// body — with the exact wire shape §A pins: Status and Ms both carry
// `,omitempty`, so a delay directive's "status" key and a pause directive's
// "status" AND "ms" keys are ABSENT on the wire, never rendered as "0".
func TestP1c2Session_delayAndPauseAccepted(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)

	tests := []struct {
		name        string
		body        map[string]any
		wantAbsent  []string // wire keys that must be absent (omitempty at zero)
		wantPresent string   // a substring that must be present, proving the field round-tripped
	}{
		{
			name:        "delay",
			body:        map[string]any{"target": map[string]string{"method": "GET", "path": "/widgets"}, "action": "delay", "ms": 300},
			wantAbsent:  []string{`"status"`},
			wantPresent: `"ms":300`,
		},
		{
			name:        "pause",
			body:        map[string]any{"target": map[string]string{"method": "GET", "path": "/widgets"}, "action": "pause"},
			wantAbsent:  []string{`"status"`, `"ms"`},
			wantPresent: `"action":"pause"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// A workspace of its own, not shared with the other subtest: the
			// list endpoint answers the FULL directive list, so a shared
			// workspace would have the OTHER subtest's directive sitting
			// right beside this one and the substring checks below would see
			// its fields too.
			cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex-"+tt.name, "Demo")
			target := p1c2SessionURL(ts, id)

			req := jsonRequest(t, http.MethodPost, target, tt.body, cookie, csrfToken)
			rec := ts.do(req)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST session %s: status = %d, want 200; body = %s", tt.name, rec.Code, rec.Body.String())
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(rec.Body.String(), absent) {
					t.Errorf("POST session %s response = %s, must NOT contain %s (omitempty at zero)", tt.name, rec.Body.String(), absent)
				}
			}
			if !strings.Contains(rec.Body.String(), tt.wantPresent) {
				t.Errorf("POST session %s response = %s, want it to contain %s", tt.name, rec.Body.String(), tt.wantPresent)
			}

			// GET reflects the SAME directive out of the SAME Store — proving
			// Set actually stored it rather than the handler merely echoing
			// the POST's own body back unstored.
			get := httptest.NewRequest(http.MethodGet, target, nil)
			get.AddCookie(cookie)
			rec = ts.do(get)
			var got struct {
				Directives []livestate.Directive `json:"directives"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode GET response: %v", err)
			}
			if len(got.Directives) != 1 {
				t.Fatalf("GET after POST session %s: %d directives, want 1; body = %s", tt.name, len(got.Directives), rec.Body.String())
			}
			if got.Directives[0].Action != livestate.Action(tt.name) {
				t.Errorf("GET after POST session %s: action = %q, want %q", tt.name, got.Directives[0].Action, tt.name)
			}
		})
	}
}

// TestP1c2Session_delayPauseWireBoundsRejected proves neither handler
// validates delay/pause's field rules on its own: a delay carrying a
// non-zero status, or a pause carrying a non-zero ms, is rejected 400 by
// [livestate.Store.Set] (through normalize) exactly like
// TestP1c2Session_badStatus400's plain status-out-of-range case — this
// handler removed its own hard-coded 501 for these two actions but added no
// bounds check of its own.
func TestP1c2Session_delayPauseWireBoundsRejected(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	tests := []struct {
		name string
		body map[string]any
	}{
		{"delay carrying a non-zero status", map[string]any{"target": "*", "action": "delay", "ms": 300, "status": 200}},
		{"pause carrying a non-zero ms", map[string]any{"target": "*", "action": "pause", "ms": 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := jsonRequest(t, http.MethodPost, target, tt.body, cookie, csrfToken)
			rec := ts.do(req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST session %s: status = %d, want 400; body = %s", tt.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestP1c2Session_badStatus400 is the plain validation path: a status
// outside [100,599] must answer 400, not 500 and not silently accepted.
func TestP1c2Session_badStatus400(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	req := jsonRequest(t, http.MethodPost, target, map[string]any{
		"target": "*", "action": "status", "status": 9999,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST session with status=9999: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Session_bodyOverCap400 is round-1 review finding 4's transport-
// level half, on the admin plane's own POST .../session — the identical
// wire shape internal/mockplane's serveLiveStatePost accepts, and now the
// identical [livestate.MaxDirectiveBodyBytes] cap. The padding field rides
// along as extra JSON weight; decodeJSON's DisallowUnknownFields would
// refuse it too, but http.MaxBytesReader trips first — the body is bigger
// than the cap regardless of which field carries the extra bytes.
func TestP1c2Session_bodyOverCap400(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	req := jsonRequest(t, http.MethodPost, target, map[string]any{
		"target": "*", "action": "status", "status": 200,
		"padding": strings.Repeat("a", livestate.MaxDirectiveBodyBytes+1024),
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST session with an over-cap body: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Session_workspaceNotFound is a 404 for an id that parses but names
// no workspace, on every session route.
func TestP1c2Session_workspaceNotFound(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, _, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, 999999)

	get := httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	if rec := ts.do(get); rec.Code != http.StatusNotFound {
		t.Fatalf("GET session for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}

	post := jsonRequest(t, http.MethodPost, target, map[string]any{
		"target": "*", "action": "status", "status": 500,
	}, cookie, csrfToken)
	if rec := ts.do(post); rec.Code != http.StatusNotFound {
		t.Fatalf("POST session for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Session_noLiveStateSource503 proves the nil-source path is a clean
// 503, never a panic on a nil LiveStateSource.
func TestP1c2Session_noLiveStateSource503(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServerNoLiveState(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	get := httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	if rec := ts.do(get); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET session with no LiveStateSource: status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}

	post := jsonRequest(t, http.MethodPost, target, map[string]any{
		"target": "*", "action": "status", "status": 500,
	}, cookie, csrfToken)
	if rec := ts.do(post); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST session with no LiveStateSource: status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}

	del := jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken)
	if rec := ts.do(del); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("DELETE session with no LiveStateSource: status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2WorkspaceView_urlRefusesAnInjectedPort is the regression for the
// authority-injection hole in workspaceURL's port handling. It borrowed the
// port with net.SplitHostPort, which splits on the last colon and returns
// whatever followed WITHOUT validating it — and returns a nil error while
// doing so. `Host: mocker.local:9@evil.example` therefore yielded the port
// "9@evil.example", the URL built from it parsed with evil.example as its
// HOST and the workspace host demoted to userinfo, and config.normalizeHost
// strips after the last colon too, so the dispatcher still admitted the
// request. That URL is not merely displayed: POST .../probe dials it and
// reflects the answer, which turned this into a read-SSRF pivot out of the
// box for anyone holding the shared password.
//
// Asserted through the wire field rather than on workspaceURL directly, so it
// covers what a caller actually receives, and in BOTH routing modes because
// path mode has no Host check in front of it at all.
func TestP1c2WorkspaceView_urlRefusesAnInjectedPort(t *testing.T) {
	t.Parallel()

	const hostile = "mocker.local:9@evil.example"

	for _, tc := range []struct {
		name    string
		routing config.Routing
	}{
		{"host mode", config.RoutingHost},
		{"path mode", config.RoutingPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig(t)
			cfg.Routing = tc.routing
			ts := p1c2NewTestServerWithConfig(t, cfg)
			cookie, _, id, slug := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", id), nil)
			req.Host = hostile
			req.AddCookie(cookie)
			rec := ts.do(req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET workspace: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
			var body struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			u, err := url.Parse(body.URL)
			if err != nil {
				t.Fatalf("url %q does not parse: %v", body.URL, err)
			}
			// The property, stated as what a dialler would actually connect to
			// rather than as a substring of the string: an injected authority
			// hides in userinfo, where a "does it contain the right host" check
			// would not see it.
			wantHost := slug + "." + cfg.BaseDomain
			if tc.routing == config.RoutingPath {
				wantHost = cfg.AdminHost
			}
			if u.Hostname() != wantHost {
				t.Errorf("url %q resolves to host %q, want %q — an injected port smuggled a different authority", body.URL, u.Hostname(), wantHost)
			}
			if u.User != nil {
				t.Errorf("url %q carries userinfo %q; nothing in this URL may come from the request's Host beyond a numeric port", body.URL, u.User)
			}
		})
	}
}

// TestP1c2WorkspaceView_url lives here (rather than in a
// workspace_handlers_test.go this task does not own) because it needs the
// p1c2TestServer builder above. It checks workspaceURL's two branches
// directly through GET /api/workspaces/{id}'s "url" field.
func TestP1c2WorkspaceView_url(t *testing.T) {
	t.Parallel()

	t.Run("host mode", func(t *testing.T) {
		t.Parallel()
		ts := p1c2NewTestServer(t) // testConfig defaults to RoutingHost, BaseDomain "mock.local"
		cookie, _, id, slug := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local:8080/api/workspaces/%d", id), nil)
		req.Host = "mocker.local:8080"
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET workspace: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		want := fmt.Sprintf("http://%s.%s:8080", slug, ts.cfg.BaseDomain)
		if body.URL != want {
			t.Errorf("url = %q, want %q", body.URL, want)
		}
	})

	t.Run("path mode", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		cfg.Routing = config.RoutingPath
		ts := p1c2NewTestServerWithConfig(t, cfg)
		cookie, _, id, slug := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local:8080/api/workspaces/%d", id), nil)
		req.Host = "mocker.local:8080"
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET workspace: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		want := fmt.Sprintf("http://%s:8080/w/%s", cfg.AdminHost, slug)
		if body.URL != want {
			t.Errorf("url = %q, want %q", body.URL, want)
		}
	})
}

// A13: DELETE .../session with a body clears one target; without, all.
func TestP1c2Session_deleteOneTarget(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	target := p1c2SessionURL(ts, id)

	for _, body := range []map[string]any{
		{"target": map[string]string{"method": "GET", "path": "/orders"}, "action": "status", "status": 500},
		{"target": "*", "action": "delay", "ms": 5},
	} {
		if rec := ts.do(jsonRequest(t, http.MethodPost, target, body, cookie, csrfToken)); rec.Code != http.StatusOK {
			t.Fatalf("POST session: status = %d; body = %s", rec.Code, rec.Body.String())
		}
	}
	rec := ts.do(jsonRequest(t, http.MethodDelete, target,
		map[string]any{"target": map[string]string{"method": "GET", "path": "/orders"}}, cookie, csrfToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE one target: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var cleared struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil || cleared.Cleared != 1 {
		t.Errorf("cleared = %+v (err %v), want 1", cleared, err)
	}
	rec = ts.do(jsonRequest(t, http.MethodGet, target, nil, cookie, ""))
	var list struct {
		Directives []struct {
			Action string `json:"action"`
		} `json:"directives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Directives) != 1 || list.Directives[0].Action != "delay" {
		t.Errorf("after the delete: %s (err %v), want only the * delay", rec.Body.String(), err)
	}
	rec = ts.do(jsonRequest(t, http.MethodDelete, target, map[string]any{"action": "delay"}, cookie, csrfToken))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("DELETE with a body naming no target: status = %d, want 400", rec.Code)
	}
	rec = ts.do(jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken))
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); rec.Code != http.StatusOK || err != nil || cleared.Cleared != 1 {
		t.Errorf("bodyless DELETE: status %d cleared %+v (err %v), want 200 and 1", rec.Code, cleared, err)
	}
}
