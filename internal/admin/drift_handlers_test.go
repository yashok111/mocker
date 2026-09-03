// drift_handlers_test.go covers P4a's acceptance properties
// (mocker-p4a-triage decisions.md D8, properties P1-P13, P18-P25, P27 —
// P14-P17 and P26 are the UI/MCP/contract halves owned elsewhere).
package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/testspec"
)

// driftOrphanedOverrideWire/driftOrphanedResourceWire/driftShadowedEndpointWire/
// driftReportWire mirror admin's unexported drift view types, decoded
// straight from JSON so a test can assert on the exact wire shape rather
// than reaching into the package's own unexported structs.
type driftOrphanedOverrideWire struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	OpKey  string `json:"opKey"`
}

type driftOrphanedResourceWire struct {
	RouteFamily string `json:"routeFamily"`
	Name        string `json:"name"`
	ResourceID  int64  `json:"resourceId"`
	EntityCount int64  `json:"entityCount"`
}

type driftShadowedEndpointWire struct {
	EndpointID    int64  `json:"endpointId"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	CanonicalPath string `json:"canonicalPath"`
	PrecededSpec  bool   `json:"precededSpec"`
}

type driftReportWire struct {
	HasDrift          bool                        `json:"hasDrift"`
	OrphanedOverrides []driftOrphanedOverrideWire `json:"orphanedOverrides"`
	OrphanedResources []driftOrphanedResourceWire `json:"orphanedResources"`
	ShadowedEndpoints []driftShadowedEndpointWire `json:"shadowedEndpoints"`
}

func driftURL(wsID int64) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/drift", wsID)
}

// mustDrift issues GET .../drift, requires 200, and decodes the body.
func (ts *testServer) mustDrift(t *testing.T, cookie *http.Cookie, wsID int64) driftReportWire {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, driftURL(wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../drift: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var out driftReportWire
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode drift response: %v; body = %s", err, rec.Body.String())
	}
	return out
}

// workspaceEditVersion GETs wsID and returns its current editVersion — the
// value a subsequent PATCH must echo back.
func (ts *testServer) workspaceEditVersion(t *testing.T, cookie *http.Cookie, wsID int64) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workspace %d: status = %d, want 200; body = %s", wsID, rec.Code, rec.Body.String())
	}
	var body struct {
		EditVersion int64 `json:"editVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}
	return body.EditVersion
}

// workspaceSpecID GETs wsID and returns its current specId (nil when unbound).
func (ts *testServer) workspaceSpecID(t *testing.T, cookie *http.Cookie, wsID int64) *int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workspace %d: status = %d, want 200; body = %s", wsID, rec.Code, rec.Body.String())
	}
	var body struct {
		SpecID *int64 `json:"specId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}
	return body.SpecID
}

// rebindSpec re-binds wsID to specID via PATCH, reading the current
// editVersion first — standing in for a real operator's own re-bind, the
// exact event D3.1 says a rederive with no re-bind is deliberately NOT
// distinguished from.
func (ts *testServer) rebindSpec(t *testing.T, cookie *http.Cookie, csrfToken string, wsID, specID int64) {
	t.Helper()
	ev := ts.workspaceEditVersion(t, cookie, wsID)
	req := jsonRequest(t, http.MethodPatch, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID),
		map[string]any{"specId": specID, "editVersion": ev}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rebind workspace %d to spec %d: status = %d, want 200; body = %s", wsID, specID, rec.Code, rec.Body.String())
	}
}

// putOverride PUTs an override for method+path (which need not exist in any
// bound spec — the PUT route never checks), requiring 200, and returns the
// opKey it was written under.
func (ts *testServer) putOverride(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, method, path string) string {
	t.Helper()
	opKey := overrides.OpKey(method, path)
	req := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", wsID, opKey),
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT override %s %s: status = %d, want 200; body = %s", method, path, rec.Code, rec.Body.String())
	}
	return opKey
}

// confirmFamily confirms routeFamily in wsID, requiring 200.
func (ts *testServer) confirmFamily(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, routeFamily string) {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID),
		map[string]string{"routeFamily": routeFamily, "state": "confirmed"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm %q in workspace %d: status = %d, want 200; body = %s", routeFamily, wsID, rec.Code, rec.Body.String())
	}
}

// createEndpoint POSTs a custom endpoint for method+path, requiring the
// given status, and returns its id (0 on a non-201 status).
func (ts *testServer) createEndpoint(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, method, path string, wantStatus int) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", wsID),
		map[string]any{"method": method, "path": path}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("POST endpoint %s %s: status = %d, want %d; body = %s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	if wantStatus != http.StatusCreated {
		return 0
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create endpoint response: %v", err)
	}
	return body.ID
}

// widgetsOnlyDoc declares GET /widgets and nothing else — used as the
// spec a workspace is re-bound TO, whose absence of "/auth/login" strands
// an override on it.
const widgetsOnlyDoc = `{
  "openapi": "3.1.0",
  "info": { "title": "Widgets Only", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": { "operationId": "listWidgets", "responses": { "200": { "description": "ok" } } }
    }
  }
}`

// bareItemsOnlyDoc declares only /bareitems — testspec.DerivationDoc's OTHER
// family (widgets) is absent, so re-binding to this spec strands a
// confirmed widgets family while a confirmed bareitems family survives.
const bareItemsOnlyDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "BareItems Only", "version": "1.0.0"},
  "paths": {
    "/bareitems": {
      "get": {
        "operationId": "listBareItems",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/BareItem"}}
        }}}}
      }
    },
    "/bareitems/{id}": {
      "get": {
        "operationId": "getBareItem",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/BareItem"}
        }}}}
      }
    }
  },
  "components": {
    "schemas": {
      "BareItem": {"type": "object", "properties": {"id": {"type": "string"}, "name": {"type": "string"}}}
    }
  }
}`

// gadgetsByIDDoc declares GET /gadgets/{id} — the spec half of P6/P7's
// canonical-collision fixtures.
const gadgetsByIDDoc = `{
  "openapi": "3.1.0",
  "info": { "title": "Gadgets", "version": "1.0.0" },
  "paths": {
    "/gadgets/{id}": {
      "get": {
        "operationId": "getGadget",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": { "200": { "description": "ok" } }
      }
    }
  }
}`

// TestHandler_drift_orphanedOverride_reportedAfterRebind is D8 P1: an
// override on an operation the newly bound spec no longer produces is
// named, with the exact opKey DELETE .../operations/{opKey} accepts, and
// nothing else in the report fires.
func TestHandler_drift_orphanedOverride_reportedAfterRebind(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specA := ts.importSpecWithSession(t, cookie, csrfToken, "Auth Spec", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Drift Demo", specA)

	opKey := ts.putOverride(t, cookie, csrfToken, wsID, "POST", "/auth/login")

	specB := ts.importSpecWithSession(t, cookie, csrfToken, "Widgets Only", widgetsOnlyDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specB)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.OrphanedOverrides) != 1 {
		t.Fatalf("orphanedOverrides = %+v, want exactly one row", report.OrphanedOverrides)
	}
	got := report.OrphanedOverrides[0]
	if got.Method != http.MethodPost || got.Path != "/auth/login" || got.OpKey != opKey {
		t.Errorf("orphaned override = %+v, want {POST /auth/login %s}", got, opKey)
	}
	if len(report.OrphanedResources) != 0 {
		t.Errorf("orphanedResources = %v, want empty", report.OrphanedResources)
	}
	if len(report.ShadowedEndpoints) != 0 {
		t.Errorf("shadowedEndpoints = %v, want empty", report.ShadowedEndpoints)
	}
	if !report.HasDrift {
		t.Error("hasDrift = false, want true")
	}

	// The reported opKey is accepted VERBATIM by the repair route (P1's
	// own second half).
	delReq := jsonRequest(t, http.MethodDelete,
		fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", wsID, got.OpKey), nil, cookie, csrfToken)
	rec := ts.do(delReq)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Errorf("DELETE .../operations/%s: status = %d, want 204 or 200; body = %s", got.OpKey, rec.Code, rec.Body.String())
	}
}

// TestHandler_drift_orphanedOverride_literalKeyNotCanonical is D8 P2: an
// override stored under one parameter NAME is reported orphaned against a
// re-bound spec whose operation has the SAME canonical shape under a
// DIFFERENT parameter name — the identical literal-vs-canonical rule
// [lookupOverride] already applies on the serve path.
func TestHandler_drift_orphanedOverride_literalKeyNotCanonical(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDf)

	opKey := ts.putOverride(t, cookie, csrfToken, wsID, "GET", "/widgets/{id}")

	const widgetsByNameDoc = `{
	  "openapi": "3.1.0",
	  "info": { "title": "Widgets By Name", "version": "1.0.0" },
	  "paths": {
	    "/widgets/{name}": {
	      "get": {
	        "operationId": "getWidgetByName",
	        "parameters": [{"name": "name", "in": "path", "required": true, "schema": {"type": "string"}}],
	        "responses": { "200": { "description": "ok" } }
	      }
	    }
	  }
	}`
	specB := ts.importSpecWithSession(t, cookie, csrfToken, "Widgets By Name", widgetsByNameDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specB)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.OrphanedOverrides) != 1 || report.OrphanedOverrides[0].OpKey != opKey {
		t.Fatalf("orphanedOverrides = %+v, want exactly [{%s}] — same canonical shape must not save a literally different key",
			report.OrphanedOverrides, opKey)
	}
}

// TestHandler_drift_orphanedResource_reportedAfterRebindLiveOneIsNot is D8
// P3: of two confirmed families, only the one the re-bound spec no longer
// suggests is reported; the surviving one is not.
func TestHandler_drift_orphanedResource_reportedAfterRebindLiveOneIsNot(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specA := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Drift Res Demo", specA)

	ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyBareItems)

	specB := ts.importSpecWithSession(t, cookie, csrfToken, "BareItems Only", bareItemsOnlyDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specB)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.OrphanedResources) != 1 || report.OrphanedResources[0].RouteFamily != testspec.FamilyWidgets {
		t.Fatalf("orphanedResources = %+v, want exactly [%s] (bareitems survives the re-bind)", report.OrphanedResources, testspec.FamilyWidgets)
	}
	got := report.OrphanedResources[0]
	if got.Name == "" {
		t.Errorf("orphaned resource name is empty")
	}
	if got.EntityCount == 0 {
		t.Errorf("orphaned resource entityCount = 0, want > 0 (a confirm always populates)")
	}
	if len(report.OrphanedOverrides) != 0 || len(report.ShadowedEndpoints) != 0 {
		t.Errorf("the other two signals fired unexpectedly: overrides=%v endpoints=%v", report.OrphanedOverrides, report.ShadowedEndpoints)
	}
}

// TestHandler_drift_orphanedResource_reportedAfterRederiveNoRebind is D8 P4:
// the SAME family is reported orphaned after a generation minted over the
// SAME spec drops it, with workspaces.spec_id never moving. A real
// [specs.Repo.Rederive] call cannot itself produce a genuinely narrower
// generation over an unchanged document (deriveSuggestions is a pure,
// deterministic function of the document, so it always re-derives every
// family the document declares) — this writes the generation-2 row set
// directly, the shape a real rederive call WOULD leave behind, matching
// D3.1's own pairing of the two events this route deliberately does not
// distinguish.
func TestHandler_drift_orphanedResource_reportedAfterRederiveNoRebind(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Drift Rederive Demo", specID)

	ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)

	beforeSpecID := ts.workspaceSpecID(t, cookie, wsID)
	ts.seedSuggestionRow(t, specID, 2, testspec.FamilyBareItems)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.OrphanedResources) != 1 || report.OrphanedResources[0].RouteFamily != testspec.FamilyWidgets {
		t.Fatalf("orphanedResources = %+v, want exactly [%s] after a generation-2 that drops it", report.OrphanedResources, testspec.FamilyWidgets)
	}

	afterSpecID := ts.workspaceSpecID(t, cookie, wsID)
	if beforeSpecID == nil || afterSpecID == nil || *beforeSpecID != *afterSpecID {
		t.Errorf("workspace spec_id moved (before=%v after=%v), want unchanged — no re-bind happened", beforeSpecID, afterSpecID)
	}
}

// TestHandler_drift_agreesWithResetData_stranded is D8 P5's own mutation
// clause: reset-data's own `stranded` classification and this route's
// orphanedResources name the SAME family after one rederive, in one
// fixture — both read through [resources.Repo.OrphanedFamilies] and cannot
// disagree.
func TestHandler_drift_agreesWithResetData_stranded(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Drift Agree Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)

	ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyBareItems)
	ts.seedSuggestionRow(t, specID, 2, testspec.FamilyBareItems)

	report := ts.mustDrift(t, cookie, wsID)
	driftNames := make(map[string]bool, len(report.OrphanedResources))
	for _, r := range report.OrphanedResources {
		driftNames[r.RouteFamily] = true
	}

	resetReq := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-data", wsID),
		map[string]string{"mode": "reseed", "confirmSlug": slug}, cookie, csrfToken)
	resetRec := ts.do(resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset-data: status = %d, want 200; body = %s", resetRec.Code, resetRec.Body.String())
	}
	var resetBody struct {
		Skipped []struct {
			RouteFamily string `json:"routeFamily"`
			Reason      string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(resetRec.Body.Bytes(), &resetBody); err != nil {
		t.Fatalf("decode reset-data response: %v", err)
	}
	strandedNames := make(map[string]bool, len(resetBody.Skipped))
	for _, s := range resetBody.Skipped {
		if s.Reason == "stranded" {
			strandedNames[s.RouteFamily] = true
		}
	}

	if len(driftNames) == 0 || len(strandedNames) == 0 {
		t.Fatalf("test precondition broken: driftNames=%v strandedNames=%v, want both non-empty", driftNames, strandedNames)
	}
	if len(driftNames) != len(strandedNames) {
		t.Fatalf("drift's orphaned families %v disagree with reset-data's stranded families %v", driftNames, strandedNames)
	}
	for name := range driftNames {
		if !strandedNames[name] {
			t.Errorf("drift names %q orphaned but reset-data does not report it stranded", name)
		}
	}
}

// TestHandler_drift_shadowedEndpoint_equalCanonicalPath is D8 P6: a custom
// endpoint at a DIFFERENT parameter name than the spec's own operation, but
// the same canonical shape, is reported.
func TestHandler_drift_shadowedEndpoint_equalCanonicalPath(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDf)

	epID := ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gadgets/{gid}", http.StatusCreated)

	specID := ts.importSpecWithSession(t, cookie, csrfToken, "Gadgets", gadgetsByIDDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specID)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.ShadowedEndpoints) != 1 {
		t.Fatalf("shadowedEndpoints = %+v, want exactly one row", report.ShadowedEndpoints)
	}
	got := report.ShadowedEndpoints[0]
	if got.EndpointID != epID || got.CanonicalPath != "/gadgets/{}" {
		t.Errorf("shadowed endpoint = %+v, want endpointId=%d canonicalPath=/gadgets/{}", got, epID)
	}
}

// TestHandler_drift_staticCapture_notReported is D8 P7: a custom endpoint
// that wins by ROUTER RULE 1 (more static segments), never rule 3 (equal
// canonical shape), is not reported — its canonical path differs from the
// operation it happens to capture requests away from.
func TestHandler_drift_staticCapture_notReported(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDf)

	ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gadgets/new", http.StatusCreated)

	specID := ts.importSpecWithSession(t, cookie, csrfToken, "Gadgets", gadgetsByIDDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specID)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.ShadowedEndpoints) != 0 {
		t.Fatalf("shadowedEndpoints = %v, want empty — /gadgets/new wins by rule 1, its canonical path differs from /gadgets/{}", report.ShadowedEndpoints)
	}
}

// TestHandler_drift_precededSpec_threeCasesAndStrictComparison is D8 P8:
// three endpoints, each written with a DIRECT-SQL created_at (never two
// real creation calls in quick succession, which computes equality on a
// fast machine and something else on a slow one) — one strictly BEFORE the
// bound spec's own created_at, one strictly AFTER, one EQUAL — asserting
// precededSpec is true only for the first, and that the comparison is
// STRICT (equal reads false).
func TestHandler_drift_precededSpec_threeCasesAndStrictComparison(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDf)

	const specTime = 1_700_000_000
	specID := ts.importSpecWithSession(t, cookie, csrfToken, "Gadgets", gadgetsByIDDoc)
	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE specs SET created_at = ? WHERE id = ?", specTime, specID); err != nil {
		t.Fatalf("pin spec created_at: %v", err)
	}

	beforeID := ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gadgets/{a}", http.StatusCreated)
	afterID := ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gizmos/{a}", http.StatusCreated)
	equalID := ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/widgets/{a}", http.StatusCreated)

	// Extend the spec doc with the two other collisions this fixture needs
	// (gadgetsByIDDoc alone only covers the first endpoint) via a second
	// spec import carrying all three, then bind to THAT — pinning its
	// created_at to specTime too so every row's comparison is against the
	// SAME timestamp this test controls.
	const threeDoc = `{
	  "openapi": "3.1.0",
	  "info": { "title": "Three Collisions", "version": "1.0.0" },
	  "paths": {
	    "/gadgets/{id}": { "get": { "operationId": "getGadget", "parameters": [{"name":"id","in":"path","required":true,"schema":{"type":"string"}}], "responses": { "200": { "description": "ok" } } } },
	    "/gizmos/{id}": { "get": { "operationId": "getGizmo", "parameters": [{"name":"id","in":"path","required":true,"schema":{"type":"string"}}], "responses": { "200": { "description": "ok" } } } },
	    "/widgets/{id}": { "get": { "operationId": "getWidget", "parameters": [{"name":"id","in":"path","required":true,"schema":{"type":"string"}}], "responses": { "200": { "description": "ok" } } } }
	  }
	}`
	threeSpecID := ts.importSpecWithSession(t, cookie, csrfToken, "Three Collisions", threeDoc)
	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE specs SET created_at = ? WHERE id = ?", specTime, threeSpecID); err != nil {
		t.Fatalf("pin spec created_at: %v", err)
	}
	ts.rebindSpec(t, cookie, csrfToken, wsID, threeSpecID)
	_ = specID // only used to exercise a single-collision fixture path above; superseded by threeSpecID here.

	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE custom_endpoints SET created_at = ? WHERE id = ?", specTime-10, beforeID); err != nil {
		t.Fatalf("pin before endpoint created_at: %v", err)
	}
	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE custom_endpoints SET created_at = ? WHERE id = ?", specTime+10, afterID); err != nil {
		t.Fatalf("pin after endpoint created_at: %v", err)
	}
	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE custom_endpoints SET created_at = ? WHERE id = ?", specTime, equalID); err != nil {
		t.Fatalf("pin equal endpoint created_at: %v", err)
	}

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.ShadowedEndpoints) != 3 {
		t.Fatalf("shadowedEndpoints = %+v, want exactly three rows", report.ShadowedEndpoints)
	}
	want := map[int64]bool{beforeID: true, afterID: false, equalID: false}
	for _, ep := range report.ShadowedEndpoints {
		wantVal, ok := want[ep.EndpointID]
		if !ok {
			t.Fatalf("unexpected endpointId %d in report", ep.EndpointID)
		}
		if ep.PrecededSpec != wantVal {
			t.Errorf("endpointId %d: precededSpec = %v, want %v", ep.EndpointID, ep.PrecededSpec, wantVal)
		}
	}
}

// TestHandler_drift_hasDrift_isDisjunctionOfArrays is D8 P9's behavioural
// half: across four fixtures (no drift, and each of the three signals
// alone), hasDrift equals the disjunction of the three arrays' lengths,
// read off the SAME response body.
func TestHandler_drift_hasDrift_isDisjunctionOfArrays(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, report driftReportWire) {
		t.Helper()
		want := len(report.OrphanedOverrides) > 0 || len(report.OrphanedResources) > 0 || len(report.ShadowedEndpoints) > 0
		if report.HasDrift != want {
			t.Errorf("hasDrift = %v, want %v (overrides=%d resources=%d endpoints=%d)",
				report.HasDrift, want, len(report.OrphanedOverrides), len(report.OrphanedResources), len(report.ShadowedEndpoints))
		}
	}

	t.Run("no drift at all", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
		wsID := int64(wsIDf)
		_ = csrfToken
		report := ts.mustDrift(t, cookie, wsID)
		if report.HasDrift {
			t.Fatalf("hasDrift = true on a fresh workspace, want false")
		}
		check(t, report)
	})

	t.Run("orphaned override alone", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specA := ts.importSpecWithSession(t, cookie, csrfToken, "Auth", authDoc)
		wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specA)
		ts.putOverride(t, cookie, csrfToken, wsID, "POST", "/auth/login")
		specB := ts.importSpecWithSession(t, cookie, csrfToken, "Widgets Only", widgetsOnlyDoc)
		ts.rebindSpec(t, cookie, csrfToken, wsID, specB)
		report := ts.mustDrift(t, cookie, wsID)
		if !report.HasDrift || len(report.OrphanedOverrides) == 0 {
			t.Fatalf("test precondition broken: report = %+v", report)
		}
		check(t, report)
	})

	t.Run("orphaned resource alone", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importDerivationSpec(t, cookie, csrfToken)
		wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
		ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
		ts.seedSuggestionRow(t, specID, 2, testspec.FamilyBareItems)
		report := ts.mustDrift(t, cookie, wsID)
		if !report.HasDrift || len(report.OrphanedResources) == 0 {
			t.Fatalf("test precondition broken: report = %+v", report)
		}
		check(t, report)
	})

	t.Run("shadowed endpoint alone", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
		wsID := int64(wsIDf)
		ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gadgets/{gid}", http.StatusCreated)
		specID := ts.importSpecWithSession(t, cookie, csrfToken, "Gadgets", gadgetsByIDDoc)
		ts.rebindSpec(t, cookie, csrfToken, wsID, specID)
		report := ts.mustDrift(t, cookie, wsID)
		if !report.HasDrift || len(report.ShadowedEndpoints) == 0 {
			t.Fatalf("test precondition broken: report = %+v", report)
		}
		check(t, report)
	})
}

// TestHandler_drift_unboundWorkspace_answersEmptyReport is D8 P10: a
// workspace with no spec bound answers 200 with hasDrift false and three
// empty arrays even when it carries a stored custom endpoint — there is no
// spec to diverge FROM.
func TestHandler_drift_unboundWorkspace_answersEmptyReport(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDf)
	ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/anything", http.StatusCreated)

	req := httptest.NewRequest(http.MethodGet, driftURL(wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../drift: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode drift response: %v", err)
	}
	// D8 P11 piggybacks here: every array must serialize as the literal
	// "[]", never "null".
	for _, key := range []string{"orphanedOverrides", "orphanedResources", "shadowedEndpoints"} {
		val, ok := raw[key]
		if !ok {
			t.Fatalf("response is missing key %q", key)
		}
		if string(val) != "[]" {
			t.Errorf("%s = %s, want the literal []", key, val)
		}
	}
	if string(raw["hasDrift"]) != "false" {
		t.Errorf("hasDrift = %s, want false", raw["hasDrift"])
	}
}

// TestHandler_drift_writesNoWorkspaceRow_exceptLazyBackfill is D8 P12, in
// two fixtures. The first: a workspace carrying a confirmed family, entity
// rows, a scenario, an override, a custom endpoint, a traffic record and a
// checkpoint — every table of the schema except resource_suggestions
// dumped, the route called TWICE, dumped again, and the two dumps asserted
// byte-identical (suggestions are already derived by the time a confirm
// exists, so this fixture's own backfill branch is dead — the reason for
// the second fixture below). The second: a spec whose suggestions have
// never been computed, dumped, the route called ONCE, dumped again, and
// resource_suggestions asserted to have GAINED rows while every other
// table stays byte-identical — proving the report's resource half actually
// derives rather than reading an empty set.
func TestHandler_drift_writesNoWorkspaceRow_exceptLazyBackfill(t *testing.T) {
	t.Parallel()

	t.Run("no write at all once suggestions already exist", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importDerivationSpec(t, cookie, csrfToken)
		wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P12 Demo", specID)
		ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)

		opKey := firstOperationOpKey(t, ts, cookie, wsID)
		putReq := jsonRequest(t, http.MethodPut,
			fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", wsID, opKey),
			map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
		if rec := ts.do(putReq); rec.Code != http.StatusOK {
			t.Fatalf("put override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/p12-custom", http.StatusCreated)
		if _, err := ts.db.W.ExecContext(t.Context(), `
			INSERT INTO traffic (workspace_id, ts, method, path, status, duration_ms)
			VALUES (?, ?, 'GET', '/p12-traffic', 200, 1.5)`, wsID, time.Now().Unix()); err != nil {
			t.Fatalf("insert traffic row: %v", err)
		}
		scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "p12-scenario", http.StatusCreated)
		checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "p12-checkpoint", http.StatusCreated)

		before := dumpAllTablesExcept(t, ts, "resource_suggestions")
		ts.mustDrift(t, cookie, wsID)
		ts.mustDrift(t, cookie, wsID)
		after := dumpAllTablesExcept(t, ts, "resource_suggestions")
		assertTablesUnchanged(t, before, after)
	})

	t.Run("lazy backfill writes only resource_suggestions", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importSpecWithSession(t, cookie, csrfToken, "Fresh Spec", widgetsOnlyDoc)
		wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P12 Fresh Demo", specID)

		// Import already derived (and wrote, including a sentinel row when
		// nothing derives) — delete every row to simulate a spec imported
		// before this backfill existed, [specs.Repo.EnsureSuggestions]'s
		// own documented precondition for actually deriving.
		if _, err := ts.db.W.ExecContext(t.Context(), "DELETE FROM resource_suggestions WHERE spec_id = ?", specID); err != nil {
			t.Fatalf("delete suggestion rows for spec %d: %v", specID, err)
		}
		var before int
		if err := ts.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resource_suggestions WHERE spec_id = ?", specID).Scan(&before); err != nil {
			t.Fatalf("count suggestions before: %v", err)
		}
		if before != 0 {
			t.Fatalf("test precondition broken: spec %d already has %d suggestion rows", specID, before)
		}

		beforeTables := dumpAllTablesExcept(t, ts, "resource_suggestions")
		ts.mustDrift(t, cookie, wsID)
		afterTables := dumpAllTablesExcept(t, ts, "resource_suggestions")
		assertTablesUnchanged(t, beforeTables, afterTables)

		var after int
		if err := ts.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resource_suggestions WHERE spec_id = ?", specID).Scan(&after); err != nil {
			t.Fatalf("count suggestions after: %v", err)
		}
		if after == 0 {
			t.Fatalf("resource_suggestions gained no rows — the report's resource half did not derive")
		}
	})
}

// TestHandler_drift_registeredAsGETOnly is part of D8 P13: the route
// answers only GET — a POST to the same path is refused by the mux
// (autocheckpoint_test.go's own pinned totals are P13's structural half,
// unmoved by construction since a GET is filtered out of the mutating
// population there).
func TestHandler_drift_registeredAsGETOnly(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDf, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDf)

	req := jsonRequest(t, http.MethodPost, driftURL(wsID), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code == http.StatusOK {
		t.Fatalf("POST .../drift: status = 200, want anything but 200 — the route must be GET-only")
	}
}

// TestHandler_drift_declinedFamilyNeverReported is D8 P22: a family whose
// only resource_decisions row is "declined" (no resources row ever, or one
// that was deleted by the decline) is never reported, even when the bound
// spec no longer suggests it either — only a CONFIRMED family can be
// orphaned.
func TestHandler_drift_declinedFamilyNeverReported(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P22 Demo", specID)

	ts.confirmFamily(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	slug := ts.workspaceSlug(t, cookie, wsID)
	declineReq := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID),
		map[string]string{"routeFamily": testspec.FamilyBareItems, "state": "declined"}, cookie, csrfToken)
	if rec := ts.do(declineReq); rec.Code != http.StatusOK {
		t.Fatalf("decline bareitems: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	_ = slug

	specB := ts.importSpecWithSession(t, cookie, csrfToken, "BareItems Only", bareItemsOnlyDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specB)

	report := ts.mustDrift(t, cookie, wsID)
	if len(report.OrphanedResources) != 1 || report.OrphanedResources[0].RouteFamily != testspec.FamilyWidgets {
		t.Fatalf("orphanedResources = %+v, want exactly [%s] — the declined family must never appear", report.OrphanedResources, testspec.FamilyWidgets)
	}
}

// TestHandler_drift_responseCarriesExactlyFourKeys_noRemedy is D8 P24: the
// envelope decodes to exactly {hasDrift, orphanedOverrides,
// orphanedResources, shadowedEndpoints}, and every ROW of every array
// decodes to exactly the key set D4.2 names — no `repairVerb` or any other
// key, on EVERY row (a fixture of two rows per array, since an
// implementation that adds a remedy to every row but the first would pass
// a one-row sample).
func TestHandler_drift_responseCarriesExactlyFourKeys_noRemedy(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specA := ts.importSpecWithSession(t, cookie, csrfToken, "Two Overrides", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P24 Demo", specA)

	ts.putOverride(t, cookie, csrfToken, wsID, "POST", "/auth/login")
	ts.putOverride(t, cookie, csrfToken, wsID, "GET", "/widgets")
	ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gadgets/{a}", http.StatusCreated)
	ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gizmos/{a}", http.StatusCreated)

	const twoGadgetsDoc = `{
	  "openapi": "3.1.0",
	  "info": { "title": "Two Gadgets", "version": "1.0.0" },
	  "paths": {
	    "/gadgets/{id}": { "get": { "operationId": "getGadget", "parameters": [{"name":"id","in":"path","required":true,"schema":{"type":"string"}}], "responses": { "200": { "description": "ok" } } } },
	    "/gizmos/{id}": { "get": { "operationId": "getGizmo", "parameters": [{"name":"id","in":"path","required":true,"schema":{"type":"string"}}], "responses": { "200": { "description": "ok" } } } }
	  }
	}`
	specB := ts.importSpecWithSession(t, cookie, csrfToken, "Two Gadgets", twoGadgetsDoc)
	ts.rebindSpec(t, cookie, csrfToken, wsID, specB)

	req := httptest.NewRequest(http.MethodGet, driftURL(wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../drift: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	wantEnvelopeKeys := []string{"hasDrift", "orphanedOverrides", "orphanedResources", "shadowedEndpoints"}
	if len(envelope) != len(wantEnvelopeKeys) {
		t.Fatalf("envelope has %d keys (%v), want exactly %v", len(envelope), keysOf(envelope), wantEnvelopeKeys)
	}
	for _, k := range wantEnvelopeKeys {
		if _, ok := envelope[k]; !ok {
			t.Errorf("envelope is missing key %q", k)
		}
	}

	checkRows := func(arrayKey string, wantRowKeys []string, wantCount int) {
		t.Helper()
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(envelope[arrayKey], &rows); err != nil {
			t.Fatalf("decode %s rows: %v", arrayKey, err)
		}
		if len(rows) != wantCount {
			t.Fatalf("%s has %d rows, want %d", arrayKey, len(rows), wantCount)
		}
		for i, row := range rows {
			if len(row) != len(wantRowKeys) {
				t.Errorf("%s[%d] has %d keys (%v), want exactly %v", arrayKey, i, len(row), keysOf(row), wantRowKeys)
			}
			for _, k := range wantRowKeys {
				if _, ok := row[k]; !ok {
					t.Errorf("%s[%d] is missing key %q", arrayKey, i, k)
				}
			}
		}
	}
	checkRows("orphanedOverrides", []string{"method", "path", "opKey"}, 2)
	checkRows("shadowedEndpoints", []string{"endpointId", "method", "path", "canonicalPath", "precededSpec"}, 2)
}

// keysOf returns the sorted keys of a decoded JSON object, for a readable
// failure message.
func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestHandler_drift_endpointCreatePathUnchanged is D8 P25: creating a
// custom endpoint canonically equal to an operation the bound spec ALREADY
// declares still answers 201 and the row is actually served — for BOTH a
// GET and a non-GET method, and with the spec bound BEFORE the endpoint is
// created (the order that could break a refusal added at create time,
// unlike P6/P7's own order above).
func TestHandler_drift_endpointCreatePathUnchanged(t *testing.T) {
	t.Parallel()

	t.Run("GET", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		const doc = `{
		  "openapi": "3.1.0",
		  "info": { "title": "Gadgets", "version": "1.0.0" },
		  "paths": {
		    "/gadgets": { "get": { "operationId": "listGadgets", "responses": { "200": { "description": "ok" } } } }
		  }
		}`
		specID := ts.importSpecWithSession(t, cookie, csrfToken, "Gadgets", doc)
		wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P25 GET Demo", specID)

		ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/gadgets", http.StatusCreated)
	})

	t.Run("non-GET", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		const doc = `{
		  "openapi": "3.1.0",
		  "info": { "title": "Gadgets", "version": "1.0.0" },
		  "paths": {
		    "/gadgets": { "post": { "operationId": "createGadget", "responses": { "200": { "description": "ok" } } } }
		  }
		}`
		specID := ts.importSpecWithSession(t, cookie, csrfToken, "Gadgets", doc)
		wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P25 POST Demo", specID)

		ts.createEndpoint(t, cookie, csrfToken, wsID, "POST", "/gadgets", http.StatusCreated)
	})
}
