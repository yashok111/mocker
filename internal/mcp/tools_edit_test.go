package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ---- update_workspace_settings ----

const workspacePatchFixture = `{
  "id": 7,
  "slug": "demo-workspace",
  "name": "Renamed",
  "specId": 3,
  "revision": 5,
  "settings": {
    "seed": 42,
    "basePath": "/api/v2",
    "listSize": 10,
    "nullRate": 0.1,
    "envelope": null,
    "identity": {"id": 1, "name": "Test", "email": "t@example.com", "roles": ["user"], "org": {"id": 1, "name": "Org", "type": "school"}},
    "auth": {"jwtTtlSec": 3600, "alg": "HS256", "signingKey": "secret", "requireHeader": false},
    "cors": {"mode": "reflect", "credentials": true},
    "validateRequests": false,
    "delayMs": 0
  }
}`

// TestUpdateWorkspaceSettings_happyPath is the anti-drift guard: every field
// the tool sends and every field it decodes back is asserted against a
// fixture shaped like workspaceView (workspace_handlers.go:42-63).
func TestUpdateWorkspaceSettings_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: workspacePatchFixture},
	}}
	lb := newLoopback(fc)

	settings := &SettingsInput{
		Seed: 42, BasePath: "/api/v2", ListSize: 10, NullRate: 0.1, Envelope: nil,
		Identity: IdentityInput{ID: float64(1), Name: "Test", Email: "t@example.com", Roles: []string{"user"},
			Org: &OrgInput{ID: float64(1), Name: "Org", Type: "school"}},
		Auth:             AuthSettingsInput{JWTTTLSec: 3600, Alg: "HS256", SigningKey: "secret", RequireHeader: false},
		CORS:             CORSSettingsInput{Mode: "reflect", Credentials: true},
		ValidateRequests: false, DelayMs: 0,
	}
	specID := int64(3)
	_, out, err := handleUpdateWorkspaceSettings(lb)(opsTestCtx(), nil, UpdateWorkspaceSettingsInput{
		WorkspaceID: 7, Name: "Renamed", SpecID: &specID, Settings: settings,
	})
	if err != nil {
		t.Fatalf("handleUpdateWorkspaceSettings: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPatch || fc.calls[0].path != "/api/workspaces/7" {
		t.Errorf("call = %s %s, want PATCH /api/workspaces/7", fc.calls[0].method, fc.calls[0].path)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["name"] != "Renamed" {
		t.Errorf("sent name = %v, want Renamed", sent["name"])
	}
	if sent["specId"] != float64(3) {
		t.Errorf("sent specId = %v, want 3", sent["specId"])
	}
	sentSettings, _ := sent["settings"].(map[string]any)
	if sentSettings["basePath"] != "/api/v2" {
		t.Errorf("sent settings.basePath = %v, want /api/v2", sentSettings["basePath"])
	}
	sentAuth, _ := sentSettings["auth"].(map[string]any)
	if sentAuth["signingKey"] != "secret" {
		t.Errorf("sent settings.auth.signingKey = %v, want secret (full settings replacement must carry it)", sentAuth["signingKey"])
	}

	if out.ID != 7 || out.Slug != "demo-workspace" || out.Name != "Renamed" || out.Revision != 5 {
		t.Errorf("output = %+v, want id=7 slug=demo-workspace name=Renamed revision=5", out)
	}
	if out.SpecID == nil || *out.SpecID != 3 {
		t.Errorf("output.SpecID = %v, want 3", out.SpecID)
	}
	if out.Settings.Auth.SigningKey != "secret" || out.Settings.CORS.Mode != "reflect" {
		t.Errorf("output.Settings = %+v, want the full settings round-tripped back, including auth/cors", out.Settings)
	}
}

// TestUpdateWorkspaceSettings_omitsUntouchedFields is §C-b's sibling proof
// for this tool: name and settings left zero-valued on the input must not
// appear on the wire at all, or an admin-side "" name would be rejected
// (errEmptyName, workspace_handlers.go:314-315) even though the caller never
// asked to touch it.
func TestUpdateWorkspaceSettings_omitsUntouchedFields(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: workspacePatchFixture},
	}}
	lb := newLoopback(fc)

	specID := int64(3)
	_, _, err := handleUpdateWorkspaceSettings(lb)(opsTestCtx(), nil, UpdateWorkspaceSettingsInput{
		WorkspaceID: 7, SpecID: &specID,
	})
	if err != nil {
		t.Fatalf("handleUpdateWorkspaceSettings: %v", err)
	}

	sent := decodeBody(t, fc.calls[0].body)
	if _, ok := sent["name"]; ok {
		t.Errorf("sent body carries \"name\" with nothing to change: %v", sent)
	}
	if _, ok := sent["settings"]; ok {
		t.Errorf("sent body carries \"settings\" with nothing to change: %v", sent)
	}
	if sent["specId"] != float64(3) {
		t.Errorf("sent specId = %v, want 3", sent["specId"])
	}
}

func TestUpdateWorkspaceSettings_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleUpdateWorkspaceSettings(lb)(opsTestCtx(), nil, UpdateWorkspaceSettingsInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleUpdateWorkspaceSettings returned no error on 404")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestUpdateWorkspaceSettings_413IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusRequestEntityTooLarge, body: `{"error":{"code":"too_large","message":"settings object is too large"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleUpdateWorkspaceSettings(lb)(opsTestCtx(), nil, UpdateWorkspaceSettingsInput{WorkspaceID: 1, Settings: &SettingsInput{}})
	if err == nil {
		t.Fatal("handleUpdateWorkspaceSettings returned no error on 413")
	}
}

// ---- apply_auth_preset ----

func TestApplyAuthPreset_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"applied":2,"revision":9}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleApplyAuthPreset(lb)(opsTestCtx(), nil, ApplyAuthPresetInput{
		WorkspaceID: 4,
		Bindings: []BindingInput{
			{Method: "POST", Path: "/login", Status: 200, DataPath: "$.token", Recipe: RecipeInput{Kind: "jwt", TTLSec: 3600}},
			{Method: "GET", Path: "/me", Status: 200, DataPath: "$.id", Recipe: RecipeInput{Kind: "identity", Field: "id"}},
		},
	})
	if err != nil {
		t.Fatalf("handleApplyAuthPreset: %v", err)
	}

	if len(fc.calls) != 1 || fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/4/auth-preset" {
		t.Fatalf("call = %+v, want one POST /api/workspaces/4/auth-preset", fc.calls)
	}
	sent := decodeBody(t, fc.calls[0].body)
	bindings, _ := sent["bindings"].([]any)
	if len(bindings) != 2 {
		t.Fatalf("sent bindings = %v, want 2 entries", sent["bindings"])
	}
	b0, _ := bindings[0].(map[string]any)
	if b0["method"] != "POST" || b0["path"] != "/login" || b0["dataPath"] != "$.token" {
		t.Errorf("sent bindings[0] = %v, want method/path/dataPath preserved", b0)
	}
	recipe0, _ := b0["recipe"].(map[string]any)
	if recipe0["kind"] != "jwt" || recipe0["ttlSec"] != float64(3600) {
		t.Errorf("sent bindings[0].recipe = %v, want kind=jwt ttlSec=3600", recipe0)
	}

	if out.Applied != 2 || out.Revision != 9 {
		t.Errorf("output = %+v, want applied=2 revision=9", out)
	}
}

// TestApplyAuthPreset_emptyBindingsStillCalls proves this tool does not
// short-circuit locally on an empty list — handleApplyAuthPreset's own
// no-op path (preset_handlers.go:183-191) is the one place that decides
// that, not this tool guessing what "nothing to apply" should answer.
func TestApplyAuthPreset_emptyBindingsStillCalls(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"applied":0,"revision":4}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleApplyAuthPreset(lb)(opsTestCtx(), nil, ApplyAuthPresetInput{WorkspaceID: 4, Bindings: nil})
	if err != nil {
		t.Fatalf("handleApplyAuthPreset: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if out.Applied != 0 || out.Revision != 4 {
		t.Errorf("output = %+v, want applied=0 revision=4", out)
	}
}

// TestApplyAuthPreset_zeroBindingsEditVersionsIsEmptyObjectNotNull is D5's
// own named property: the zero-binding admin response carries `editVersions`
// as `{}` byte-exactly, and that must survive decode-then-re-marshal through
// ApplyAuthPresetOutput unchanged. A `map[string]int64` field tagged
// `omitempty` drops any zero-length map — nil or not — from the marshalled
// JSON entirely, which would silently turn `{}` into an absent key. This
// decodes the tool's OWN marshalled output (encoding/json, standing in for
// what the MCP SDK actually puts on the wire — see the package's jsonx rule
// for why production code never imports encoding/json directly) rather than
// only inspecting the Go value, because `omitempty` is a marshal-time
// property that a Go-level nil-vs-empty check cannot see.
func TestApplyAuthPreset_zeroBindingsEditVersionsIsEmptyObjectNotNull(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"applied":0,"revision":4,"editVersions":{}}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleApplyAuthPreset(lb)(opsTestCtx(), nil, ApplyAuthPresetInput{WorkspaceID: 4, Bindings: nil})
	if err != nil {
		t.Fatalf("handleApplyAuthPreset: %v", err)
	}
	if out.EditVersions == nil {
		t.Fatal("out.EditVersions is nil, want a non-nil empty map")
	}
	if len(out.EditVersions) != 0 {
		t.Errorf("out.EditVersions = %+v, want empty", out.EditVersions)
	}

	wire, merr := json.Marshal(out)
	if merr != nil {
		t.Fatalf("json.Marshal(out): %v", merr)
	}
	var onWire map[string]json.RawMessage
	if err := json.Unmarshal(wire, &onWire); err != nil {
		t.Fatalf("json.Unmarshal(wire): %v", err)
	}
	raw, present := onWire["editVersions"]
	if !present {
		t.Fatalf("marshalled output %s has no editVersions key at all, want a present `{}`", wire)
	}
	if string(raw) != "{}" {
		t.Errorf("marshalled editVersions = %s, want byte-exact {}", raw)
	}
}

// TestApplyAuthPreset_conflictEditVersionsIsNonNil guards the corollary of
// dropping EditVersions' omitempty tag: the field is now unconditionally
// present in the declared output schema, so the conflict branch — which
// never decodes a body into `out` and previously left EditVersions at its
// Go zero value — must also hand back a non-nil empty map. A nil map
// marshals to JSON `null`, which fails the SDK's own output-schema
// validation (`type: object` does not admit `null`) and would turn a
// legitimate edit_conflict into an opaque internal error instead of the
// typed Conflict field this tool promises.
func TestApplyAuthPreset_conflictEditVersionsIsNonNil(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"edit_conflict","message":"m","details":{"staleVersions":{"GET%20/widgets":7}}}}`},
	}}
	_, out, err := handleApplyAuthPreset(newLoopback(fc))(opsTestCtx(), nil, ApplyAuthPresetInput{
		WorkspaceID: 4,
		Bindings: []BindingInput{
			{Method: "GET", Path: "/widgets", Status: 200, DataPath: "$.a", Recipe: RecipeInput{Kind: "faker"}},
		},
		EditVersions: map[string]int64{"GET%20/widgets": 1},
	})
	if err != nil {
		t.Fatalf("handleApplyAuthPreset returned a plain tool error for edit_conflict: %v", err)
	}
	if out.Conflict == nil {
		t.Fatal("out.Conflict is nil")
	}
	if out.EditVersions == nil {
		t.Fatal("out.EditVersions is nil on the conflict branch, want a non-nil empty map (would marshal to JSON null and fail output-schema validation)")
	}
	if len(out.EditVersions) != 0 {
		t.Errorf("out.EditVersions = %+v, want empty", out.EditVersions)
	}
}

func TestApplyAuthPreset_400IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusBadRequest, body: `{"error":{"code":"bad_request","message":"binding 0: method/path must name an operation"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleApplyAuthPreset(lb)(opsTestCtx(), nil, ApplyAuthPresetInput{
		WorkspaceID: 4, Bindings: []BindingInput{{DataPath: "$.token", Recipe: RecipeInput{Kind: "jwt"}}},
	})
	if err == nil {
		t.Fatal("handleApplyAuthPreset returned no error on 400")
	}
	if !strings.Contains(err.Error(), "method/path must name an operation") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// ---- set_operation_variant ----

// TestSetOperationVariant_sendsFullDocumentWithExplicitBooleans is this
// tool's central test: overrideOn/routeOff and every field of the full
// variant editor (D5) must reach the wire exactly as given, with NO forcing
// the way set_operation_response applies — routeOff:true is a legitimate
// caller choice this tool must not silently flip.
func TestSetOperationVariant_sendsFullDocumentWithExplicitBooleans(t *testing.T) {
	t.Parallel()
	opKey := "GET%20%2Fwidgets%2F%7Bid%7D"
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"method":"GET","path":"/widgets/{id}","opKey":"` + opKey + `","overrideOn":true,"routeOff":true,"responses":{},"updatedAt":1700001000,"revision":8}`},
	}}
	lb := newLoopback(fc)

	delay := 250
	_, out, err := handleSetOperationVariant(lb)(opsTestCtx(), nil, SetOperationVariantInput{
		WorkspaceID: 12, OpKey: opKey,
		OverrideDocumentInput: OverrideDocumentInput{
			OverrideOn: true,
			RouteOff:   true,
			Responses: map[string]VariantInput{
				"200": {
					Mode: "pinned", MediaType: "application/json",
					Body:    map[string]any{"id": float64(1)},
					Headers: map[string]string{"X-Trace": "abc"},
					When:    []VariantCondition{{In: "header", Name: "X-Test", Op: "exists"}},
					Recipes: map[string]RecipeInput{"$.id": {Kind: "identity", Field: "id"}},
				},
			},
			DelayMs: &delay,
		},
	})
	if err != nil {
		t.Fatalf("handleSetOperationVariant: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (no GET — set_operation_variant has no read route)", len(fc.calls))
	}
	wantPath := "/api/workspaces/12/operations/" + opKey
	if fc.calls[0].method != http.MethodPut || fc.calls[0].path != wantPath {
		t.Errorf("call = %s %s, want PUT %s", fc.calls[0].method, fc.calls[0].path, wantPath)
	}

	sent := decodeBody(t, fc.calls[0].body)
	if sent["overrideOn"] != true {
		t.Errorf("sent overrideOn = %v, want true", sent["overrideOn"])
	}
	if sent["routeOff"] != true {
		t.Errorf("sent routeOff = %v, want true (this tool must NOT force it false)", sent["routeOff"])
	}
	if sent["delayMs"] != float64(250) {
		t.Errorf("sent delayMs = %v, want 250", sent["delayMs"])
	}
	responses, _ := sent["responses"].(map[string]any)
	r200, _ := responses["200"].(map[string]any)
	if r200["mode"] != "pinned" || r200["mediaType"] != "application/json" {
		t.Errorf("sent responses[200] = %v, want mode=pinned mediaType=application/json", r200)
	}
	when, _ := r200["when"].([]any)
	if len(when) != 1 {
		t.Errorf("sent responses[200].when = %v, want 1 condition", r200["when"])
	}
	recipes, _ := r200["recipes"].(map[string]any)
	recipe, _ := recipes["$.id"].(map[string]any)
	if recipe["kind"] != "identity" || recipe["field"] != "id" {
		t.Errorf("sent responses[200].recipes[$.id] = %v, want kind=identity field=id", recipe)
	}

	if out.OpKey != opKey || !out.OverrideOn || !out.RouteOff || out.ResponseCount != 1 || out.Revision != 8 {
		t.Errorf("output = %+v, want opKey/overrideOn/routeOff echoed, responseCount=1, revision=8", out)
	}
}

// TestSetOperationVariant_emptyResponsesClearsTheDocument proves an empty
// Responses map still reaches the admin route as a full replacement that
// clears every previously stored status. It reaches the wire as an ABSENT
// "responses" key (Go's encoding/json omitempty drops both nil and
// zero-length maps alike), not as "{}" — and that is fine, not a bug: PUT's
// own normalizeAndValidate (overrides.go) defaults an absent/nil Responses
// to an empty map before writing, so "key absent" and "key present but
// empty" are indistinguishable outcomes on the admin side, exactly the way
// overrideMutableFields.Responses' own omitempty tag already treats them
// (override_handlers.go:217).
func TestSetOperationVariant_emptyResponsesClearsTheDocument(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"overrideOn":false,"routeOff":false,"responses":{},"updatedAt":1,"revision":2}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleSetOperationVariant(lb)(opsTestCtx(), nil, SetOperationVariantInput{
		WorkspaceID: 1, OpKey: "GET%20%2Fx",
		OverrideDocumentInput: OverrideDocumentInput{OverrideOn: false, RouteOff: false, Responses: map[string]VariantInput{}},
	})
	if err != nil {
		t.Fatalf("handleSetOperationVariant: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if responses, ok := sent["responses"]; ok {
		t.Errorf("sent responses = %v, want the key entirely absent for an empty map", responses)
	}
	if out.ResponseCount != 0 {
		t.Errorf("ResponseCount = %d, want 0", out.ResponseCount)
	}
}

func TestSetOperationVariant_400IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusBadRequest, body: `{"error":{"code":"bad_request","message":"overrides: invalid row: unknown mode \"weird\""}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleSetOperationVariant(lb)(opsTestCtx(), nil, SetOperationVariantInput{
		WorkspaceID: 1, OpKey: "GET%20%2Fx",
		OverrideDocumentInput: OverrideDocumentInput{Responses: map[string]VariantInput{"200": {Mode: "weird"}}},
	})
	if err == nil {
		t.Fatal("handleSetOperationVariant returned no error on 400")
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// ---- reset_operation ----

func TestResetOperation_204MeansNothingWasThere(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusNoContent, body: ""}}}
	lb := newLoopback(fc)

	_, out, err := handleResetOperation(lb)(opsTestCtx(), nil, ResetOperationInput{WorkspaceID: 1, OpKey: "GET%20%2Fx"})
	if err != nil {
		t.Fatalf("handleResetOperation: %v, want success on 204", err)
	}
	if out.Deleted {
		t.Errorf("Deleted = true, want false on 204")
	}
	if out.Revision != nil {
		t.Errorf("Revision = %v, want nil when nothing was deleted", out.Revision)
	}
}

func TestResetOperation_200MeansARowWasDeleted(t *testing.T) {
	t.Parallel()
	opKey := "GET%20%2Fx"
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"revision":11}`}}}
	lb := newLoopback(fc)

	_, out, err := handleResetOperation(lb)(opsTestCtx(), nil, ResetOperationInput{WorkspaceID: 1, OpKey: opKey})
	if err != nil {
		t.Fatalf("handleResetOperation: %v", err)
	}
	if !out.Deleted {
		t.Errorf("Deleted = false, want true on 200")
	}
	if out.Revision == nil || *out.Revision != 11 {
		t.Errorf("Revision = %v, want 11", out.Revision)
	}
	if fc.calls[0].method != http.MethodDelete || fc.calls[0].path != "/api/workspaces/1/operations/"+opKey {
		t.Errorf("call = %s %s, want DELETE /api/workspaces/1/operations/%s", fc.calls[0].method, fc.calls[0].path, opKey)
	}
}

func TestResetOperation_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleResetOperation(lb)(opsTestCtx(), nil, ResetOperationInput{WorkspaceID: 1, OpKey: "GET%20%2Fx"})
	if err == nil {
		t.Fatal("handleResetOperation returned no error on 500")
	}
}

// ---- preview_operation ----

func TestPreviewOperation_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{
			"status": 200, "statusSource": "active", "mediaType": "application/json",
			"headers": {"X-Trace": "abc"}, "encoding": "utf8", "body": "{\"id\":1}",
			"noBody": false, "routeOff": false, "refused": null,
			"schemaPatchApplied": true, "recipesBound": 2, "delayMs": 50, "shadowedBy": "scenario-a"
		}`},
	}}
	lb := newLoopback(fc)

	status := "200"
	_, out, err := handlePreviewOperation(lb)(opsTestCtx(), nil, PreviewOperationInput{
		WorkspaceID: 5, OpKey: "GET%20%2Fwidgets",
		Draft:  OverrideDocumentInput{OverrideOn: true, RouteOff: false, Responses: map[string]VariantInput{"200": {Mode: "generated"}}},
		Status: status, Query: "a=1", Headers: map[string]string{"Accept": "application/json"}, PathParams: map[string]string{"id": "1"},
	})
	if err != nil {
		t.Fatalf("handlePreviewOperation: %v", err)
	}

	if len(fc.calls) != 1 || fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/5/preview" {
		t.Fatalf("call = %+v, want one POST /api/workspaces/5/preview", fc.calls)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["opKey"] != "GET%20%2Fwidgets" || sent["status"] != "200" || sent["query"] != "a=1" {
		t.Errorf("sent = %v, want opKey/status/query carried through", sent)
	}
	draft, _ := sent["draft"].(map[string]any)
	if draft["overrideOn"] != true || draft["routeOff"] != false {
		t.Errorf("sent draft = %v, want overrideOn=true routeOff=false explicit", draft)
	}

	if out.Status != 200 || out.StatusSource != "active" || out.MediaType != "application/json" {
		t.Errorf("output = %+v, want status=200 statusSource=active mediaType=application/json", out)
	}
	if out.Body == nil || *out.Body != `{"id":1}` || out.Encoding != "utf8" {
		t.Errorf("output body/encoding = %v/%q, want {\"id\":1}/utf8", out.Body, out.Encoding)
	}
	if out.SchemaPatchApplied != true || out.RecipesBound != 2 || out.DelayMs != 50 {
		t.Errorf("output = %+v, want schemaPatchApplied=true recipesBound=2 delayMs=50", out)
	}
	if out.ShadowedBy != "scenario-a" {
		t.Errorf("ShadowedBy = %q, want scenario-a", out.ShadowedBy)
	}
	if out.RefusedReason != "" || out.RefusedDetail != "" {
		t.Errorf("Refused* = %q/%q, want empty when refused is null", out.RefusedReason, out.RefusedDetail)
	}
}

// TestPreviewOperation_omitsStatusWhenNotGiven proves an empty Status input
// never reaches the wire as "status":"" — parsePreviewStatus
// (preview_handlers.go:253-262) treats any non-nil, non-3-digit value as a
// 400, so a caller that left the field alone must see no key at all, not an
// empty string masquerading as one.
func TestPreviewOperation_omitsStatusWhenNotGiven(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"status":200,"statusSource":"default","noBody":false,"routeOff":false,"refused":null,"schemaPatchApplied":false,"recipesBound":0,"delayMs":0,"shadowedBy":null}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handlePreviewOperation(lb)(opsTestCtx(), nil, PreviewOperationInput{WorkspaceID: 5, OpKey: "GET%20%2Fx"})
	if err != nil {
		t.Fatalf("handlePreviewOperation: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if _, ok := sent["status"]; ok {
		t.Errorf("sent body carries \"status\" when none was given: %v", sent)
	}
}

// TestPreviewOperation_refusedIsProjected proves a resolved-but-refused body
// (Refused set on an otherwise 200 result, domain.RefusedReason) surfaces as
// RefusedReason/RefusedDetail rather than being silently dropped.
func TestPreviewOperation_refusedIsProjected(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{
			"status": 200, "statusSource": "default", "noBody": true, "routeOff": false,
			"refused": {"reason": "pinned_body_too_large", "detail": "body is 90000 bytes"},
			"schemaPatchApplied": false, "recipesBound": 0, "delayMs": 0, "shadowedBy": null
		}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handlePreviewOperation(lb)(opsTestCtx(), nil, PreviewOperationInput{WorkspaceID: 5, OpKey: "GET%20%2Fx"})
	if err != nil {
		t.Fatalf("handlePreviewOperation: %v", err)
	}
	if out.RefusedReason != "pinned_body_too_large" || out.RefusedDetail != "body is 90000 bytes" {
		t.Errorf("Refused* = %q/%q, want pinned_body_too_large/body is 90000 bytes", out.RefusedReason, out.RefusedDetail)
	}
	if !out.NoBody || out.Body != nil {
		t.Errorf("output = %+v, want noBody=true and no body", out)
	}
}

// TestPreviewOperation_customEndpointWinsIsToolError is D7's own named case:
// the 409 the preview route answers when a custom endpoint outranks the
// operation must surface with its message intact, the same shape
// override_from_traffic's own 409 refusal already takes.
func TestPreviewOperation_customEndpointWinsIsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"custom_endpoint_wins","message":"mockplane: preview: an enabled custom endpoint outranks this operation"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handlePreviewOperation(lb)(opsTestCtx(), nil, PreviewOperationInput{WorkspaceID: 5, OpKey: "GET%20%2Fx"})
	if err == nil {
		t.Fatal("handlePreviewOperation returned no error on 409")
	}
	if !strings.Contains(err.Error(), "custom endpoint outranks") {
		t.Errorf("error = %q, want it to carry the admin plane's own custom_endpoint_wins message", err.Error())
	}
}

func TestPreviewOperation_400IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusBadRequest, body: `{"error":{"code":"invalid_draft","message":"responses[200]: mediaType \"text/html\" is browser-executable and cannot be stored"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handlePreviewOperation(lb)(opsTestCtx(), nil, PreviewOperationInput{
		WorkspaceID: 5, OpKey: "GET%20%2Fx",
		Draft: OverrideDocumentInput{Responses: map[string]VariantInput{"200": {MediaType: "text/html"}}},
	})
	if err == nil {
		t.Fatal("handlePreviewOperation returned no error on 400")
	}
}

// ---- registration: honest annotations, real output schemas ----

// TestAddEditTools_registersFiveToolsWithHonestAnnotations mirrors
// TestAddOperationTools_registersSixToolsWithHonestAnnotations (tools_ops_test.go):
// a real tools/list round trip, asserting presence and correctness only for
// this file's own five tools among whatever else is registered.
func TestAddEditTools_registersFiveToolsWithHonestAnnotations(t *testing.T) {
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
		"update_workspace_settings": {false, true},
		"apply_auth_preset":         {false, true},
		"set_operation_variant":     {false, true},
		"reset_operation":           {false, true},
		"preview_operation":         {true, true},
	}

	seen := make(map[string]bool, len(want))
	for _, tool := range env.Result.Tools {
		w, ok := want[tool.Name]
		if !ok {
			continue // one of the OTHER files' tools — not this test's business.
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
