// tools_probe_test.go covers probe_workspace, A4's first tool
// (decisions.md mocker-a4-mcp-reach D1, D5): the JSON projection round trip
// for every one of serverProbeView's five Kind values, that no confirmSlug
// field exists on the input struct (D5's own Fails-if), and that the tool
// makes no body request (probe_handlers.go's own handler reads no body).
package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestProbeWorkspace_ok is the happy-path shape: a 2xx health body naming
// this workspace's own slug.
func TestProbeWorkspace_ok(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"kind":"ok","workspace":"widgets-co","revision":7}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleProbeWorkspace(lb)(opsTestCtx(), nil, ProbeWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleProbeWorkspace: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/7/probe" {
		t.Errorf("call = %s %s, want POST /api/workspaces/7/probe", fc.calls[0].method, fc.calls[0].path)
	}
	if fc.calls[0].body != "" {
		t.Errorf("call body = %q, want empty — the route reads no body", fc.calls[0].body)
	}
	if out.Kind != "ok" || out.Workspace != "widgets-co" || out.Revision != 7 {
		t.Errorf("out = %+v, want kind=ok workspace=widgets-co revision=7", out)
	}
}

// TestProbeWorkspace_wrongWorkspace covers the routing-mismatch shape: a
// 2xx health body naming a DIFFERENT workspace than the one asked about.
func TestProbeWorkspace_wrongWorkspace(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"kind":"wrong-workspace","workspace":"some-other-slug"}`},
	}}
	_, out, err := handleProbeWorkspace(newLoopback(fc))(opsTestCtx(), nil, ProbeWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleProbeWorkspace: %v", err)
	}
	if out.Kind != "wrong-workspace" || out.Workspace != "some-other-slug" {
		t.Errorf("out = %+v, want kind=wrong-workspace workspace=some-other-slug", out)
	}
	if out.Revision != 0 {
		t.Errorf("Revision = %d, want 0 (absent on this Kind)", out.Revision)
	}
}

// TestProbeWorkspace_httpError, TestProbeWorkspace_timeout and
// TestProbeWorkspace_networkError cover the three remaining Kind values —
// each still a 200 from THIS route (handleProbeWorkspace's own doc comment:
// the target's own failure is reported inside the body, never as this
// route's own HTTP status), so lb.call sees no non-2xx to turn into a tool
// error on any of them.
func TestProbeWorkspace_httpError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"kind":"http-error","status":500,"message":"boom"}`},
	}}
	_, out, err := handleProbeWorkspace(newLoopback(fc))(opsTestCtx(), nil, ProbeWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleProbeWorkspace: %v", err)
	}
	if out.Kind != "http-error" || out.Status != 500 || out.Message != "boom" {
		t.Errorf("out = %+v, want kind=http-error status=500 message=boom", out)
	}
}

func TestProbeWorkspace_timeout(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"kind":"timeout"}`}}}
	_, out, err := handleProbeWorkspace(newLoopback(fc))(opsTestCtx(), nil, ProbeWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleProbeWorkspace: %v", err)
	}
	if out.Kind != "timeout" {
		t.Errorf("Kind = %q, want timeout", out.Kind)
	}
}

func TestProbeWorkspace_networkError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"kind":"network-error"}`}}}
	_, out, err := handleProbeWorkspace(newLoopback(fc))(opsTestCtx(), nil, ProbeWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleProbeWorkspace: %v", err)
	}
	if out.Kind != "network-error" {
		t.Errorf("Kind = %q, want network-error", out.Kind)
	}
}

// TestProbeWorkspace_404IsToolError is the one genuine transport-level
// failure this route can answer: an unknown workspace id.
func TestProbeWorkspace_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleProbeWorkspace(newLoopback(fc))(opsTestCtx(), nil, ProbeWorkspaceInput{WorkspaceID: 999})
	if err == nil {
		t.Fatal("handleProbeWorkspace returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// TestProbeWorkspaceInput_hasNoConfirmSlugField is D5's own Fails-if,
// enforced structurally: probe destroys nothing, so the input schema must
// carry no confirmSlug field at all, unlike decide_resource's and
// reset_resource_data's own input structs.
func TestProbeWorkspaceInput_hasNoConfirmSlugField(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(ProbeWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("marshal ProbeWorkspaceInput: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "confirmslug") {
		t.Errorf("ProbeWorkspaceInput carries a confirmSlug-shaped field: %s (D5: a probe destroys nothing)", raw)
	}
}

// TestProbeWorkspace_registeredWithHonestAnnotations mirrors
// tools_traffic_test.go's own TestAddTrafficTools_registersThreeToolsWith
// HonestAnnotations: a real tools/list round trip through the full
// registered surface, asserting PRESENCE and the tool's own annotations —
// read-only and idempotent, matching what handleProbeWorkspace actually
// does (mocker dialling a target and reporting what happened, never
// changing anything itself), even though the admin route it wraps is a
// POST for CSRF reasons unrelated to what this tool changes.
func TestProbeWorkspace_registeredWithHonestAnnotations(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t}
	ep := New(fc, testKey, testConfig(), nil)
	h := ep.Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}

	var found bool
	for _, tool := range env.Result.Tools {
		if tool.Name != "probe_workspace" {
			continue
		}
		found = true
		if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Errorf("probe_workspace annotations = %+v, want ReadOnlyHint and IdempotentHint both true", tool.Annotations)
		}
	}
	if !found {
		t.Fatal("probe_workspace not found in tools/list")
	}
}
