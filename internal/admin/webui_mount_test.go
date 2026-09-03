package admin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// distDir points at the SAME built output [admin.Server.Handler] embeds
// through webui.Dist, read straight off disk so these tests compare against
// the real bytes rather than a guess at what `make ui` produced. A path
// relative to this package (not an absolute one) is what keeps `go test
// ./...` working regardless of the checkout's location.
const distDir = "../webui/dist"

// realIndexHTML reads dist/index.html so tests can assert byte-for-byte that
// the SPA fallback served THIS content, not merely "some 200".
func realIndexHTML(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(distDir, "index.html"))
	if err != nil {
		t.Fatalf("read dist/index.html: %v (internal/webui/dist must hold a real build for these tests)", err)
	}
	return b
}

// realDistBuilt reports whether internal/webui/dist holds a real UI build
// rather than the placeholder .gitkeep a fresh clone ships with. Tests below
// that compare a response against dist/index.html or dist/assets/* call this
// first and skip rather than fail when it's false: `go test ./cmd/...
// ./internal/...` (== `make test`) must stay green on a clone where nobody
// has run `make ui` yet — only `make ui` and the Docker build ever need
// node, per the build contract these tests must not contradict.
func realDistBuilt(t *testing.T) bool {
	t.Helper()
	info, err := os.Stat(filepath.Join(distDir, "index.html"))
	return err == nil && !info.IsDir()
}

// realAsset returns the name and bytes of one file under dist/assets/,
// picked at test time rather than hard-coded: Vite content-hashes every
// asset filename, so a literal name here would break the next time anyone
// runs `make ui`.
func realAsset(t *testing.T) (name string, body []byte) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(distDir, "assets"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("read dist/assets: %v (internal/webui/dist must hold a real build for these tests)", err)
	}
	name = entries[0].Name()
	body, err = os.ReadFile(filepath.Join(distDir, "assets", name))
	if err != nil {
		t.Fatalf("read dist/assets/%s: %v", name, err)
	}
	return name, body
}

// TestHandler_unmatchedPath_servesSPAFallback is TESTS-THAT-MUST-EXIST case
// 1: any GET the admin mux doesn't own falls to the SPA shell, not a 404 —
// client-side routing means /w/7 is a real address a person can reload.
func TestHandler_unmatchedPath_servesSPAFallback(t *testing.T) {
	t.Parallel()
	if !realDistBuilt(t) {
		t.Skip("internal/webui/dist has no real build; run `make ui` first")
	}
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/nope", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(realIndexHTML(t)) {
		t.Error("body does not match dist/index.html")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

// case 2: a Vite-built asset gets the right Content-Type and an immutable,
// year-long Cache-Control — content-hashed filenames never change body at
// the same name, so caching forever is safe.
func TestHandler_assetPath_immutableCacheAndContentType(t *testing.T) {
	t.Parallel()
	if !realDistBuilt(t) {
		t.Skip("internal/webui/dist has no real build; run `make ui` first")
	}
	ts := newTestServer(t)
	name, body := realAsset(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/assets/"+name, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(body) {
		t.Error("body does not match dist/assets/" + name)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want it to contain %q", cc, "immutable")
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Error("Content-Type is empty, want one derived from the file extension")
	}
}

// case 3: an /api/ path no route matches must keep answering net/http's own
// plain-text 404 — never the SPA's HTML, and never a JSON catch-all either
// (a JSON catch-all is exactly what would swallow the 405s cases 4-6 pin).
func TestHandler_unknownAPIPath_staysPlain404(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/api/definitely-nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "html") {
		t.Errorf("Content-Type = %q, want NOT html (the SPA must not answer this path)", ct)
	}
	if body := rec.Body.String(); strings.Contains(body, "<html") || strings.Contains(body, "root") {
		t.Errorf("body = %q, looks like the SPA shell leaked through", body)
	}
}

// cases 4-6 are the regression tests for the defect this mount's own design
// note measured: registering the UI as a mux pattern turns a real route's
// 405 into a 404, and answers a state-changing method on a GET-only route
// with the SPA and a 200. jsonRequest supplies Content-Type: application/json
// and a same-origin Origin — required for the two state-changing methods to
// even reach the mux, since enforceCSRF runs ahead of it (an unauthenticated
// request skips the token check itself, per [Server.enforceCSRF]'s own doc
// comment, but still needs those two headers to pass through to the mux at
// all).
func TestHandler_stateChangingMethodMismatches_stay405(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"PUT /api/workspaces (registered GET/POST only)", http.MethodPut, "/api/workspaces"},
		{"GET /api/auth/login (registered POST only)", http.MethodGet, "/api/auth/login"},
		{"POST /healthz (registered GET only)", http.MethodPost, "/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newTestServer(t)
			req := jsonRequest(t, tt.method, "http://mocker.local"+tt.path, nil, nil, "")
			rec := ts.do(req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// case 7.
func TestHandler_headRoot_okNoBody(t *testing.T) {
	t.Parallel()
	if !realDistBuilt(t) {
		t.Skip("internal/webui/dist has no real build; run `make ui` first")
	}
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodHead, "http://mocker.local/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %d bytes, want 0", rec.Body.Len())
	}
}

// case 9, through the real mount: a traversal attempt must never read
// anything outside webui.Dist. There is no file to leak in this repo's own
// dist/ that a legitimate route wouldn't already serve, so the meaningful
// assertion is that the response is the ordinary SPA fallback (200,
// index.html) rather than an error, a hang, or a directory listing.
func TestHandler_pathTraversal_neverEscapesDist(t *testing.T) {
	t.Parallel()
	if !realDistBuilt(t) {
		t.Skip("internal/webui/dist has no real build; run `make ui` first")
	}

	tests := []struct {
		name   string
		target string
	}{
		{"raw dot-dot", "http://mocker.local/../../etc/passwd"},
		{"percent-encoded dot-dot", "http://mocker.local/%2e%2e/%2e%2e/etc/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := newTestServer(t)
			rec := ts.do(httptest.NewRequest(http.MethodGet, tt.target, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (safe fallback); body = %s", rec.Code, rec.Body.String())
			}
			if rec.Body.String() != string(realIndexHTML(t)) {
				t.Error("body does not match dist/index.html — something other than the safe fallback was served")
			}
		})
	}
}

// case 10: every response the UI serves carries the CSP, script-src is
// spelled out, nothing carries 'unsafe-inline'/'unsafe-eval' except
// style-src, and connect-src names testConfig()'s BaseDomain with a
// wildcard port (":*" — a CSP host-source with no port matches only the
// scheme's default port, and mocker runs on 8080).
func TestHandler_cspHeader_onUIResponses(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	rec := ts.do(httptest.NewRequest(http.MethodGet, "http://mocker.local/nope", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q, want it to name script-src 'self' explicitly", csp)
	}
	for directive := range strings.SplitSeq(csp, "; ") {
		if strings.HasPrefix(directive, "style-src") {
			continue
		}
		if strings.Contains(directive, "unsafe-inline") || strings.Contains(directive, "unsafe-eval") {
			t.Errorf("directive %q carries unsafe-inline/unsafe-eval outside style-src", directive)
		}
	}
	if want := "connect-src 'self' http://*.mock.local:* https://*.mock.local:*"; !strings.Contains(csp, want) {
		t.Errorf("CSP = %q, want it to contain %q (testConfig()'s BaseDomain)", csp, want)
	}
}

// case 11, exercised specifically through the UI-wrapped chain (auth_config_
// test.go pins the config object's VALUES against varying cfgs; this pins
// that mounting the UI in front of mux did not disturb these two routes'
// SHAPE at all).
func TestHandler_apiMeAndLogin_answerUnchangedWithUIMounted(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	if csrfToken == "" {
		t.Fatal("login: csrfToken is empty")
	}

	req := jsonRequest(t, http.MethodGet, "http://mocker.local/api/me", nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("GET /api/me: Content-Type = %q, want application/json", ct)
	}
}
