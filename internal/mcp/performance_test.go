package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Separate registration from the real authenticated SDK request path: otherwise
// a benchmark of callTool hides how much time is spent rebuilding the server.
func BenchmarkEndpointConstruction(b *testing.B) {
	for b.Loop() {
		New(&recordingCaller{status: 200, body: []byte(`{}`)}, testKey, testConfig(), nil)
	}
}

func BenchmarkToolFixtureCall(b *testing.B) {
	fixture := newToolFixture(&recordingCaller{status: 200, body: []byte(`{}`)})
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_backend_capabilities","arguments":{}}}`))
		req.Header.Set("Authorization", "Bearer "+testKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"result"`) {
			b.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

// Warm registration separately so this measures the per-call helper cost.
func BenchmarkToolFixture(b *testing.B) {
	newToolFixture(&recordingCaller{status: 200, body: []byte(`{}`)})
	for b.Loop() {
		newToolFixture(&recordingCaller{status: 200, body: []byte(`{}`)})
	}
}
