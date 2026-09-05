package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestEndpointRefusals_carryTheNamedCode is A18 review finding 10: the gate
// document, the embedded guide and api/openapi.json promised seven named 400
// codes for a function's and a hook's write-time refusals, and the server
// answered every one of them as `bad_request` with prose — so an agent that
// branched on the code, as the guide told it to, never matched. Every
// promised code, through the real route, once.
func TestEndpointRefusals_carryTheNamedCode(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	for _, tc := range []struct {
		code string
		body map[string]any
	}{
		{"bad_function", map[string]any{"method": "POST", "path": "/a", "function": "return 200, }"}},
		{"function_and_body", map[string]any{"method": "POST", "path": "/b", "body": map[string]any{"a": 1}, "function": "return 200, {}"}},
		{"function_on_stream", map[string]any{"method": "GET", "path": "/c", "kind": "sse", "function": "return 200, {}",
			"stream": map[string]any{"tick": map[string]any{"intervalMs": 100, "lua": "return {}"}}}},
		{"tick_lua_and_schema", map[string]any{"method": "GET", "path": "/d", "kind": "sse",
			"stream": map[string]any{"tick": map[string]any{"intervalMs": 100, "lua": "return {}", "schema": map[string]any{"type": "object"}}}}},
		{"on_frame_on_sse", map[string]any{"method": "GET", "path": "/e", "kind": "sse",
			"stream": map[string]any{"onFrame": "return nil", "tick": map[string]any{"intervalMs": 100, "lua": "return {}"}}}},
		{"on_frame_and_reactive", map[string]any{"method": "GET", "path": "/f", "kind": "ws",
			"stream": map[string]any{"onFrame": "return nil", "reactive": []any{map[string]any{"data": map[string]any{}}}}}},
		{"on_frame_and_echo", map[string]any{"method": "GET", "path": "/g", "kind": "ws",
			"stream": map[string]any{"onFrame": "return nil", "echo": true}}},
		{"bad_function", map[string]any{"method": "GET", "path": "/h", "kind": "ws",
			"stream": map[string]any{"onFrame": "return }"}}},
	} {
		t.Run(tc.code+" "+tc.body["path"].(string), func(t *testing.T) {
			req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", wsID), tc.body, cookie, csrfToken)
			rec := ts.do(req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v; raw = %s", err, rec.Body.String())
			}
			if env.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q (message: %s)", env.Error.Code, tc.code, env.Error.Message)
			}
		})
	}
}
