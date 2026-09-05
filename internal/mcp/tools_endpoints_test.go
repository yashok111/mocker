package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// ---- create_endpoint ----

const createEndpointRespFixture = `{
  "id": 9,
  "method": "GET",
  "path": "/legacy/ping",
  "canonicalPath": "/legacy/ping",
  "overrideOn": true,
  "routeOff": false,
  "activeStatus": 200,
  "responses": {"200": {"mode": "pinned", "mediaType": "application/json", "body": {"ok": true}}},
  "createdAt": 1700000000,
  "updatedAt": 1700000000
}`

func TestCreateEndpoint_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createEndpointRespFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleCreateEndpoint(lb)(opsTestCtx(), nil, CreateEndpointInput{
		WorkspaceID: 7, Method: "GET", Path: "/legacy/ping", Status: 200,
		Body: map[string]any{"ok": true}, MediaType: "application/json",
	})
	if err != nil {
		t.Fatalf("handleCreateEndpoint: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/7/endpoints" {
		t.Errorf("call = %s %s, want POST /api/workspaces/7/endpoints", fc.calls[0].method, fc.calls[0].path)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["method"] != "GET" || sent["path"] != "/legacy/ping" || sent["status"] != float64(200) {
		t.Errorf("sent = %+v, want method=GET path=/legacy/ping status=200", sent)
	}
	sentBody, _ := sent["body"].(map[string]any)
	if sentBody["ok"] != true {
		t.Errorf("sent body = %v, want {ok:true}", sent["body"])
	}

	if out.Endpoint.ID != 9 || out.Endpoint.CanonicalPath != "/legacy/ping" || !out.Endpoint.OverrideOn {
		t.Errorf("Endpoint = %+v, want id=9 canonicalPath=/legacy/ping overrideOn=true", out.Endpoint)
	}
	rs, ok := out.Endpoint.Responses["200"]
	if !ok || rs.Mode != "pinned" || !rs.HasBody {
		t.Errorf("Responses[200] = %+v, want mode=pinned hasBody=true (never the raw body bytes)", rs)
	}
}

// TestCreateEndpoint_omitsAbsentStatus is the negative control for the
// request-shape assertion above: a zero Status must not appear on the wire
// at all, mirroring TestCreateWorkspace_omitsAbsentFields (tools_ops_test.go)
// — the admin route's own default (200, defaultCreateEndpointStatus) only
// fires when the key is absent, not when it is present as a literal 0.
func TestCreateEndpoint_omitsAbsentStatus(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createEndpointRespFixture}}}
	lb := newLoopback(fc)

	_, _, err := handleCreateEndpoint(lb)(opsTestCtx(), nil, CreateEndpointInput{
		WorkspaceID: 7, Method: "GET", Path: "/legacy/ping",
	})
	if err != nil {
		t.Fatalf("handleCreateEndpoint: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if _, ok := sent["status"]; ok {
		t.Errorf("sent body carries a status key with nothing supplied: %v", sent)
	}
	if _, ok := sent["body"]; ok {
		t.Errorf("sent body carries a body key with nothing supplied: %v", sent)
	}
}

func TestCreateEndpoint_409IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"an override already exists for this method and path"}}`},
	}}
	_, _, err := handleCreateEndpoint(newLoopback(fc))(opsTestCtx(), nil, CreateEndpointInput{
		WorkspaceID: 7, Method: "GET", Path: "/widgets",
	})
	if err == nil {
		t.Fatal("handleCreateEndpoint returned no error for a 409")
	}
	if !strings.Contains(err.Error(), "an override already exists") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// ---- update_endpoint ----

const updateEndpointRespFixture = `{
  "id": 9,
  "method": "GET",
  "path": "/legacy/ping",
  "canonicalPath": "/legacy/ping",
  "overrideOn": true,
  "routeOff": false,
  "activeStatus": 503,
  "responses": {"503": {"mode": "pinned"}},
  "listSize": {"min": 2, "max": 6},
  "delayMs": 100,
  "createdAt": 1700000000,
  "updatedAt": 1700000900
}`

// TestUpdateEndpoint_happyPath is §C-b's own full-replacement proof, applied
// to this route: every field the caller supplies is sent verbatim, and
// OverrideOn/RouteOff — left nil here — must NOT appear on the wire, so the
// admin plane's own default (true/false) fires rather than a caller-supplied
// false/true this test never asked for.
func TestUpdateEndpoint_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: updateEndpointRespFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleUpdateEndpoint(lb)(opsTestCtx(), nil, UpdateEndpointInput{
		WorkspaceID: 7, EndpointID: 9, Method: "GET", Path: "/legacy/ping", ActiveStatus: 503,
		Responses: map[string]VariantInput{"503": {Mode: "pinned"}},
		ListSize:  &ListSizeView{Min: 2, Max: 6},
	})
	if err != nil {
		t.Fatalf("handleUpdateEndpoint: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	wantPath := "/api/workspaces/7/endpoints/9"
	if fc.calls[0].method != http.MethodPut || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want PUT %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if _, ok := sent["overrideOn"]; ok {
		t.Errorf("sent body carries overrideOn with nothing supplied: %v (must stay omitted so the admin plane's own default fires)", sent)
	}
	if _, ok := sent["routeOff"]; ok {
		t.Errorf("sent body carries routeOff with nothing supplied: %v", sent)
	}
	if sent["activeStatus"] != float64(503) {
		t.Errorf("sent activeStatus = %v, want 503", sent["activeStatus"])
	}
	sentListSize, _ := sent["listSize"].(map[string]any)
	if sentListSize["min"] != float64(2) || sentListSize["max"] != float64(6) {
		t.Errorf("sent listSize = %v, want {min:2 max:6}", sent["listSize"])
	}

	if out.Endpoint.ActiveStatus != 503 || out.Endpoint.ListSize == nil || out.Endpoint.ListSize.Max != 6 {
		t.Errorf("Endpoint = %+v, want activeStatus=503 listSize.max=6", out.Endpoint)
	}
}

// TestUpdateEndpoint_explicitFlagsSent is the positive control: when the
// caller DOES set overrideOn/routeOff, both must reach the wire exactly as
// given, never silently dropped or replaced by the admin plane's own
// default.
func TestUpdateEndpoint_explicitFlagsSent(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: updateEndpointRespFixture}}}
	lb := newLoopback(fc)

	overrideOn := false
	routeOff := true
	_, _, err := handleUpdateEndpoint(lb)(opsTestCtx(), nil, UpdateEndpointInput{
		WorkspaceID: 7, EndpointID: 9, Method: "GET", Path: "/legacy/ping", ActiveStatus: 503,
		OverrideOn: &overrideOn, RouteOff: &routeOff,
	})
	if err != nil {
		t.Fatalf("handleUpdateEndpoint: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["overrideOn"] != false {
		t.Errorf("sent overrideOn = %v, want false", sent["overrideOn"])
	}
	if sent["routeOff"] != true {
		t.Errorf("sent routeOff = %v, want true", sent["routeOff"])
	}
}

func TestUpdateEndpoint_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"endpoint not found"}}`},
	}}
	_, _, err := handleUpdateEndpoint(newLoopback(fc))(opsTestCtx(), nil, UpdateEndpointInput{
		WorkspaceID: 7, EndpointID: 999, Method: "GET", Path: "/x", ActiveStatus: 200,
	})
	if err == nil {
		t.Fatal("handleUpdateEndpoint returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "endpoint not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestUpdateEndpoint_409IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"an override already exists for this method and path"}}`},
	}}
	_, _, err := handleUpdateEndpoint(newLoopback(fc))(opsTestCtx(), nil, UpdateEndpointInput{
		WorkspaceID: 7, EndpointID: 9, Method: "GET", Path: "/widgets", ActiveStatus: 200,
	})
	if err == nil {
		t.Fatal("handleUpdateEndpoint returned no error for a 409")
	}
}

// ---- delete_endpoint ----

func TestDeleteEndpoint_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusNoContent, body: ""}}}
	lb := newLoopback(fc)

	_, out, err := handleDeleteEndpoint(lb)(opsTestCtx(), nil, DeleteEndpointInput{WorkspaceID: 7, EndpointID: 9})
	if err != nil {
		t.Fatalf("handleDeleteEndpoint: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	wantPath := "/api/workspaces/7/endpoints/9"
	if fc.calls[0].method != http.MethodDelete || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want DELETE %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	if !out.Deleted || out.EndpointID != 9 {
		t.Errorf("output = %+v, want deleted=true endpointId=9", out)
	}
}

func TestDeleteEndpoint_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"endpoint not found"}}`},
	}}
	_, _, err := handleDeleteEndpoint(newLoopback(fc))(opsTestCtx(), nil, DeleteEndpointInput{WorkspaceID: 7, EndpointID: 999})
	if err == nil {
		t.Fatal("handleDeleteEndpoint returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "endpoint not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// ---- endpoint_from_traffic ----

const toEndpointRespFixture = `{"id": 11, "method": "GET", "path": "/legacy/ping", "revision": 6}`

func TestEndpointFromTraffic_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: toEndpointRespFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleEndpointFromTraffic(lb)(opsTestCtx(), nil, EndpointFromTrafficInput{WorkspaceID: 7, TrafficID: 42})
	if err != nil {
		t.Fatalf("handleEndpointFromTraffic: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	wantPath := "/api/workspaces/7/traffic/42/to-endpoint"
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want POST %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	if out.ID != 11 || out.Method != http.MethodGet || out.Path != "/legacy/ping" || out.Revision != 6 {
		t.Errorf("output = %+v, want id=11 method=GET path=/legacy/ping revision=6", out)
	}
}

func TestEndpointFromTraffic_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"traffic row not found"}}`},
	}}
	_, _, err := handleEndpointFromTraffic(newLoopback(fc))(opsTestCtx(), nil, EndpointFromTrafficInput{WorkspaceID: 7, TrafficID: 999})
	if err == nil {
		t.Fatal("handleEndpointFromTraffic returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "traffic row not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestEndpointFromTraffic_409IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"traffic row's body was truncated or redacted; pinning it would ship a lie"}}`},
	}}
	_, _, err := handleEndpointFromTraffic(newLoopback(fc))(opsTestCtx(), nil, EndpointFromTrafficInput{WorkspaceID: 7, TrafficID: 42})
	if err == nil {
		t.Fatal("handleEndpointFromTraffic returned no error for a 409")
	}
	if !strings.Contains(err.Error(), "truncated or redacted") {
		t.Errorf("error = %q, want it to carry the admin plane's refusal reason", err.Error())
	}
}

// ---- create_scenario ----

const createScenarioRespFixture = `{
  "id": 4,
  "name": "happy-path",
  "createdAt": 1700000000,
  "isActive": false,
  "settings": {"seed": 42, "basePath": "/v1", "listSize": 10, "nullRate": 0.1, "validateRequests": false, "delayMs": 0},
  "basePath": "/v1",
  "spec": {"hash": "abc123", "name": "Widgets API", "inline": null},
  "overrides": []
}`

func TestCreateScenario_happyPath_currentState(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createScenarioRespFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleCreateScenario(lb)(opsTestCtx(), nil, CreateScenarioInput{WorkspaceID: 7, Name: "happy-path"})
	if err != nil {
		t.Fatalf("handleCreateScenario: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/7/scenarios" {
		t.Errorf("call = %s %s, want POST /api/workspaces/7/scenarios", fc.calls[0].method, fc.calls[0].path)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["name"] != "happy-path" {
		t.Errorf("sent name = %v, want happy-path", sent["name"])
	}
	if _, ok := sent["from"]; ok {
		t.Errorf("sent body carries \"from\" with nothing supplied: %v", sent)
	}
	if out.Scenario.ID != 4 || out.Scenario.Name != "happy-path" || out.Scenario.IsActive {
		t.Errorf("Scenario = %+v, want id=4 name=happy-path isActive=false", out.Scenario)
	}
}

// TestCreateScenario_withFrom_sendsCloneSource pins the request-shape half
// of D5's clone path: `from` must reach the wire exactly, not be dropped the
// way TestCreateScenario_happyPath_currentState proves it is when absent.
func TestCreateScenario_withFrom_sendsCloneSource(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createScenarioRespFixture}}}
	lb := newLoopback(fc)

	from := int64(2)
	_, _, err := handleCreateScenario(lb)(opsTestCtx(), nil, CreateScenarioInput{WorkspaceID: 7, Name: "happy-path", From: &from})
	if err != nil {
		t.Fatalf("handleCreateScenario: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["from"] != float64(2) {
		t.Errorf("sent from = %v, want 2", sent["from"])
	}
}

func TestCreateScenario_409IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"a scenario is already active on this workspace"}}`},
	}}
	_, _, err := handleCreateScenario(newLoopback(fc))(opsTestCtx(), nil, CreateScenarioInput{WorkspaceID: 7, Name: "x"})
	if err == nil {
		t.Fatal("handleCreateScenario returned no error for a 409")
	}
	if !strings.Contains(err.Error(), "already active") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// ---- rename_scenario ----

func TestRenameScenario_happyPath(t *testing.T) {
	t.Parallel()
	renamed := `{"id": 4, "name": "renamed", "createdAt": 1700000000, "isActive": true, "settings": {}, "basePath": "", "spec": {"hash":"","name":"","inline":null}, "overrides": []}`
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: renamed}}}
	lb := newLoopback(fc)

	_, out, err := handleRenameScenario(lb)(opsTestCtx(), nil, RenameScenarioInput{WorkspaceID: 7, ScenarioID: 4, Name: "renamed"})
	if err != nil {
		t.Fatalf("handleRenameScenario: %v", err)
	}
	wantPath := "/api/workspaces/7/scenarios/4"
	if fc.calls[0].method != http.MethodPut || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want PUT %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["name"] != "renamed" {
		t.Errorf("sent name = %v, want renamed", sent["name"])
	}
	if out.Scenario.Name != "renamed" || !out.Scenario.IsActive {
		t.Errorf("Scenario = %+v, want name=renamed isActive=true (rename can target the ACTIVE scenario)", out.Scenario)
	}
}

func TestRenameScenario_409DuplicateNameIsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"a scenario named \"renamed\" already exists"}}`},
	}}
	_, _, err := handleRenameScenario(newLoopback(fc))(opsTestCtx(), nil, RenameScenarioInput{WorkspaceID: 7, ScenarioID: 4, Name: "renamed"})
	if err == nil {
		t.Fatal("handleRenameScenario returned no error for a 409")
	}
}

func TestRenameScenario_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"scenario not found"}}`},
	}}
	_, _, err := handleRenameScenario(newLoopback(fc))(opsTestCtx(), nil, RenameScenarioInput{WorkspaceID: 7, ScenarioID: 999, Name: "x"})
	if err == nil {
		t.Fatal("handleRenameScenario returned no error for a 404")
	}
}

// ---- delete_scenario ----

const deleteScenarioWorkspaceFixture = `{"id":7,"slug":"orders-api","name":"Orders"}`

// TestDeleteScenario_confirmedDeletes is the integration proof that this
// handler actually calls confirmWorkspaceSlug FIRST and, on a match, issues
// the DELETE second — confirm_test.go already pins the helper's own rules in
// isolation, so this only needs to prove the wiring.
func TestDeleteScenario_confirmedDeletes(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: deleteScenarioWorkspaceFixture},
		{status: http.StatusNoContent, body: ""},
	}}
	lb := newLoopback(fc)

	_, out, err := handleDeleteScenario(lb)(opsTestCtx(), nil, DeleteScenarioInput{
		WorkspaceID: 7, ScenarioID: 4, ConfirmSlug: "orders-api",
	})
	if err != nil {
		t.Fatalf("handleDeleteScenario: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then DELETE)", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces/7" {
		t.Errorf("first call = %s %s, want GET /api/workspaces/7", fc.calls[0].method, fc.calls[0].path)
	}
	wantPath := "/api/workspaces/7/scenarios/4"
	if fc.calls[1].method != http.MethodDelete || fc.calls[1].path != wantPath {
		t.Errorf("second call = %s %s, want DELETE %s", fc.calls[1].method, fc.calls[1].path, wantPath)
	}
	if !out.Deleted || out.ScenarioID != 4 {
		t.Errorf("output = %+v, want deleted=true scenarioId=4", out)
	}
}

// TestDeleteScenario_mismatchedSlugNeverDeletes proves the refusal actually
// stops this handler from issuing its own DELETE: scriptedCaller queues only
// the confirmation GET, so a second call would fail this test loudly.
func TestDeleteScenario_mismatchedSlugNeverDeletes(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: deleteScenarioWorkspaceFixture},
	}}
	lb := newLoopback(fc)

	_, _, err := handleDeleteScenario(lb)(opsTestCtx(), nil, DeleteScenarioInput{
		WorkspaceID: 7, ScenarioID: 4, ConfirmSlug: "wrong-slug",
	})
	if err == nil {
		t.Fatal("handleDeleteScenario returned no error for a mismatched confirmSlug")
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (confirm GET only — the DELETE must never be issued)", len(fc.calls))
	}
}

// TestDeleteScenario_emptySlugNeverCallsAdminPlane proves the refusal fires
// before even the confirmation read — scriptedCaller queues NO responses,
// so any call at all fails this test loudly.
func TestDeleteScenario_emptySlugNeverCallsAdminPlane(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: nil}
	lb := newLoopback(fc)

	_, _, err := handleDeleteScenario(lb)(opsTestCtx(), nil, DeleteScenarioInput{WorkspaceID: 7, ScenarioID: 4})
	if err == nil {
		t.Fatal("handleDeleteScenario returned no error for an empty confirmSlug")
	}
	if len(fc.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(fc.calls))
	}
}

// ---- activate_scenario ----

func TestActivateScenario_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"scenarioId": 4, "revision": 8}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleActivateScenario(lb)(opsTestCtx(), nil, ActivateScenarioInput{WorkspaceID: 7, ScenarioID: 4})
	if err != nil {
		t.Fatalf("handleActivateScenario: %v", err)
	}
	wantPath := "/api/workspaces/7/scenarios/4/activate"
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want POST %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	if out.ScenarioID != 4 || out.Revision != 8 {
		t.Errorf("output = %+v, want scenarioId=4 revision=8", out)
	}
}

func TestActivateScenario_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"scenario 999 in workspace 7"}}`},
	}}
	_, _, err := handleActivateScenario(newLoopback(fc))(opsTestCtx(), nil, ActivateScenarioInput{WorkspaceID: 7, ScenarioID: 999})
	if err == nil {
		t.Fatal("handleActivateScenario returned no error for a 404")
	}
}

// ---- deactivate_scenario ----

func TestDeactivateScenario_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"scenarioId": null, "revision": 9}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleDeactivateScenario(lb)(opsTestCtx(), nil, DeactivateScenarioInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleDeactivateScenario: %v", err)
	}
	wantPath := "/api/workspaces/7/scenarios/deactivate"
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want POST %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	if out.Revision != 9 {
		t.Errorf("output.Revision = %d, want 9", out.Revision)
	}
}

func TestDeactivateScenario_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleDeactivateScenario(newLoopback(fc))(opsTestCtx(), nil, DeactivateScenarioInput{WorkspaceID: 7})
	if err == nil {
		t.Fatal("handleDeactivateScenario returned no error for a 500")
	}
}

// TestListEndpoints_carriesKindAndStream is P6b's own guard on the ONE
// projection rule: list_endpoints must answer the same kind/stream pair
// create_endpoint and update_endpoint answer, because an agent that edits a
// stream reads its current document through the list first. The smoke run
// of 2026-09-02 caught a hand-rolled copy of the field list in
// handleListEndpoints that had silently dropped both.
func TestListEndpoints_carriesKindAndStream(t *testing.T) {
	t.Parallel()
	fc := &fakeCaller{status: http.StatusOK, body: []byte(`{"endpoints":[{"id":3,"method":"GET","path":"/events","canonicalPath":"/events","overrideOn":true,"routeOff":false,"activeStatus":200,"responses":{},"kind":"sse","stream":{"tick":{"intervalMs":500,"schema":{"type":"object"}}},"createdAt":0,"updatedAt":0,"editVersion":1}]}`)}
	h := New(fc, testKey, testConfig(), nil).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_endpoints","arguments":{"workspaceId":7}}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	body := rec.Body.String()
	if !strings.Contains(body, `"kind":"sse"`) || !strings.Contains(body, `"intervalMs":500`) {
		t.Fatalf("list_endpoints dropped kind/stream: %s", body)
	}
}

// TestCreateEndpoint_functionReachesTheWire is review finding 4: the input
// declared `function` and the wire body did not carry it, so create_endpoint
// with a function stored a pinned variant with an empty body and answered
// 201 — the PRIMARY surface, dropping the feature it was for, with no error.
func TestCreateEndpoint_functionReachesTheWire(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createEndpointRespFixture}}}
	lb := newLoopback(fc)

	const src = "return 401, {error = 'bad credentials'}"
	if _, _, err := handleCreateEndpoint(lb)(opsTestCtx(), nil, CreateEndpointInput{
		WorkspaceID: 7, Method: "POST", Path: "/sign-in", Function: src,
	}); err != nil {
		t.Fatalf("handleCreateEndpoint: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["function"] != src {
		t.Fatalf("sent = %+v, want function=%q on the wire", sent, src)
	}
}
