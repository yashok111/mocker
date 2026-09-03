package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/server"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// testPassword is the shared password every full-stack test server below is
// configured with.
const testPassword = "correct horse battery staple"

// adminOrigin is the Origin every state-changing admin request in this file
// carries: it must equal cfg.AdminHost for [admin.Server]'s CSRF check to
// accept it (internal/admin's enforceCSRF).
const adminOrigin = "http://mocker.local"

// testConfig returns a Config satisfying every trap the TEST CONFIG contract
// warns about: MaxBody:0 would 413 every request, ReservedPrefix:"" would
// make health unmatchable, Dev:false would set a Secure cookie a plain-http
// test client silently drops, and CheckpointRetention:0 would construct
// admin.New's checkpoints.Repo at C7's "prune nothing" (harmless today, but
// a silent trap the day a test here writes enough pre-destructive
// checkpoints to expect the default retention to prune them).
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
		RuntimeCache:        32,
		Dev:                 true,
		CheckpointRetention: 20,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildStack wires the full P0 stack — database, auth, workspaces, both
// planes, the dispatcher, and the same Recover/RequestLog/MaxBody chain
// cmd/mocker/main.go wraps it in — over a fresh, migrated SQLite database.
// This is deliberately NOT a lighter stand-in: the whole point of this test
// file is proving the wiring, not just [server.New]'s own logic in isolation
// (see TestDispatcher_HostMode/PathMode below for that).
func buildStack(t *testing.T) (http.Handler, *config.Config) {
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
	log := testLogger()

	adminSrv := admin.New(cfg, sessions, ws, db, log)
	mockPlane := mockplane.New(cfg, ws, nil, log)
	dispatcher := server.New(cfg, adminSrv.Handler(), mockPlane, log)
	handler := httpx.Chain(dispatcher, httpx.Recover(log), httpx.RequestLog(log), httpx.MaxBody(cfg.MaxBody))
	return handler, cfg
}

func do(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// jsonRequest builds a request carrying a JSON body and the headers a
// legitimate same-origin admin-UI request always sends: Content-Type and
// Origin (DESIGN §15's CSRF check requires both). cookie and csrfToken, when
// non-empty, are attached too.
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

// login authenticates against handler and returns the session cookie and
// CSRF token every subsequent state-changing request must carry.
func login(t *testing.T, handler http.Handler, name string) (*http.Cookie, string) {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
		map[string]string{"name": name, "password": testPassword}, nil, "")
	rec := do(handler, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200; body = %s", rec.Code, rec.Body)
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

// TestDispatcher_P0Acceptance is DESIGN §19's P0 acceptance criterion, encoded
// without DNS or TLS: the dispatcher decides on the Host header alone, so
// setting req.Host (via the target URL httptest.NewRequest parses) exercises
// exactly the routing the criterion exists to prove — the same substitution
// scripts/smoke.sh makes with curl's -H 'Host: ...' against a real listener.
func TestDispatcher_P0Acceptance(t *testing.T) {
	t.Parallel()
	handler, cfg := buildStack(t)

	cookie, csrf := login(t, handler, "alex-admin")

	createReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": "alex"}, cookie, csrf)
	createRec := do(handler, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", createRec.Code, createRec.Body)
	}
	var created struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}
	if created.Slug != "alex" {
		t.Fatalf("created workspace slug = %q, want %q (a fresh database should never need alex-2)", created.Slug, "alex")
	}

	tests := []struct {
		name        string
		host        string
		path        string
		wantStatus  int
		wantBody    string   // exact match when non-empty
		wantSubstrs []string // substrings that must all appear in the body
	}{
		{
			name:       "known workspace health",
			host:       "alex.mock.local",
			path:       "/__mocker/health",
			wantStatus: http.StatusOK,
			wantBody:   `{"ok":true,"workspace":"alex","revision":1,"spec":null}`,
		},
		{
			name:       "unknown workspace host",
			host:       "nope.mock.local",
			path:       "/__mocker/health",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "admin liveness",
			host:       "mocker.local",
			path:       "/healthz",
			wantStatus: http.StatusOK,
		},
		{
			// The dispatcher, not DNS, is under test: a host shaped like
			// neither plane must 404 with a body that names BOTH expected
			// shapes, because in a closed contour the real question is
			// "which of DNS, TLS or my config is wrong" (server.go's own
			// doc comment on unknownHost).
			name:        "unrelated host",
			host:        "totally-unrelated.example.com",
			path:        "/healthz",
			wantStatus:  http.StatusNotFound,
			wantSubstrs: []string{cfg.AdminHost, cfg.BaseDomain},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+tt.path, nil)
			rec := do(handler, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %s, want %s", rec.Body.String(), tt.wantBody)
			}
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(rec.Body.String(), sub) {
					t.Errorf("body %q missing expected substring %q", rec.Body.String(), sub)
				}
			}
		})
	}
}

// fakeSource is an in-memory [mockplane.Source]: TestDispatcher_HostMode and
// TestDispatcher_PathMode below exercise [server.New]'s own dispatch logic —
// which host or path goes to which plane — and need no database to do it,
// mirroring the same fake mockplane's own tests use.
type fakeSource struct {
	bySlug map[string]*workspaces.Workspace
}

func (f *fakeSource) BySlug(_ context.Context, slug string) (*workspaces.Workspace, error) {
	if ws, ok := f.bySlug[slug]; ok {
		return ws, nil
	}
	return nil, workspaces.ErrNotFound
}

func fakePlane(cfg *config.Config, wss ...*workspaces.Workspace) *mockplane.Plane {
	src := &fakeSource{bySlug: make(map[string]*workspaces.Workspace, len(wss))}
	for _, ws := range wss {
		src.bySlug[ws.Slug] = ws
	}
	return mockplane.New(cfg, src, nil, testLogger())
}

// recordingHandler is a minimal admin-plane stand-in: it records every
// request it was actually handed, so a dispatcher test can assert "admin was
// reached" without a real admin.Server (and the database that would need).
type recordingHandler struct {
	calls []*http.Request
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls = append(h.calls, r)
	w.WriteHeader(http.StatusOK)
}

// TestDispatcher_HostMode exercises [server.New] in the normal
// MOCKER_ROUTING=host mode directly, isolated from the database: the
// dispatcher's own job is choosing a plane, not what either plane does once
// chosen (that is covered by the full-stack acceptance test above, and by
// each plane's own package tests).
func TestDispatcher_HostMode(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		MaxBody:        10 << 20,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
	adminStub := &recordingHandler{}
	mock := fakePlane(cfg, &workspaces.Workspace{Slug: "alex", Revision: 5, Settings: domain.DefaultSettings()})
	handler := server.New(cfg, adminStub, mock, testLogger())

	t.Run("admin host reaches admin", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://mocker.local/healthz", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusOK || len(adminStub.calls) != 1 {
			t.Fatalf("status = %d, adminStub.calls = %d, want 200 and 1 call", rec.Code, len(adminStub.calls))
		}
	})

	t.Run("workspace host reaches the mock plane", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/health", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
		if len(adminStub.calls) != 0 {
			t.Fatalf("admin was called %d times, want 0 — a workspace host must never reach the admin plane", len(adminStub.calls))
		}
		if !strings.Contains(rec.Body.String(), `"workspace":"alex"`) {
			t.Fatalf("body = %s, missing workspace alex", rec.Body)
		}
	})

	t.Run("neither host 404s naming both shapes", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://neither.example.com/healthz", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if len(adminStub.calls) != 0 {
			t.Fatalf("admin was called %d times, want 0", len(adminStub.calls))
		}
		body := rec.Body.String()
		if !strings.Contains(body, cfg.AdminHost) || !strings.Contains(body, cfg.BaseDomain) {
			t.Fatalf("body %q does not name both host shapes (%q and %q)", body, cfg.AdminHost, cfg.BaseDomain)
		}
	})
}

// TestDispatcher_PathMode exercises the emergency MOCKER_ROUTING=path mode
// (DESIGN §16): one origin, /api/*+/healthz+/readyz to admin, /w/{slug}/...
// to the mock plane with the prefix stripped. BaseDomain is deliberately left
// empty — path mode does not require one, and a dispatcher that secretly
// depended on it would be a bug this test exists to catch.
func TestDispatcher_PathMode(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		AdminHost:      "mocker.local",
		Routing:        config.RoutingPath,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		MaxBody:        10 << 20,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
	adminStub := &recordingHandler{}
	mock := fakePlane(cfg, &workspaces.Workspace{Slug: "demo", Revision: 7, Settings: domain.DefaultSettings()})
	handler := server.New(cfg, adminStub, mock, testLogger())

	t.Run("admin prefixes reach admin unchanged", func(t *testing.T) {
		for _, path := range []string{"/healthz", "/readyz", "/api/workspaces"} {
			adminStub.calls = nil
			req := httptest.NewRequest(http.MethodGet, "http://origin.example"+path, nil)
			do(handler, req)
			if len(adminStub.calls) != 1 {
				t.Fatalf("%s: adminStub.calls = %d, want 1", path, len(adminStub.calls))
			}
			if got := adminStub.calls[0].URL.Path; got != path {
				t.Fatalf("%s: admin saw path %q, want it unchanged", path, got)
			}
		}
	})

	t.Run("workspace path strips the prefix and reaches the mock plane", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://origin.example/w/demo/__mocker/health", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
		if len(adminStub.calls) != 0 {
			t.Fatalf("admin was called %d times, want 0", len(adminStub.calls))
		}
		want := `{"ok":true,"workspace":"demo","revision":7,"spec":null}`
		if rec.Body.String() != want {
			t.Fatalf("body = %s, want %s", rec.Body, want)
		}
	})

	t.Run("bare /w/ with no slug 404s instead of reaching either plane", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://origin.example/w/", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if len(adminStub.calls) != 0 {
			t.Fatalf("admin was called %d times, want 0", len(adminStub.calls))
		}
	})

	// REWRITTEN by task 2 (the phase explicitly authorizes exactly this one
	// subtest to change): servePath's default branch used to answer
	// pathModeNotFound for anything unmatched; it now hands the request to
	// d.admin instead, which is precisely what makes the SPA and its client
	// routes reachable under MOCKER_ROUTING=path. This subtest used to
	// assert the diagnostic 404 the old behaviour produced; it now asserts
	// the admin handler was actually reached, not merely that the status
	// isn't 404 (adminStub always answers 200, so a status-only assertion
	// would pass even if this reverted to some OTHER non-404, non-admin
	// answer).
	t.Run("unmatched path reaches the admin handler instead of a diagnostic 404", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://origin.example/nonsense", nil)
		rec := do(handler, req)
		if len(adminStub.calls) != 1 {
			t.Fatalf("adminStub.calls = %d, want 1 (default branch now hands unmatched paths to the admin plane)", len(adminStub.calls))
		}
		if got := adminStub.calls[0].URL.Path; got != "/nonsense" {
			t.Fatalf("admin saw path %q, want it unchanged", got)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (recordingHandler's own fixed answer)", rec.Code)
		}
	})

	t.Run("GET / reaches the admin handler (the SPA shell's own route)", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://origin.example/", nil)
		rec := do(handler, req)
		if len(adminStub.calls) != 1 {
			t.Fatalf("adminStub.calls = %d, want 1", len(adminStub.calls))
		}
		if got := adminStub.calls[0].URL.Path; got != "/" {
			t.Fatalf("admin saw path %q, want %q", got, "/")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("GET /assets/app.js reaches the admin handler (a client-side asset)", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://origin.example/assets/app.js", nil)
		rec := do(handler, req)
		if len(adminStub.calls) != 1 {
			t.Fatalf("adminStub.calls = %d, want 1", len(adminStub.calls))
		}
		if got := adminStub.calls[0].URL.Path; got != "/assets/app.js" {
			t.Fatalf("admin saw path %q, want it unchanged", got)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("a plain workspace path (not the /__mocker control endpoint) still reaches the mock plane with the prefix stripped", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodGet, "http://origin.example/w/demo/pets", nil)
		rec := do(handler, req)
		if len(adminStub.calls) != 0 {
			t.Fatalf("admin was called %d times, want 0 (this path belongs to the mock plane)", len(adminStub.calls))
		}
		// The mock plane itself (not the dispatcher) answers this 404 —
		// serveNoRoute's own message names the workspace and the STRIPPED
		// path, proving the request actually reached [mockplane.Plane]
		// rather than merely not reaching admin. recordingHandler can only
		// ever answer 200, so a 404 here could not have come from it either.
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (the mock plane's own not-found)", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "no route for GET /pets") || !strings.Contains(rec.Body.String(), "demo") {
			t.Fatalf("body %q does not look like the mock plane's own no-route answer for workspace demo's /pets", rec.Body.String())
		}
	})

	t.Run("a non-GET request to / still reaches the admin handler", func(t *testing.T) {
		adminStub.calls = nil
		req := httptest.NewRequest(http.MethodPost, "http://origin.example/", nil)
		rec := do(handler, req)
		if len(adminStub.calls) != 1 {
			t.Fatalf("adminStub.calls = %d, want 1", len(adminStub.calls))
		}
		if got := adminStub.calls[0].Method; got != http.MethodPost {
			t.Fatalf("admin saw method %q, want POST", got)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}
