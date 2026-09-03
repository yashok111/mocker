package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

// p1c2EndpointsURL and p1c2EndpointURL build the endpoint CRUD routes'
// targets on the p1c2TestServer harness (livestate_handlers_test.go's own
// builder — this file reuses it rather than admin_test.go's newTestServer,
// since none of these tests need SetLiveState/SetTraffic themselves, but
// staying on one harness per HARD RULE 2's spirit avoids a second,
// near-identical one).
func p1c2EndpointsURL(ts *p1c2TestServer, id int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/endpoints", ts.cfg.AdminHost, id)
}

func p1c2EndpointURL(ts *p1c2TestServer, id, eid int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/endpoints/%d", ts.cfg.AdminHost, id, eid)
}

// p1c2EndpointListJSON mirrors endpoint_handlers.go's own unexported
// endpointListView/endpointView wire shapes: enough of them for these tests
// to assert on without reaching into the package's own types.
type p1c2EndpointListJSON struct {
	Endpoints []p1c2EndpointJSON `json:"endpoints"`
}

type p1c2EndpointJSON struct {
	ID            int64           `json:"id"`
	Method        string          `json:"method"`
	Path          string          `json:"path"`
	CanonicalPath string          `json:"canonicalPath"`
	ActiveStatus  int             `json:"activeStatus"`
	Responses     json.RawMessage `json:"responses"`
	// EditVersion is A3's per-row compare-and-swap token (D5): carried by
	// both create's 201 and update's 200, since both answer [endpointView].
	EditVersion int64 `json:"editVersion"`
}

// p1c2CreateEndpoint is a test helper: POST one explicit endpoint and
// require the given status.
func p1c2CreateEndpoint(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, csrfToken string, wsID int64, body map[string]any, wantStatus int) p1c2EndpointJSON {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, p1c2EndpointsURL(ts, wsID), body, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("create endpoint: status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	var got p1c2EndpointJSON
	if wantStatus == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode create endpoint response: %v", err)
		}
	}
	return got
}

func p1c2ListEndpoints(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, wsID int64) p1c2EndpointListJSON {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, p1c2EndpointsURL(ts, wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list endpoints: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got p1c2EndpointListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list endpoints response: %v", err)
	}
	return got
}

// TestP1c2Endpoints_unauthenticated covers every endpoint route: no session
// cookie must answer 401.
func TestP1c2Endpoints_unauthenticated(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	_, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	targets := []struct {
		name   string
		method string
		url    string
	}{
		{"list", http.MethodGet, p1c2EndpointsURL(ts, id)},
		{"create", http.MethodPost, p1c2EndpointsURL(ts, id)},
		{"delete", http.MethodDelete, p1c2EndpointURL(ts, id, 1)},
	}
	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var req *http.Request
			if tt.method == http.MethodGet {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			} else {
				// jsonRequest, not a bare httptest.NewRequest: enforceCSRF's
				// Content-Type/Origin checks run BEFORE requireUser, so a
				// state-changing request missing either would 415/403 first
				// and never reach the 401 this test is about (same reasoning
				// as traffic_handlers_test.go's identical shaped test).
				req = jsonRequest(t, tt.method, tt.url, map[string]any{"method": "GET", "path": "/x"}, nil, "")
			}
			rec := ts.do(req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without a session: status = %d, want 401; body = %s",
					tt.method, tt.url, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestP1c2Endpoints_missingCSRF covers the two state-changing routes: a
// valid session but no CSRF token must answer 403.
func TestP1c2Endpoints_missingCSRF(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	create := jsonRequest(t, http.MethodPost, p1c2EndpointsURL(ts, id),
		map[string]any{"method": "GET", "path": "/x"}, cookie, "")
	if rec := ts.do(create); rec.Code != http.StatusForbidden {
		t.Fatalf("POST endpoints with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}

	del := jsonRequest(t, http.MethodDelete, p1c2EndpointURL(ts, id, 1), nil, cookie, "")
	if rec := ts.do(del); rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE endpoint with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_createListDelete is the CRUD happy path: create answers
// 201 with the stored row, list shows it, delete answers 204, and the list
// is empty again.
func TestP1c2Endpoints_createListDelete(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "get", "path": "/legacy/ping", "status": 200, "body": map[string]any{"ok": true},
	}, http.StatusCreated)
	if created.Method != http.MethodGet {
		t.Errorf("created.Method = %q, want upper-cased GET", created.Method)
	}
	if created.Path != "/legacy/ping" {
		t.Errorf("created.Path = %q, want /legacy/ping", created.Path)
	}
	if created.ActiveStatus != 200 {
		t.Errorf("created.ActiveStatus = %d, want 200", created.ActiveStatus)
	}

	list := p1c2ListEndpoints(t, ts, cookie, id)
	if len(list.Endpoints) != 1 || list.Endpoints[0].ID != created.ID {
		t.Fatalf("list after create = %+v, want exactly the created row", list)
	}

	del := jsonRequest(t, http.MethodDelete, p1c2EndpointURL(ts, id, created.ID), nil, cookie, csrfToken)
	rec := ts.do(del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE endpoint: status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	list = p1c2ListEndpoints(t, ts, cookie, id)
	if len(list.Endpoints) != 0 {
		t.Errorf("list after delete = %+v, want empty", list.Endpoints)
	}
}

// TestP1c2Endpoints_createDefaultsStatus200 checks a POST with no "status"
// field defaults to 200 rather than being rejected.
func TestP1c2Endpoints_createDefaultsStatus200(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "POST", "path": "/things",
	}, http.StatusCreated)
	if created.ActiveStatus != 200 {
		t.Errorf("ActiveStatus with no status field = %d, want default 200", created.ActiveStatus)
	}
}

// TestP1c2Endpoints_deleteUnknown404 checks DELETE for an id that parses but
// names no endpoint.
func TestP1c2Endpoints_deleteUnknown404(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	del := jsonRequest(t, http.MethodDelete, p1c2EndpointURL(ts, id, 999999), nil, cookie, csrfToken)
	rec := ts.do(del)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown endpoint: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_duplicateCanonicalPathConflict checks the second
// endpoint at the same (method, canonical path) answers 409, per
// [customep.Repo.Create]'s own UNIQUE-index-backed ErrConflict.
func TestP1c2Endpoints_duplicateCanonicalPathConflict(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "GET", "path": "/dupe",
	}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPost, p1c2EndpointsURL(ts, id),
		map[string]any{"method": "GET", "path": "/dupe"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_dangerousMediaTypeRejected mirrors
// override_handlers_test.go's identically-motivated case: a custom endpoint
// pins a body under an operator-chosen Content-Type on the SAME origin the
// admin session cookie is sent to, so text/html must be refused at write
// time exactly as it is for op_overrides.
func TestP1c2Endpoints_dangerousMediaTypeRejected(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPost, p1c2EndpointsURL(ts, id), map[string]any{
		"method": "GET", "path": "/evil", "mediaType": "text/html", "body": "<script>1</script>",
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dangerous mediaType create: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_conflictWithExistingOverride is round-1 review finding
// 3: from_traffic.go's handleToEndpoint already refuses a custom endpoint at
// a (method, path) an op_overrides row occupies (DESIGN §8's cross-table
// rule — router.compareRoutes gives a custom route priority at equal
// specificity, which would silently strand the override's when[],
// activeStatus, recipes and pinned body). Before this fix, only that
// traffic-conversion path enforced it; the manual create route this test
// exercises did not, so an operator could reach the identical forbidden
// state by hand with no error at all. Mirrors
// TestP1c2ToEndpoint_conflictWithExistingOverride's shape on the manual
// create route instead of the traffic conversion.
func TestP1c2Endpoints_conflictWithExistingOverride(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	opKey := overrides.OpKey("GET", "/widgets")
	// editVersion: 0 -- D7: legal and means "I expect no row", the state
	// every first PUT of an operation nobody has overridden yet is in.
	putReq := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, id, opKey),
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	if rec := ts.do(putReq); rec.Code != http.StatusOK {
		t.Fatalf("seed override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	req := jsonRequest(t, http.MethodPost, p1c2EndpointsURL(ts, id),
		map[string]any{"method": "GET", "path": "/widgets"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("create endpoint over an existing override: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if list := p1c2ListEndpoints(t, ts, cookie, id); len(list.Endpoints) != 0 {
		t.Errorf("endpoints after a refused create = %+v, want none written", list.Endpoints)
	}
}

// p1c2UpdateEndpoint is a test helper: PUT one endpoint's full definition
// and require the given status.
func p1c2UpdateEndpoint(t *testing.T, ts *p1c2TestServer, cookie *http.Cookie, csrfToken string, wsID, eid int64, body map[string]any, wantStatus int) p1c2EndpointJSON {
	t.Helper()
	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, wsID, eid), body, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("update endpoint: status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	var got p1c2EndpointJSON
	if wantStatus == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode update endpoint response: %v", err)
		}
	}
	return got
}

// TestP1c2Endpoints_updateUnauthenticated and TestP1c2Endpoints_updateMissingCSRF
// extend the two blanket auth/CSRF checks above (targets/jsonRequest set up
// there) onto the PUT route specifically — those two tests were written
// before PUT existed and their own "targets" tables don't cover it.
func TestP1c2Endpoints_updateUnauthenticated(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	_, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, 1),
		map[string]any{"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{}}, nil, "")
	rec := ts.do(req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("PUT endpoint without a session: status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestP1c2Endpoints_updateMissingCSRF(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, 1),
		map[string]any{"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{}}, cookie, "")
	if rec := ts.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("PUT endpoint with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_updateFullReplacement is D4's central contract: PUT
// rewrites method, path, both booleans, status and responses, and the id
// (which GET /endpoints already handed the caller) survives the edit even
// though Path changed — the exact property an Update() built on upsertTx
// instead of its own UPDATE would fail.
func TestP1c2Endpoints_updateFullReplacement(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "GET", "path": "/before", "status": 200, "body": map[string]any{"a": 1},
	}, http.StatusCreated)

	updated := p1c2UpdateEndpoint(t, ts, cookie, csrfToken, id, created.ID, map[string]any{
		"method": "post", "path": "/after/{id}", "activeStatus": 201,
		"responses":   map[string]any{"201": map[string]any{"mode": "pinned", "body": map[string]any{"ok": true}}},
		"editVersion": created.EditVersion,
	}, http.StatusOK)

	if updated.ID != created.ID {
		t.Errorf("updated.ID = %d, want unchanged %d (a Path edit must not reassign identity)", updated.ID, created.ID)
	}
	if updated.Method != http.MethodPost {
		t.Errorf("updated.Method = %q, want upper-cased POST", updated.Method)
	}
	if updated.Path != "/after/{id}" {
		t.Errorf("updated.Path = %q, want /after/{id}", updated.Path)
	}
	if updated.CanonicalPath != "/after/{}" {
		t.Errorf("updated.CanonicalPath = %q, want /after/{} (recomputed, never carried)", updated.CanonicalPath)
	}
	if updated.ActiveStatus != 201 {
		t.Errorf("updated.ActiveStatus = %d, want 201", updated.ActiveStatus)
	}

	list := p1c2ListEndpoints(t, ts, cookie, id)
	if len(list.Endpoints) != 1 {
		t.Fatalf("list after update = %+v, want exactly 1 row (an id-unstable update would leave 2)", list.Endpoints)
	}
}

// TestP1c2Endpoints_updateOmittedOverrideOnDefaultsTrue and
// TestP1c2Endpoints_updateOmittedRouteOffDefaultsFalse are D4 3c's wire
// contract directly: a PUT body that omits overrideOn/routeOff must NOT
// silently disable (or fail to re-enable) the endpoint — the exact trap a
// plain bool (rather than *bool) on the wire cannot avoid.
func TestP1c2Endpoints_updateOmittedOverrideOnDefaultsTrue(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "GET", "path": "/x",
	}, http.StatusCreated)

	// p1c2EndpointJSON does not decode overrideOn (it isn't asserted on
	// anywhere else in this file) — read the wire body directly here rather
	// than widening that shared type for one test.
	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{}, "editVersion": created.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if on, _ := raw["overrideOn"].(bool); !on {
		t.Errorf("overrideOn after an update omitting the field = %v, want true (the create-time default, unchanged)", raw["overrideOn"])
	}
}

func TestP1c2Endpoints_updateOmittedRouteOffDefaultsFalse(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "GET", "path": "/x",
	}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{}, "editVersion": created.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if off, _ := raw["routeOff"].(bool); off {
		t.Errorf("routeOff after an update omitting the field = %v, want false", raw["routeOff"])
	}
}

// TestP1c2Endpoints_updateExplicitOverrideOnFalsePersists is the flip side
// of the default test above: an explicit false must actually stick, not be
// coerced back to the default by some accidental "zero value means unset"
// logic downstream of the *bool decode.
func TestP1c2Endpoints_updateExplicitOverrideOnFalsePersists(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "GET", "path": "/x",
	}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{}, "overrideOn": false,
		"editVersion": created.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if on, _ := raw["overrideOn"].(bool); on {
		t.Errorf("overrideOn after an explicit false = %v, want false to persist", raw["overrideOn"])
	}
}

// TestP1c2Endpoints_updateUnknown404 used to mirror the delete route's
// identical 404 -- A3/D7 deliberately OVERRIDES that for this route: an
// expectation was sent and the target row is absent, which D7 calls a lost
// update rather than a missing resource, uniformly on every one of the five
// guarded routes ("This overrides an existing 404 on two routes and the
// override is deliberate" -- customep.Repo.Update is one of the two named).
// So a caller sending a non-nil editVersion for an id that never existed now
// gets 409 edit_conflict with the D6 tombstone, not 404 -- customep.Repo
// itself can't tell "never existed" from "existed then got deleted", and D7
// resolves both the same way once an expectation was sent.
func TestP1c2Endpoints_updateUnknown404(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, 999999), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{}, "editVersion": 1,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT unknown endpoint with an expectation: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errObj, _ := body["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "edit_conflict" {
		t.Errorf("wire code = %q, want %q", code, "edit_conflict")
	}
	details, _ := errObj["details"].(map[string]any)
	if gone, _ := details["gone"].(bool); !gone {
		t.Errorf("details.gone = %v, want true", details["gone"])
	}
	if v, ok := details["editVersion"]; ok && v != nil {
		t.Errorf("details.editVersion = %v, want explicit null", v)
	}
}

// TestP1c2Endpoints_updateConflictNaturalKey and
// TestP1c2Endpoints_updateConflictCanonicalKey are D4 item 2's two halves,
// driven through the HTTP route: editing an endpoint onto a key ANOTHER row
// already holds must answer 409, for both of custom_endpoints' unique
// indexes (0001_init.sql:210-211), not just the one Create already covered.
func TestP1c2Endpoints_updateConflictNaturalKey(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/taken"}, http.StatusCreated)
	mover := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/mover"}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, mover.ID), map[string]any{
		"method": "GET", "path": "/taken", "activeStatus": 200, "responses": map[string]any{}, "editVersion": mover.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("update onto an existing (method, path): status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestP1c2Endpoints_updateConflictCanonicalKey(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/a/{id}"}, http.StatusCreated)
	mover := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/mover"}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, mover.ID), map[string]any{
		"method": "GET", "path": "/a/{name}", "activeStatus": 200, "responses": map[string]any{}, "editVersion": mover.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("update onto a colliding canonical path: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_updateConflictWithExistingOverride mirrors
// TestP1c2Endpoints_conflictWithExistingOverride on the PUT route: an edit
// that MOVES an endpoint onto a (method, path) an op_overrides row already
// occupies must be refused exactly like a create at that key would be.
func TestP1c2Endpoints_updateConflictWithExistingOverride(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	opKey := overrides.OpKey("GET", "/widgets")
	putReq := jsonRequest(t, http.MethodPut, fmt.Sprintf("http://%s/api/workspaces/%d/operations/%s", ts.cfg.AdminHost, id, opKey),
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	if rec := ts.do(putReq); rec.Code != http.StatusOK {
		t.Fatalf("seed override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/movable"}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/widgets", "activeStatus": 200, "responses": map[string]any{}, "editVersion": created.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("update onto an existing override's key: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_updateDangerousMediaTypeRejected mirrors the create
// route's identical case, looped over PUT's Responses map instead of
// create's single mediaType field.
func TestP1c2Endpoints_updateDangerousMediaTypeRejected(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/x"}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200,
		"responses": map[string]any{"200": map[string]any{"mode": "pinned", "mediaType": "text/html", "body": "<script>1</script>"}},
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dangerous mediaType update: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_updateWorkspaceNotFound mirrors the create/list route's
// identical case.
func TestP1c2Endpoints_updateWorkspaceNotFound(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, _, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, 999999, 1), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{},
	}, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT endpoint for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_workspaceNotFound is a 404 for an id that parses but
// names no workspace, on every endpoint route.
func TestP1c2Endpoints_workspaceNotFound(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, _, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	get := httptest.NewRequest(http.MethodGet, p1c2EndpointsURL(ts, 999999), nil)
	get.AddCookie(cookie)
	if rec := ts.do(get); rec.Code != http.StatusNotFound {
		t.Fatalf("GET endpoints for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}

	create := jsonRequest(t, http.MethodPost, p1c2EndpointsURL(ts, 999999),
		map[string]any{"method": "GET", "path": "/x"}, cookie, csrfToken)
	if rec := ts.do(create); rec.Code != http.StatusNotFound {
		t.Fatalf("POST endpoints for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_updateEditVersionRequired pins A3/D10: a PUT body that
// omits editVersion is rejected by name (400), never silently accepted as
// an unguarded write.
func TestP1c2Endpoints_updateEditVersionRequired(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{"method": "GET", "path": "/x"}, http.StatusCreated)

	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 200, "responses": map[string]any{},
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT with no editVersion: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Endpoints_updateEditConflictMismatch pins D6's round-trippable
// conflict payload for the endpoint route: a second writer sends the
// version it originally read, which is now stale because a first writer
// already updated the row — refused 409 edit_conflict, carrying the
// CURRENT stored document (never an echo of the second writer's own body)
// plus the version the server actually holds.
func TestP1c2Endpoints_updateEditConflictMismatch(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	created := p1c2CreateEndpoint(t, ts, cookie, csrfToken, id, map[string]any{
		"method": "GET", "path": "/x", "status": 200, "body": map[string]any{"a": 1},
	}, http.StatusCreated)

	// First writer updates using the version it read at create time.
	first := p1c2UpdateEndpoint(t, ts, cookie, csrfToken, id, created.ID, map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 201,
		"responses":   map[string]any{"201": map[string]any{"mode": "pinned", "body": map[string]any{"first": true}}},
		"editVersion": created.EditVersion,
	}, http.StatusOK)

	// Second writer still believes the row is at the create-time version —
	// stale now that the first writer already moved it.
	req := jsonRequest(t, http.MethodPut, p1c2EndpointURL(ts, id, created.ID), map[string]any{
		"method": "GET", "path": "/x", "activeStatus": 202,
		"responses":   map[string]any{"202": map[string]any{"mode": "pinned", "body": map[string]any{"second": true}}},
		"editVersion": created.EditVersion,
	}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second (stale) PUT: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				ActiveStatus int   `json:"activeStatus"`
				EditVersion  int64 `json:"editVersion"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Error.Code != "edit_conflict" {
		t.Errorf("conflict code = %q, want %q", conflict.Error.Code, "edit_conflict")
	}
	if conflict.Error.Details.ActiveStatus != 201 {
		t.Errorf("conflict details.activeStatus = %d, want 201 (the server's current document, from the FIRST write, not the second writer's own 202)", conflict.Error.Details.ActiveStatus)
	}
	if conflict.Error.Details.EditVersion != first.EditVersion {
		t.Errorf("conflict details.editVersion = %d, want %d (the version the server actually holds)", conflict.Error.Details.EditVersion, first.EditVersion)
	}
}
