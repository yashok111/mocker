package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/traffic"
)

// p1c2WidgetsDoc declares a NON-EMPTY base path ("/api/v1", via servers[])
// and one operation WITH a {param} — "/widgets/{widgetId}" — deliberately:
// per this task's own warning, a test built against a param-less path can
// pass vacuously even when the conversion strips the base path instead of
// resolving the operation's own template, so every to-override test in this
// file uses this operation, never a bare "/widgets".
const p1c2WidgetsDoc = `{
	"openapi": "3.1.0",
	"info": { "title": "Widgets", "version": "1.0.0" },
	"servers": [ { "url": "https://api.example.com/api/v1" } ],
	"paths": {
		"/widgets/{widgetId}": {
			"get": {
				"operationId": "getWidget",
				"parameters": [
					{ "name": "widgetId", "in": "path", "required": true, "schema": { "type": "string" } }
				],
				"responses": { "200": { "description": "ok" } }
			}
		}
	}
}`

// p1c2OtherSpecDoc is a second, unrelated spec: its own single operation
// exists only so a traffic row's matched_id can point at an operation row
// that belongs to THIS spec while the workspace under test is attached to
// p1c2WidgetsDoc's spec instead — the "re-import orphaned matched_id" shape.
const p1c2OtherSpecDoc = `{
	"openapi": "3.1.0",
	"info": { "title": "Other", "version": "1.0.0" },
	"paths": {
		"/other": {
			"get": { "operationId": "getOther", "responses": { "200": { "description": "ok" } } }
		}
	}
}`

// p1c2ImportSpec logs in a fresh user and imports document, requiring 201.
func p1c2ImportSpec(t *testing.T, ts *p1c2TestServer, userName, name, document string) (cookie *http.Cookie, csrfToken string, specID int64) {
	t.Helper()
	cookie, csrfToken = ts.p1c2Login(t, userName)
	req := jsonRequest(t, http.MethodPost, "http://"+ts.cfg.AdminHost+"/api/specs",
		map[string]string{"name": name, "source": "upload", "document": document}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	return cookie, csrfToken, body.ID
}

// p1c2CreateWorkspaceWithSpec creates a workspace attached to specID,
// reusing an existing session — [handleCreateWorkspace] copies the spec's
// own base_path into settings.BasePath, which is how every test in this
// file gets a NON-EMPTY base path without hand-editing settings.
func p1c2CreateWorkspaceWithSpec(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, csrfToken, wsName string, specID int64) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://"+ts.cfg.AdminHost+"/api/workspaces",
		map[string]any{"name": wsName, "specId": specID}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace with spec: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create-workspace-with-spec response: %v", err)
	}
	return body.ID
}

// p1c2OperationID finds specID's operation matching method/path (as stored,
// i.e. the TEMPLATE path) via GET /api/specs/{id}/operations and returns its
// row id — the value a traffic row's matched_id must carry to reference it.
func p1c2OperationID(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, specID int64, method, path string) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/specs/%d/operations", ts.cfg.AdminHost, specID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list spec operations: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var ops []struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ops); err != nil {
		t.Fatalf("decode spec operations: %v", err)
	}
	for _, op := range ops {
		if op.Method == method && op.Path == path {
			return op.ID
		}
	}
	t.Fatalf("no operation %s %s in spec %d operations %+v", method, path, specID, ops)
	return 0
}

// p1c2RecordOne queues ev through the SAME recorder [Server.SetTraffic]
// wired (the real redact/queue/flush path, not a hand-rolled INSERT — see
// p1c2RecordAndFlush's identical reasoning in traffic_handlers_test.go),
// flushes it out, and returns the row's own id — read back through the
// admin API itself (GET .../traffic, newest first) rather than reaching
// into the database directly, since this package's own tests never query
// store.DB by hand elsewhere. cookie must belong to a session already
// logged in (GET /traffic requires one); this file only ever records one
// row at a time per workspace before reading it back, so "newest" is
// unambiguous.
func p1c2RecordOne(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, ev traffic.Event) int64 {
	t.Helper()
	ts.recorder.Record(ev)
	if err := ts.recorder.Flush(t.Context()); err != nil {
		t.Fatalf("flush recorder: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, p1c2TrafficURL(ts, ev.WorkspaceID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("find recorded row id: list traffic: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Rows []struct {
			ID int64 `json:"id"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode traffic list: %v", err)
	}
	if len(body.Rows) == 0 {
		t.Fatalf("no traffic rows for workspace %d after recording one", ev.WorkspaceID)
	}
	return body.Rows[0].ID // GET /traffic is newest-first
}

func p1c2ToOverrideURL(ts *p1c2TestServer, wsID, trafficID int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/traffic/%d/to-override", ts.cfg.AdminHost, wsID, trafficID)
}

func p1c2ToEndpointURL(ts *p1c2TestServer, wsID, trafficID int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/traffic/%d/to-endpoint", ts.cfg.AdminHost, wsID, trafficID)
}

func p1c2WorkspaceRevision(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, wsID int64) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/workspaces/%d", ts.cfg.AdminHost, wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get workspace: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	return body.Revision
}

// --- to-endpoint ---------------------------------------------------------

// TestP1c2ToEndpoint_stripsBasePath is the pinned regression this task warns
// about by name: workspace basePath is "/api/v1" (non-empty), the observed
// traffic row's path is "/api/v1/legacy/ping", and the created endpoint's
// Path must be the RELATIVE "/legacy/ping" — never the untouched concrete
// path, which would never match a request again (the base path is glued
// back on at match time).
func TestP1c2ToEndpoint_stripsBasePath(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_000, 0),
		Method: "GET", Path: "/api/v1/legacy/ping", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("to-endpoint: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode to-endpoint response: %v", err)
	}
	if body.Path != "/legacy/ping" {
		t.Fatalf("created endpoint path = %q, want the RELATIVE %q", body.Path, "/legacy/ping")
	}

	list := p1c2ListEndpoints(t, ts, cookie, wsID)
	if len(list.Endpoints) != 1 {
		t.Fatalf("endpoints after conversion = %d, want 1", len(list.Endpoints))
	}
	got := list.Endpoints[0]
	if got.Path != "/legacy/ping" || got.CanonicalPath != "/legacy/ping" {
		t.Errorf("stored endpoint = %+v, want relative path/canonicalPath /legacy/ping", got)
	}
	// The pinned 404->200 rule: an observed 404 with no body becomes 200
	// carrying "{}".
	if got.ActiveStatus != http.StatusOK {
		t.Errorf("stored endpoint activeStatus = %d, want 200 (404 is rewritten)", got.ActiveStatus)
	}
	var responses map[string]struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(got.Responses, &responses); err != nil {
		t.Fatalf("decode stored responses: %v", err)
	}
	v, ok := responses["200"]
	if !ok {
		t.Fatalf("stored responses = %+v, want a 200 entry", responses)
	}
	if string(v.Body) != "{}" {
		t.Errorf("stored 200 body = %s, want the literal {}", v.Body)
	}
}

// TestP1c2ToEndpoint_preservesNonNotFoundStatus checks the OTHER half of the
// pinned rule: any status besides 404 is preserved exactly, body included.
func TestP1c2ToEndpoint_preservesNonNotFoundStatus(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_001, 0),
		Method: "GET", Path: "/api/v1/broken", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusInternalServerError, Duration: time.Millisecond,
		RespBody: []byte(`{"error":"boom"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("to-endpoint: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	list := p1c2ListEndpoints(t, ts, cookie, wsID)
	if len(list.Endpoints) != 1 {
		t.Fatalf("endpoints after conversion = %d, want 1", len(list.Endpoints))
	}
	got := list.Endpoints[0]
	if got.ActiveStatus != http.StatusInternalServerError {
		t.Errorf("stored endpoint activeStatus = %d, want the preserved 500", got.ActiveStatus)
	}
	var responses map[string]struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(got.Responses, &responses); err != nil {
		t.Fatalf("decode stored responses: %v", err)
	}
	v, ok := responses["500"]
	if !ok {
		t.Fatalf("stored responses = %+v, want a 500 entry", responses)
	}
	if string(v.Body) != `{"error":"boom"}` {
		t.Errorf("stored 500 body = %s, want the observed body preserved", v.Body)
	}
}

// TestP1c2ToEndpoint_pathOutsideBasePath409 checks a traffic row whose path
// does not start with the workspace's CURRENT base path is refused rather
// than guessed at.
func TestP1c2ToEndpoint_pathOutsideBasePath409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_002, 0),
		Method: "GET", Path: "/somewhere/else", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-endpoint outside base path: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// p1c2PatchBasePath PATCHes wsID's settings.basePath to newBasePath, keeping
// every other settings field byte-identical to whatever the workspace
// currently holds (PATCH replaces settings WHOLESALE, D2/A3) — GET first,
// mutate the one field, PATCH back with the workspace's own current
// editVersion. Fails the test on anything but 200.
func p1c2PatchBasePath(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, csrfToken string, wsID int64, newBasePath string) {
	t.Helper()
	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/workspaces/%d", ts.cfg.AdminHost, wsID), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get workspace before base-path patch: status = %d; body = %s", getRec.Code, getRec.Body.String())
	}
	var current struct {
		Settings    map[string]any `json:"settings"`
		EditVersion int64          `json:"editVersion"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode workspace before base-path patch: %v", err)
	}
	current.Settings["basePath"] = newBasePath

	patchReq := jsonRequest(t, http.MethodPatch, fmt.Sprintf("http://%s/api/workspaces/%d", ts.cfg.AdminHost, wsID),
		map[string]any{"settings": current.Settings, "editVersion": current.EditVersion}, cookie, csrfToken)
	patchRec := ts.do(patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch base path to %q: status = %d, want 200; body = %s", newBasePath, patchRec.Code, patchRec.Body.String())
	}
}

// TestP1c2ToEndpoint_stripsParameterisedBasePath is P17 (mocker-p3h-basepath
// D12/D14): stripBasePath's mutation is restoring the LITERAL
// strings.HasPrefix comparison it replaced, and the shape neither
// TestP1c2ToEndpoint_stripsBasePath above nor
// TestP1c2ToEndpoint_pathOutsideBasePath409 exercises is a basePath that
// itself carries a {param} segment — "/orgs/7/quizzes" neither starts with
// nor equals "/orgs/{orgId}" as TEXT, which is exactly the case a literal
// comparison degrades on silently. This test is this property's own red:
// restoring the literal prefix check makes stripBasePath refuse this
// request outright (the observed path does not start with the literal
// string "/orgs/{orgId}"), which the assertion below would catch as a 409
// where it expects 201.
func TestP1c2ToEndpoint_stripsParameterisedBasePath(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	p1c2PatchBasePath(t, ts, cookie, csrfToken, wsID, "/orgs/{orgId}")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_003, 0),
		Method: "GET", Path: "/orgs/7/quizzes", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("to-endpoint under parameterised base path: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode to-endpoint response: %v", err)
	}
	if body.Path != "/quizzes" {
		t.Fatalf("created endpoint path = %q, want the RELATIVE %q (basePath's own {orgId} segment stripped positionally)", body.Path, "/quizzes")
	}
}

// TestP1c2ToEndpoint_conflictWithExistingOverride checks the cross-table
// rule (DESIGN §8): an op_overrides row already at (method, relative path)
// refuses the conversion, and nothing is written (the revision must not
// move beyond whatever the PUT that created the override already bumped it
// to).
func TestP1c2ToEndpoint_conflictWithExistingOverride(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)

	// An override at the SAME relative path the traffic row will strip down
	// to: GET /legacy/ping.
	opKey := overrides.OpKey("GET", "/legacy/ping")
	putReq := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, wsID, opKey),
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	if rec := ts.do(putReq); rec.Code != http.StatusOK {
		t.Fatalf("seed override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	revisionBefore := p1c2WorkspaceRevision(t, ts, cookie, wsID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_003, 0),
		Method: "GET", Path: "/api/v1/legacy/ping", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-endpoint over an existing override: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if got := p1c2WorkspaceRevision(t, ts, cookie, wsID); got != revisionBefore {
		t.Errorf("revision after a refused conversion = %d, want unchanged %d", got, revisionBefore)
	}
	if list := p1c2ListEndpoints(t, ts, cookie, wsID); len(list.Endpoints) != 0 {
		t.Errorf("endpoints after a refused conversion = %+v, want none written", list.Endpoints)
	}
}

// TestP1c2ToEndpoint_conflictWithExistingEndpoint checks
// [customep.ErrConflict] (a second endpoint at the same canonical path)
// surfaces as 409 through this conversion too, not just the manual create
// route.
func TestP1c2ToEndpoint_conflictWithExistingEndpoint(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)

	p1c2CreateEndpoint(t, ts, cookie, csrfToken, wsID, map[string]any{
		"method": "GET", "path": "/legacy/ping",
	}, http.StatusCreated)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_004, 0),
		Method: "GET", Path: "/api/v1/legacy/ping", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-endpoint over an existing endpoint: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2ToEndpoint_truncatedRow409 is round-1 review finding 2: a row the
// recorder marked Truncated must be refused here exactly like it already is
// on the to-override path (TestP1c2ToOverride_truncatedRow409) — creating a
// custom endpoint from a cut body ships the same lie a pinned override
// would, and nothing must be written.
func TestP1c2ToEndpoint_truncatedRow409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: id, TS: time.Unix(1_700_000_030, 0),
		Method: "GET", Path: "/legacy/ping", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"ok":true}`), Truncated: true,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, id, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-endpoint on a truncated row: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if list := p1c2ListEndpoints(t, ts, cookie, id); len(list.Endpoints) != 0 {
		t.Errorf("endpoints after a refused conversion = %+v, want none written", list.Endpoints)
	}
}

// TestP1c2ToEndpoint_redactedRow409 is round-1 review finding 2's other
// half: a row the recorder redacted (a secret field in the observed body)
// must be refused here for the same reason it already is on the to-override
// path (TestP1c2ToOverride_redactedRow409) — before this fix, a suppressed
// or redacted row's empty/placeholder body silently became a 201-Created
// endpoint with no error telling the admin the pin did not capture what
// they thought it did.
func TestP1c2ToEndpoint_redactedRow409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: id, TS: time.Unix(1_700_000_031, 0),
		Method: "GET", Path: "/legacy/ping", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"password":"hunter2"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, id, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-endpoint on a redacted row: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if list := p1c2ListEndpoints(t, ts, cookie, id); len(list.Endpoints) != 0 {
		t.Errorf("endpoints after a refused conversion = %+v, want none written", list.Endpoints)
	}
}

// TestP1c2ToEndpoint_unknownTrafficRow404 checks a {tid} that parses but
// names no traffic row.
func TestP1c2ToEndpoint_unknownTrafficRow404(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, id, 999999), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("to-endpoint for an unknown traffic row: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2ToEndpoint_refusesShapeCollisionWithUnmatchedOperation is the
// round-3 review regression: row.Path is [mockplane.NormalizeSegments]
// rejoined into a plain string for DISPLAY ONLY (see that function's own
// warning), so it cannot distinguish a genuine two-segment request from a
// single segment that contained an encoded slash (%2F). This traffic row
// models exactly the second case — the router recorded MatchedKind "none"
// because nothing in this spec has a ONE-segment route there — but its
// stored Path string, "/api/v1/widgets/123", is byte-identical to what a
// real two-segment "GET /widgets/123" would have produced, and the spec's
// own "/widgets/{widgetId}" is exactly that shape. Naively re-splitting the
// display string would silently create a custom endpoint the router then
// prefers over the real operation (customep beats a spec route on a tie) —
// for a route the original caller never actually matched and the admin
// never saw hit. It must be refused, and nothing may be written.
func TestP1c2ToEndpoint_refusesShapeCollisionWithUnmatchedOperation(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	revisionBefore := p1c2WorkspaceRevision(t, ts, cookie, wsID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_020, 0),
		Method: "GET", Path: "/api/v1/widgets/123", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-endpoint colliding with an unmatched operation's shape: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if got := p1c2WorkspaceRevision(t, ts, cookie, wsID); got != revisionBefore {
		t.Errorf("revision after a refused conversion = %d, want unchanged %d", got, revisionBefore)
	}
	if list := p1c2ListEndpoints(t, ts, cookie, wsID); len(list.Endpoints) != 0 {
		t.Errorf("endpoints after a refused conversion = %+v, want none written", list.Endpoints)
	}
}

// TestP1c2ToEndpoint_allowsShapeMatchingItsOwnMatchedOperation checks the
// other side of the same guard: a row that genuinely, verifiably matched
// this exact operation live (MatchedKind "operation", MatchedID pointing at
// it) is NOT refused just because its re-split segments land on that same
// operation's shape — the router already proved those segments real before
// row.Path's lossy string ever existed, and DESIGN §8/customep's own
// package doc say a custom endpoint shadowing a spec operation on purpose
// is the point of the feature, not a conflict.
func TestP1c2ToEndpoint_allowsShapeMatchingItsOwnMatchedOperation(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	opID := p1c2OperationID(t, ts, cookie, specID, "GET", "/widgets/{widgetId}")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_021, 0),
		Method: "GET", Path: "/api/v1/widgets/7", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: opID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"id":"7"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("to-endpoint on a row that genuinely matched this operation: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
}

// --- to-override -----------------------------------------------------------

// TestP1c2ToOverride_writesTemplateKey is the pinned regression this task
// warns about by name: the traffic row's CONCRETE path is
// "/api/v1/widgets/7"; the stored override key must be
// overrides.OpKey("GET", "/widgets/{widgetId}") — the operation's own
// TEMPLATE path, resolved via matched_id, never a base-path strip of the
// concrete path (which would produce "/widgets/7", a key no route lookup
// will ever produce again). Also proves the merged operations view shows it
// afterward, end to end.
func TestP1c2ToOverride_writesTemplateKey(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	opID := p1c2OperationID(t, ts, cookie, specID, "GET", "/widgets/{widgetId}")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_010, 0),
		Method: "GET", Path: "/api/v1/widgets/7", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: opID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"id":"7"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("to-override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	wantKey := overrides.OpKey("GET", "/widgets/{widgetId}")
	var body struct {
		OpKey  string `json:"opKey"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode to-override response: %v", err)
	}
	if body.OpKey != wantKey {
		t.Fatalf("to-override opKey = %q, want the TEMPLATE key %q", body.OpKey, wantKey)
	}
	if body.Status != http.StatusOK {
		t.Errorf("to-override status = %d, want 200 (the observed status)", body.Status)
	}

	// Read it back through the operations API, proving the merged view
	// actually surfaces it — a row written under a key the mock plane's
	// lookup can never produce would leave this GET showing no override at
	// all.
	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, wsID, wantKey), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET the written override: status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
	}
	var doc struct {
		Responses map[string]struct {
			Mode string          `json:"mode"`
			Body json.RawMessage `json:"body"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode override doc: %v", err)
	}
	v, ok := doc.Responses["200"]
	if !ok || v.Mode != "pinned" || string(v.Body) != `{"id":"7"}` {
		t.Errorf("stored override responses[200] = %+v, want a pinned 200 carrying the observed body", doc.Responses)
	}

	listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/workspaces/%d/operations", ts.cfg.AdminHost, wsID), nil)
	listReq.AddCookie(cookie)
	listRec := ts.do(listReq)
	var list []struct {
		OpKey    string `json:"opKey"`
		Override *struct {
			OverrideOn bool `json:"overrideOn"`
		} `json:"override,omitempty"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode merged operations list: %v", err)
	}
	found := false
	for _, op := range list {
		if op.OpKey == wantKey {
			found = true
			if op.Override == nil || !op.Override.OverrideOn {
				t.Errorf("merged view entry for %q = %+v, want an ON override", wantKey, op)
			}
		}
	}
	if !found {
		t.Fatalf("merged operations list %+v does not contain %q", list, wantKey)
	}
}

// TestP1c2ToOverride_existingOverrideOffBecomesOn checks pinning onto an
// EXISTING row whose override_on is false flips it on — Repo.Put defaults
// OverrideOn true only for a brand-new row, so this handler must set it
// explicitly or the pinned body would be stored but never served.
func TestP1c2ToOverride_existingOverrideOffBecomesOn(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	opID := p1c2OperationID(t, ts, cookie, specID, "GET", "/widgets/{widgetId}")

	opKey := overrides.OpKey("GET", "/widgets/{widgetId}")
	putReq := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, wsID, opKey),
		map[string]any{"overrideOn": false, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	if rec := ts.do(putReq); rec.Code != http.StatusOK {
		t.Fatalf("seed switched-off override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_011, 0),
		Method: "GET", Path: "/api/v1/widgets/9", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: opID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"id":"9"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusOK {
		t.Fatalf("to-override onto a switched-off row: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, wsID, opKey), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	var doc struct {
		OverrideOn bool `json:"overrideOn"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode override doc: %v", err)
	}
	if !doc.OverrideOn {
		t.Error("overrideOn after to-override onto a switched-off row = false, want true (on and serving)")
	}
}

// TestP1c2ToOverride_preservesExistingVariantFields is TASK 1's own pinned
// regression: seed responses["200"] with recipes, when[], headers, mediaType
// AND schemaPatch — the FIVE fields a fresh overrides.Variant{} struct
// literal would silently discard — then run a to-override conversion at
// that same status and assert the STORED row still carries every one of
// them alongside the new pinned body. Five, not the two or three that feel
// representative: a fix that copies only the named fields would still pass
// a guard that names only some of them.
//
// Against the code this replaces — cur.Responses[statusKey] =
// overrides.Variant{Mode: "pinned", Body: respBody, BodyEncoding: encoding},
// a bare struct literal replacing the whole map entry — every one of the
// five assertions below would fail: that is exactly the shape that turns
// the auth preset's JWT recipe on a login route's responses["200"] into a
// token-less pinned body the instant an operator clicks «создать правку из
// этого ответа» on the login row.
func TestP1c2ToOverride_preservesExistingVariantFields(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	opID := p1c2OperationID(t, ts, cookie, specID, "GET", "/widgets/{widgetId}")
	opKey := overrides.OpKey("GET", "/widgets/{widgetId}")

	// mode "generated" (not "pinned") on purpose: this seed's whole point is
	// to look like a variant an operator or the auth preset built BEFORE any
	// conversion touched it, which is exactly the state the conversion must
	// carry forward rather than replace. The mediaType below is deliberately
	// innocuous — a browser-executable one is refused at the write boundary
	// under every mode now (override_handlers.go), and what happens to one
	// that reached the row anyway is pinned in from_traffic_internal_test.go.
	seeded := map[string]any{
		"overrideOn": true,
		"responses": map[string]any{
			"200": map[string]any{
				"mode": "generated",
				"recipes": map[string]any{
					"token": map[string]any{"kind": "jwt"},
				},
				"when": []map[string]any{
					{"in": "query", "name": "debug", "op": "exists"},
				},
				"headers":   map[string]any{"X-Seeded": "yes"},
				"mediaType": "application/json",
				// A real RFC 6902 array, not the bare object this fixture
				// used to seed: the ingress gate (P2e) re-validates a
				// schemaPatch that differs from what is stored, and against
				// a fresh operation with no prior row, ANY non-empty patch
				// is "different" by construction — a non-conforming object
				// would now be refused at this very PUT instead of surviving
				// to the assertion below.
				"schemaPatch": []map[string]any{
					{"op": "add", "path": "/properties/note", "value": "seeded"},
				},
			},
		},
		"editVersion": 0,
	}
	putReq := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, wsID, opKey),
		seeded, cookie, csrfToken)
	if rec := ts.do(putReq); rec.Code != http.StatusOK {
		t.Fatalf("seed variant with all five fields: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_020, 0),
		Method: "GET", Path: "/api/v1/widgets/42", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: opID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"id":"42"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusOK {
		t.Fatalf("to-override onto a variant with recipes/when/headers/mediaType/schemaPatch: status = %d, want 200; body = %s",
			rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, wsID, opKey), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET the converted override: status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
	}
	var doc struct {
		Responses map[string]overrides.Variant `json:"responses"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode override doc: %v", err)
	}
	v, ok := doc.Responses["200"]
	if !ok {
		t.Fatalf("stored responses = %+v, want a 200 entry", doc.Responses)
	}

	// The new pinned body — this is the ONE thing the conversion is meant
	// to change.
	if v.Mode != "pinned" || string(v.Body) != `{"id":"42"}` {
		t.Errorf("stored 200 variant mode/body = %q/%s, want pinned/{\"id\":\"42\"}", v.Mode, v.Body)
	}
	// The five fields the old code discarded — every one must survive
	// byte-for-byte.
	if len(v.Recipes) != 1 || v.Recipes["token"].Kind != "jwt" {
		t.Errorf("stored 200 variant recipes = %+v, want the seeded jwt recipe under %q to survive", v.Recipes, "token")
	}
	if len(v.When) != 1 || v.When[0].In != "query" || v.When[0].Name != "debug" || v.When[0].Op != "exists" {
		t.Errorf("stored 200 variant when = %+v, want the seeded condition to survive", v.When)
	}
	if v.Headers["X-Seeded"] != "yes" {
		t.Errorf("stored 200 variant headers = %+v, want X-Seeded=yes to survive", v.Headers)
	}
	if v.MediaType != "application/json" {
		t.Errorf("stored 200 variant mediaType = %q, want the seeded application/json to survive", v.MediaType)
	}
	const wantSchemaPatch = `[{"op":"add","path":"/properties/note","value":"seeded"}]`
	if string(v.SchemaPatch) != wantSchemaPatch {
		t.Errorf("stored 200 variant schemaPatch = %s, want the seeded %s to survive", v.SchemaPatch, wantSchemaPatch)
	}
}

// TestP1c2ToOverride_truncatedRow409 checks a row the recorder itself marked
// Truncated is refused: a pinned body assembled from a cut body would ship
// the operator a lie.
func TestP1c2ToOverride_truncatedRow409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	opID := p1c2OperationID(t, ts, cookie, specID, "GET", "/widgets/{widgetId}")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_012, 0),
		Method: "GET", Path: "/api/v1/widgets/1", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: opID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"id":"1"}`), Truncated: true,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-override on a truncated row: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2ToOverride_redactedRow409 checks a row the recorder redacted (a
// secret field in the observed body) is refused for the same reason a
// truncated one is.
func TestP1c2ToOverride_redactedRow409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)
	opID := p1c2OperationID(t, ts, cookie, specID, "GET", "/widgets/{widgetId}")

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_013, 0),
		Method: "GET", Path: "/api/v1/widgets/2", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: opID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{"id":"2","password":"hunter2"}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-override on a redacted row: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2ToOverride_noneRow409 checks a row that matched nothing (typically
// a 404) has nothing to pin to.
func TestP1c2ToOverride_noneRow409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, specID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", specID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_014, 0),
		Method: "GET", Path: "/api/v1/nope", PeerIP: "127.0.0.1",
		MatchedKind: "none", Status: http.StatusNotFound, Duration: time.Millisecond,
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-override on a none row: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2ToOverride_operationFromAnotherSpec409 checks a traffic row whose
// matched_id names an operation belonging to a DIFFERENT spec than the
// workspace's own — the "re-import orphaned it" shape.
func TestP1c2ToOverride_operationFromAnotherSpec409(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, widgetsSpecID := p1c2ImportSpec(t, ts, "Alex", "Widgets", p1c2WidgetsDoc)
	_, _, otherSpecID := p1c2ImportSpec(t, ts, "Alex", "Other", p1c2OtherSpecDoc)
	otherOpID := p1c2OperationID(t, ts, cookie, otherSpecID, "GET", "/other")

	// The workspace is attached to WIDGETS, but the traffic row's matched_id
	// points at OTHER's operation.
	wsID := p1c2CreateWorkspaceWithSpec(t, ts, cookie, csrfToken, "Demo", widgetsSpecID)

	tid := p1c2RecordOne(t, ts, cookie, traffic.Event{
		WorkspaceID: wsID, TS: time.Unix(1_700_000_015, 0),
		Method: "GET", Path: "/api/v1/other", PeerIP: "127.0.0.1",
		MatchedKind: "operation", MatchedID: otherOpID, Status: http.StatusOK, Duration: time.Millisecond,
		RespBody: []byte(`{}`),
	})

	req := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, wsID, tid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("to-override on a cross-spec operation: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2ToOverride_unauthenticatedAndCSRF covers auth/CSRF on the one
// state-changing route this file adds beyond endpoint_handlers_test.go's own
// coverage of the endpoint routes.
func TestP1c2ToOverride_unauthenticatedAndCSRF(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	noAuth := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, id, 1), nil, nil, "")
	if rec := ts.do(noAuth); rec.Code != http.StatusUnauthorized {
		t.Fatalf("to-override with no session: status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}

	noCSRF := jsonRequest(t, http.MethodPost, p1c2ToOverrideURL(ts, id, 1), nil, cookie, "")
	if rec := ts.do(noCSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("to-override with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}

	noAuthEP := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, id, 1), nil, nil, "")
	if rec := ts.do(noAuthEP); rec.Code != http.StatusUnauthorized {
		t.Fatalf("to-endpoint with no session: status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}

	noCSRFEP := jsonRequest(t, http.MethodPost, p1c2ToEndpointURL(ts, id, 1), nil, cookie, "")
	if rec := ts.do(noCSRFEP); rec.Code != http.StatusForbidden {
		t.Fatalf("to-endpoint with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}
