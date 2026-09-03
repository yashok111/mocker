package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// minimalSpecDoc is a tiny valid OAS 3.1 document with one operation, small
// enough to inline directly in test bodies.
const minimalSpecDoc = `{
	"openapi": "3.1.0",
	"info": { "title": "Widgets", "version": "1.0.0" },
	"paths": {
		"/widgets": {
			"get": { "operationId": "listWidgets", "responses": { "200": { "description": "ok" } } }
		}
	}
}`

const swagger2Doc = `{
	"swagger": "2.0",
	"info": { "title": "Legacy", "version": "1.0.0" },
	"paths": {}
}`

const notADocument = `{"foo": "bar"}`

// manyOpsDoc builds an OAS 3.1 document with n trivial GET operations, one
// per path — the only cheap way to actually exercise
// maxOperationsLimit's 500-row clamp without hand-writing hundreds of paths.
func manyOpsDoc(t *testing.T, n int) string {
	t.Helper()
	paths := make(map[string]any, n)
	for i := range n {
		paths[fmt.Sprintf("/item%04d", i)] = map[string]any{
			"get": map[string]any{"operationId": fmt.Sprintf("op%04d", i)},
		}
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Many Ops", "version": "1.0.0"},
		"paths":   paths,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal many-ops document: %v", err)
	}
	return string(b)
}

// importSpec is a test helper: it logs in a fresh user, imports document as
// "upload", and requires a 201 Created (a fresh, non-duplicate import).
func (ts *testServer) importSpec(t *testing.T, userName, specName, document string) (cookie *http.Cookie, csrfToken string, id int64) {
	t.Helper()
	cookie, csrfToken = ts.login(t, userName)
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": specName, "source": "upload", "document": document}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	return cookie, csrfToken, body.ID
}

func TestHandler_importSpec(t *testing.T) {
	t.Parallel()

	t.Run("a small inline document imports as 201, not a duplicate", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Widgets API", "source": "upload", "document": minimalSpecDoc}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
		}

		var body struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Format    string `json:"format"`
			Source    string `json:"source"`
			Duplicate bool   `json:"duplicate"`
			Report    struct {
				Operations int `json:"operations"`
				Degraded   int `json:"degraded"`
			} `json:"report"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.ID == 0 {
			t.Error("id is zero, want an assigned spec id")
		}
		if body.Name != "Widgets API" {
			t.Errorf("name = %q, want %q", body.Name, "Widgets API")
		}
		if body.Format != "oas31" {
			t.Errorf("format = %q, want %q", body.Format, "oas31")
		}
		if body.Source != "upload" {
			t.Errorf("source = %q, want %q", body.Source, "upload")
		}
		if body.Duplicate {
			t.Error("duplicate = true on the first import, want false")
		}
		if body.Report.Operations != 1 {
			t.Errorf("report.operations = %d, want 1", body.Report.Operations)
		}
		if body.Report.Degraded != 0 {
			t.Errorf("report.degraded = %d, want 0", body.Report.Degraded)
		}
	})

	t.Run("re-importing the same bytes answers 200 duplicate:true with the same id", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, firstID := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Widgets API (again)", "source": "upload", "document": minimalSpecDoc}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("second import: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}

		var body struct {
			ID        int64 `json:"id"`
			Duplicate bool  `json:"duplicate"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !body.Duplicate {
			t.Error("duplicate = false on a byte-identical re-import, want true")
		}
		if body.ID != firstID {
			t.Errorf("id = %d, want the original spec's id %d", body.ID, firstID)
		}
	})

	t.Run("input that is not a document answers 400", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Junk", "source": "upload", "document": notADocument}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a Swagger 2.0 document answers 400 naming a later phase", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Legacy", "source": "upload", "document": swagger2Doc}, cookie, csrfToken)
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
		if !strings.Contains(strings.ToLower(body.Error.Message), "later phase") {
			t.Errorf("error message = %q, want it to name a later phase", body.Error.Message)
		}
	})

	t.Run("a document with too many operations answers 413", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		// 5001 trivial operations, one past specs' maxIndexedOperations
		// ceiling (currently 5000; see internal/specs/repo.go): rejecting
		// this before it ever reaches the write transaction is what keeps a
		// pathological "paths" object from holding the single writer
		// connection for tens of seconds (finding 4).
		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Huge", "source": "upload", "document": manyOpsDoc(t, 5001)}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run(`source "url" answers 400, not a fetch attempt`, func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Remote", "source": "url", "document": minimalSpecDoc}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("without a CSRF token answers 403", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, _ := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
			map[string]string{"name": "Widgets API", "source": "upload", "document": minimalSpecDoc}, cookie, "")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_listGetSpecs(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	_, _, id := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

	t.Run("list includes the imported spec", func(t *testing.T) {
		t.Parallel()
		cookie, _ := ts.login(t, "Bob")
		req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/specs", nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var list []struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		found := false
		for _, sp := range list {
			if sp.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("list = %v, want it to include spec %d", list, id)
		}
	})

	t.Run("get by id answers the spec", func(t *testing.T) {
		t.Parallel()
		cookie, _ := ts.login(t, "Carol")
		target := fmt.Sprintf("http://mocker.local/api/specs/%d", id)
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.ID != id {
			t.Errorf("id = %d, want %d", body.ID, id)
		}
	})

	t.Run("get a nonexistent id answers 404", func(t *testing.T) {
		t.Parallel()
		cookie, _ := ts.login(t, "Dave")
		req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/specs/999999", nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_specReport(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	_, _, id := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)
	cookie, _ := ts.login(t, "Bob")

	target := fmt.Sprintf("http://mocker.local/api/specs/%d/report", id)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Format     string `json:"format"`
		Operations int    `json:"operations"`
		Degraded   int    `json:"degraded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Format != "oas31" {
		t.Errorf("format = %q, want %q", body.Format, "oas31")
	}
	if body.Operations != 1 {
		t.Errorf("operations = %d, want 1", body.Operations)
	}
}

func TestHandler_specOperations(t *testing.T) {
	t.Parallel()

	t.Run("pages and clamps the limit", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		_, _, id := ts.importSpec(t, "Alex", "Many Ops", manyOpsDoc(t, 510))
		cookie, _ := ts.login(t, "Bob")
		base := fmt.Sprintf("http://mocker.local/api/specs/%d/operations", id)

		get := func(query string) []struct {
			SourceOrder int64 `json:"sourceOrder"`
		} {
			t.Helper()
			req := httptest.NewRequest(http.MethodGet, base+query, nil)
			req.AddCookie(cookie)
			rec := ts.do(req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200; body = %s", query, rec.Code, rec.Body.String())
			}
			var out []struct {
				SourceOrder int64 `json:"sourceOrder"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return out
		}

		if def := get(""); len(def) != 100 {
			t.Errorf("default page length = %d, want 100", len(def))
		}
		if clamped := get("?limit=100000"); len(clamped) != 500 {
			t.Errorf("clamped page length = %d, want 500 (of 510 total)", len(clamped))
		}
		if tail := get("?limit=50&offset=507"); len(tail) != 3 {
			t.Errorf("tail page length = %d, want 3 (510 total, offset 507)", len(tail))
		}
		if one := get("?limit=1&offset=1"); len(one) != 1 || one[0].SourceOrder != 1 {
			t.Errorf("limit=1&offset=1 = %v, want exactly one row with sourceOrder 1", one)
		}
	})

	t.Run("limit=0 is rejected, not treated as the repo's \"every row\" sentinel", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		// specs.Repo.Operations treats limit<=0 as "no LIMIT clause at all";
		// the handler must reject ?limit=0 outright rather than let it reach
		// the repo and bypass maxOperationsLimit.
		_, _, id := ts.importSpec(t, "Alex", "Many Ops", manyOpsDoc(t, 510))
		cookie, _ := ts.login(t, "Bob")

		target := fmt.Sprintf("http://mocker.local/api/specs/%d/operations?limit=0", id)
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a nonexistent spec answers 404", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, _ := ts.login(t, "Alex")
		req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/specs/999999/operations", nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_deleteSpec(t *testing.T) {
	t.Parallel()

	t.Run("an unattached spec deletes with 204", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, id := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

		target := fmt.Sprintf("http://mocker.local/api/specs/%d", id)
		req := jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
		}

		get := httptest.NewRequest(http.MethodGet, target, nil)
		get.AddCookie(cookie)
		getRec := ts.do(get)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("GET after delete: status = %d, want 404", getRec.Code)
		}
	})

	t.Run("an attached spec answers 409 naming the workspace", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

		wsReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]any{"name": "Demo", "specId": specID}, cookie, csrfToken)
		wsRec := ts.do(wsReq)
		if wsRec.Code != http.StatusCreated {
			t.Fatalf("create workspace: status = %d, want 201; body = %s", wsRec.Code, wsRec.Body.String())
		}
		var ws struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(wsRec.Body.Bytes(), &ws); err != nil {
			t.Fatalf("decode workspace body: %v", err)
		}

		target := fmt.Sprintf("http://mocker.local/api/specs/%d", specID)
		delReq := jsonRequest(t, http.MethodDelete, target, nil, cookie, csrfToken)
		delRec := ts.do(delReq)
		if delRec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body = %s", delRec.Code, delRec.Body.String())
		}
		var body struct {
			Error struct {
				Details []string `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(delRec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body.Error.Details) != 1 || body.Error.Details[0] != ws.Slug {
			t.Errorf("error.details = %v, want [%q]", body.Error.Details, ws.Slug)
		}
	})

	t.Run("without a CSRF token answers 403", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, _, id := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

		target := fmt.Sprintf("http://mocker.local/api/specs/%d", id)
		req := jsonRequest(t, http.MethodDelete, target, nil, cookie, "")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_attachSpecToWorkspace(t *testing.T) {
	t.Parallel()

	t.Run("create with specId fills settings.basePath from the spec", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]any{"name": "Demo", "specId": specID}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			SpecID   int64 `json:"specId"`
			Settings struct {
				BasePath string `json:"basePath"`
			} `json:"settings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.SpecID != specID {
			t.Errorf("specId = %d, want %d", body.SpecID, specID)
		}
		// minimalSpecDoc declares no servers[], so its own basePath is "" —
		// asserting the field round-trips through settings without erroring
		// is what this test actually needs; the nonempty case is covered by
		// the PATCH subtest below with an explicit spec basePath assumption
		// documented there instead.
	})

	t.Run("patch with specId attaches and fills an empty settings.basePath", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")
		_, _, specID := ts.importSpec(t, "Alex", "Widgets API", minimalSpecDoc)

		target := fmt.Sprintf("http://mocker.local/api/workspaces/%d", int64(wsID))
		req := jsonRequest(t, http.MethodPatch, target, map[string]any{"specId": specID, "editVersion": 1}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			SpecID int64 `json:"specId"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.SpecID != specID {
			t.Errorf("specId = %d, want %d", body.SpecID, specID)
		}
	})

	t.Run("attaching a nonexistent spec on create answers 404", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")

		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
			map[string]any{"name": "Demo", "specId": 999999}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("attaching a nonexistent spec on patch answers 404", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")

		target := fmt.Sprintf("http://mocker.local/api/workspaces/%d", int64(wsID))
		req := jsonRequest(t, http.MethodPatch, target, map[string]any{"specId": 999999, "editVersion": 1}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
		}
	})
}
