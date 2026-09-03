package admin_test

// P7a (decisions.md mocker-p7-api-design): the admin plane's half of
// DESIGN §34 — the export route, the `$ref` refusals on every writer that
// could store a dangling one (D6), the schema-on-override refusal (D2) and
// the operationId collision (D3). Each test names the acceptance clause
// it observes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

// otherSpecDoc is a second spec with NO components at all, so a row
// referencing the derivation spec's Widget cannot resolve into it.
const otherSpecDoc = `{
	"openapi": "3.1.0",
	"info": { "title": "Other", "version": "1.0.0" },
	"paths": {
		"/other": {
			"get": { "operationId": "getOther", "responses": { "200": { "description": "ok" } } }
		}
	}
}`

func openapiURL(wsID int64, query string) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/openapi.json%s", wsID, query)
}

func (ts *testServer) importSpecDoc(t *testing.T, cookie *http.Cookie, csrfToken, name, document string) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": name, "source": "upload", "document": document}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import spec %q: status = %d, want 201; body = %s", name, rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	return body.ID
}

// postEndpoint POSTs an arbitrary endpoint document and returns the
// recorder, so a test can read the status, the error code or the row.
func (ts *testServer) postEndpoint(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", wsID), body, cookie, csrfToken)
	return ts.do(req)
}

func (ts *testServer) getOpenAPI(t *testing.T, cookie *http.Cookie, wsID int64, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, openapiURL(wsID, query), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET openapi.json: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode the contract: %v; body = %s", err, rec.Body.String())
	}
	return rec, doc
}

func designErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body = %s", err, rec.Body.String())
	}
	return env.Error.Code
}

// designSpecID reads the workspace's bound specId as GET reports it.
func (ts *testServer) designSpecID(t *testing.T, cookie *http.Cookie, wsID int64) (specID *int64, editVersion int64) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workspace: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		SpecID      *int64 `json:"specId"`
		EditVersion int64  `json:"editVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	return body.SpecID, body.EditVersion
}

func (ts *testServer) rebind(t *testing.T, cookie *http.Cookie, csrfToken string, wsID, specID int64) *httptest.ResponseRecorder {
	t.Helper()
	_, ev := ts.designSpecID(t, cookie, wsID)
	req := jsonRequest(t, http.MethodPatch, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID),
		map[string]any{"specId": specID, "editVersion": ev}, cookie, csrfToken)
	return ts.do(req)
}

// TestDesign_exportComposesTheWorkspace is A11's shape in the small: the
// route answers the RAW document (no envelope), the base's operations are
// kept, the custom row is an operation with its own fields, and the
// version carries the draft suffix. `?download=1` is the one header
// difference.
func TestDesign_exportComposesTheWorkspace(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Design")
	rec := ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "POST", "path": "/things/{thingId}", "status": 201,
		"schema":    map[string]any{"$ref": "#/components/schemas/Widget"},
		"reqSchema": map[string]any{"type": "object", "required": []string{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}},
		"operation": map[string]any{"summary": "Make a thing", "operationId": "makeThing", "tags": []string{"things"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create the designed endpoint: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	rec, doc := ts.getOpenAPI(t, cookie, wsID, "")
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if _, wrapped := doc["error"]; wrapped || doc["openapi"] == nil {
		t.Fatalf("the body is not a raw OpenAPI document: %s", rec.Body.String())
	}
	paths, _ := doc["paths"].(map[string]any)
	if _, ok := paths["/widgets"]; !ok {
		t.Errorf("the base's /widgets is gone from the export; paths = %v", designKeys(paths))
	}
	if _, ok := paths["/extra"]; !ok {
		t.Errorf("the custom /extra row is not an operation; paths = %v", designKeys(paths))
	}
	item, _ := paths["/things/{thingId}"].(map[string]any)
	op, _ := item["post"].(map[string]any)
	if op == nil {
		t.Fatalf("POST /things/{thingId} is not in the export; paths = %v", designKeys(paths))
	}
	if op["operationId"] != "makeThing" || op["summary"] != "Make a thing" {
		t.Errorf("operation fields = %v; want operationId makeThing, summary 'Make a thing'", op)
	}
	params, _ := op["parameters"].([]any)
	if len(params) != 1 {
		t.Fatalf("parameters = %v; want the derived {thingId} path parameter alone", op["parameters"])
	}
	if p, _ := params[0].(map[string]any); p["name"] != "thingId" || p["in"] != "path" || p["required"] != true {
		t.Errorf("derived parameter = %v; want {name: thingId, in: path, required: true}", params[0])
	}
	if rb, _ := op["requestBody"].(map[string]any); rb == nil {
		t.Errorf("reqSchema did not become requestBody: %v", op)
	}
	resp := digMap(op, "responses", "201", "content", "application/json")
	if resp == nil {
		t.Fatalf("responses[201].content[application/json] missing: %v", op["responses"])
	}
	if schema, _ := resp["schema"].(map[string]any); schema == nil || schema["$ref"] != "#/components/schemas/Widget" {
		t.Errorf("schema = %v; want the $ref written as stored", resp["schema"])
	}
	info, _ := doc["info"].(map[string]any)
	version, _ := info["version"].(string)
	rev := ts.workspaceRevision(t, cookie, wsID)
	if !strings.HasSuffix(version, fmt.Sprintf("-draft.%d", rev)) {
		t.Errorf("info.version = %q, want the -draft.%d suffix", version, rev)
	}

	rec, _ = ts.getOpenAPI(t, cookie, wsID, "?download=1")
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=") || !strings.Contains(cd, "-draft-") {
		t.Errorf("download=1: Content-Disposition = %q, want an attachment named <slug>-draft-<rev>", cd)
	}
}

// TestDesign_exportWithNoSpecIsTheSkeleton is A13: a workspace bound to
// nothing exports the 3.1 skeleton titled with its own name, and a
// schema-bearing row on it is an operation there.
func TestDesign_exportWithNoSpecIsTheSkeleton(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "From Nothing")
	wsID := int64(id)

	_, doc := ts.getOpenAPI(t, cookie, wsID, "")
	info, _ := doc["info"].(map[string]any)
	if doc["openapi"] != "3.1.0" || info["title"] != "From Nothing" {
		t.Errorf("skeleton = openapi %v, title %v; want 3.1.0 titled 'From Nothing'", doc["openapi"], info["title"])
	}
	if paths, _ := doc["paths"].(map[string]any); len(paths) != 0 {
		t.Errorf("paths = %v; want none", paths)
	}
	if v, _ := info["version"].(string); !strings.HasPrefix(v, "0.0.0-draft.") {
		t.Errorf("info.version = %q; want 0.0.0-draft.<rev>", v)
	}

	rec := ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/things",
		"schema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("a schema with no $ref on a no-spec workspace: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	_, doc = ts.getOpenAPI(t, cookie, wsID, "")
	if digMap(doc, "paths", "/things", "get", "responses", "200", "content", "application/json") == nil {
		t.Errorf("the row is not an operation of the skeleton export: %v", doc["paths"])
	}
}

// TestDesign_refRefusedAtWrite is A6: a `$ref` the bound spec lacks is
// refused 400 schema_ref_unresolved naming the pointer and NOTHING is
// stored; one it has is accepted; with no spec bound ANY `$ref` is
// refused.
func TestDesign_refRefusedAtWrite(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Refs")

	rec := ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/dangling", "schema": map[string]any{"$ref": "#/components/schemas/Nope"},
	})
	if rec.Code != http.StatusBadRequest || designErrorCode(t, rec) != "schema_ref_unresolved" || !strings.Contains(rec.Body.String(), "#/components/schemas/Nope") {
		t.Errorf("dangling $ref: status = %d, body = %s; want 400 schema_ref_unresolved naming the pointer", rec.Code, rec.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", wsID), nil)
	list.AddCookie(cookie)
	if body := ts.do(list).Body.String(); strings.Contains(body, "/dangling") {
		t.Errorf("the refused row was stored: %s", body)
	}

	rec = ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/me", "schema": map[string]any{"$ref": "#/components/schemas/Widget"},
	})
	if rec.Code != http.StatusCreated {
		t.Errorf("a $ref the spec has: status = %d; body = %s; want 201", rec.Code, rec.Body.String())
	}

	// The same pointer inside reqSchema and inside a parameter's schema
	// takes the same door.
	rec = ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "POST", "path": "/things",
		"reqSchema": map[string]any{"$ref": "#/components/schemas/Nope"},
	})
	if rec.Code != http.StatusBadRequest || designErrorCode(t, rec) != "schema_ref_unresolved" {
		t.Errorf("dangling $ref in reqSchema: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/search",
		"operation": map[string]any{"parameters": []map[string]any{{"name": "q", "in": "query", "schema": map[string]any{"$ref": "#/components/schemas/Nope"}}}},
	})
	if rec.Code != http.StatusBadRequest || designErrorCode(t, rec) != "schema_ref_unresolved" {
		t.Errorf("dangling $ref in a parameter: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	cookieBare, csrfBare, bareID, _ := ts.createWorkspace(t, "Alex", "Bare")
	rec = ts.postEndpoint(t, cookieBare, csrfBare, int64(bareID), map[string]any{
		"method": "GET", "path": "/me", "schema": map[string]any{"$ref": "#/components/schemas/Widget"},
	})
	if rec.Code != http.StatusBadRequest || designErrorCode(t, rec) != "schema_ref_unresolved" {
		t.Errorf("no spec bound: status = %d, body = %s; want 400 schema_ref_unresolved", rec.Code, rec.Body.String())
	}
}

// TestDesign_schemaOnOverrideIsRefused is A8: `schema` on a spec
// operation's variant answers 400 schema_on_override and the field is not
// stored.
func TestDesign_schemaOnOverrideIsRefused(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Override")
	opKey := overrides.OpKey("GET", "/widgets")
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", wsID, opKey)

	get := httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	var current struct {
		Override struct {
			EditVersion int64 `json:"editVersion"`
		} `json:"override"`
		EditVersion int64 `json:"editVersion"`
	}
	getRec := ts.do(get)
	if err := json.Unmarshal(getRec.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode GET operation: %v; body = %s", err, getRec.Body.String())
	}
	ev := current.EditVersion
	if current.Override.EditVersion != 0 {
		ev = current.Override.EditVersion
	}
	req := jsonRequest(t, http.MethodPut, target, map[string]any{
		"overrideOn":  true,
		"responses":   map[string]any{"200": map[string]any{"mode": "generated", "schema": map[string]any{"type": "object"}}},
		"editVersion": ev,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest || designErrorCode(t, rec) != "schema_on_override" {
		t.Errorf("schema on an override: status = %d, body = %s; want 400 schema_on_override", rec.Code, rec.Body.String())
	}
	get = httptest.NewRequest(http.MethodGet, target, nil)
	get.AddCookie(cookie)
	if body := ts.do(get).Body.String(); strings.Contains(body, `"schema"`) {
		t.Errorf("the schema was stored on the override: %s", body)
	}
}

// TestDesign_rebindRefusesADanglingRow is A9's first half: PATCH specId
// onto a spec without the referenced component answers 409
// endpoint_ref_unresolved naming the row, and the binding does not move.
func TestDesign_rebindRefusesADanglingRow(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID, wsID := ts.configuredWorkspace(t, "Rebind")
	rec := ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/me", "schema": map[string]any{"$ref": "#/components/schemas/Widget"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	otherID := ts.importSpecDoc(t, cookie, csrfToken, "Other", otherSpecDoc)

	rec = ts.rebind(t, cookie, csrfToken, wsID, otherID)
	if rec.Code != http.StatusConflict || designErrorCode(t, rec) != "endpoint_ref_unresolved" {
		t.Fatalf("rebind: status = %d, body = %s; want 409 endpoint_ref_unresolved", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Details []struct {
				EndpointID int64  `json:"endpointId"`
				Reason     string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || len(env.Error.Details) != 1 || env.Error.Details[0].EndpointID != created.ID {
		t.Errorf("details = %s (err %v); want the one row by id %d", rec.Body.String(), err, created.ID)
	}
	if got, _ := ts.designSpecID(t, cookie, wsID); got == nil || *got != specID {
		t.Errorf("specId after the refused rebind = %v, want %d unchanged", got, specID)
	}

	// Delete the row and the same rebind goes through.
	del := jsonRequest(t, http.MethodDelete, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints/%d", wsID, created.ID), nil, cookie, csrfToken)
	if r := ts.do(del); r.Code != http.StatusNoContent {
		t.Fatalf("delete endpoint: %d %s", r.Code, r.Body.String())
	}
	if rec = ts.rebind(t, cookie, csrfToken, wsID, otherID); rec.Code != http.StatusOK {
		t.Errorf("rebind after the row is gone: status = %d, body = %s; want 200", rec.Code, rec.Body.String())
	}
}

// TestDesign_importAndRollbackRefuseADanglingRow is A9's second half plus
// D6's rollback clause: an export whose endpoints reference Widget refuses
// to import against a spec without it (no workspace created), and a
// checkpoint holding such a row refuses to roll back once the workspace
// is bound to that spec.
func TestDesign_importAndRollbackRefuseADanglingRow(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Source")
	rec := ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/me", "schema": map[string]any{"$ref": "#/components/schemas/Widget"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	otherID := ts.importSpecDoc(t, cookie, csrfToken, "Other", otherSpecDoc)

	_, raw, _ := ts.exportWorkspace(t, cookie, wsID, "")
	before := ts.countWorkspaces(t, cookie)
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/import",
		map[string]any{"bundle": json.RawMessage(raw), "name": "Imported", "specId": otherID}, cookie, csrfToken)
	rec = ts.do(req)
	if rec.Code != http.StatusConflict || designErrorCode(t, rec) != "endpoint_ref_unresolved" {
		t.Errorf("import against a spec without Widget: status = %d, body = %s; want 409 endpoint_ref_unresolved", rec.Code, rec.Body.String())
	}
	if after := ts.countWorkspaces(t, cookie); after != before {
		t.Errorf("workspaces after the refused import = %d, want %d unchanged", after, before)
	}

	// A checkpoint with the row, the row deleted, the workspace rebound to
	// Other — the rollback would bring the dangling row back, and refuses.
	cp := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "with the ref", http.StatusCreated)
	cid := int64(cp["id"].(float64))
	del := jsonRequest(t, http.MethodDelete, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints/%d", wsID, created.ID), nil, cookie, csrfToken)
	if r := ts.do(del); r.Code != http.StatusNoContent {
		t.Fatalf("delete endpoint: %d %s", r.Code, r.Body.String())
	}
	if r := ts.rebind(t, cookie, csrfToken, wsID, otherID); r.Code != http.StatusOK {
		t.Fatalf("rebind: %d %s", r.Code, r.Body.String())
	}
	rb := jsonRequest(t, http.MethodPost, rollbackURL(wsID, cid), map[string]any{"restoreData": false}, cookie, csrfToken)
	rec = ts.do(rb)
	if rec.Code != http.StatusConflict || designErrorCode(t, rec) != "endpoint_ref_unresolved" {
		t.Errorf("rollback bringing a dangling row back: status = %d, body = %s; want 409 endpoint_ref_unresolved", rec.Code, rec.Body.String())
	}
}

// TestDesign_operationIDMustBeUnique is D3: an operationId held by the
// bound spec's operation or by another custom row answers 409
// operation_id_taken naming the holder; a free one is accepted, and an
// update keeping its own id is not a collision with itself.
func TestDesign_operationIDMustBeUnique(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "OpIDs")

	rec := ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/mine", "operation": map[string]any{"operationId": "listWidgets"},
	})
	if rec.Code != http.StatusConflict || designErrorCode(t, rec) != "operation_id_taken" || !strings.Contains(rec.Body.String(), "spec operation GET /widgets") {
		t.Errorf("a spec operation's id: status = %d, body = %s; want 409 operation_id_taken naming GET /widgets", rec.Code, rec.Body.String())
	}
	rec = ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/mine", "operation": map[string]any{"operationId": "listMine"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("a free id: %d %s", rec.Code, rec.Body.String())
	}
	var mine struct {
		ID          int64 `json:"id"`
		EditVersion int64 `json:"editVersion"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &mine)
	rec = ts.postEndpoint(t, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/theirs", "operation": map[string]any{"operationId": "listMine"},
	})
	if rec.Code != http.StatusConflict || designErrorCode(t, rec) != "operation_id_taken" || !strings.Contains(rec.Body.String(), "custom endpoint GET /mine") {
		t.Errorf("another row's id: status = %d, body = %s; want 409 naming GET /mine", rec.Code, rec.Body.String())
	}
	put := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints/%d", wsID, mine.ID), map[string]any{
		"method": "GET", "path": "/mine", "activeStatus": 200, "responses": map[string]any{},
		"operation": map[string]any{"operationId": "listMine", "summary": "renamed"}, "editVersion": mine.EditVersion,
	}, cookie, csrfToken)
	if r := ts.do(put); r.Code != http.StatusOK {
		t.Errorf("an update keeping its own id: status = %d, body = %s; want 200", r.Code, r.Body.String())
	}
}

func (ts *testServer) countWorkspaces(t *testing.T, cookie *http.Cookie) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/workspaces", nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	var body []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspaces: %v; body = %s", err, rec.Body.String())
	}
	return len(body)
}

func designKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// digMap walks nested objects by key and returns the innermost object,
// nil when any step is missing or not an object.
func digMap(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		next, _ := cur[k].(map[string]any)
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
