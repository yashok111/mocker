package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ---- get_workspace ----

const workspaceGetFixture = `{
  "id": 7,
  "slug": "demo-workspace",
  "name": "Demo Workspace",
  "specId": 3,
  "ownerId": 1,
  "forkedFrom": null,
  "scenarioId": 9,
  "revision": 4,
  "settings": {
    "seed": 42,
    "basePath": "/api/v1",
    "listSize": 10,
    "nullRate": 0.1,
    "envelope": "response",
    "identity": {},
    "auth": {},
    "cors": {},
    "validateRequests": true,
    "delayMs": 50
  },
  "url": "https://demo-workspace.mocker.local",
  "createdAt": 1700000000,
  "updatedAt": 1700000100
}`

// TestGetWorkspace_happyPath is the anti-drift guard §C's compaction rule
// demands (see WorkspaceLine's identical guard in tools_ops_test.go): every
// field WorkspaceDetail projects is asserted against a fixture copied from
// workspaceView's own shape (workspace_handlers.go:42-63).
func TestGetWorkspace_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: workspaceGetFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleGetWorkspace(lb)(opsTestCtx(), nil, GetWorkspaceInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleGetWorkspace: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces/7" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7", fc.calls)
	}

	w := out.Workspace
	if w.ID != 7 || w.Slug != "demo-workspace" || w.Name != "Demo Workspace" {
		t.Errorf("Workspace = %+v, want id=7 slug=demo-workspace name=%q", w, "Demo Workspace")
	}
	if w.URL != "https://demo-workspace.mocker.local" {
		t.Errorf("URL = %q, want https://demo-workspace.mocker.local", w.URL)
	}
	if w.BasePath != "/api/v1" {
		t.Errorf("BasePath = %q, want /api/v1 (pulled out of settings)", w.BasePath)
	}
	if w.SpecID == nil || *w.SpecID != 3 {
		t.Errorf("SpecID = %v, want *3", w.SpecID)
	}
	if w.ScenarioID == nil || *w.ScenarioID != 9 {
		t.Errorf("ScenarioID = %v, want *9", w.ScenarioID)
	}
	if w.Revision != 4 {
		t.Errorf("Revision = %d, want 4", w.Revision)
	}
	if w.Seed != 42 || w.ListSize != 10 || w.NullRate != 0.1 || !w.ValidateRequests || w.DelayMs != 50 {
		t.Errorf("settings knobs = %+v, want seed=42 listSize=10 nullRate=0.1 validateRequests=true delayMs=50", w)
	}
	if w.Envelope == nil || *w.Envelope != "response" {
		t.Errorf("Envelope = %v, want *\"response\"", w.Envelope)
	}
	if w.CreatedAt != 1700000000 || w.UpdatedAt != 1700000100 {
		t.Errorf("timestamps = %+v, want createdAt=1700000000 updatedAt=1700000100", w)
	}
}

func TestGetWorkspace_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleGetWorkspace(newLoopback(fc))(opsTestCtx(), nil, GetWorkspaceInput{WorkspaceID: 999})
	if err == nil {
		t.Fatal("handleGetWorkspace returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestGetWorkspace_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleGetWorkspace(newLoopback(fc))(opsTestCtx(), nil, GetWorkspaceInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleGetWorkspace returned no error for a 500")
	}
}

// ---- list_endpoints ----

const endpointListFixture = `{
  "endpoints": [
    {
      "id": 5,
      "method": "POST",
      "path": "/widgets/bulk",
      "canonicalPath": "/widgets/bulk",
      "overrideOn": true,
      "routeOff": false,
      "activeStatus": 201,
      "responses": {
        "201": {"mode": "pinned", "mediaType": "application/json", "body": {"ok": true}, "recipes": {"$.id": {"kind": "sequence"}}},
        "400": {}
      },
      "listSize": {"min": 2, "max": 4},
      "delayMs": 25,
      "createdAt": 1700000000,
      "updatedAt": 1700000200
    }
  ]
}`

// TestListEndpoints_happyPath is list_endpoints' own anti-drift guard: every
// EndpointLine field is asserted against a fixture copied from endpointView
// (endpoint_handlers.go:37-49), including the "204's shape, not its bytes"
// rule ResponseShape enforces.
func TestListEndpoints_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: endpointListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListEndpoints(lb)(opsTestCtx(), nil, ListEndpointsInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListEndpoints: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/workspaces/7/endpoints" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7/endpoints", fc.calls)
	}
	if len(out.Endpoints) != 1 {
		t.Fatalf("len(Endpoints) = %d, want 1", len(out.Endpoints))
	}
	e := out.Endpoints[0]
	if e.ID != 5 || e.Method != http.MethodPost || e.Path != "/widgets/bulk" || e.CanonicalPath != "/widgets/bulk" {
		t.Errorf("Endpoints[0] = %+v, want id=5 method=POST path=/widgets/bulk", e)
	}
	if !e.OverrideOn || e.RouteOff || e.ActiveStatus != 201 {
		t.Errorf("on/off/status = %+v, want overrideOn=true routeOff=false activeStatus=201", e)
	}
	rs201, ok := e.Responses["201"]
	if !ok {
		t.Fatal(`Responses has no "201" entry`)
	}
	if rs201.Mode != "pinned" || rs201.MediaType != "application/json" || !rs201.HasBody || rs201.RecipeCount != 1 {
		t.Errorf("Responses[201] = %+v, want mode=pinned mediaType=application/json hasBody=true recipeCount=1", rs201)
	}
	rs400, ok := e.Responses["400"]
	if !ok {
		t.Fatal(`Responses has no "400" entry`)
	}
	if rs400.Mode != "generated" || rs400.HasBody || rs400.RecipeCount != 0 {
		t.Errorf("Responses[400] = %+v, want the documented default mode=generated, no body, no recipes", rs400)
	}
	if e.ListSize == nil || e.ListSize.Min != 2 || e.ListSize.Max != 4 {
		t.Errorf("ListSize = %v, want {2 4}", e.ListSize)
	}
	if e.DelayMs == nil || *e.DelayMs != 25 {
		t.Errorf("DelayMs = %v, want *25", e.DelayMs)
	}
	if e.CreatedAt != 1700000000 || e.UpdatedAt != 1700000200 {
		t.Errorf("timestamps = %+v, want createdAt=1700000000 updatedAt=1700000200", e)
	}

	// Never a pinned body byte, on a route whose whole point is guarding
	// against leaking one (§C's compaction rule) — the marshalled output
	// must not carry a "body" key anywhere.
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal ListEndpointsOutput: %v", err)
	}
	if strings.Contains(string(raw), `"body"`) || strings.Contains(string(raw), `"ok":true`) {
		t.Errorf("marshalled output leaks a pinned body: %s", raw)
	}
}

func TestListEndpoints_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleListEndpoints(newLoopback(fc))(opsTestCtx(), nil, ListEndpointsInput{WorkspaceID: 999})
	if err == nil {
		t.Fatal("handleListEndpoints returned no error for a 404")
	}
}

func TestListEndpoints_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleListEndpoints(newLoopback(fc))(opsTestCtx(), nil, ListEndpointsInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleListEndpoints returned no error for a 500")
	}
}

// ---- list_scenarios ----

const scenarioListFixture = `{
  "scenarios": [
    {"id": 9, "name": "happy-path", "createdAt": 1700000000, "isActive": true},
    {"id": 11, "name": "rate-limited", "createdAt": 1700000300, "isActive": false}
  ]
}`

func TestListScenarios_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: scenarioListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListScenarios(lb)(opsTestCtx(), nil, ListScenariosInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListScenarios: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/workspaces/7/scenarios" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7/scenarios", fc.calls)
	}
	if len(out.Scenarios) != 2 {
		t.Fatalf("len(Scenarios) = %d, want 2", len(out.Scenarios))
	}
	if out.Scenarios[0] != (ScenarioSummaryLine{ID: 9, Name: "happy-path", CreatedAt: 1700000000, IsActive: true}) {
		t.Errorf("Scenarios[0] = %+v, want the active happy-path row", out.Scenarios[0])
	}
	if out.Scenarios[1].IsActive {
		t.Errorf("Scenarios[1].IsActive = true, want false")
	}
}

func TestListScenarios_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleListScenarios(newLoopback(fc))(opsTestCtx(), nil, ListScenariosInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleListScenarios returned no error for a 500")
	}
}

// ---- get_scenario ----

func scenarioDetailFixture(overrideCount int) string {
	var overrides strings.Builder
	for i := range overrideCount {
		if i > 0 {
			overrides.WriteString(",")
		}
		fmt.Fprintf(&overrides, `{
			"method": "GET", "path": "/widgets/%d",
			"overrideOn": true, "routeOff": false, "activeStatus": 200,
			"responses": {"200": {"mode": "pinned", "body": {"n": %d}}},
			"delayMs": null
		}`, i, i)
	}
	return fmt.Sprintf(`{
  "id": 9,
  "name": "happy-path",
  "createdAt": 1700000000,
  "isActive": true,
  "settings": {"seed": 42, "basePath": "/api/v1", "listSize": 10, "nullRate": 0.1, "validateRequests": false, "delayMs": 0},
  "basePath": "/api/v1",
  "spec": {"hash": "abc123", "name": "Widgets API", "inline": null},
  "overrides": [%s]
}`, overrides.String())
}

// TestGetScenario_happyPath is get_scenario's anti-drift guard, over a
// fixture shaped like scenarioDetailView (scenario_handlers.go:82-91) — and
// the direct check on §C's stated cost concern: the marshalled output must
// never carry a pinned override body.
func TestGetScenario_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: scenarioDetailFixture(2)}}}
	lb := newLoopback(fc)

	_, out, err := handleGetScenario(lb)(opsTestCtx(), nil, GetScenarioInput{WorkspaceID: 7, ScenarioID: 9})
	if err != nil {
		t.Fatalf("handleGetScenario: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/workspaces/7/scenarios/9" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7/scenarios/9", fc.calls)
	}

	sc := out.Scenario
	if sc.ID != 9 || sc.Name != "happy-path" || !sc.IsActive || sc.CreatedAt != 1700000000 {
		t.Errorf("Scenario = %+v, want id=9 name=happy-path isActive=true createdAt=1700000000", sc)
	}
	if sc.BasePath != "/api/v1" {
		t.Errorf("BasePath = %q, want /api/v1", sc.BasePath)
	}
	if sc.Seed != 42 || sc.ListSize != 10 || sc.NullRate != 0.1 {
		t.Errorf("settings = %+v, want seed=42 listSize=10 nullRate=0.1", sc)
	}
	if sc.SpecName != "Widgets API" || sc.SpecHash != "abc123" {
		t.Errorf("Spec = name=%q hash=%q, want Widgets API/abc123", sc.SpecName, sc.SpecHash)
	}
	if sc.OverridesReturned != 2 || sc.OverridesTotal != 2 || sc.OverridesTruncated {
		t.Errorf("override counts = returned=%d total=%d truncated=%v, want 2/2/false",
			sc.OverridesReturned, sc.OverridesTotal, sc.OverridesTruncated)
	}
	if len(sc.Overrides) != 2 {
		t.Fatalf("len(Overrides) = %d, want 2", len(sc.Overrides))
	}
	ov := sc.Overrides[0]
	if ov.Method != http.MethodGet || ov.Path != "/widgets/0" || !ov.OverrideOn || ov.RouteOff {
		t.Errorf("Overrides[0] = %+v, want method=GET path=/widgets/0 overrideOn=true routeOff=false", ov)
	}
	if ov.ActiveStatus == nil || *ov.ActiveStatus != 200 {
		t.Errorf("ActiveStatus = %v, want *200", ov.ActiveStatus)
	}
	rs, ok := ov.Responses["200"]
	if !ok || rs.Mode != "pinned" || !rs.HasBody {
		t.Errorf("Responses[200] = %+v, want mode=pinned hasBody=true", rs)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal GetScenarioOutput: %v", err)
	}
	if strings.Contains(string(raw), `"body"`) || strings.Contains(string(raw), `"n":`) {
		t.Errorf("marshalled output leaks a pinned override body: %s", raw)
	}
}

// TestGetScenario_capsOverridesAndReportsTruncation is §C's compaction rule
// applied to a scenario snapshot that touches more operations than this
// tool will inline: getScenarioMaxOverrides caps the list, and
// OverridesTruncated must say so — never silently, which would read as "this
// scenario touches nothing past what's listed".
func TestGetScenario_capsOverridesAndReportsTruncation(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: scenarioDetailFixture(getScenarioMaxOverrides + 7)},
	}}
	lb := newLoopback(fc)

	_, out, err := handleGetScenario(lb)(opsTestCtx(), nil, GetScenarioInput{WorkspaceID: 7, ScenarioID: 9})
	if err != nil {
		t.Fatalf("handleGetScenario: %v", err)
	}
	if len(out.Scenario.Overrides) != getScenarioMaxOverrides {
		t.Errorf("len(Overrides) = %d, want the cap %d", len(out.Scenario.Overrides), getScenarioMaxOverrides)
	}
	if out.Scenario.OverridesReturned != getScenarioMaxOverrides {
		t.Errorf("OverridesReturned = %d, want %d", out.Scenario.OverridesReturned, getScenarioMaxOverrides)
	}
	if out.Scenario.OverridesTotal != getScenarioMaxOverrides+7 {
		t.Errorf("OverridesTotal = %d, want %d", out.Scenario.OverridesTotal, getScenarioMaxOverrides+7)
	}
	if !out.Scenario.OverridesTruncated {
		t.Error("OverridesTruncated = false, want true")
	}
}

func TestGetScenario_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"scenario not found"}}`},
	}}
	_, _, err := handleGetScenario(newLoopback(fc))(opsTestCtx(), nil, GetScenarioInput{WorkspaceID: 7, ScenarioID: 999})
	if err == nil {
		t.Fatal("handleGetScenario returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "scenario not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestGetScenario_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleGetScenario(newLoopback(fc))(opsTestCtx(), nil, GetScenarioInput{WorkspaceID: 7, ScenarioID: 9})
	if err == nil {
		t.Fatal("handleGetScenario returned no error for a 500")
	}
}

// ---- list_checkpoints ----

const checkpointListFixture = `{
  "checkpoints": [
    {"id": 21, "kind": "manual", "label": "before big edit", "createdAt": 1700000000, "createdBy": 1},
    {"id": 20, "kind": "auto", "label": "правка операции", "createdAt": 1699999000, "createdBy": null}
  ]
}`

func TestListCheckpoints_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: checkpointListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListCheckpoints(lb)(opsTestCtx(), nil, ListCheckpointsInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListCheckpoints: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/workspaces/7/checkpoints" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7/checkpoints", fc.calls)
	}
	if len(out.Checkpoints) != 2 {
		t.Fatalf("len(Checkpoints) = %d, want 2", len(out.Checkpoints))
	}
	c0 := out.Checkpoints[0]
	if c0.ID != 21 || c0.Kind != "manual" || c0.Label != "before big edit" || c0.CreatedAt != 1700000000 {
		t.Errorf("Checkpoints[0] = %+v, want id=21 kind=manual label=%q createdAt=1700000000", c0, "before big edit")
	}
	if c0.CreatedBy == nil || *c0.CreatedBy != 1 {
		t.Errorf("Checkpoints[0].CreatedBy = %v, want *1", c0.CreatedBy)
	}
	c1 := out.Checkpoints[1]
	if c1.Kind != "auto" || c1.CreatedBy != nil {
		t.Errorf("Checkpoints[1] = %+v, want kind=auto createdBy=nil", c1)
	}
}

func TestListCheckpoints_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleListCheckpoints(newLoopback(fc))(opsTestCtx(), nil, ListCheckpointsInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleListCheckpoints returned no error for a 500")
	}
}

// ---- get_session_directive ----

const sessionDirectiveListFixture = `{
  "directives": [
    {"target": "*", "action": "status", "status": 503, "setAt": "2024-01-01T00:00:00Z"},
    {"target": {"method": "GET", "path": "/widgets/{id}"}, "action": "delay", "ms": 250, "setAt": "2024-01-01T00:00:05Z"}
  ]
}`

// TestGetSessionDirective_happyPath proves this tool decodes the SAME
// sessionListWire set_session_directive already declares (tools_traffic.go)
// rather than a second copy of the union-target logic.
func TestGetSessionDirective_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: sessionDirectiveListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleGetSessionDirective(lb)(opsTestCtx(), nil, GetSessionDirectiveInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleGetSessionDirective: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces/7/session" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7/session", fc.calls)
	}
	if len(out.Directives) != 2 {
		t.Fatalf("len(Directives) = %d, want 2", len(out.Directives))
	}
	d0 := out.Directives[0]
	if !d0.Target.All || d0.Action != "status" || d0.Status != 503 {
		t.Errorf("Directives[0] = %+v, want target.all=true action=status status=503", d0)
	}
	d1 := out.Directives[1]
	if d1.Target.All || d1.Target.Method != http.MethodGet || d1.Target.Path != "/widgets/{id}" || d1.Action != "delay" || d1.Ms != 250 {
		t.Errorf("Directives[1] = %+v, want target={GET /widgets/{id}} action=delay ms=250", d1)
	}
}

func TestGetSessionDirective_emptyList(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"directives":[]}`}}}
	_, out, err := handleGetSessionDirective(newLoopback(fc))(opsTestCtx(), nil, GetSessionDirectiveInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleGetSessionDirective: %v", err)
	}
	if len(out.Directives) != 0 {
		t.Errorf("len(Directives) = %d, want 0", len(out.Directives))
	}
}

func TestGetSessionDirective_503IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusServiceUnavailable, body: `{"error":{"code":"service_unavailable","message":"no live state source wired"}}`},
	}}
	_, _, err := handleGetSessionDirective(newLoopback(fc))(opsTestCtx(), nil, GetSessionDirectiveInput{WorkspaceID: 7})
	if err == nil {
		t.Fatal("handleGetSessionDirective returned no error for a 503")
	}
}

// ---- get_auth_preset ----

const authPresetFixture = `{
  "bindings": [
    {"method": "POST", "path": "/login", "status": 200, "dataPath": "$.token", "recipe": {"kind": "jwt", "ttlSec": 3600}, "reason": "field name \"token\" on an auth path -> jwt", "source": "auth-path"}
  ],
  "schemes": ["bearer: http bearer"],
  "authPaths": ["/login"],
  "notes": ["skipped /refresh: no recognizable token field"],
  "sampleJwt": "eyJhbGciOiJIUzI1NiJ9.e30.sig"
}`

func TestGetAuthPreset_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: authPresetFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleGetAuthPreset(lb)(opsTestCtx(), nil, GetAuthPresetInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleGetAuthPreset: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/workspaces/7/auth-preset" {
		t.Fatalf("calls = %+v, want one GET /api/workspaces/7/auth-preset", fc.calls)
	}
	if len(out.Bindings) != 1 {
		t.Fatalf("len(Bindings) = %d, want 1", len(out.Bindings))
	}
	b := out.Bindings[0]
	if b.Method != http.MethodPost || b.Path != "/login" || b.Status != 200 || b.DataPath != "$.token" || b.Source != "auth-path" {
		t.Errorf("Bindings[0] = %+v, want method=POST path=/login status=200 dataPath=$.token source=auth-path", b)
	}
	if !strings.Contains(string(b.Recipe), `"jwt"`) {
		t.Errorf("Recipe = %s, want the raw recipe document preserved, kind=jwt", b.Recipe)
	}
	if len(out.Schemes) != 1 || len(out.AuthPaths) != 1 || len(out.Notes) != 1 {
		t.Errorf("Schemes/AuthPaths/Notes = %v/%v/%v, want one entry each", out.Schemes, out.AuthPaths, out.Notes)
	}
	if out.SampleJWT != "eyJhbGciOiJIUzI1NiJ9.e30.sig" {
		t.Errorf("SampleJWT = %q, want the fixture's token", out.SampleJWT)
	}
}

// TestGetAuthPreset_specLessWorkspaceAnswersEmptyProposal mirrors
// handleGetAuthPreset's own documented 200-with-empty-proposal behaviour for
// a workspace with no spec attached (preset_handlers.go:37-61) — never an
// error, since that state is entirely ordinary.
func TestGetAuthPreset_specLessWorkspaceAnswersEmptyProposal(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{
		"bindings": [], "schemes": [], "authPaths": [],
		"notes": ["workspace has no spec attached; nothing to propose"],
		"sampleJwt": "eyJhbGciOiJIUzI1NiJ9.e30.sig"
	}`}}}
	_, out, err := handleGetAuthPreset(newLoopback(fc))(opsTestCtx(), nil, GetAuthPresetInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleGetAuthPreset: %v, want no error for a spec-less workspace", err)
	}
	if len(out.Bindings) != 0 {
		t.Errorf("len(Bindings) = %d, want 0", len(out.Bindings))
	}
	if len(out.Notes) != 1 || !strings.Contains(out.Notes[0], "no spec attached") {
		t.Errorf("Notes = %v, want the no-spec explanation", out.Notes)
	}
}

func TestGetAuthPreset_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleGetAuthPreset(newLoopback(fc))(opsTestCtx(), nil, GetAuthPresetInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleGetAuthPreset returned no error for a 500")
	}
}

// ---- get_spec ----

const specGetFixture = `{
  "id": 3, "name": "Widgets API", "version": "1.2.0", "format": "openapi3",
  "source": "upload", "sourceRef": "", "basePath": "/v1", "hash": "abc123",
  "createdAt": 1700000000, "createdBy": 1
}`

func TestGetSpec_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: specGetFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleGetSpec(lb)(opsTestCtx(), nil, GetSpecInput{SpecID: 3})
	if err != nil {
		t.Fatalf("handleGetSpec: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/specs/3" {
		t.Fatalf("calls = %+v, want one GET /api/specs/3", fc.calls)
	}
	sp := out.Spec
	if sp.ID != 3 || sp.Name != "Widgets API" || sp.Version != "1.2.0" || sp.Format != "openapi3" {
		t.Errorf("Spec = %+v, want id=3 name=%q version=1.2.0 format=openapi3", sp, "Widgets API")
	}
	if sp.Source != "upload" || sp.BasePath != "/v1" || sp.Hash != "abc123" {
		t.Errorf("Spec = %+v, want source=upload basePath=/v1 hash=abc123", sp)
	}
	if sp.CreatedAt != 1700000000 || sp.CreatedBy == nil || *sp.CreatedBy != 1 {
		t.Errorf("Spec = %+v, want createdAt=1700000000 createdBy=*1", sp)
	}
}

func TestGetSpec_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"spec not found"}}`},
	}}
	_, _, err := handleGetSpec(newLoopback(fc))(opsTestCtx(), nil, GetSpecInput{SpecID: 999})
	if err == nil {
		t.Fatal("handleGetSpec returned no error for a 404")
	}
}

func TestGetSpec_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleGetSpec(newLoopback(fc))(opsTestCtx(), nil, GetSpecInput{SpecID: 1})
	if err == nil {
		t.Fatal("handleGetSpec returned no error for a 500")
	}
}

// ---- list_spec_operations ----

func specOperationsFixture(n int) string {
	var b strings.Builder
	b.WriteString("[")
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{
			"id": %d, "specId": 3, "method": "GET", "path": "/widgets/%d",
			"canonicalPath": "/widgets/%d", "operationId": "getWidget%d",
			"summary": null, "tag": "widgets", "sourceOrder": %d,
			"pointer": "#/paths/~1widgets~1%d/get", "parseError": null
		}`, i, i, i, i, i, i)
	}
	b.WriteString("]")
	return b.String()
}

func TestListSpecOperations_happyPath_defaultLimit(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: specOperationsFixture(3)}}}
	lb := newLoopback(fc)

	_, out, err := handleListSpecOperations(lb)(opsTestCtx(), nil, ListSpecOperationsInput{SpecID: 3})
	if err != nil {
		t.Fatalf("handleListSpecOperations: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].path != "/api/specs/3/operations?limit=100&offset=0" {
		t.Fatalf("calls = %+v, want one GET /api/specs/3/operations?limit=100&offset=0", fc.calls)
	}
	if len(out.Operations) != 3 || out.Returned != 3 || out.Limit != 100 || out.Offset != 0 {
		t.Fatalf("out = %+v, want 3 operations, returned=3 limit=100 offset=0", out)
	}
	if out.HasMore {
		t.Error("HasMore = true, want false (3 rows for a 100-row page)")
	}
	op := out.Operations[0]
	if op.ID != 0 || op.Method != http.MethodGet || op.Path != "/widgets/0" || op.CanonicalPath != "/widgets/0" {
		t.Errorf("Operations[0] = %+v, want id=0 method=GET path=/widgets/0", op)
	}
	if op.OperationID == nil || *op.OperationID != "getWidget0" {
		t.Errorf("OperationID = %v, want *getWidget0", op.OperationID)
	}
	if op.Tag == nil || *op.Tag != "widgets" {
		t.Errorf("Tag = %v, want *widgets", op.Tag)
	}
	if op.Summary != nil {
		t.Errorf("Summary = %v, want nil", op.Summary)
	}
	if op.ParseError != nil {
		t.Errorf("ParseError = %v, want nil", op.ParseError)
	}
	if op.Pointer != "#/paths/~1widgets~10/get" {
		t.Errorf("Pointer = %q, want #/paths/~1widgets~10/get", op.Pointer)
	}
}

func TestListSpecOperations_customLimitAndOffset(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: specOperationsFixture(5)}}}
	lb := newLoopback(fc)

	_, _, err := handleListSpecOperations(lb)(opsTestCtx(), nil, ListSpecOperationsInput{SpecID: 3, Limit: 5, Offset: 20})
	if err != nil {
		t.Fatalf("handleListSpecOperations: %v", err)
	}
	if fc.calls[0].path != "/api/specs/3/operations?limit=5&offset=20" {
		t.Errorf("path = %q, want /api/specs/3/operations?limit=5&offset=20", fc.calls[0].path)
	}
}

// TestListSpecOperations_limitClampedToMax proves a caller cannot force a
// single call to load an entire large spec's operations — the same ceiling
// spec_handlers.go itself enforces (maxOperationsLimit), applied here so the
// tool's own reported `limit` never lies about what it actually asked for.
func TestListSpecOperations_limitClampedToMax(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: "[]"}}}
	lb := newLoopback(fc)

	_, out, err := handleListSpecOperations(lb)(opsTestCtx(), nil, ListSpecOperationsInput{SpecID: 3, Limit: 100000})
	if err != nil {
		t.Fatalf("handleListSpecOperations: %v", err)
	}
	if out.Limit != specOperationsMaxLimit {
		t.Errorf("Limit = %d, want the clamp %d", out.Limit, specOperationsMaxLimit)
	}
	if fc.calls[0].path != fmt.Sprintf("/api/specs/3/operations?limit=%d&offset=0", specOperationsMaxLimit) {
		t.Errorf("path = %q, want limit clamped to %d", fc.calls[0].path, specOperationsMaxLimit)
	}
}

// TestListSpecOperations_hasMoreWhenReturnedEqualsLimit mirrors
// TestListTraffic_hasMoreWhenReturnedEqualsLimit's own reasoning
// (tools_traffic_test.go): with no total count available from the admin
// route (spec_handlers.go pages in SQL, same as GET .../traffic), a full
// page is the only signal this tool has that there might be more.
func TestListSpecOperations_hasMoreWhenReturnedEqualsLimit(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: specOperationsFixture(2)}}}
	lb := newLoopback(fc)

	_, out, err := handleListSpecOperations(lb)(opsTestCtx(), nil, ListSpecOperationsInput{SpecID: 3, Limit: 2})
	if err != nil {
		t.Fatalf("handleListSpecOperations: %v", err)
	}
	if !out.HasMore {
		t.Error("HasMore = false, want true (returned rows == limit)")
	}
}

func TestListSpecOperations_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"spec not found"}}`},
	}}
	_, _, err := handleListSpecOperations(newLoopback(fc))(opsTestCtx(), nil, ListSpecOperationsInput{SpecID: 999})
	if err == nil {
		t.Fatal("handleListSpecOperations returned no error for a 404")
	}
}

func TestListSpecOperations_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleListSpecOperations(newLoopback(fc))(opsTestCtx(), nil, ListSpecOperationsInput{SpecID: 1})
	if err == nil {
		t.Fatal("handleListSpecOperations returned no error for a 500")
	}
}

// ---- registration ----

// TestAddReadTools_registersNineToolsWithHonestAnnotations mirrors
// TestAddTrafficTools_registersThreeToolsWithHonestAnnotations
// (tools_traffic_test.go): every tool this file adds must publish a
// non-empty Description, a non-nil OutputSchema, and Annotations that match
// what the tool actually does — all nine are pure reads, so every one of
// them must be ReadOnlyHint:true, IdempotentHint:true.
func TestAddReadTools_registersNineToolsWithHonestAnnotations(t *testing.T) {
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
				Name         string         `json:"name"`
				Description  string         `json:"description"`
				OutputSchema map[string]any `json:"outputSchema"`
				Annotations  struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}

	want := []string{
		"get_workspace", "list_endpoints", "list_scenarios", "get_scenario",
		"list_checkpoints", "get_session_directive", "get_auth_preset",
		"get_spec", "list_spec_operations",
	}
	seen := make(map[string]bool, len(want))
	for _, tool := range env.Result.Tools {
		isOurs := false
		for _, name := range want {
			if tool.Name == name {
				isOurs = true
				break
			}
		}
		if !isOurs {
			continue // another file's tool — not this test's business.
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s: empty Description", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema published — declared output type must not be any", tool.Name)
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: ReadOnlyHint = false, want true (every tool in this file is a pure read)", tool.Name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Errorf("%s: IdempotentHint = false, want true", tool.Name)
		}
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("tool %q was never registered", name)
		}
	}
}
