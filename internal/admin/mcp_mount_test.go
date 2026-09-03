package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandlerBypass_content pins the bypass table §A4 of the MCP slice's
// context document requires: nothing in the code enumerates "paths served
// outside the mux" on its own, so this assertion — length AND membership —
// is what turns a second bypass added later into a red test instead of a
// silent discovery.
func TestHandlerBypass_content(t *testing.T) {
	t.Parallel()
	if len(handlerBypass) != 1 {
		t.Fatalf("len(handlerBypass) = %d, want 1 — a new bypass entry is a decision for the MCP slice's context document first, not a silent addition here", len(handlerBypass))
	}
	if handlerBypass[0] != mcpPath {
		t.Errorf("handlerBypass[0] = %q, want %q", handlerBypass[0], mcpPath)
	}
}

// TestHandler_mcpNotMountedIsA404 is observation 1's unit-level counterpart:
// with SetMCP never called, POST /mcp must answer 404 — not 403 (which is
// what a missing Origin would 403 through enforceCSRF, the trap this
// request's own Content-Type/Origin headers are set to avoid) and not the
// SPA shell.
func TestHandler_mcpNotMountedIsA404(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	h := srv.Handler() // SetMCP deliberately never called

	req := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://mocker.local")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandler_mcpMountedBypassesCSRFAndSession proves the mount branch's
// whole reason to exist: a request shaped to be REJECTED by
// enforceCSRF/attachSession if it went through the normal chain — no
// Origin, a non-JSON Content-Type, no session cookie, exactly what a real
// MCP client sends per §A2 ("a real MCP client is not a browser and sends
// none") — still reaches the installed stub, unrejected, with no session
// attached to its context.
func TestHandler_mcpMountedBypassesCSRFAndSession(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)

	var (
		reached    bool
		gotCookie  string
		gotContext bool
	)
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		gotCookie = r.Header.Get("Cookie")
		_, gotContext = UserFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	srv.SetMCP(stub)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "text/plain") // would 415 through enforceCSRF
	// No Origin (would 403 through enforceCSRF) and no Cookie (a real MCP
	// client sends none).

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatalf("stub was never reached; status=%d body=%s — /mcp went through enforceCSRF/attachSession instead of bypassing them", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 from the stub", rec.Code)
	}
	if gotCookie != "" {
		t.Errorf("stub saw Cookie: %q, want none forwarded", gotCookie)
	}
	if gotContext {
		t.Error("request context carried an authenticated user — attachSession must not run on /mcp")
	}

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q — securityHeaders must wrap /mcp too, success or rejection alike", header, got, want)
		}
	}
}

// TestHandler_mcpMountedGETReachesStubNotSPA is observation 10's unit-level
// counterpart: GET /mcp with a handler installed must reach that handler —
// proving webui.Handler, which does not know /mcp, never intercepted the
// GET — and the handler's own 405 (an SDK MCP transport rejects GET; the
// stub here stands in for it) is what comes back, not the SPA shell's 200
// and text/html.
func TestHandler_mcpMountedGETReachesStubNotSPA(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	srv.SetMCP(stub)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "http://mocker.local/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 from the stub (not the SPA shell); body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, looks like the SPA shell answered this request instead of the stub", ct)
	}
}

// TestHandler_otherPathsStillRequireCSRFContentType is the branch-widening
// guard: the exact request shape TestHandler_mcpMountedBypassesCSRFAndSession
// proves bypasses enforceCSRF for /mcp must still 415 on a normal admin
// route. If handlerBypass, or Handler()'s path check, ever grew to cover
// more than /mcp, this is the test that would catch it.
func TestHandler_otherPathsStillRequireCSRFContentType(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	srv.SetMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/workspaces", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "text/plain")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415 (enforceCSRF's Content-Type check) on a normal admin route", rec.Code)
	}
}

// TestHandler_otherPathsStillRequireCSRFOrigin is
// TestHandler_otherPathsStillRequireCSRFContentType's sibling for the 403
// half of enforceCSRF (a correct Content-Type but no Origin/Referer).
func TestHandler_otherPathsStillRequireCSRFOrigin(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	srv.SetMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/workspaces", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	// Deliberately no Origin and no Referer.

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (enforceCSRF's Origin/Referer check) on a normal admin route", rec.Code)
	}
}
