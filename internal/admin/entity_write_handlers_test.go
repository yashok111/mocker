package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/yashok111/mocker/internal/testspec"
)

// A11's handler tests, over the same harness A4's read uses: a confirmed
// /widgets family with the derivation spec's own rows.

// entityURL escapes the family exactly as the client must (url.PathEscape:
// "/widgets" -> "%2Fwidgets"); an unescaped family would put "//" in the
// path and earn ServeMux's 307 clean-path redirect, never a handler.
func entityURL(wsID int64, family, key string) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources/%s/entities/%s", wsID, url.PathEscape(family), key)
}

func TestHandler_setResourceEntity_createsThenReplaces_andRaisesSeq(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Entity Writes", specID)
	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)

	// A key far beyond the populated rows: created, the id field forced to it.
	req := jsonRequest(t, http.MethodPut, entityURL(wsID, testspec.FamilyWidgets, "500"),
		map[string]any{"data": map[string]any{"id": 1, "name": "placed"}}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT new key: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Row     resourceEntityWire `json:"row"`
		Created bool               `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Created || out.Row.EntityKey != "500" {
		t.Errorf("created=%v key=%q, want created=true key=500", out.Created, out.Row.EntityKey)
	}
	var data map[string]any
	if err := json.Unmarshal(out.Row.Data, &data); err != nil {
		t.Fatalf("row data: %v", err)
	}
	if fmt.Sprint(data["id"]) != "500" || data["name"] != "placed" {
		t.Errorf("stored data = %v; the id field must be overwritten with the key", data)
	}

	// The family's counter moved past the placed key, so the mock plane's
	// next POST cannot mint 500 again.
	var seq int64
	if err := ts.db.W.QueryRowContext(t.Context(),
		"SELECT seq FROM resources WHERE workspace_id = ? AND route_family = ?", wsID, testspec.FamilyWidgets).Scan(&seq); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	if seq < 500 {
		t.Errorf("resources.seq = %d after placing key 500, want >= 500", seq)
	}

	// Replace: same key, new body, created=false, data replaced whole.
	req = jsonRequest(t, http.MethodPut, entityURL(wsID, testspec.FamilyWidgets, "500"),
		map[string]any{"data": map[string]any{"status": "blocked"}}, cookie, csrfToken)
	rec = ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT replace: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Created {
		t.Errorf("replace reported created=true")
	}
	data = nil
	_ = json.Unmarshal(out.Row.Data, &data)
	if data["status"] != "blocked" || data["name"] != nil || fmt.Sprint(data["id"]) != "500" {
		t.Errorf("replaced data = %v; want the new body whole with id forced", data)
	}

	// The list read sees exactly one row under that key.
	_, list := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyWidgets, "limit=500")
	n := 0
	for _, row := range list.Rows {
		if row.EntityKey == "500" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("rows with key 500 after create+replace = %d, want 1", n)
	}
}

func TestHandler_deleteResourceEntity_removesOnce(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Entity Deletes", specID)
	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)

	_, before := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyWidgets, "limit=500")
	if len(before.Rows) == 0 {
		t.Fatal("fixture populated no rows")
	}
	key := before.Rows[0].EntityKey

	// A body-less DELETE addresses the top-level scope.
	req := jsonRequest(t, http.MethodDelete, entityURL(wsID, testspec.FamilyWidgets, key), nil, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	req = jsonRequest(t, http.MethodDelete, entityURL(wsID, testspec.FamilyWidgets, key), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "entity_not_found" {
		t.Errorf("second DELETE: status = %d code = %q, want 404 entity_not_found", rec.Code, rec.Body.String())
	}
	_, after := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyWidgets, "limit=500")
	if len(after.Rows) != len(before.Rows)-1 {
		t.Errorf("rows after delete = %d, want %d", len(after.Rows), len(before.Rows)-1)
	}
}

func TestHandler_entityWrites_refusals(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Entity Refusals", specID)

	// Unconfirmed family: the read's own unknown_family.
	req := jsonRequest(t, http.MethodPut, entityURL(wsID, testspec.FamilyWidgets, "1"),
		map[string]any{"data": map[string]any{}}, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusNotFound || errorCode(t, rec) != "unknown_family" {
		t.Errorf("unconfirmed family: status = %d body = %s, want 404 unknown_family", rec.Code, rec.Body.String())
	}

	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)

	// A key outside the segment alphabet.
	req = jsonRequest(t, http.MethodPut, entityURL(wsID, testspec.FamilyWidgets, "bad%20key"),
		map[string]any{"data": map[string]any{}}, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_entity_key" {
		t.Errorf("bad key: status = %d body = %s, want 400 invalid_entity_key", rec.Code, rec.Body.String())
	}

	// data missing.
	req = jsonRequest(t, http.MethodPut, entityURL(wsID, testspec.FamilyWidgets, "7"),
		map[string]any{"scopeKey": ""}, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusBadRequest {
		t.Errorf("no data: status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
}
