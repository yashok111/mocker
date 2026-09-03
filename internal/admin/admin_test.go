package admin_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// testPassword is the shared password every test server is configured with.
const testPassword = "correct horse battery staple"

// adminOrigin is the Origin/Referer value every properly-formed
// state-changing test request carries: it must equal cfg.AdminHost for
// [Server.enforceCSRF] to accept it.
const adminOrigin = "http://mocker.local"

// testConfig returns a Config with every field the TEST CONFIG contract
// requires: MaxBody:0 would make every request 413, ReservedPrefix:"" would
// make health unmatchable, Dev:false would set a Secure cookie a plain-http
// test client silently drops, and CheckpointRetention:0 would construct
// admin.New's checkpoints.Repo at C7's "prune nothing" — harmless for most
// tests here, but a silent trap for the day one of them writes more than a
// handful of pre-destructive checkpoints and expects the default retention
// (20, config.go:158) to prune them.
//
// CheckpointDebounce is left at its zero value DELIBERATELY, and that
// inverts what this same function does for CheckpointRetention just above:
// retention gets a non-zero default so tests don't silently rely on
// "prune nothing", while debounce gets ZERO so [Server.routeMux]'s P2d
// auto-checkpoint wrapper is not installed at all (see that method's own
// comment) — every existing test in this package therefore keeps running
// with no trigger in front of any handler, which is what makes
// scripts/smoke.sh:2013's "no observation asserts a checkpoint count delta
// except 5 and 12" claim true by construction here too, not by luck. A test
// that means to exercise the wrapper builds its own *config.Config with a
// non-zero window (see autocheckpoint_test.go) rather than mutating this
// one out from under every other test in the package.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	hash, err := auth.HashPassword(testPassword)
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
		CheckpointDebounce:  0,
	}
}

// testServer bundles a live admin.Server with the database underneath it, so
// tests that need to reach past the HTTP surface (e.g. closing the database
// to force /readyz to fail) can.
type testServer struct {
	handler http.Handler
	db      *store.DB
	cfg     *config.Config
}

// newTestServer builds a fully wired admin.Server over a fresh, migrated
// SQLite database.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return newTestServerCfg(t, nil)
}

// newTestServerCfg is [newTestServer] with one hook: mutate, when non-nil,
// edits the config BEFORE anything is built from it. It exists for the one
// property that is about a config value REACHING a constructor rather than
// about a handler's behaviour — [config.Config.MaxEntities] is read at
// admin.New and handed to resources.NewRepo, and a test that cannot move it
// off its default cannot tell a wired field from an ignored one.
func newTestServerCfg(t *testing.T, mutate func(*config.Config)) *testServer {
	t.Helper()
	cfg := testConfig(t)
	if mutate != nil {
		mutate(cfg)
	}
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := admin.New(cfg, sessions, ws, db, log)
	return &testServer{handler: srv.Handler(), db: db, cfg: cfg}
}

// do issues one request against the server in-process (no real listener) and
// returns the recorded response.
func (ts *testServer) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	return rec
}

// jsonRequest builds a request carrying a JSON body and the headers a
// legitimate same-origin admin-UI request always sends: Content-Type and
// Origin. cookie and csrfToken, when non-empty, are attached too — omit them
// to exercise the CSRF/auth failure paths.
func jsonRequest(t *testing.T, method, target string, body any, cookie *http.Cookie, csrfToken string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode request body: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", adminOrigin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	return req
}

// login performs a successful login and returns the session cookie and CSRF
// token, ready to attach to subsequent requests.
func (ts *testServer) login(t *testing.T, name string) (*http.Cookie, string) {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
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

// createWorkspace is a helper for tests whose focus is elsewhere: it logs in
// a fresh user and creates one workspace, returning everything a caller might
// need to keep going.
func (ts *testServer) createWorkspace(t *testing.T, userName, wsName string) (cookie *http.Cookie, csrfToken string, id float64, slug string) {
	t.Helper()
	cookie, csrfToken = ts.login(t, userName)
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": wsName}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}
	return cookie, csrfToken, got["id"].(float64), got["slug"].(string)
}

func TestHandler_healthz(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want 200", rec.Code)
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.OK {
		t.Error("GET /healthz: ok = false, want true")
	}
}

func TestHandler_readyz(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz (database up): status = %d, want 200", rec.Code)
	}

	// Close the database out from under the server: /readyz must notice
	// instead of reporting healthy on a dead connection pool.
	if err := ts.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	rec = ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz (database down): status = %d, want 503", rec.Code)
	}
}

func TestHandler_securityHeaders(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/healthz", nil))
	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "no-referrer"},
	}
	for _, tt := range tests {
		if got := rec.Header().Get(tt.header); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
		}
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want unset: the admin plane serves no CORS headers", got)
	}
}

func TestHandler_login(t *testing.T) {
	t.Parallel()

	t.Run("success sets an httpOnly cookie with no Domain", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		if !cookie.HttpOnly {
			t.Error("session cookie HttpOnly = false, want true")
		}
		if cookie.Domain != "" {
			t.Errorf("session cookie Domain = %q, want empty", cookie.Domain)
		}
		if csrfToken == "" {
			t.Error("login response csrfToken is empty")
		}
	})

	t.Run("wrong password answers 401", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
			map[string]string{"name": "Alex", "password": "not-the-password"}, nil, "")
		rec := ts.do(req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("rate limit answers 429 after 10 attempts", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)

		var last *httptest.ResponseRecorder
		for range 11 {
			req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
				map[string]string{"name": "Alex", "password": "not-the-password"}, nil, "")
			last = ts.do(req)
		}
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("11th login attempt in a minute: status = %d, want 429; body = %s", last.Code, last.Body.String())
		}
	})

	// Round-1 review finding 5: keying the limiter on address alone
	// collapses every caller behind the same reverse proxy into one bucket
	// (the default MOCKER_TRUST_PROXY=off means every request in this test
	// resolves to the same httptest RemoteAddr, exactly that shared-proxy
	// shape). Flooding under one name must not lock out a login under a
	// different name from the very same address.
	t.Run("flooding one name does not rate-limit a different name from the same address", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)

		for range 11 {
			req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
				map[string]string{"name": "attacker-noise", "password": "wrong"}, nil, "")
			ts.do(req)
		}

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
			map[string]string{"name": "Alex", "password": testPassword}, nil, "")
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login as a different name after another name was flooded from the same address: "+
				"status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_me(t *testing.T) {
	t.Parallel()

	t.Run("without a cookie answers 401", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/api/me", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("with a valid session answers the caller's identity", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/me", nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}

		var body struct {
			User struct {
				Name string `json:"name"`
			} `json:"user"`
			CSRFToken string `json:"csrfToken"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.User.Name != "Alex" {
			t.Errorf("user.name = %q, want %q", body.User.Name, "Alex")
		}
		if body.CSRFToken != csrfToken {
			t.Errorf("csrfToken = %q, want the token from login %q", body.CSRFToken, csrfToken)
		}
	})
}

func TestHandler_logout(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")

	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/logout", nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	me := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/me", nil)
	me.AddCookie(cookie)
	rec = ts.do(me)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/me after logout: status = %d, want 401", rec.Code)
	}
}

func TestHandler_csrf(t *testing.T) {
	t.Parallel()

	t.Run("valid session but no CSRF token answers 403", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, _ := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "demo"}, cookie, "")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("foreign Origin answers 403", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "demo"}, cookie, csrfToken)
		req.Header.Set("Origin", "http://evil.example")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("neither Origin nor Referer answers 403", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "demo"}, cookie, csrfToken)
		req.Header.Del("Origin")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Content-Type text/plain answers 415", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "demo"}, cookie, csrfToken)
		req.Header.Set("Content-Type", "text/plain")
		rec := ts.do(req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status = %d, want 415; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_createWorkspace(t *testing.T) {
	t.Parallel()

	t.Run("201 with the chosen slug in the body", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "Demo Workspace"}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
		}

		var body struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Slug == "" {
			t.Error("response slug is empty, want the assigned slug")
		}
		if body.Name != "Demo Workspace" {
			t.Errorf("name = %q, want %q", body.Name, "Demo Workspace")
		}
	})

	t.Run("taken slug answers 409", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		first := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "Demo", "slug": "taken-slug"}, cookie, csrfToken)
		if rec := ts.do(first); rec.Code != http.StatusCreated {
			t.Fatalf("first create: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
		}

		second := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]string{"name": "Another", "slug": "taken-slug"}, cookie, csrfToken)
		rec := ts.do(second)
		if rec.Code != http.StatusConflict {
			t.Fatalf("second create with the same slug: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_patchWorkspace(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d", int64(id))
	// editVersion: 1 -- A3/D10 requires it, and workspaces.Repo.Create's own
	// INSERT bootstraps edit_version at 1 (it cannot allocate from itself),
	// never the column's DEFAULT of 0 (repo.go's own comment on that INSERT).
	req := jsonRequest(t, http.MethodPatch, target, map[string]any{"name": "Renamed", "editVersion": 1}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Name        string `json:"name"`
		Revision    int64  `json:"revision"`
		EditVersion int64  `json:"editVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Name != "Renamed" {
		t.Errorf("name = %q, want %q", body.Name, "Renamed")
	}
	if body.Revision != 2 {
		t.Errorf("revision = %d, want 2 (bumped once from the create at revision 1)", body.Revision)
	}
	// A3/D9: a PATCH that writes a guarded field always allocates a fresh
	// edit_version, distinct from the 0 this write started at.
	if body.EditVersion == 0 {
		t.Errorf("editVersion after a successful PATCH = 0, want a freshly allocated (nonzero) value")
	}

	// A3/D6: retrying the exact same PATCH with the now-stale editVersion=1
	// expectation (the version this workspace started at, no longer what
	// the server holds after the write above) must be refused as a
	// conflict naming the version the server actually holds, not silently
	// accepted again.
	retry := jsonRequest(t, http.MethodPatch, target, map[string]any{"name": "Renamed Again", "editVersion": 1}, cookie, csrfToken)
	retryRec := ts.do(retry)
	if retryRec.Code != http.StatusConflict {
		t.Fatalf("PATCH retried with a stale editVersion: status = %d, want 409; body = %s", retryRec.Code, retryRec.Body.String())
	}
	var conflictBody struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Name        string `json:"name"`
				EditVersion int64  `json:"editVersion"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(retryRec.Body.Bytes(), &conflictBody); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflictBody.Error.Code != "edit_conflict" {
		t.Errorf("conflict code = %q, want %q", conflictBody.Error.Code, "edit_conflict")
	}
	if conflictBody.Error.Details.Name != "Renamed" {
		t.Errorf("conflict details.name = %q, want %q (the server's current document)", conflictBody.Error.Details.Name, "Renamed")
	}
	if conflictBody.Error.Details.EditVersion != body.EditVersion {
		t.Errorf("conflict details.editVersion = %d, want %d (the version the server actually holds)", conflictBody.Error.Details.EditVersion, body.EditVersion)
	}
}

// TestHandler_patchWorkspace_settingsTooLarge is round-1 review finding 3's
// regression: an oversized settings object must be rejected with 413, not
// stored — the mock plane re-parses settings on every unauthenticated
// request to the workspace, so persisting one would turn every future
// request into an amplified cost.
func TestHandler_patchWorkspace_settingsTooLarge(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d", int64(id))
	body := map[string]any{
		"settings": map[string]any{
			"notFoundBody": strings.Repeat("x", 70000),
		},
		"editVersion": 1, // Repo.Create bootstraps a fresh workspace at 1, not 0.
	}
	req := jsonRequest(t, http.MethodPatch, target, body, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PATCH with an oversized settings blob: status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}

	// Rejected, not silently applied: the workspace's revision must not
	// have moved.
	get := httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	getRec := ts.do(get)
	var got struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Revision != 1 {
		t.Errorf("revision after rejected PATCH = %d, want unchanged 1", got.Revision)
	}
}

func TestHandler_deleteWorkspace(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d", int64(id))

	del := jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken)
	rec := ts.do(del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	rec = ts.do(get)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE: status = %d, want 404", rec.Code)
	}
}

func TestHandler_listWorkspaces(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	aliceCookie, _, _, aliceSlug := ts.createWorkspace(t, "Alice", "Alice's Workspace")
	_, _, _, bobSlug := ts.createWorkspace(t, "Bob", "Bob's Workspace")

	req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/workspaces", nil)
	req.AddCookie(aliceCookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/workspaces (own): status = %d, want 200", rec.Code)
	}
	var own []struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &own); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(own) != 1 || own[0].Slug != aliceSlug {
		t.Errorf("GET /api/workspaces (own) = %v, want exactly [%q]", own, aliceSlug)
	}

	// Ownership is a label, not an authorization boundary (DESIGN §15):
	// ?all=1 must show Bob's workspace to Alice too.
	reqAll := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/workspaces?all=1", nil)
	reqAll.AddCookie(aliceCookie)
	recAll := ts.do(reqAll)
	if recAll.Code != http.StatusOK {
		t.Fatalf("GET /api/workspaces?all=1: status = %d, want 200", recAll.Code)
	}
	var all []struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(recAll.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	found := false
	for _, ws := range all {
		if ws.Slug == bobSlug {
			found = true
		}
	}
	if !found {
		t.Errorf("GET /api/workspaces?all=1 = %v, want it to include Bob's workspace %q", all, bobSlug)
	}
}
