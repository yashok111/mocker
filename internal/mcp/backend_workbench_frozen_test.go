package mcp

import (
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"
)

// TestFrozenBackendWorkbenchIsInvisibleOverMCP pins the 2026-10-12 freeze on
// the agent's side: with config.Config.BackendWorkbenchFrozen set, nothing an
// MCP client can list or read names the Backend Workbench — not the tools,
// not the initialize instructions, not get_guide's description, topic list or
// texts — while every other tool is still there.
func TestFrozenBackendWorkbenchIsInvisibleOverMCP(t *testing.T) {
	caller := &fakeCaller{status: http.StatusOK, body: []byte(`{}`)}
	auth := map[string]string{"Authorization": "Bearer " + testKey}
	frozenCfg := testConfig()
	frozenCfg.BackendWorkbenchFrozen = true
	frozen := New(caller, testKey, frozenCfg, nil).Handler()
	thawed := New(caller, testKey, testConfig(), nil).Handler()

	type listedTool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema any    `json:"inputSchema"`
	}
	list := func(h http.Handler) []listedTool {
		rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, auth)
		var env struct {
			Result struct {
				Tools []listedTool `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
		}
		return env.Result.Tools
	}

	frozenTools, thawedTools := list(frozen), list(thawed)
	kept := map[string]bool{}
	for _, tool := range frozenTools {
		kept[tool.Name] = true
	}
	// The two artifact-snapshot reads are registered by the workbench's own
	// tool group (tools_backend_*artifacts.go) to verify a backend pin, so
	// they go with it although their routes are API-design routes.
	snapshotReads := map[string]bool{"get_api_artifact_snapshot": true, "get_design_scenario_artifact_snapshot": true}
	hidden := 0
	for _, tool := range thawedTools {
		switch {
		case kept[tool.Name]:
			delete(kept, tool.Name)
		case strings.Contains(tool.Name, "backend") || snapshotReads[tool.Name]:
			hidden++
		default:
			t.Errorf("frozen tools/list lost %s, which is not a workbench tool", tool.Name)
		}
	}
	if hidden < 100 {
		t.Fatalf("only %d workbench tools hidden; the freeze or this test is broken", hidden)
	}
	for name := range kept {
		t.Errorf("frozen tools/list has %s, which the thawed one does not", name)
	}
	for _, tool := range frozenTools {
		raw, _ := json.Marshal(tool)
		if strings.Contains(strings.ToLower(string(raw)), "backend-") || strings.Contains(tool.Name, "backend") {
			t.Errorf("frozen tools/list still shows the workbench in %s", tool.Name)
		}
	}

	rec := doMCP(t, frozen, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}`, auth)
	var init struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &init); err != nil || init.Result.Instructions == "" {
		t.Fatalf("initialize: %v; body=%s", err, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(init.Result.Instructions), "backend") {
		t.Errorf("frozen initialize instructions mention the workbench: %s", init.Result.Instructions)
	}

	for _, tc := range []struct {
		name, args, wantErr string
	}{
		{"get_guide", `{"topic":"backend-import"}`, "unknown topic"},
		{"describe_tool", `{"name":"get_backend_capabilities"}`, "get_backend_capabilities"},
		{"get_backend_capabilities", `{}`, "get_backend_capabilities"},
	} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tc.name + `","arguments":` + tc.args + `}}`
		rec := doMCP(t, frozen, body, auth)
		text := rec.Body.String()
		if !strings.Contains(text, `"isError":true`) && !strings.Contains(text, `"error":`) {
			t.Errorf("frozen %s %s did not fail: %s", tc.name, tc.args, text)
		}
		if !strings.Contains(text, tc.wantErr) {
			t.Errorf("frozen %s %s: error does not mention %q: %s", tc.name, tc.args, tc.wantErr, text)
		}
	}

	for _, topic := range []string{"overview", "tools", "cookbook", "http", "design"} {
		rec := doMCP(t, frozen, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_guide","arguments":{"topic":"`+topic+`"}}}`, auth)
		var env struct {
			Result struct {
				StructuredContent GetGuideOutput `json:"structuredContent"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("get_guide %s: %v; body=%.300s", topic, err, rec.Body.String())
		}
		out := env.Result.StructuredContent
		if out.Markdown == "" {
			t.Fatalf("get_guide %s: empty markdown; body=%.300s", topic, rec.Body.String())
		}
		lower := strings.ToLower(strings.ReplaceAll(out.Markdown, "backend team", ""))
		if strings.Contains(lower, "backend") {
			t.Errorf("frozen get_guide %s still mentions the workbench", topic)
		}
		for _, listed := range out.Topics {
			if strings.HasPrefix(listed, "backend-") {
				t.Errorf("frozen get_guide %s lists topic %s", topic, listed)
			}
		}
	}
}
