package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ---- fake Caller: scriptedCaller ----

// recordedCall is one call scriptedCaller observed, kept so a test can assert
// exactly what a tool SENT — method, path (with opKey substituted exactly
// once, §C-c) and body — not just what it returned.
type recordedCall struct {
	method string
	path   string
	body   string
}

// cannedResponse is one queued answer for scriptedCaller.
type cannedResponse struct {
	status int
	body   string
}

// scriptedCaller is the fake Caller (§B4: Caller is an interface precisely
// so a tool test can drive one without a database) every test in this file
// uses. Calls are answered strictly in the order queued: call N gets
// responses[N]. A tool that makes one call fewer or more than expected fails
// LOUDLY (t.Fatalf) rather than silently reusing or skipping a response —
// which is exactly the shape of bug §C-a and §C-b exist to prevent (a tool
// that should stop after a 404 but calls through anyway, or one that skips
// the GET half of GET-modify-PUT).
type scriptedCaller struct {
	t         *testing.T
	responses []cannedResponse
	calls     []recordedCall
}

func (c *scriptedCaller) CallAsMCP(_ context.Context, _ *http.Request, method, path string, body []byte) (int, []byte, error) {
	c.t.Helper()
	c.calls = append(c.calls, recordedCall{method: method, path: path, body: string(body)})
	if len(c.calls) > len(c.responses) {
		c.t.Fatalf("scriptedCaller: call #%d (%s %s) has no queued response — this test scripted only %d call(s); body=%s",
			len(c.calls), method, path, len(c.responses), body)
	}
	r := c.responses[len(c.calls)-1]
	return r.status, []byte(r.body), nil
}

// opsTestCtx is the context every handler under test needs: an inbound
// request attached exactly as Handler (mcp.go) attaches the real one, since
// loopback.do/call read it off the context, not a parameter.
func opsTestCtx() context.Context {
	return withInboundRequest(context.Background(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil))
}

// decodeBody is the common "was this request body what the tool claims to
// have sent" helper: decode a recordedCall.body into a generic map so a test
// can assert individual keys without modeling the whole overrides.Row schema
// a second time.
func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode request body: %v; body=%s", err, body)
	}
	return m
}

// ---- list_workspaces ----

const workspaceListFixture = `[
  {
    "id": 7,
    "slug": "demo-workspace",
    "name": "Demo Workspace",
    "specId": 3,
    "ownerId": 1,
    "forkedFrom": null,
    "scenarioId": null,
    "revision": 4,
    "settings": {
      "seed": 42,
      "basePath": "/api/v1",
      "listSize": 10,
      "nullRate": 0.1,
      "envelope": null,
      "identity": {},
      "auth": {},
      "cors": {},
      "validateRequests": false,
      "delayMs": 0
    },
    "url": "https://demo-workspace.mocker.local",
    "createdAt": 1700000000,
    "updatedAt": 1700000100
  }
]`

// TestListWorkspaces_happyPath is the anti-drift guard §C's compaction rule
// demands: every field ListWorkspacesOutput projects is asserted against a
// fixture copied from workspaceView's own shape (workspace_handlers.go:42-63)
// — an admin-side rename of any of these JSON keys must turn this test red,
// not silently leave the Go field at its zero value.
func TestListWorkspaces_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: workspaceListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListWorkspaces(lb)(opsTestCtx(), nil, ListWorkspacesInput{})
	if err != nil {
		t.Fatalf("handleListWorkspaces: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces?all=1" {
		t.Errorf("call = %s %s, want GET /api/workspaces?all=1", fc.calls[0].method, fc.calls[0].path)
	}

	if len(out.Workspaces) != 1 {
		t.Fatalf("len(Workspaces) = %d, want 1", len(out.Workspaces))
	}
	w := out.Workspaces[0]
	if w.ID != 7 {
		t.Errorf("ID = %d, want 7", w.ID)
	}
	if w.Slug != "demo-workspace" {
		t.Errorf("Slug = %q, want %q", w.Slug, "demo-workspace")
	}
	if w.Name != "Demo Workspace" {
		t.Errorf("Name = %q, want %q", w.Name, "Demo Workspace")
	}
	if w.URL != "https://demo-workspace.mocker.local" {
		t.Errorf("URL = %q, want %q", w.URL, "https://demo-workspace.mocker.local")
	}
	// BasePath is pulled out of settings.basePath, not a top-level field —
	// the one field workspaceView's own URL cannot carry (§0).
	if w.BasePath != "/api/v1" {
		t.Errorf("BasePath = %q, want %q", w.BasePath, "/api/v1")
	}
	if w.SpecID == nil || *w.SpecID != 3 {
		t.Errorf("SpecID = %v, want *3", w.SpecID)
	}
	if w.Revision != 4 {
		t.Errorf("Revision = %d, want 4", w.Revision)
	}
}

func TestListWorkspaces_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"nothing to list here"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleListWorkspaces(lb)(opsTestCtx(), nil, ListWorkspacesInput{})
	if err == nil {
		t.Fatal("handleListWorkspaces returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "nothing to list here") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestListWorkspaces_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"db is locked, dsn=/data/mocker.db"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleListWorkspaces(lb)(opsTestCtx(), nil, ListWorkspacesInput{})
	if err == nil {
		t.Fatal("handleListWorkspaces returned no error for a 500")
	}
	if strings.Contains(err.Error(), "dsn") {
		t.Errorf("error = %q, leaks internal detail past a 5xx status", err.Error())
	}
}

// ---- create_workspace ----

const createWorkspaceRespFixture = `{
  "id": 42,
  "slug": "shiny-workspace-2",
  "name": "Shiny Workspace",
  "specId": 3,
  "ownerId": 9,
  "forkedFrom": null,
  "scenarioId": null,
  "revision": 1,
  "settings": {"basePath": "/v1"},
  "url": "https://shiny-workspace-2.mocker.local",
  "createdAt": 1700000200,
  "updatedAt": 1700000200
}`

// TestCreateWorkspace_happyPath asserts both directions: the request the
// tool SENDS (name, slug and specId all round-tripped into the POST body)
// and the response fields it projects back.
func TestCreateWorkspace_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createWorkspaceRespFixture}}}
	lb := newLoopback(fc)

	specID := int64(3)
	_, out, err := handleCreateWorkspace(lb)(opsTestCtx(), nil, CreateWorkspaceInput{
		Name: "Shiny Workspace", Slug: "shiny-workspace", SpecID: &specID,
	})
	if err != nil {
		t.Fatalf("handleCreateWorkspace: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces" {
		t.Errorf("call = %s %s, want POST /api/workspaces", fc.calls[0].method, fc.calls[0].path)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["name"] != "Shiny Workspace" {
		t.Errorf("request name = %v, want %q", sent["name"], "Shiny Workspace")
	}
	if sent["slug"] != "shiny-workspace" {
		t.Errorf("request slug = %v, want %q", sent["slug"], "shiny-workspace")
	}
	if sent["specId"] != float64(3) {
		t.Errorf("request specId = %v, want 3", sent["specId"])
	}

	if out.ID != 42 {
		t.Errorf("ID = %d, want 42", out.ID)
	}
	if out.Slug != "shiny-workspace-2" {
		t.Errorf("Slug = %q, want %q", out.Slug, "shiny-workspace-2")
	}
	if out.URL != "https://shiny-workspace-2.mocker.local" {
		t.Errorf("URL = %q, want %q", out.URL, "https://shiny-workspace-2.mocker.local")
	}
}

// TestCreateWorkspace_omitsAbsentFields is the negative control for the
// request-shape assertion above: Slug and SpecID left zero must not appear
// in the request body at all (omitempty) — a UI or agent sending an EXPLICIT
// empty slug is a different request from sending none, and only omitempty
// keeps that distinction on the wire.
func TestCreateWorkspace_omitsAbsentFields(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusCreated, body: createWorkspaceRespFixture}}}
	lb := newLoopback(fc)

	_, _, err := handleCreateWorkspace(lb)(opsTestCtx(), nil, CreateWorkspaceInput{Name: "Shiny Workspace"})
	if err != nil {
		t.Fatalf("handleCreateWorkspace: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if _, ok := sent["slug"]; ok {
		t.Errorf("request body carries a slug key with nothing supplied: %v", sent)
	}
	if _, ok := sent["specId"]; ok {
		t.Errorf("request body carries a specId key with nothing supplied: %v", sent)
	}
}

func TestCreateWorkspace_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"spec not found"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleCreateWorkspace(lb)(opsTestCtx(), nil, CreateWorkspaceInput{Name: "x"})
	if err == nil {
		t.Fatal("handleCreateWorkspace returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "spec not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestCreateWorkspace_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleCreateWorkspace(lb)(opsTestCtx(), nil, CreateWorkspaceInput{Name: "x"})
	if err == nil {
		t.Fatal("handleCreateWorkspace returned no error for a 500")
	}
}

// ---- list_specs ----

const specsListFixture = `[
  {"id": 3, "name": "Widgets API", "version": "1.2.0", "format": "openapi3", "source": "upload", "sourceRef": "", "basePath": "/v1", "hash": "abc123", "createdAt": 1700000000, "createdBy": 1},
  {"id": 5, "name": "Other API", "version": "2.0.0", "format": "openapi3", "source": "upload", "sourceRef": "", "basePath": "/v2", "hash": "def456", "createdAt": 1700000000, "createdBy": 1}
]`

const specReportFixture = `{
  "format": "openapi3",
  "basePath": "/v1",
  "basePathOrigin": "spec",
  "warnings": [
    {"pointer": "#/paths/~1widgets/get", "code": "missing_example", "message": "no example provided"},
    {"pointer": "#/paths/~1widgets/post", "code": "missing_example", "message": "no example provided"}
  ],
  "operations": 12,
  "degraded": 1
}`

func TestListSpecs_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: specsListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListSpecs(lb)(opsTestCtx(), nil, ListSpecsInput{})
	if err != nil {
		t.Fatalf("handleListSpecs: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (no specId supplied, no report call expected)", len(fc.calls))
	}
	if len(out.Specs) != 2 {
		t.Fatalf("len(Specs) = %d, want 2", len(out.Specs))
	}
	sp := out.Specs[0]
	if sp.ID != 3 || sp.Name != "Widgets API" || sp.Version != "1.2.0" || sp.Format != "openapi3" || sp.BasePath != "/v1" {
		t.Errorf("Specs[0] = %+v, want id=3 name=%q version=1.2.0 format=openapi3 basePath=/v1", sp, "Widgets API")
	}
	if sp.Operations != nil || sp.Degraded != nil || sp.Warnings != nil {
		t.Errorf("Specs[0] carries report fields without specId being requested: %+v", sp)
	}
}

// TestListSpecs_withSpecID_attachesReportCounts is C's own documented
// composition (§0/§C table row 3): a second call to GET .../report,
// attaching operations/degraded/warnings ONLY to the matching line.
func TestListSpecs_withSpecID_attachesReportCounts(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: specsListFixture},
		{status: http.StatusOK, body: specReportFixture},
	}}
	lb := newLoopback(fc)

	specID := int64(3)
	_, out, err := handleListSpecs(lb)(opsTestCtx(), nil, ListSpecsInput{SpecID: &specID})
	if err != nil {
		t.Fatalf("handleListSpecs: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fc.calls))
	}
	if fc.calls[1].path != "/api/specs/3/report" {
		t.Errorf("second call path = %q, want /api/specs/3/report", fc.calls[1].path)
	}

	var withReport, withoutReport *SpecLine
	for i := range out.Specs {
		if out.Specs[i].ID == 3 {
			withReport = &out.Specs[i]
		} else {
			withoutReport = &out.Specs[i]
		}
	}
	if withReport == nil || withoutReport == nil {
		t.Fatalf("Specs = %+v, want one line with id=3 and one without", out.Specs)
	}
	if withReport.Operations == nil || *withReport.Operations != 12 {
		t.Errorf("Operations = %v, want *12", withReport.Operations)
	}
	if withReport.Degraded == nil || *withReport.Degraded != 1 {
		t.Errorf("Degraded = %v, want *1", withReport.Degraded)
	}
	if withReport.Warnings == nil || *withReport.Warnings != 2 {
		t.Errorf("Warnings = %v, want *2 (len of the report's warnings array)", withReport.Warnings)
	}
	if withoutReport.Operations != nil || withoutReport.Degraded != nil || withoutReport.Warnings != nil {
		t.Errorf("the OTHER spec's line carries report fields it was never asked for: %+v", withoutReport)
	}
}

func TestListSpecs_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"spec not found"}}`},
	}}
	lb := newLoopback(fc)

	specID := int64(999)
	_, _, err := handleListSpecs(lb)(opsTestCtx(), nil, ListSpecsInput{SpecID: &specID})
	if err == nil {
		t.Fatal("handleListSpecs returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "spec not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestListSpecs_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleListSpecs(lb)(opsTestCtx(), nil, ListSpecsInput{})
	if err == nil {
		t.Fatal("handleListSpecs returned no error for a 500")
	}
}

// ---- shared fixtures: the merged operations list ----

// fixtureStatus/fixtureOverride/fixtureOp mirror mergedOperationView
// (override_handlers.go:116-122) closely enough to build a merged-list
// fixture programmatically — used by both find_operations and get_operation
// tests, since both compose GET .../operations first (§C-a).
type fixtureStatus struct {
	Selector   string `json:"selector"`
	HTTPStatus int    `json:"httpStatus"`
	IsDefault  bool   `json:"isDefault"`
	MediaType  string `json:"mediaType,omitempty"`
}

type fixtureOverride struct {
	OverrideOn bool `json:"overrideOn"`
}

type fixtureOp struct {
	Method   string           `json:"method"`
	Path     string           `json:"path"`
	OpKey    string           `json:"opKey"`
	Statuses []fixtureStatus  `json:"statuses"`
	Override *fixtureOverride `json:"override,omitempty"`
}

func marshalOps(t *testing.T, ops []fixtureOp) string {
	t.Helper()
	b, err := json.Marshal(ops)
	if err != nil {
		t.Fatalf("marshal fixture ops: %v", err)
	}
	return string(b)
}

// ---- find_operations ----

func threeOpsFixture() []fixtureOp {
	return []fixtureOp{
		{
			Method: "GET", Path: "/widgets", OpKey: "GET%20%2Fwidgets",
			Statuses: []fixtureStatus{{Selector: "200", HTTPStatus: 200, IsDefault: true, MediaType: "application/json"}, {Selector: "404", HTTPStatus: 404}},
			Override: &fixtureOverride{OverrideOn: true},
		},
		{
			Method: "POST", Path: "/widgets", OpKey: "POST%20%2Fwidgets",
			Statuses: []fixtureStatus{{Selector: "201", HTTPStatus: 201, IsDefault: true, MediaType: "application/json"}},
		},
		{
			Method: "GET", Path: "/gadgets", OpKey: "GET%20%2Fgadgets",
			Statuses: []fixtureStatus{{Selector: "200", HTTPStatus: 200, IsDefault: true, MediaType: "application/json"}},
		},
	}
}

// TestFindOperations_queryFilters is the filter half of §C's own requirement
// ("the filter actually filters, and the cap actually caps"): query="widget"
// must match the two /widgets operations and skip /gadgets, case-insensitively.
func TestFindOperations_queryFilters(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: marshalOps(t, threeOpsFixture())}}}
	lb := newLoopback(fc)

	_, out, err := handleFindOperations(lb)(opsTestCtx(), nil, FindOperationsInput{WorkspaceID: 12, Query: "WIDGET"})
	if err != nil {
		t.Fatalf("handleFindOperations: %v", err)
	}
	if fc.calls[0].path != "/api/workspaces/12/operations" {
		t.Errorf("call path = %q, want /api/workspaces/12/operations", fc.calls[0].path)
	}
	if out.Total != 2 || out.Returned != 2 || out.Truncated {
		t.Errorf("Total/Returned/Truncated = %d/%d/%v, want 2/2/false", out.Total, out.Returned, out.Truncated)
	}
	if len(out.Operations) != 2 {
		t.Fatalf("len(Operations) = %d, want 2", len(out.Operations))
	}
	first := out.Operations[0]
	if first.OpKey != "GET%20%2Fwidgets" || first.Method != http.MethodGet || first.Path != "/widgets" {
		t.Errorf("Operations[0] = %+v, want the GET /widgets row", first)
	}
	if len(first.Statuses) != 2 || first.Statuses[0] != 200 || first.Statuses[1] != 404 {
		t.Errorf("Operations[0].Statuses = %v, want [200 404]", first.Statuses)
	}
	if !first.Overridden {
		t.Error("Operations[0].Overridden = false, want true (fixture carries an override)")
	}
	second := out.Operations[1]
	if second.Overridden {
		t.Error("Operations[1].Overridden = true, want false (fixture carries no override)")
	}
	for _, op := range out.Operations {
		if op.Path == "/gadgets" {
			t.Errorf("query %q matched /gadgets, which does not contain \"widget\"", "WIDGET")
		}
	}
}

// TestFindOperations_capTruncates is the cap half: more matches than the
// requested (and max) limit allows must come back marked Truncated, with
// Total still reporting the full match count — never silently hiding rows
// (§C's own compaction rule: "no such operation" read off a capped list is
// the failure this guards against).
func TestFindOperations_capTruncates(t *testing.T) {
	t.Parallel()
	// 150 rows: comfortably over BOTH findOperationsDefaultLimit (20) and
	// findOperationsMaxLimit (100) — the earlier draft of this test used 30,
	// which is over the default but UNDER the max, so it could not actually
	// exercise the max-clamp assertion below (min(30,100) is 30 regardless
	// of clamping); caught by running this test, not by inspection.
	const totalOps = 150
	ops := make([]fixtureOp, 0, totalOps)
	for i := 0; i < totalOps; i++ {
		ops = append(ops, fixtureOp{
			Method: "GET", Path: "/items", OpKey: "GET%20%2Fitems",
			Statuses: []fixtureStatus{{Selector: "200", HTTPStatus: 200, IsDefault: true}},
		})
	}
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: marshalOps(t, ops)}}}
	lb := newLoopback(fc)

	// Default limit (no Limit supplied): findOperationsDefaultLimit caps it.
	_, out, err := handleFindOperations(lb)(opsTestCtx(), nil, FindOperationsInput{WorkspaceID: 1})
	if err != nil {
		t.Fatalf("handleFindOperations: %v", err)
	}
	if out.Total != totalOps {
		t.Errorf("Total = %d, want %d", out.Total, totalOps)
	}
	if out.Returned != findOperationsDefaultLimit {
		t.Errorf("Returned = %d, want the default limit %d", out.Returned, findOperationsDefaultLimit)
	}
	if !out.Truncated {
		t.Errorf("Truncated = false, want true (%d matches, only the default limit returned)", totalOps)
	}

	// An explicit Limit above findOperationsMaxLimit is clamped, not honored
	// verbatim — a caller cannot widen the cap past the hard ceiling.
	fc2 := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: marshalOps(t, ops)}}}
	_, out2, err := handleFindOperations(newLoopback(fc2))(opsTestCtx(), nil, FindOperationsInput{WorkspaceID: 1, Limit: 10000})
	if err != nil {
		t.Fatalf("handleFindOperations: %v", err)
	}
	if out2.Returned != findOperationsMaxLimit {
		t.Errorf("Returned = %d with Limit=10000, want it clamped to %d", out2.Returned, findOperationsMaxLimit)
	}
	if !out2.Truncated {
		t.Errorf("Truncated = false with Limit=10000 over %d matches (clamped to %d), want true", totalOps, findOperationsMaxLimit)
	}

	// A Limit below the default is honored as a narrower cap.
	fc3 := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: marshalOps(t, ops)}}}
	_, out3, err := handleFindOperations(newLoopback(fc3))(opsTestCtx(), nil, FindOperationsInput{WorkspaceID: 1, Limit: 5})
	if err != nil {
		t.Fatalf("handleFindOperations: %v", err)
	}
	if out3.Returned != 5 {
		t.Errorf("Returned = %d with Limit=5, want 5", out3.Returned)
	}
	if !out3.Truncated {
		t.Errorf("Truncated = false with Limit=5 over %d matches, want true", totalOps)
	}
}

func TestFindOperations_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleFindOperations(newLoopback(fc))(opsTestCtx(), nil, FindOperationsInput{WorkspaceID: 999})
	if err == nil {
		t.Fatal("handleFindOperations returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestFindOperations_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleFindOperations(newLoopback(fc))(opsTestCtx(), nil, FindOperationsInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleFindOperations returned no error for a 500")
	}
}

// ---- get_operation ----

const overrideDetailFixture = `{
  "method": "GET",
  "path": "/widgets",
  "opKey": "GET%20%2Fwidgets",
  "overrideOn": true,
  "routeOff": false,
  "activeStatus": 200,
  "responses": {
    "200": {"mode": "pinned", "mediaType": "application/json", "body": {"id":1,"name":"gizmo"}, "recipes": {"id": {"kind":"uuid"}}},
    "404": {},
    "401": {"function": "return 401, {error = 'bad'}"}
  },
  "listSize": {"min": 1, "max": 5},
  "delayMs": 250,
  "validateReq": true,
  "updatedAt": 1700000500
}`

// TestGetOperation_happyPath_withOverride is get_operation's full composition
// (§C-a): the merged list supplies method/path/statuses, the single-operation
// route supplies the override's shape — never its pinned body bytes (§C's
// compaction rule).
func TestGetOperation_happyPath_withOverride(t *testing.T) {
	t.Parallel()
	ops := []fixtureOp{{
		Method: "GET", Path: "/widgets", OpKey: "GET%20%2Fwidgets",
		Statuses: []fixtureStatus{
			{Selector: "200", HTTPStatus: 200, IsDefault: true, MediaType: "application/json"},
			{Selector: "404", HTTPStatus: 404, IsDefault: false},
		},
	}}
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: marshalOps(t, ops)},
		{status: http.StatusOK, body: overrideDetailFixture},
	}}
	lb := newLoopback(fc)

	_, out, err := handleGetOperation(lb)(opsTestCtx(), nil, GetOperationInput{WorkspaceID: 12, OpKey: "GET%20%2Fwidgets"})
	if err != nil {
		t.Fatalf("handleGetOperation: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fc.calls))
	}
	if fc.calls[0].path != "/api/workspaces/12/operations" {
		t.Errorf("first call path = %q, want /api/workspaces/12/operations", fc.calls[0].path)
	}
	if fc.calls[1].method != http.MethodGet || fc.calls[1].path != "/api/workspaces/12/operations/GET%20%2Fwidgets" {
		t.Errorf("second call = %s %s, want GET /api/workspaces/12/operations/GET%%20%%2Fwidgets", fc.calls[1].method, fc.calls[1].path)
	}

	op := out.Operation
	if op.OpKey != "GET%20%2Fwidgets" || op.Method != http.MethodGet || op.Path != "/widgets" {
		t.Errorf("Operation = %+v, want the GET /widgets row", op)
	}
	if len(op.Statuses) != 2 || op.Statuses[0].HTTPStatus != 200 || op.Statuses[1].HTTPStatus != 404 {
		t.Errorf("Statuses = %+v, want [200 404]", op.Statuses)
	}
	if op.Override == nil {
		t.Fatal("Override = nil, want the stored document's shape")
	}
	ov := op.Override
	if !ov.OverrideOn {
		t.Error("OverrideOn = false, want true")
	}
	if ov.RouteOff {
		t.Error("RouteOff = true, want false")
	}
	if ov.ActiveStatus == nil || *ov.ActiveStatus != 200 {
		t.Errorf("ActiveStatus = %v, want *200", ov.ActiveStatus)
	}
	if ov.DelayMs == nil || *ov.DelayMs != 250 {
		t.Errorf("DelayMs = %v, want *250", ov.DelayMs)
	}
	if ov.ListSize == nil || ov.ListSize.Min != 1 || ov.ListSize.Max != 5 {
		t.Errorf("ListSize = %v, want {1 5}", ov.ListSize)
	}
	if ov.ValidateReq == nil || !*ov.ValidateReq {
		t.Errorf("ValidateReq = %v, want *true", ov.ValidateReq)
	}
	if ov.UpdatedAt != 1700000500 {
		t.Errorf("UpdatedAt = %d, want 1700000500", ov.UpdatedAt)
	}
	rs200, ok := ov.Responses["200"]
	if !ok {
		t.Fatal("Responses has no \"200\" entry")
	}
	if rs200.Mode != "pinned" || rs200.MediaType != "application/json" || !rs200.HasBody || rs200.RecipeCount != 1 {
		t.Errorf("Responses[200] = %+v, want mode=pinned mediaType=application/json hasBody=true recipeCount=1", rs200)
	}
	// Review finding 5: a function variant used to project as
	// {mode:"generated", hasBody:false} — indistinguishable from an empty
	// one — and the guide's read-then-write-whole-document flow deleted
	// the Lua. The source comes back verbatim.
	if rs401 := ov.Responses["401"]; rs401.Function != "return 401, {error = 'bad'}" {
		t.Errorf("Responses[401] = %+v, want the function source verbatim", rs401)
	}
	rs404, ok := ov.Responses["404"]
	if !ok {
		t.Fatal("Responses has no \"404\" entry")
	}
	if rs404.Mode != "generated" || rs404.HasBody || rs404.RecipeCount != 0 {
		t.Errorf("Responses[404] = %+v, want the documented default mode=generated, no body, no recipes", rs404)
	}
}

// TestGetOperation_noOverrideIsNotAnError is §C-a's whole point, pinned by a
// test: a 404 from the single-operation route means "nobody has overridden
// this operation yet", not a failure — the exact operation an agent most
// wants to inspect.
func TestGetOperation_noOverrideIsNotAnError(t *testing.T) {
	t.Parallel()
	ops := []fixtureOp{{
		Method: "GET", Path: "/gadgets", OpKey: "GET%20%2Fgadgets",
		Statuses: []fixtureStatus{{Selector: "200", HTTPStatus: 200, IsDefault: true}},
	}}
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: marshalOps(t, ops)},
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"no override for this operation"}}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleGetOperation(lb)(opsTestCtx(), nil, GetOperationInput{WorkspaceID: 1, OpKey: "GET%20%2Fgadgets"})
	if err != nil {
		t.Fatalf("handleGetOperation: %v, want no error on a pristine operation", err)
	}
	if out.Operation.Override != nil {
		t.Errorf("Override = %+v, want nil for an operation nobody has overridden", out.Operation.Override)
	}
	if out.Operation.OpKey != "GET%20%2Fgadgets" {
		t.Errorf("OpKey = %q, want the merged row's own opKey", out.Operation.OpKey)
	}
}

// TestGetOperation_unknownOpKeyIsToolError covers the OTHER 404-shaped case
// §C-a distinguishes: an opKey that names no operation at all in this
// workspace's spec. The single-operation route must never even be called —
// scriptedCaller has only one response queued, so a second call would fail
// the test with a clear message rather than silently reusing a response.
func TestGetOperation_unknownOpKeyIsToolError(t *testing.T) {
	t.Parallel()
	ops := []fixtureOp{{Method: "GET", Path: "/gadgets", OpKey: "GET%20%2Fgadgets"}}
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: marshalOps(t, ops)}}}
	lb := newLoopback(fc)

	_, _, err := handleGetOperation(lb)(opsTestCtx(), nil, GetOperationInput{WorkspaceID: 1, OpKey: "GET%20%2Fnope"})
	if err == nil {
		t.Fatal("handleGetOperation returned no error for an opKey absent from the merged list")
	}
	if !strings.Contains(err.Error(), "GET%20%2Fnope") {
		t.Errorf("error = %q, want it to name the offending opKey", err.Error())
	}
}

func TestGetOperation_listRoute404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleGetOperation(newLoopback(fc))(opsTestCtx(), nil, GetOperationInput{WorkspaceID: 999, OpKey: "GET%20%2Fx"})
	if err == nil {
		t.Fatal("handleGetOperation returned no error when the merged list itself 404s")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestGetOperation_listRoute500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleGetOperation(newLoopback(fc))(opsTestCtx(), nil, GetOperationInput{WorkspaceID: 1, OpKey: "GET%20%2Fx"})
	if err == nil {
		t.Fatal("handleGetOperation returned no error when the merged list 500s")
	}
}

// TestGetOperation_singleRouteOtherErrorIsToolError proves the single-
// operation call's error handling does not treat EVERY non-2xx as "no
// override" — only exactly 404 gets that treatment; a 500 there still
// surfaces as a tool error.
func TestGetOperation_singleRouteOtherErrorIsToolError(t *testing.T) {
	t.Parallel()
	ops := []fixtureOp{{Method: "GET", Path: "/widgets", OpKey: "GET%20%2Fwidgets"}}
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: marshalOps(t, ops)},
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleGetOperation(newLoopback(fc))(opsTestCtx(), nil, GetOperationInput{WorkspaceID: 1, OpKey: "GET%20%2Fwidgets"})
	if err == nil {
		t.Fatal("handleGetOperation returned no error when the single-operation route 500s")
	}
}

// ---- set_operation_response ----

// TestSetOperationResponse_preservesPinnedBodyAndSetsFlags is §C-b's central
// test: forcing a status on an operation that already has a PINNED body must
// resend that body byte-for-byte, never silently drop it, and must
// unconditionally turn overrideOn on and routeOff off. The opKey deliberately
// contains "%2F" (computed via url.PathEscape, not hand-typed) so the request
// path assertion below also proves operationPath substitutes it exactly
// once — a second escape pass would turn "%2F" into "%252F" (§C-c).
func TestSetOperationResponse_preservesPinnedBodyAndSetsFlags(t *testing.T) {
	t.Parallel()
	opKey := url.PathEscape("GET /widgets/{id}")

	getBody := `{
		"method": "GET",
		"path": "/widgets/{id}",
		"opKey": "` + opKey + `",
		"overrideOn": true,
		"routeOff": false,
		"activeStatus": 200,
		"responses": {
			"200": {"mode":"pinned","mediaType":"application/json","body":{"id":1,"name":"gizmo","tags":["a","b"]},"headers":{"X-Trace":"abc"}}
		},
		"listSize": {"min":2,"max":9},
		"delayMs": 375,
		"failDirective": {"kind":"timeout","after":3},
		"validateReq": true,
		"updatedAt": 1700000900
	}`
	putResp := `{
		"method": "GET", "path": "/widgets/{id}", "opKey": "` + opKey + `",
		"overrideOn": true, "routeOff": false, "activeStatus": 503,
		"responses": {}, "updatedAt": 1700001000, "revision": 5
	}`

	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: getBody},
		{status: http.StatusOK, body: putResp},
	}}
	lb := newLoopback(fc)

	_, out, err := handleSetOperationResponse(lb)(opsTestCtx(), nil, SetOperationResponseInput{
		WorkspaceID: 12, OpKey: opKey, Status: 503,
	})
	if err != nil {
		t.Fatalf("handleSetOperationResponse: %v", err)
	}

	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (GET then PUT)", len(fc.calls))
	}
	wantPath := "/api/workspaces/12/operations/" + opKey
	if fc.calls[0].method != http.MethodGet || fc.calls[0].path != wantPath {
		t.Errorf("first call = %s %s, want GET %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}
	if fc.calls[1].method != http.MethodPut || fc.calls[1].path != wantPath {
		t.Errorf("second call = %s %s, want PUT %s (opKey substituted exactly once, never re-escaped)", fc.calls[1].method, fc.calls[1].path, wantPath)
	}

	sent := decodeBody(t, fc.calls[1].body)
	if sent["overrideOn"] != true {
		t.Errorf("sent overrideOn = %v, want true", sent["overrideOn"])
	}
	if sent["routeOff"] != false {
		t.Errorf("sent routeOff = %v, want false", sent["routeOff"])
	}
	if sent["activeStatus"] != float64(503) {
		t.Errorf("sent activeStatus = %v, want 503", sent["activeStatus"])
	}
	if sent["delayMs"] != float64(375) {
		t.Errorf("sent delayMs = %v, want 375 (preserved from GET)", sent["delayMs"])
	}
	if sent["validateReq"] != true {
		t.Errorf("sent validateReq = %v, want true (preserved from GET)", sent["validateReq"])
	}
	listSize, _ := sent["listSize"].(map[string]any)
	if listSize["min"] != float64(2) || listSize["max"] != float64(9) {
		t.Errorf("sent listSize = %v, want {min:2 max:9} (preserved from GET)", sent["listSize"])
	}
	failDirective, _ := sent["failDirective"].(map[string]any)
	if failDirective["kind"] != "timeout" || failDirective["after"] != float64(3) {
		t.Errorf("sent failDirective = %v, want {kind:timeout after:3} (preserved from GET)", sent["failDirective"])
	}

	responses, _ := sent["responses"].(map[string]any)
	r200, _ := responses["200"].(map[string]any)
	if r200 == nil {
		t.Fatalf("sent responses = %v, want a preserved \"200\" entry", sent["responses"])
	}
	if r200["mode"] != "pinned" || r200["mediaType"] != "application/json" {
		t.Errorf("sent responses[200] mode/mediaType = %v/%v, want pinned/application/json", r200["mode"], r200["mediaType"])
	}
	body, _ := r200["body"].(map[string]any)
	if body["id"] != float64(1) || body["name"] != "gizmo" {
		// The core assertion §C-b's own test requirement names: setting a
		// status on an operation WITH a pinned body must preserve that body.
		t.Errorf("sent responses[200].body = %v, want the pinned {id:1 name:gizmo tags:[a b]} preserved byte-for-byte", body)
	}
	headers, _ := r200["headers"].(map[string]any)
	if headers["X-Trace"] != "abc" {
		t.Errorf("sent responses[200].headers = %v, want X-Trace preserved", headers)
	}

	if out.OpKey != opKey || out.ActiveStatus != 503 {
		t.Errorf("output = %+v, want opKey=%s activeStatus=503", out, opKey)
	}
	if len(out.Changed) != 1 || !strings.Contains(out.Changed[0], "200") || !strings.Contains(out.Changed[0], "503") {
		t.Errorf("Changed = %v, want exactly one entry describing activeStatus 200 -> 503", out.Changed)
	}
}

// TestSetOperationResponse_seedsFreshDocumentOn404 is §C-b's pristine-
// operation case: a 404 from the GET half must not fail the tool — it seeds
// a brand-new document instead, carrying nothing forward because there was
// nothing to preserve.
func TestSetOperationResponse_seedsFreshDocumentOn404(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"no override for this operation"}}`},
		{status: http.StatusOK, body: `{"overrideOn":true,"routeOff":false,"activeStatus":503,"responses":{},"updatedAt":1700001100,"revision":2}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleSetOperationResponse(lb)(opsTestCtx(), nil, SetOperationResponseInput{
		WorkspaceID: 1, OpKey: "GET%20%2Fwidgets", Status: 503,
	})
	if err != nil {
		t.Fatalf("handleSetOperationResponse: %v, want success seeding a fresh document", err)
	}

	sent := decodeBody(t, fc.calls[1].body)
	if sent["overrideOn"] != true || sent["routeOff"] != false || sent["activeStatus"] != float64(503) {
		t.Errorf("sent = %v, want overrideOn=true routeOff=false activeStatus=503", sent)
	}
	for _, key := range []string{"responses", "listSize", "delayMs", "failDirective", "validateReq"} {
		if _, ok := sent[key]; ok {
			t.Errorf("sent body carries %q with nothing to preserve from a pristine operation: %v", key, sent)
		}
	}

	if out.ActiveStatus != 503 {
		t.Errorf("ActiveStatus = %d, want 503", out.ActiveStatus)
	}
	if len(out.Changed) != 1 || out.Changed[0] != "overrideOn: false -> true" {
		t.Errorf("Changed = %v, want exactly [\"overrideOn: false -> true\"]", out.Changed)
	}
}

func TestSetOperationResponse_GET500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleSetOperationResponse(lb)(opsTestCtx(), nil, SetOperationResponseInput{WorkspaceID: 1, OpKey: "GET%20%2Fx", Status: 503})
	if err == nil {
		t.Fatal("handleSetOperationResponse returned no error when GET 500s")
	}
	if len(fc.calls) != 1 {
		t.Errorf("calls = %d, want 1 (PUT must not run after a failed GET)", len(fc.calls))
	}
}

// TestSetOperationResponse_PUT404IsToolError covers overrides.ErrWorkspaceNotFound's
// PUT-time 404 (override_handlers.go:375-379) — distinct from the GET-time 404
// tested above, which means "no override" rather than an error.
func TestSetOperationResponse_PUT404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"overrideOn":true,"routeOff":false,"activeStatus":200,"responses":{},"updatedAt":1}`},
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleSetOperationResponse(lb)(opsTestCtx(), nil, SetOperationResponseInput{WorkspaceID: 1, OpKey: "GET%20%2Fx", Status: 503})
	if err == nil {
		t.Fatal("handleSetOperationResponse returned no error when PUT 404s")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestSetOperationResponse_PUT500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"overrideOn":true,"routeOff":false,"activeStatus":200,"responses":{},"updatedAt":1}`},
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleSetOperationResponse(lb)(opsTestCtx(), nil, SetOperationResponseInput{WorkspaceID: 1, OpKey: "GET%20%2Fx", Status: 503})
	if err == nil {
		t.Fatal("handleSetOperationResponse returned no error when PUT 500s")
	}
}

// ---- registration: honest annotations, real output schemas ----

// TestAddOperationTools_registersSixToolsWithHonestAnnotations drives a real
// tools/list JSON-RPC round trip (not a direct call to registerTools) so it
// exercises exactly what a client sees. It asserts PRESENCE and correctness
// for this file's own six tools among whatever tools/list returns —
// deliberately not an exact count, since internal/mcp/tools_traffic.go's
// three tools are a LATER phase's file this test must keep passing against
// once they exist (§F: ownership of that file is not this one's).
func TestAddOperationTools_registersSixToolsWithHonestAnnotations(t *testing.T) {
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

	want := map[string]struct {
		readOnly   bool
		idempotent bool
	}{
		"list_workspaces":        {true, true},
		"create_workspace":       {false, false},
		"list_specs":             {true, true},
		"find_operations":        {true, true},
		"get_operation":          {true, true},
		"set_operation_response": {false, true},
	}

	seen := make(map[string]bool, len(want))
	for _, tool := range env.Result.Tools {
		w, ok := want[tool.Name]
		if !ok {
			continue // one of the OTHER file's tools — not this test's business.
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s: empty Description", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema published — declared output type must not be any", tool.Name)
		}
		if tool.Annotations.ReadOnlyHint != w.readOnly {
			t.Errorf("%s: ReadOnlyHint = %v, want %v", tool.Name, tool.Annotations.ReadOnlyHint, w.readOnly)
		}
		if tool.Annotations.IdempotentHint != w.idempotent {
			t.Errorf("%s: IdempotentHint = %v, want %v", tool.Name, tool.Annotations.IdempotentHint, w.idempotent)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("tools/list did not include %q", name)
		}
	}
}
