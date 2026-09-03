package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// authDoc is a small OAS 3.1 document with a login-shaped endpoint (a
// property literally named "token", on a path matching DESIGN §10's
// auth-path trigger list) and one unrelated endpoint, so tests can exercise
// both "this operation has an override" and "this operation does not" in the
// same merged view.
const authDoc = `{
	"openapi": "3.1.0",
	"info": { "title": "Auth Demo", "version": "1.0.0" },
	"paths": {
		"/auth/login": {
			"post": {
				"operationId": "login",
				"responses": {
					"200": {
						"description": "ok",
						"content": {
							"application/json": {
								"schema": {
									"type": "object",
									"properties": {
										"token": { "type": "string" },
										"refresh_token": { "type": "string" },
										"expires_in": { "type": "integer" },
										"user": {
											"type": "object",
											"properties": {
												"id": { "type": "integer" },
												"email": { "type": "string" },
												"name": { "type": "string" }
											}
										}
									}
								}
							}
						}
					}
				}
			}
		},
		"/widgets": {
			"get": {
				"operationId": "listWidgets",
				"responses": { "200": { "description": "ok" } }
			}
		}
	}
}`

// createWorkspaceWithSpec creates a workspace already attached to specID,
// reusing an existing session (typically the one [testServer.importSpec]
// already logged in), and requires 201.
func (ts *testServer) createWorkspaceWithSpec(t *testing.T, cookie *http.Cookie, csrfToken, wsName string, specID int64) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
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

// mergedOperationJSON mirrors admin's unexported mergedOperationView: the
// wire shape of one GET .../operations list entry.
type mergedOperationJSON struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	OpKey    string `json:"opKey"`
	Statuses []struct {
		Selector   string `json:"selector"`
		HTTPStatus int    `json:"httpStatus"`
		IsDefault  bool   `json:"isDefault"`
	} `json:"statuses"`
	Override *struct {
		OverrideOn   bool `json:"overrideOn"`
		RouteOff     bool `json:"routeOff"`
		ActiveStatus *int `json:"activeStatus"`
		Responses    map[string]struct {
			Mode        string `json:"mode"`
			RecipeCount int    `json:"recipeCount"`
		} `json:"responses"`
		UpdatedAt   int64 `json:"updatedAt"`
		EditVersion int64 `json:"editVersion"`
	} `json:"override"`
}

// overrideDocJSON mirrors admin's unexported overrideDocView/overridePutView:
// the full stored override document GET/PUT answer. DelayMs/ValidateReq/
// FailDirective are here (alongside the fields earlier tests already used)
// specifically so round-1 finding #2's coverage
// (TestHandler_authPresetApply_PreservesExistingOverrideFields) can assert
// on them by name rather than re-declaring a second, narrower mirror
// struct.
type overrideDocJSON struct {
	Method        string          `json:"method"`
	Path          string          `json:"path"`
	OpKey         string          `json:"opKey"`
	OverrideOn    bool            `json:"overrideOn"`
	RouteOff      bool            `json:"routeOff"`
	ActiveStatus  *int            `json:"activeStatus"`
	DelayMs       *int            `json:"delayMs"`
	ValidateReq   *bool           `json:"validateReq"`
	FailDirective json.RawMessage `json:"failDirective"`
	Responses     map[string]struct {
		Mode string `json:"mode"`
		When []struct {
			In    string `json:"in"`
			Name  string `json:"name"`
			Op    string `json:"op"`
			Value string `json:"value"`
		} `json:"when"`
		Recipes map[string]struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"recipes"`
	} `json:"responses"`
	UpdatedAt   int64 `json:"updatedAt"`
	Revision    int64 `json:"revision"`
	EditVersion int64 `json:"editVersion"`
}

func decodeOverrideDoc(t *testing.T, rec *httptest.ResponseRecorder) overrideDocJSON {
	t.Helper()
	var doc overrideDocJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode override document: %v; body = %s", err, rec.Body.String())
	}
	return doc
}

func TestHandler_listOperations_noSpec(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations", int64(wsID))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var out []mergedOperationJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("operations for a workspace with no spec = %v, want an empty list, not a 404", out)
	}
}

func TestHandler_listOperations_nonexistentWorkspace(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _ := ts.login(t, "Alex")

	req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/workspaces/999999/operations", nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_operationsLifecycle(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	base := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations", wsID)

	// The merged view lists both operations with no override state yet:
	// creating the workspace does not itself write anything to op_overrides.
	t.Run("merged view before any override: both operations, no override state", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, base, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var out []mergedOperationJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(out) != 2 {
			t.Fatalf("operations = %d, want 2 (POST /auth/login and GET /widgets)", len(out))
		}
		for _, op := range out {
			if op.Override != nil {
				t.Errorf("operation %s %s has override = %+v, want nil (no override written yet)", op.Method, op.Path, op.Override)
			}
			if op.OpKey == "" {
				t.Errorf("operation %s %s has an empty opKey", op.Method, op.Path)
			}
			if len(op.Statuses) != 1 || op.Statuses[0].HTTPStatus != 200 {
				t.Errorf("operation %s %s statuses = %+v, want exactly one 200", op.Method, op.Path, op.Statuses)
			}
		}
	})

	loginOpKey := "POST%20%2Fauth%2Flogin"
	var revisionAfterPut int64
	// editVersion tracks the CURRENT expectation this test's next guarded
	// write must send — A3/D10 requires it on every PUT, and D7 makes 0 the
	// legal "I expect no row" value the operation starts at (this is the
	// first write to this opKey in the whole subtest chain).
	var editVersion int64

	t.Run("PUT an override with a recipe and a when[] this slice does not implement", func(t *testing.T) {
		putBody := map[string]any{
			"overrideOn": true,
			"routeOff":   false,
			"responses": map[string]any{
				"200": map[string]any{
					"mode": "generated",
					"when": []map[string]any{
						{"in": "header", "name": "X-Test", "op": "exists"},
					},
					"recipes": map[string]any{
						"token": map[string]any{"kind": "const", "value": "pinned-token"},
					},
				},
			},
			"editVersion": editVersion,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+loginOpKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		doc := decodeOverrideDoc(t, rec)
		if doc.Method != http.MethodPost || doc.Path != "/auth/login" {
			t.Errorf("PUT response method/path = %q %q, want POST /auth/login", doc.Method, doc.Path)
		}
		if doc.Revision != 2 {
			t.Errorf("PUT response revision = %d, want 2 (bumped once from the create at revision 1)", doc.Revision)
		}
		revisionAfterPut = doc.Revision
		editVersion = doc.EditVersion

		v, ok := doc.Responses["200"]
		if !ok {
			t.Fatalf("PUT response has no responses[200], body = %s", rec.Body.String())
		}
		if len(v.When) != 1 || v.When[0].In != "header" || v.When[0].Name != "X-Test" || v.When[0].Op != "exists" {
			t.Errorf("PUT response responses[200].when = %+v, want the submitted condition echoed back", v.When)
		}
		if gotRecipe, ok := v.Recipes["token"]; !ok || gotRecipe.Kind != "const" {
			t.Errorf("PUT response responses[200].recipes[token] = %+v, want kind=const", gotRecipe)
		}
	})

	t.Run("GET the same override back: the when[] and recipe round-trip unchanged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, base+"/"+loginOpKey, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		doc := decodeOverrideDoc(t, rec)
		if doc.OpKey != loginOpKey {
			t.Errorf("GET response opKey = %q, want %q", doc.OpKey, loginOpKey)
		}
		v, ok := doc.Responses["200"]
		if !ok {
			t.Fatalf("GET response has no responses[200], body = %s", rec.Body.String())
		}
		if len(v.When) != 1 || v.When[0].In != "header" || v.When[0].Name != "X-Test" || v.When[0].Op != "exists" {
			t.Errorf("GET response responses[200].when = %+v, want it preserved from the PUT, unimplemented or not", v.When)
		}
		if gotRecipe, ok := v.Recipes["token"]; !ok || gotRecipe.Kind != "const" {
			t.Errorf("GET response responses[200].recipes[token] = %+v, want kind=const", gotRecipe)
		}
	})

	t.Run("merged view now shows the override summary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, base, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var out []mergedOperationJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		found := false
		for _, op := range out {
			if op.Method != http.MethodPost || op.Path != "/auth/login" {
				continue
			}
			found = true
			if op.Override == nil {
				t.Fatal("login operation has no override in the merged view, want one after the PUT above")
			}
			v, ok := op.Override.Responses["200"]
			if !ok || v.RecipeCount != 1 {
				t.Errorf("login operation override.responses[200] = %+v, want recipeCount 1", v)
			}
		}
		if !found {
			t.Fatal("merged view does not include POST /auth/login at all")
		}
	})

	t.Run("PUT with an invalid recipe answers 400 naming the field, never 500", func(t *testing.T) {
		putBody := map[string]any{
			"responses": map[string]any{
				"200": map[string]any{
					"recipes": map[string]any{
						"token": map[string]any{"kind": "not-a-real-kind"},
					},
				},
			},
			"editVersion": editVersion,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+loginOpKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Error.Message == "" {
			t.Error("error message is empty, want it to name the offending field")
		}
	})

	t.Run("PUT with an unknown status key answers 400", func(t *testing.T) {
		putBody := map[string]any{
			"responses": map[string]any{
				"abc": map[string]any{"mode": "generated"},
			},
			"editVersion": editVersion,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+loginOpKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("PUT storing a browser-executable mediaType answers 400 under ANY mode, never stored", func(t *testing.T) {
		// Regression for the same-origin stored-XSS finding: under
		// MOCKER_ROUTING=path (server.go's servePath) the admin session
		// cookie (Path=/, no Domain) is sent to a mocked URL exactly as it
		// is sent to /api/*, so a pinned text/html body an operator could
		// otherwise set would run same-origin with any teammate's live
		// session the moment they open the mocked URL.
		//
		// The "generated" rows below are the second half of that regression,
		// and they are the ones that used to pass. The guard read
		// `Mode == "pinned" && dangerous(...)`, so a generated variant
		// carrying text/html was stored happily — and the traffic-to-override
		// conversion (from_traffic.go) then flipped that same stored variant
		// to "pinned" in place, landing exactly the row this check exists to
		// refuse by a route this check never saw. Mode is mutable after the
		// fact; the rule cannot depend on it.
		for _, tc := range []struct{ mode, mediaType string }{
			{"pinned", "text/html"},
			{"pinned", "TEXT/HTML; charset=utf-8"},
			{"pinned", "application/xhtml+xml"},
			{"pinned", "image/svg+xml"},
			{"generated", "text/html"},
			{"generated", "TEXT/HTML; charset=utf-8"},
			{"generated", "application/xhtml+xml"},
			{"generated", "image/svg+xml"},
			{"", "text/html"},
		} {
			putBody := map[string]any{
				"responses": map[string]any{
					"200": map[string]any{"mode": tc.mode, "mediaType": tc.mediaType, "body": "<script>alert(1)</script>"},
				},
				"editVersion": editVersion,
			}
			req := jsonRequest(t, http.MethodPut, base+"/"+loginOpKey, putBody, cookie, csrfToken)
			rec := ts.do(req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("mode %q mediaType %q: status = %d, want 400; body = %s", tc.mode, tc.mediaType, rec.Code, rec.Body.String())
			}
		}

		// None of the rejected PUTs above must have reached [overrides.Repo.Put]
		// — not merely that the handler answered 400. A GET closes the gap a
		// finding rejected at the handler but still applied underneath would
		// leave open: the row an earlier subtest wrote (mode "generated", a
		// "token" recipe) is still exactly what is stored, never overwritten
		// with the rejected pinned-HTML attempt.
		get := httptest.NewRequest(http.MethodGet, base+"/"+loginOpKey, nil)
		get.AddCookie(cookie)
		getRec := ts.do(get)
		if getRec.Code != http.StatusOK {
			t.Fatalf("GET after rejected pinned-HTML PUTs: status = %d, want 200 (the earlier override is untouched); body = %s",
				getRec.Code, getRec.Body.String())
		}
		doc := decodeOverrideDoc(t, getRec)
		v, ok := doc.Responses["200"]
		if !ok || v.Mode != "generated" || v.Recipes["token"].Kind != "const" {
			t.Fatalf("GET after rejected pinned-HTML PUTs: responses[200] = %+v, want the untouched earlier override (mode=generated, recipes[token].kind=const)", v)
		}
	})

	t.Run("PUT pinning a safe mediaType still succeeds", func(t *testing.T) {
		putBody := map[string]any{
			"responses": map[string]any{
				"200": map[string]any{"mode": "pinned", "mediaType": "text/plain", "body": `"hello"`},
			},
			"editVersion": editVersion,
		}
		req := jsonRequest(t, http.MethodPut, base+"/"+loginOpKey, putBody, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		// This PUT lands and bumps the revision same as any other — keep the
		// tracked revision (and editVersion) in step so the DELETE subtest
		// below, which checks revisionAfterPut+1, still checks against the
		// write that ACTUALLY preceded it rather than a stale count from
		// three subtests ago.
		doc := decodeOverrideDoc(t, rec)
		revisionAfterPut = doc.Revision
		editVersion = doc.EditVersion
	})

	t.Run("PUT without a CSRF token answers 403", func(t *testing.T) {
		req := jsonRequest(t, http.MethodPut, base+"/"+loginOpKey, map[string]any{"overrideOn": true, "editVersion": editVersion}, cookie, "")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET a malformed opKey answers 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, base+"/not-a-valid-opkey", nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET an operation with no override answers 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, base+"/GET%20%2Fwidgets", nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE without a CSRF token answers 403", func(t *testing.T) {
		req := jsonRequest(t, http.MethodDelete, base+"/"+loginOpKey, nil, cookie, "")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE the override: 200 with the bumped revision, then GET answers 404", func(t *testing.T) {
		req := jsonRequest(t, http.MethodDelete, base+"/"+loginOpKey, nil, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Revision int64 `json:"revision"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode delete response: %v", err)
		}
		if body.Revision != revisionAfterPut+1 {
			t.Errorf("DELETE revision = %d, want %d (one more than the PUT's)", body.Revision, revisionAfterPut+1)
		}

		get := httptest.NewRequest(http.MethodGet, base+"/"+loginOpKey, nil)
		get.AddCookie(cookie)
		getRec := ts.do(get)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("GET after delete: status = %d, want 404; body = %s", getRec.Code, getRec.Body.String())
		}
	})

	t.Run("DELETE an override that is already gone is a no-op 204", func(t *testing.T) {
		req := jsonRequest(t, http.MethodDelete, base+"/"+loginOpKey, nil, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE on a nonexistent workspace answers 404", func(t *testing.T) {
		req := jsonRequest(t, http.MethodDelete,
			"http://mocker.local/api/workspaces/999999/operations/"+loginOpKey, nil, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestHandler_putOperation_editVersionRequired pins A3/D10 on the flagship
// route: a body that omits editVersion is rejected by name (400), never
// silently treated as an unguarded write.
func TestHandler_putOperation_editVersionRequired(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", int64(wsID), "GET%20%2Fwidgets")
	req := jsonRequest(t, http.MethodPut, target, map[string]any{"overrideOn": true, "responses": map[string]any{}}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT with no editVersion: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Message == "" {
		t.Error("error message is empty, want it to name the missing field")
	}
}

// TestHandler_putOperation_editConflictMismatch pins D6's round-trippable
// conflict payload for the operation route: two writers read the same row,
// the first writes and moves the version, and the second's write — sent
// with the now-stale version it originally read — is refused 409
// edit_conflict, carrying the CURRENT stored document (not an echo of what
// the second writer sent) plus the version the server actually holds.
func TestHandler_putOperation_editConflictMismatch(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", int64(wsID), "GET%20%2Fwidgets")

	// Both "readers" start from the same expectation: 0, no row yet.
	firstReq := jsonRequest(t, http.MethodPut, target,
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	firstRec := ts.do(firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first PUT: status = %d, want 200; body = %s", firstRec.Code, firstRec.Body.String())
	}
	first := decodeOverrideDoc(t, firstRec)

	// The second writer still believes the row does not exist (editVersion
	// 0) even though the first writer already created it — a lost update.
	secondReq := jsonRequest(t, http.MethodPut, target,
		map[string]any{"overrideOn": false, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	secondRec := ts.do(secondReq)
	if secondRec.Code != http.StatusConflict {
		t.Fatalf("second (stale) PUT: status = %d, want 409; body = %s", secondRec.Code, secondRec.Body.String())
	}

	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				OverrideOn  bool  `json:"overrideOn"`
				EditVersion int64 `json:"editVersion"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(secondRec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Error.Code != "edit_conflict" {
		t.Errorf("conflict code = %q, want %q", conflict.Error.Code, "edit_conflict")
	}
	// The document is the CURRENT stored one (overrideOn: true, from the
	// FIRST write), never an echo of the second writer's own submitted
	// body (overrideOn: false) — D6's whole point.
	if !conflict.Error.Details.OverrideOn {
		t.Errorf("conflict details.overrideOn = %v, want true (the server's current document, not the caller's submitted one)", conflict.Error.Details.OverrideOn)
	}
	if conflict.Error.Details.EditVersion != first.EditVersion {
		t.Errorf("conflict details.editVersion = %d, want %d (the version the server actually holds)", conflict.Error.Details.EditVersion, first.EditVersion)
	}
}

// TestHandler_putOperation_editConflictGone pins D6/D7's tombstone: an
// expectation was sent for a row that has since been deleted, which is a
// lost update (409 edit_conflict, gone:true) rather than a plain 404.
func TestHandler_putOperation_editConflictGone(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", int64(wsID), "GET%20%2Fwidgets")

	putReq := jsonRequest(t, http.MethodPut, target,
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": 0}, cookie, csrfToken)
	putRec := ts.do(putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("seed PUT: status = %d, want 200; body = %s", putRec.Code, putRec.Body.String())
	}
	doc := decodeOverrideDoc(t, putRec)

	delReq := jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken)
	if rec := ts.do(delReq); rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// A caller that read the row before it was deleted retries with the
	// version it last saw — the target row is gone, which D7 makes a
	// conflict, not a 404.
	retryReq := jsonRequest(t, http.MethodPut, target,
		map[string]any{"overrideOn": true, "responses": map[string]any{}, "editVersion": doc.EditVersion}, cookie, csrfToken)
	retryRec := ts.do(retryReq)
	if retryRec.Code != http.StatusConflict {
		t.Fatalf("PUT after the row was deleted: status = %d, want 409; body = %s", retryRec.Code, retryRec.Body.String())
	}
	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Gone        bool `json:"gone"`
				EditVersion *int `json:"editVersion"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(retryRec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Error.Code != "edit_conflict" {
		t.Errorf("conflict code = %q, want %q", conflict.Error.Code, "edit_conflict")
	}
	if !conflict.Error.Details.Gone {
		t.Errorf("details.gone = %v, want true", conflict.Error.Details.Gone)
	}
	if conflict.Error.Details.EditVersion != nil {
		t.Errorf("details.editVersion = %v, want explicit null", *conflict.Error.Details.EditVersion)
	}
}
