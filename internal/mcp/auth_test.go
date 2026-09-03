package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// TestEndpoint_noAuthorizationHeaderIs401 is observation 1's unit-level
// counterpart with a key configured (a build where MOCKER_MCP_KEY is unset
// is covered separately — /mcp is not even mounted then, tested at the
// admin-package level by internal/admin/mcp_mount_test.go, since that is
// where the mount decision lives).
func TestEndpoint_noAuthorizationHeaderIs401(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestEndpoint_wrongKeyIs401AndNeverLeaksTheRealKey is observation 2 of §G,
// at the unit level: a wrong key must be rejected, and the rejection body
// must never contain the configured key, never mocker's HTML shell, and
// never a hint about the key's length (§A2's own wording) — so this checks
// a body far shorter than the key AND that it does not contain the key
// itself, rather than just "does not equal the key".
func TestEndpoint_wrongKeyIs401AndNeverLeaksTheRealKey(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"Authorization": "Bearer this-is-not-the-configured-key-at-all",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, testKey) {
		t.Errorf("response body contains the configured key: %s", body)
	}
	if strings.Contains(body, "<!doctype") || strings.Contains(body, "<html") {
		t.Errorf("response body looks like mocker's HTML shell, want a plain small body: %s", body)
	}
	if len(body) > 200 {
		t.Errorf("response body is %d bytes, want a small plain rejection: %s", len(body), body)
	}
}

// TestEndpoint_correctKeyReachesSDKHandler is the guard's whole reason to
// exist proven positively: the right key, with a well-formed request, must
// pass through to the SDK's own dispatch rather than being rejected by
// this package's own check.
func TestEndpoint_correctKeyReachesSDKHandler(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"Authorization": "Bearer " + testKey,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the SDK's own answer to tools/list); body=%s", rec.Code, rec.Body.String())
	}
}

// TestEndpoint_originNamingDifferentHostIsRejected covers §A2's Origin
// rule's rejecting half: an Origin naming somewhere other than
// cfg.AdminHost must be refused even with a correct bearer key — the
// bearer key alone is not the ENTIRE Origin story, this project keeps both
// checks.
func TestEndpoint_originNamingDifferentHostIsRejected(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"Authorization": "Bearer " + testKey,
		"Origin":        "http://evil.example",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// TestEndpoint_originNamingAdminHostIsAllowed is the positive control for
// the same rule: an Origin that DOES name cfg.AdminHost (a scheme and an
// optional port are irrelevant — only Hostname() is compared) must not be
// rejected on Origin grounds.
func TestEndpoint_originNamingAdminHostIsAllowed(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"Authorization": "Bearer " + testKey,
		"Origin":        "https://mocker.local:8443",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with an Origin naming the admin host; body=%s", rec.Code, rec.Body.String())
	}
}

// TestEndpoint_noOriginIsAllowed is §A2's own justification made concrete:
// "a real MCP client is not a browser and sends none" — a request with no
// Origin header at all must not be rejected on Origin grounds (it may still
// be rejected on other grounds this test controls for by supplying the
// right key).
func TestEndpoint_noOriginIsAllowed(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"Authorization": "Bearer " + testKey,
		// Deliberately no Origin.
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with no Origin at all; body=%s", rec.Code, rec.Body.String())
	}
}
