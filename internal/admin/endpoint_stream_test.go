package admin_test

// endpoint_stream_test.go covers P6b's admin surface (decisions.md
// mocker-p6b-sse-mock D6, D13): an sse row through POST .../endpoints, its
// kind and document on the list, the by-name refusals the admin writer
// answers (the MCP writer reaches the identical validator through the same
// route), a PUT that drops the kind, and the preview route's own refusals.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errorMessage decodes the envelope's message, so an assertion compares
// the server's own words rather than their JSON-escaped form.
func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", rec.Body.String(), err)
	}
	return env.Error.Message
}

func streamDoc(intervalMs int) map[string]any {
	return map[string]any{
		"timeline": map[string]any{"frames": []map[string]any{{"delayMs": 0, "event": "hello", "data": map[string]any{"n": 1}}}},
		"tick":     map[string]any{"intervalMs": intervalMs, "schema": map[string]any{"type": "object"}},
	}
}

func TestEndpoints_streamRowThroughTheAdminWriter(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrf, wsFloat, _ := ts.createWorkspace(t, "stream-ep", "ws")
	wsID := int64(wsFloat)
	base := fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", wsID)

	// Create.
	rec := ts.do(jsonRequest(t, http.MethodPost, base, map[string]any{
		"method": "GET", "path": "/events", "kind": "sse", "stream": streamDoc(500),
	}, cookie, csrf))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sse: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID          int64          `json:"id"`
		Kind        string         `json:"kind"`
		Stream      map[string]any `json:"stream"`
		EditVersion int64          `json:"editVersion"`
		Responses   map[string]any `json:"responses"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Kind != "sse" || created.Stream == nil || len(created.Responses) != 0 {
		t.Fatalf("created = %+v, want kind sse with a stream and no responses", created)
	}

	// List carries it.
	rec = ts.do(jsonRequest(t, http.MethodGet, base, nil, cookie, ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kind":"sse"`) || !strings.Contains(rec.Body.String(), `"intervalMs":500`) {
		t.Fatalf("list: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Refusals by name, on this writer.
	refusals := []struct {
		name string
		body map[string]any
		want string
	}{
		{"tick below the floor", map[string]any{"method": "GET", "path": "/e1", "kind": "sse", "stream": streamDoc(50)}, "below the floor of 100"},
		{"sse with a body", map[string]any{"method": "GET", "path": "/e2", "kind": "sse", "body": map[string]any{}, "stream": streamDoc(500)}, "takes no status, body or mediaType"},
		{"sse with POST", map[string]any{"method": "POST", "path": "/e3", "kind": "sse", "stream": streamDoc(500)}, "requires method GET"},
		{"http with a stream", map[string]any{"method": "GET", "path": "/e4", "stream": streamDoc(500)}, `only allowed with kind "sse"`},
		// P6d: kind ws is served; it is strict like sse, and its inbound
		// fields are refused by name on sse.
		{"ws with a body", map[string]any{"method": "GET", "path": "/e5", "kind": "ws", "body": map[string]any{}, "stream": streamDoc(500)}, "takes no status, body or mediaType"},
		{"echo on sse", map[string]any{"method": "GET", "path": "/e6", "kind": "sse", "stream": map[string]any{"tick": map[string]any{"intervalMs": 500, "schema": map[string]any{"type": "object"}}, "echo": true}}, "echo has no meaning on kind"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			rec := ts.do(jsonRequest(t, http.MethodPost, base, tc.body, cookie, csrf))
			if rec.Code != http.StatusBadRequest || !strings.Contains(errorMessage(t, rec), tc.want) {
				t.Fatalf("status = %d; body = %s; want 400 naming %q", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}

	// A PUT that resends everything but the kind is refused, never
	// downgraded to an http row that silently stops streaming.
	rec = ts.do(jsonRequest(t, http.MethodPut, fmt.Sprintf("%s/%d", base, created.ID), map[string]any{
		"method": "GET", "path": "/events", "activeStatus": 200, "responses": map[string]any{},
		"stream": streamDoc(500), "editVersion": created.EditVersion,
	}, cookie, csrf))
	if rec.Code != http.StatusBadRequest || !strings.Contains(errorMessage(t, rec), `only allowed with kind "sse"`) {
		t.Fatalf("PUT without kind: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	// And one that keeps it edits the document.
	rec = ts.do(jsonRequest(t, http.MethodPut, fmt.Sprintf("%s/%d", base, created.ID), map[string]any{
		"method": "GET", "path": "/events", "activeStatus": 200, "kind": "sse",
		"stream": streamDoc(1000), "editVersion": created.EditVersion,
	}, cookie, csrf))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"intervalMs":1000`) {
		t.Fatalf("PUT with kind: status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

// TestEndpointPreview_refusals: the preview route validates like the writer
// (the same message for the same draft), refuses an http draft, and answers
// 503 when no stream previewer is wired — newTestServer wires none.
func TestEndpointPreview_refusals(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrf, wsFloat, _ := ts.createWorkspace(t, "stream-preview", "ws")
	url := fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints/preview", int64(wsFloat))

	rec := ts.do(jsonRequest(t, http.MethodPost, url, map[string]any{
		"method": "GET", "path": "/e", "kind": "sse", "stream": streamDoc(500),
	}, cookie, csrf))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no previewer wired: status = %d; body = %s, want 503", rec.Code, rec.Body.String())
	}
	rec = ts.do(jsonRequest(t, http.MethodPost, url, map[string]any{
		"method": "GET", "path": "/e", "kind": "sse", "stream": streamDoc(500),
	}, nil, ""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: status = %d, want 401", rec.Code)
	}
}
