package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

// probeTargetHost points a probed workspace's URL at a real httptest.Server
// this test controls, WITHOUT any real DNS: workspaceURL always builds a
// workspace's host from cfg (AdminHost in path mode, BaseDomain+slug in host
// mode), never from the raw incoming request's hostname, so the only way to
// make the SERVER actually dial back into this test process is to point
// cfg.AdminHost at a loopback literal (no lookup required) and switch to path
// routing, then carry the target's real port through the probe request's own
// Host header — workspaceURL borrows ONLY the port from that.
//
// This mutates ts.cfg in place (Server holds cfg as a pointer, not a copy —
// see [admin.New]'s own doc comment), so every test below builds its own
// *testServer and never shares one across subtests running in parallel.
func probeTargetHost(ts *testServer) {
	ts.cfg.Routing = config.RoutingPath
	ts.cfg.AdminHost = "127.0.0.1"
}

// probeRequest builds POST /api/workspaces/{id}/probe against target's real
// address: jsonRequest's own Origin default ("http://mocker.local") no
// longer matches cfg.AdminHost once probeTargetHost has run, so it is
// overwritten here to the same loopback literal, and Host is set to target's
// listener address so workspaceURL's port-borrowing lands on the real port
// this test's fake target server is actually listening on.
func probeRequest(t *testing.T, id int64, cookie *http.Cookie, csrfToken string, target *httptest.Server) *http.Request {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://127.0.0.1/api/workspaces/%d/probe", id),
		map[string]any{}, cookie, csrfToken)
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Host = target.Listener.Addr().String()
	return req
}

func decodeProbeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode probe response: %v; body = %s", err, rec.Body.String())
	}
	return body
}

// TestProbeWorkspace_ok is the whole point of the slice end to end: a target
// that answers the real health shape for the SAME workspace is reported
// "ok", with the workspace and revision it actually returned.
func TestProbeWorkspace_ok(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, slug := ts.createWorkspace(t, "Alex", "Demo")

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"workspace":%q,"revision":5}`, slug)
	}))
	defer target.Close()
	probeTargetHost(ts)

	rec := ts.do(probeRequest(t, int64(id), cookie, csrfToken, target))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST probe: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := decodeProbeResponse(t, rec)
	if body["kind"] != "ok" {
		t.Fatalf("kind = %v, want %q; body = %s", body["kind"], "ok", rec.Body.String())
	}
	if body["workspace"] != slug {
		t.Errorf("workspace = %v, want %q", body["workspace"], slug)
	}
	if body["revision"] != float64(5) {
		t.Errorf("revision = %v, want 5", body["revision"])
	}
}

// TestProbeWorkspace_wrongWorkspace covers the wildcard-DNS/misrouted-proxy
// case: a target that answers 2xx with SOMEONE ELSE'S slug must not be
// reported as "ok".
func TestProbeWorkspace_wrongWorkspace(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"workspace":"someone-else","revision":1}`))
	}))
	defer target.Close()
	probeTargetHost(ts)

	rec := ts.do(probeRequest(t, int64(id), cookie, csrfToken, target))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST probe: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := decodeProbeResponse(t, rec)
	if body["kind"] != "wrong-workspace" {
		t.Fatalf("kind = %v, want %q; body = %s", body["kind"], "wrong-workspace", rec.Body.String())
	}
	if body["workspace"] != "someone-else" {
		t.Errorf("workspace = %v, want %q", body["workspace"], "someone-else")
	}
}

// TestProbeWorkspace_httpError covers a target that answers, but with a
// non-2xx status — the mock plane's own health route down for whatever
// reason.
func TestProbeWorkspace_httpError(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"not ready"}}`))
	}))
	defer target.Close()
	probeTargetHost(ts)

	rec := ts.do(probeRequest(t, int64(id), cookie, csrfToken, target))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST probe: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := decodeProbeResponse(t, rec)
	if body["kind"] != "http-error" {
		t.Fatalf("kind = %v, want %q; body = %s", body["kind"], "http-error", rec.Body.String())
	}
	if body["status"] != float64(503) {
		t.Errorf("status = %v, want 503", body["status"])
	}
	if body["message"] != "not ready" {
		t.Errorf("message = %v, want %q", body["message"], "not ready")
	}
}

// TestProbeWorkspace_networkError covers a target that refuses the
// connection outright: this route's OWN status must stay 200 — the target's
// failure is reported inside the body, exactly like the "ok" case above,
// never surfaced as this route's own 5xx.
func TestProbeWorkspace_networkError(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	target.Close() // closed before the probe request is ever sent: connection refused.
	probeTargetHost(ts)

	rec := ts.do(probeRequest(t, int64(id), cookie, csrfToken, target))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST probe: status = %d, want 200 (the target's failure belongs IN the body); body = %s", rec.Code, rec.Body.String())
	}
	body := decodeProbeResponse(t, rec)
	if body["kind"] != "network-error" {
		t.Fatalf("kind = %v, want %q; body = %s", body["kind"], "network-error", rec.Body.String())
	}
}

// TestProbeWorkspace_unauthenticated: no session cookie at all must answer
// 401 and never attempt an outbound dial.
func TestProbeWorkspace_unauthenticated(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	_, _, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/probe", int64(id)),
		map[string]any{}, nil, "")
	rec := ts.do(req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST probe with no session: status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

// TestProbeWorkspace_missingCSRF: a valid session but no CSRF token must
// answer 403 — the whole reason this route is a POST rather than a GET (see
// probe_handlers.go's own package doc).
func TestProbeWorkspace_missingCSRF(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/probe", int64(id)),
		map[string]any{}, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST probe with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

// TestProbeWorkspace_notFound: an id that parses but names no workspace
// answers 404, exactly like every other {id}-scoped route.
func TestProbeWorkspace_notFound(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")

	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/999999/probe",
		map[string]any{}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST probe for a nonexistent workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}
