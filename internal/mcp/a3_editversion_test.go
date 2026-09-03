// A3 (mocker-a3-cas): the six MCP write tools carry a REQUIRED per-row
// compare-and-swap expectation to the HTTP write they make, and an
// edit_conflict answer reaches the model as typed data rather than a bare
// status/sentence. This file's tests are the two properties (D13's 7's MCP
// half and 8) no other section can name a test for: a handler that builds
// its own request body and drops the caller's editVersion compiles, passes
// every other bar, and grants nothing — see tools_edit.go/tools_ops.go/
// tools_endpoints.go's own comments on set_operation_variant's identical
// pre-slice bug for why this is not a hypothetical.
package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ---- property 8: the caller's editVersion reaches the HTTP request body ----

// TestUpdateWorkspaceSettings_sendsCallersEditVersion pins the exact bug
// class D7 names for set_operation_variant: a handler that marshals a
// hand-built request struct must include the field the caller sent, not
// silently omit it. A body missing "editVersion" entirely is indistinguishable
// on the wire from one carrying the zero value, so this asserts the KEY is
// present with the caller's own number, not merely that marshalling
// succeeded.
func TestUpdateWorkspaceSettings_sendsCallersEditVersion(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"id":7,"slug":"orders-api","name":"Orders","specId":null,"revision":3,"settings":{},"editVersion":43}`},
	}}
	_, out, err := handleUpdateWorkspaceSettings(newLoopback(fc))(opsTestCtx(), nil, UpdateWorkspaceSettingsInput{
		WorkspaceID: 7, Name: "Orders", EditVersion: 42,
	})
	if err != nil {
		t.Fatalf("handleUpdateWorkspaceSettings: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	assertBodyEditVersion(t, fc.calls[0].body, "editVersion", 42)
	if out.EditVersion != 43 {
		t.Errorf("out.EditVersion = %d, want 43 (the WRITE response's fresh version)", out.EditVersion)
	}
}

// TestApplyAuthPreset_sendsCallersEditVersionsMap is update_workspace_
// settings' sibling test for the preset's map-shaped expectation (D12):
// the caller's editVersions map must reach the request body verbatim,
// under its own name, never folded into or confused with the response's
// own editVersions.
func TestApplyAuthPreset_sendsCallersEditVersionsMap(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"applied":1,"revision":9,"editVersions":{"GET%20/widgets":6}}`},
	}}
	_, out, err := handleApplyAuthPreset(newLoopback(fc))(opsTestCtx(), nil, ApplyAuthPresetInput{
		WorkspaceID:  7,
		Bindings:     []BindingInput{{Method: "GET", Path: "/widgets", Status: 200, DataPath: "$.token", Recipe: RecipeInput{Kind: "faker"}}},
		EditVersions: map[string]int64{"GET%20/widgets": 5},
	})
	if err != nil {
		t.Fatalf("handleApplyAuthPreset: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	var body struct {
		EditVersions map[string]int64 `json:"editVersions"`
	}
	if err := json.Unmarshal([]byte(fc.calls[0].body), &body); err != nil {
		t.Fatalf("decode sent body: %v; body=%s", err, fc.calls[0].body)
	}
	if got, want := body.EditVersions["GET%20/widgets"], int64(5); got != want {
		t.Errorf("sent editVersions[GET%%20/widgets] = %d, want %d (the caller's, not the response's)", got, want)
	}
	if out.EditVersions["GET%20/widgets"] != 6 {
		t.Errorf("out.EditVersions = %+v, want the fresh version 6 from the write response", out.EditVersions)
	}
}

// TestSetOperationVariant_sendsCallersEditVersion is the EXACT regression D7
// names by file:line: handleSetOperationVariant used to marshal
// in.OverrideDocumentInput ALONE, so a sibling EditVersion field was
// accepted by the tool schema and then never reached the wire — a required
// argument that did nothing. This fails red against that old code (the sent
// body would have no "editVersion" key at all) and green against the fix.
func TestSetOperationVariant_sendsCallersEditVersion(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"revision":11,"editVersion":8}`},
	}}
	_, out, err := handleSetOperationVariant(newLoopback(fc))(opsTestCtx(), nil, SetOperationVariantInput{
		WorkspaceID: 7, OpKey: "GET%20/widgets",
		OverrideDocumentInput: OverrideDocumentInput{OverrideOn: true, RouteOff: false},
		EditVersion:           7,
	})
	if err != nil {
		t.Fatalf("handleSetOperationVariant: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	assertBodyEditVersion(t, fc.calls[0].body, "editVersion", 7)
	if out.EditVersion != 8 {
		t.Errorf("out.EditVersion = %d, want 8", out.EditVersion)
	}
}

// TestSetOperationResponse_forwardsCallersEditVersionNotItsOwnInternalGET is
// D11's named special case: this tool is a GET-then-PUT inside one call, and
// TWO editVersions are available to its internal PUT — the one it just read
// off the GET, and the caller's own argument. Forwarding the internal GET's
// own version would make the whole check vacuous (D11: "the guarded window
// is microseconds wide"). The fixture's GET answers editVersion 99; the
// caller passes 3; the PUT must carry 3, never 99.
func TestSetOperationResponse_forwardsCallersEditVersionNotItsOwnInternalGET(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"overrideOn":true,"routeOff":false,"activeStatus":200,"responses":{},"editVersion":99}`},
		{status: http.StatusOK, body: `{"revision":4,"editVersion":100}`},
	}}
	_, out, err := handleSetOperationResponse(newLoopback(fc))(opsTestCtx(), nil, SetOperationResponseInput{
		WorkspaceID: 7, OpKey: "GET%20/widgets", Status: 500, EditVersion: 3,
	})
	if err != nil {
		t.Fatalf("handleSetOperationResponse: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (GET then PUT)", len(fc.calls))
	}
	assertBodyEditVersion(t, fc.calls[1].body, "editVersion", 3)
	if strings.Contains(fc.calls[1].body, `"editVersion":99`) {
		t.Fatalf("PUT body forwarded the internal GET's own editVersion (99) instead of the caller's (3): %s", fc.calls[1].body)
	}
	if out.EditVersion != 100 {
		t.Errorf("out.EditVersion = %d, want 100", out.EditVersion)
	}
}

// TestUpdateEndpoint_sendsCallersEditVersion mirrors the operation-write
// regression test above for the endpoint table.
func TestUpdateEndpoint_sendsCallersEditVersion(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"id":9,"method":"GET","path":"/widgets","canonicalPath":"/widgets","overrideOn":true,"routeOff":false,"activeStatus":200,"responses":{},"createdAt":1,"updatedAt":2,"editVersion":15}`},
	}}
	_, out, err := handleUpdateEndpoint(newLoopback(fc))(opsTestCtx(), nil, UpdateEndpointInput{
		WorkspaceID: 7, EndpointID: 9, Method: "GET", Path: "/widgets", ActiveStatus: 200, EditVersion: 14,
	})
	if err != nil {
		t.Fatalf("handleUpdateEndpoint: %v", err)
	}
	assertBodyEditVersion(t, fc.calls[0].body, "editVersion", 14)
	if out.Endpoint.EditVersion != 15 {
		t.Errorf("out.Endpoint.EditVersion = %d, want 15", out.Endpoint.EditVersion)
	}
}

// TestRenameScenario_sendsCallersEditVersion mirrors the same regression
// test for the scenario table.
func TestRenameScenario_sendsCallersEditVersion(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: `{"id":4,"name":"renamed","createdAt":1,"isActive":false,"editVersion":21}`},
	}}
	_, out, err := handleRenameScenario(newLoopback(fc))(opsTestCtx(), nil, RenameScenarioInput{
		WorkspaceID: 7, ScenarioID: 4, Name: "renamed", EditVersion: 20,
	})
	if err != nil {
		t.Fatalf("handleRenameScenario: %v", err)
	}
	assertBodyEditVersion(t, fc.calls[0].body, "editVersion", 20)
	if out.EditVersion != 21 {
		t.Errorf("out.EditVersion = %d, want 21", out.EditVersion)
	}
}

// assertBodyEditVersion decodes body's top-level `key` as an integer and
// fails unless it is present and equals want — a body that merely CONTAINS
// the substring (e.g. inside a nested, unrelated field) would not catch a
// handler that silently drops the top-level sibling field D7 requires.
func assertBodyEditVersion(t *testing.T, body, key string, want int64) {
	t.Helper()
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatalf("decode sent body: %v; body=%s", err, body)
	}
	raw, ok := probe[key]
	if !ok {
		t.Fatalf("sent body has no top-level %q key at all — the caller's expectation was dropped; body=%s", key, body)
	}
	var got int64
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("sent body's %q did not decode as an integer (%v) — body=%s", key, err, body)
	}
	if got != want {
		t.Errorf("sent body's %q = %d, want %d; body=%s", key, got, want, body)
	}
}

// ---- property 7 (MCP half): edit_conflict reaches the model as typed data ----

// TestUpdateWorkspaceSettings_editConflictIsTypedNotError is the sharpest
// edge D6 names: before this slice, EVERY non-2xx from lb.call became a bare
// Go error the SDK packs into text — a 409 arrived at the model as a status
// number and a sentence, with no code, no document, no version to retry
// with. This asserts the opposite: no tool error at all, and a populated,
// typed Conflict field carrying the current document and its real
// editVersion.
func TestUpdateWorkspaceSettings_editConflictIsTypedNotError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"edit_conflict","message":"workspace was changed by another write","details":{"name":"Orders (renamed elsewhere)","specId":null,"settings":{},"editVersion":9}}}`},
	}}
	_, out, err := handleUpdateWorkspaceSettings(newLoopback(fc))(opsTestCtx(), nil, UpdateWorkspaceSettingsInput{
		WorkspaceID: 7, Name: "Orders", EditVersion: 5,
	})
	if err != nil {
		t.Fatalf("handleUpdateWorkspaceSettings returned a plain tool error for edit_conflict, want a typed Conflict field: %v", err)
	}
	if out.Conflict == nil {
		t.Fatal("out.Conflict is nil — the 409 was not surfaced as typed data at all")
	}
	if out.Conflict.Gone {
		t.Fatal("out.Conflict.Gone = true, want false (this fixture's row is not gone)")
	}
	if out.Conflict.Document == nil {
		t.Fatal("out.Conflict.Document is nil, want the current document")
	}
	if out.Conflict.Document.EditVersion != 9 {
		t.Errorf("out.Conflict.Document.EditVersion = %d, want 9 (the current server version to retry with)", out.Conflict.Document.EditVersion)
	}
	if out.Conflict.Document.Name != "Orders (renamed elsewhere)" {
		t.Errorf("out.Conflict.Document.Name = %q, want the server's current name", out.Conflict.Document.Name)
	}
}

// TestSetOperationVariant_editConflictGoneIsTombstoneNotDocument covers D6's
// second conflict shape: the row was deleted, not merely changed, so there
// is no current document to return — Document must be nil and Gone true,
// never a Document carrying a null/zero editVersion (which would read as
// "the row exists at version 0", D7's own meaning for that value).
func TestSetOperationVariant_editConflictGoneIsTombstoneNotDocument(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"edit_conflict","message":"operation override was deleted by another write","details":{"gone":true,"editVersion":null}}}`},
	}}
	_, out, err := handleSetOperationVariant(newLoopback(fc))(opsTestCtx(), nil, SetOperationVariantInput{
		WorkspaceID: 7, OpKey: "GET%20/widgets",
		OverrideDocumentInput: OverrideDocumentInput{OverrideOn: true},
		EditVersion:           4,
	})
	if err != nil {
		t.Fatalf("handleSetOperationVariant returned a plain tool error for edit_conflict, want a typed Conflict field: %v", err)
	}
	if out.Conflict == nil {
		t.Fatal("out.Conflict is nil")
	}
	if !out.Conflict.Gone {
		t.Error("out.Conflict.Gone = false, want true (the row was deleted)")
	}
	if out.Conflict.Document != nil {
		t.Errorf("out.Conflict.Document = %+v, want nil for a gone row — a present Document here would misreport a deleted row as one that merely changed", out.Conflict.Document)
	}
}

// TestApplyAuthPreset_staleVersionsDistinguishesNullFromAbsent is D12's own
// named contrast (D13 property 4): a `null` entry in staleVersions means
// that row is GONE; an opKey with NO entry means it did not disagree at
// all. Decoding into the wrong Go type (map[string]int64 instead of
// map[string]*int64) would collapse both null and 0 to the same Go zero
// value — this test fails against that collapse.
func TestApplyAuthPreset_staleVersionsDistinguishesNullFromAbsent(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"edit_conflict","message":"one or more auth preset bindings were changed by another write","details":{"staleVersions":{"GET%20/widgets":7,"POST%20/login":null}}}}`},
	}}
	_, out, err := handleApplyAuthPreset(newLoopback(fc))(opsTestCtx(), nil, ApplyAuthPresetInput{
		WorkspaceID: 7,
		Bindings: []BindingInput{
			{Method: "GET", Path: "/widgets", Status: 200, DataPath: "$.a", Recipe: RecipeInput{Kind: "faker"}},
			{Method: "POST", Path: "/login", Status: 200, DataPath: "$.b", Recipe: RecipeInput{Kind: "faker"}},
		},
		EditVersions: map[string]int64{"GET%20/widgets": 1, "POST%20/login": 0},
	})
	if err != nil {
		t.Fatalf("handleApplyAuthPreset returned a plain tool error for edit_conflict, want a typed Conflict field: %v", err)
	}
	if out.Conflict == nil {
		t.Fatal("out.Conflict is nil")
	}
	changed, ok := out.Conflict.StaleVersions["GET%20/widgets"]
	if !ok || changed == nil || *changed != 7 {
		t.Errorf("StaleVersions[GET widgets] = %v, want a pointer to 7", out.Conflict.StaleVersions["GET%20/widgets"])
	}
	gone, ok := out.Conflict.StaleVersions["POST%20/login"]
	if !ok {
		t.Fatal("StaleVersions has no entry for POST /login at all — a gone row must still be an entry, just a nil one (D12: null means gone, NO ENTRY means \"did not disagree\")")
	}
	if gone != nil {
		t.Errorf("StaleVersions[POST login] = %v, want nil (the row is gone) — a non-nil pointer here would misreport a deleted row as one sitting at a real version", *gone)
	}
}

// TestUpdateEndpoint_409DuplicatePathStillIsToolError_notConflictField pins
// the boundary D6 draws explicitly: this handler now inspects status
// itself, so it must NOT swallow a 409 that isn't edit_conflict into a
// successful call with an empty Conflict field. Without this guard, the
// existing TestUpdateEndpoint_409IsToolError (tools_endpoints_test.go) would
// still pass (a nil error is not what it asserts against) while this
// property — a duplicate-path refusal turning into a "successful" tool call
// — would silently start passing too. This test is the one that would catch
// that regression by name.
func TestUpdateEndpoint_409DuplicatePathStillIsToolError_notConflictField(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"an override already exists for this method and path"}}`},
	}}
	_, out, err := handleUpdateEndpoint(newLoopback(fc))(opsTestCtx(), nil, UpdateEndpointInput{
		WorkspaceID: 7, EndpointID: 9, Method: "GET", Path: "/widgets", ActiveStatus: 200, EditVersion: 1,
	})
	if err == nil {
		t.Fatalf("handleUpdateEndpoint returned no error for a non-edit_conflict 409 — it must remain a plain tool error, not out=%+v", out)
	}
	if out.Conflict != nil {
		t.Errorf("out.Conflict = %+v, want nil — a duplicate-path 409 is not this slice's conflict", out.Conflict)
	}
}

// TestRenameScenario_409DuplicateNameStillIsToolError_notConflictField is
// update_endpoint's sibling test for rename_scenario's own pre-existing,
// unrelated 409 (a duplicate scenario name).
func TestRenameScenario_409DuplicateNameStillIsToolError_notConflictField(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"a scenario named \"renamed\" already exists"}}`},
	}}
	_, out, err := handleRenameScenario(newLoopback(fc))(opsTestCtx(), nil, RenameScenarioInput{
		WorkspaceID: 7, ScenarioID: 4, Name: "renamed", EditVersion: 1,
	})
	if err == nil {
		t.Fatalf("handleRenameScenario returned no error for a non-edit_conflict 409 — it must remain a plain tool error, not out=%+v", out)
	}
	if out.Conflict != nil {
		t.Errorf("out.Conflict = %+v, want nil — a duplicate-name 409 is not this slice's conflict", out.Conflict)
	}
}

// ---- property 9: the six stale Descriptions are gone, over the ASSEMBLED
// tools/list output, not the source text (D13 property 9's own instruction:
// "a negative grep passes on deletion alone, with no replacement written") ----

var a3WriteTools = []string{
	"update_workspace_settings", "apply_auth_preset", "set_operation_variant",
	"set_operation_response", "update_endpoint", "rename_scenario",
}

// TestSixWriteTools_staleDescriptionsGoneAndReplaced fetches the real
// tools/list output (through the actual MCP handshake, not source grep) and
// asserts, for each of the six A3 write tools, that neither stale phrasing
// survives and that edit_conflict is actually named — a deletion with
// nothing written in its place would pass a grep-only check but fail this
// one.
func TestSixWriteTools_staleDescriptionsGoneAndReplaced(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}

	byName := make(map[string]string, len(env.Result.Tools))
	for _, tl := range env.Result.Tools {
		byName[tl.Name] = tl.Description
	}

	for _, name := range a3WriteTools {
		desc, ok := byName[name]
		if !ok {
			t.Errorf("%s: not present in tools/list at all", name)
			continue
		}
		if strings.Contains(desc, "silently overwritten") {
			t.Errorf("%s: Description still contains the stale \"silently overwritten\" warning", name)
		}
		if strings.Contains(desc, "Last writer wins") {
			t.Errorf("%s: Description still contains the stale \"Last writer wins\" warning", name)
		}
		if !strings.Contains(desc, "edit_conflict") {
			t.Errorf("%s: Description does not name edit_conflict at all", name)
		}
	}
}

// TestOperationTools_describeZeroForFreshRow is D7's own narrow rule (D13
// property 9's last clause): ONLY the two operation write tools may tell an
// agent to retry a 404'd get_operation with editVersion: 0 — the other four
// write tables refuse 0 outright (D7), so telling a model to retry THOSE
// with 0 would teach it a call this design guarantees will edit_conflict
// every time.
func TestOperationTools_describeZeroForFreshRow(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}
	byName := make(map[string]string, len(env.Result.Tools))
	for _, tl := range env.Result.Tools {
		byName[tl.Name] = tl.Description
	}

	for _, name := range []string{"set_operation_variant", "set_operation_response"} {
		if !strings.Contains(byName[name], "editVersion: 0") {
			t.Errorf("%s: Description does not tell the agent to retry a 404'd get_operation with editVersion: 0", name)
		}
	}
	// The three non-op_overrides write tools must NOT carry the same "0"
	// guidance — D7: 0 is refused, not meaningful, on their tables.
	for _, name := range []string{"update_workspace_settings", "update_endpoint", "rename_scenario"} {
		if strings.Contains(byName[name], "editVersion: 0") {
			t.Errorf("%s: Description tells the agent to retry with editVersion: 0, but 0 is REFUSED on this table (D7) — this would teach a guaranteed edit_conflict", name)
		}
	}
}

// ---- property 2 (schema half): required AND non-nullable, on this
// package's own schema output ----

// TestSixWriteTools_editVersionIsRequiredAndNonNullable inspects the
// ACTUAL JSON Schema jsonschema-go generated for each of the six tools'
// inputSchema — not the Go struct tags — because that generation is exactly
// where D7's pointer trap lives: *int64 is required (no omitempty) AND
// nullable (jsonschema-go follows pointers), so only reading the emitted
// schema catches it. A plain int64 must appear in `required` and its own
// schema must not declare `"type":["integer","null"]` (or an empty {}
// permissive schema).
func TestSixWriteTools_editVersionIsRequiredAndNonNullable(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}

	type schemaShape struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}

	cases := []struct {
		tool  string
		field string
	}{
		{"update_workspace_settings", "editVersion"},
		{"set_operation_variant", "editVersion"},
		{"set_operation_response", "editVersion"},
		{"update_endpoint", "editVersion"},
		{"rename_scenario", "editVersion"},
		{"apply_auth_preset", "editVersions"},
	}
	byName := make(map[string]json.RawMessage, len(env.Result.Tools))
	for _, tl := range env.Result.Tools {
		byName[tl.Name] = tl.InputSchema
	}

	for _, c := range cases {
		raw, ok := byName[c.tool]
		if !ok {
			t.Errorf("%s: not present in tools/list", c.tool)
			continue
		}
		var s schemaShape
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Errorf("%s: decode inputSchema: %v", c.tool, err)
			continue
		}
		if !containsStr(s.Required, c.field) {
			t.Errorf("%s: %q is not in inputSchema.required (%v) — a model may omit it entirely", c.tool, c.field, s.Required)
		}
		propRaw, ok := s.Properties[c.field]
		if !ok {
			t.Errorf("%s: inputSchema.properties has no %q entry", c.tool, c.field)
			continue
		}
		prop := string(propRaw)
		if strings.Contains(prop, `"null"`) {
			t.Errorf("%s: inputSchema.properties.%s = %s — declares null as a legal value, which D7 forbids for a REQUIRED expectation (a *int64 field would do exactly this)", c.tool, c.field, prop)
		}
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ---- property: the surface count does not move ----

// TestToolSurfaceStaysAt48 pins D13's own counted number: A3 added NO tool,
// this test's own name and count moved once when P3b (D7 of
// mocker-p3b-resources) added its four resource tools, 38 -> 42, once more
// when P3f (D8.3 of mocker-p3f-rederive) added rederive_suggestions,
// 42 -> 43, once more when P4a (D7 of mocker-p4a-triage) added
// get_workspace_drift, 43 -> 44, and once more when A4 (D1, D9 of
// mocker-a4-mcp-reach) added probe_workspace and list_resource_entities —
// D6's list_traffic widening adds no tool of its own — 44 -> 46, and once
// more when P6a (D16 of mocker-p6a-sse) added get_stream_stats, 46 -> 47,
// and once more when P6b (D13 of mocker-p6b-sse-mock) added
// preview_endpoint, 47 -> 48, and once more when P6c (D9 of
// mocker-p6c-live-conns) added list_stream_connections,
// close_stream_connection and push_stream_frame, 48 -> 51. mcp_test.go's
// own tools/list test only logs the count (t.Logf, "not a check" per D13's
// own text); this asserts it.
func TestToolSurfaceStaysAt51(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}
	if len(env.Result.Tools) != 63 {
		t.Errorf("tools/list returned %d tools, want 63 — A3 added no route and no tool; P3b added four; P3f added one; P4a added one; A4 added two; P6a added one; P6b added one; P6c added three; A6 added three; A7 added get_guide; A8 added import_spec; A9 added get_server_config; A11 added set_resource_entity and delete_resource_entity; P4b added export_workspace, import_workspace and fork_workspace; P7a added export_openapi", len(env.Result.Tools))
	}
}
