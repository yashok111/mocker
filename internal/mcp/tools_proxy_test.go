package mcp

import (
	"strings"
	"testing"
)

func TestProxyToolsUseAdminRoutes(t *testing.T) {
	for _, tc := range []struct{ name, args, method, path, body string }{
		{"get_workspace_proxy", `{"workspaceId":4}`, "GET", "/api/workspaces/4/proxy", `{"config":{"version":0,"mode":"off","upstream":"","timeoutSeconds":15,"forwardAuth":false,"forwardCookies":false,"overwrite":"last","captureEntities":false,"operations":{}},"allowedOrigins":[]}`},
		{"list_proxy_recordings", `{"workspaceId":4}`, "GET", "/api/workspaces/4/proxy/recordings", `{"items":[]}`},
		{"delete_proxy_recording", `{"workspaceId":4,"recordingId":2,"version":3}`, "DELETE", "/api/workspaces/4/proxy/recordings/2", `{"ok":true}`},
		{"clear_proxy_recordings", `{"workspaceId":4,"version":3,"confirmSlug":"demo"}`, "POST", "/api/workspaces/4/proxy/recordings/clear", `{"ok":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(tc.body)}
			_, msg := callTool(t, calls, tc.name, tc.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != tc.method || calls.path != tc.path {
				t.Fatalf("%s %s", calls.method, calls.path)
			}
			if strings.Contains(tc.name, "clear") && !strings.Contains(string(calls.sent), `"confirmSlug":"demo"`) {
				t.Fatalf("body %s", calls.sent)
			}
		})
	}
}
