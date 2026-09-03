package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// csrf_upgrade_test.go is P6d's D11 (decisions.md mocker-p6d-websocket;
// DESIGN §30.10): a GET that asks for a WebSocket upgrade is state-changing
// for the CSRF guard, so it goes through enforceCSRF's three checks and is
// refused before any route behind it exists. The first check to fail on a
// handshake is the content type — a WebSocket handshake carries no JSON —
// so the observable refusal is 415, and a plain GET stays untouched.
func TestEnforceCSRF_treatsAWebSocketUpgradeGETAsStateChanging(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	reached := false
	h := srv.enforceCSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	plain := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, plain)
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("a plain GET must pass the guard untouched, got %d reached=%v", rec.Code, reached)
	}

	reached = false
	up := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	up.Header.Set("Connection", "keep-alive, Upgrade")
	up.Header.Set("Upgrade", "WebSocket")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, up)
	if rec.Code != http.StatusUnsupportedMediaType || reached {
		t.Fatalf("a GET with Connection: Upgrade / Upgrade: websocket must be refused by the CSRF chain (415, no JSON content type), got %d reached=%v", rec.Code, reached)
	}

	// Upgrade without the Connection token is not a handshake (RFC 6455
	// §4.2.1 requires both) and stays an ordinary GET.
	reached = false
	half := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	half.Header.Set("Upgrade", "websocket")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, half)
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("Upgrade without Connection: upgrade is not a handshake, got %d reached=%v", rec.Code, reached)
	}
}
