package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/testspec"
)

// P4b's handler tests: an export is imported back and forked, over the
// derivation spec's confirmed /widgets family, one override and one custom
// endpoint, so all four layers of the document are exercised end to end.

// exportDoc is the wire shape the tests read back; the arrays stay raw so a
// test asserts on their COUNT and a couple of fields, not on the format
// (that is internal/bundle's own test's job).
type exportDoc struct {
	MockerBundle int `json:"mockerBundle"`
	Workspace    struct {
		Name     string          `json:"name"`
		Settings json.RawMessage `json:"settings"`
	} `json:"workspace"`
	BasePath string `json:"basePath"`
	Spec     struct {
		Hash   string          `json:"hash"`
		Name   string          `json:"name"`
		Inline json.RawMessage `json:"inline"`
	} `json:"spec"`
	Overrides []json.RawMessage `json:"overrides"`
	Endpoints []json.RawMessage `json:"endpoints"`
	Resources []json.RawMessage `json:"resources"`
	Decisions []json.RawMessage `json:"decisions"`
	Entities  json.RawMessage   `json:"entities"`
	Data      *struct {
		MockerData int `json:"mockerData"`
		Families   []struct {
			RouteFamily string            `json:"routeFamily"`
			Rows        []json.RawMessage `json:"rows"`
		} `json:"families"`
	} `json:"data"`
}

func (ts *testServer) exportWorkspace(t *testing.T, cookie *http.Cookie, wsID int64, query string) (*http.Request, []byte, exportDoc) {
	t.Helper()
	req := jsonRequest(t, http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/export%s", wsID, query), nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET export%s: status = %d, want 200; body = %s", query, rec.Code, rec.Body.String())
	}
	var doc exportDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=") || !strings.HasSuffix(cd, `.mocker.json"`) {
		t.Errorf("Content-Disposition = %q, want attachment; filename=\"<slug>.mocker.json\"", cd)
	}
	return req, rec.Body.Bytes(), doc
}

// configuredWorkspace stands up the fixture every test here starts from:
// the derivation spec, a workspace bound to it, /widgets confirmed (rows
// populated), one override and one custom endpoint.
func (ts *testServer) configuredWorkspace(t *testing.T, name string) (cookie *http.Cookie, csrfToken string, specID, wsID int64) {
	t.Helper()
	cookie, csrfToken = ts.login(t, "Alex")
	specID = ts.importDerivationSpec(t, cookie, csrfToken)
	wsID = ts.createWorkspaceWithSpec(t, cookie, csrfToken, name, specID)
	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	ts.putOverride(t, cookie, csrfToken, wsID, "GET", "/widgets")
	ts.createEndpoint(t, cookie, csrfToken, wsID, "GET", "/extra", http.StatusCreated)
	return cookie, csrfToken, specID, wsID
}

func TestHandler_exportWorkspace_carriesEveryLayerAndOptionalHalves(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _, _, wsID := ts.configuredWorkspace(t, "Export Me")

	_, _, plain := ts.exportWorkspace(t, cookie, wsID, "")
	if plain.MockerBundle != 6 {
		t.Errorf("mockerBundle = %d, want 6", plain.MockerBundle)
	}
	if len(plain.Overrides) != 1 || len(plain.Endpoints) != 1 || len(plain.Resources) != 1 || len(plain.Decisions) != 1 {
		t.Errorf("layers = overrides %d, endpoints %d, resources %d, decisions %d; want 1 each",
			len(plain.Overrides), len(plain.Endpoints), len(plain.Resources), len(plain.Decisions))
	}
	if string(plain.Entities) != "null" {
		t.Errorf("entities = %s, want null (rows travel under data, P3d)", plain.Entities)
	}
	if plain.Data != nil {
		t.Errorf("plain export carries data; want it only with includeData=true")
	}
	if plain.Spec.Hash == "" || string(plain.Spec.Inline) != "null" {
		t.Errorf("spec = hash %q inline %s; want a hash and inline null", plain.Spec.Hash, plain.Spec.Inline)
	}

	_, _, full := ts.exportWorkspace(t, cookie, wsID, "?includeData=true&includeSpec=true")
	if full.Data == nil || len(full.Data.Families) != 1 || full.Data.Families[0].RouteFamily != testspec.FamilyWidgets || len(full.Data.Families[0].Rows) == 0 {
		t.Fatalf("includeData: data = %+v; want the /widgets family with rows", full.Data)
	}
	var inline string
	if err := json.Unmarshal(full.Spec.Inline, &inline); err != nil || !strings.Contains(inline, `"openapi"`) {
		t.Errorf("includeSpec: spec.inline = %.60s (err %v); want the document as one JSON string", full.Spec.Inline, err)
	}
}

func TestHandler_importWorkspace_roundTripsAnExportAndResolvesTheSpecByHash(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID, wsID := ts.configuredWorkspace(t, "Source")
	_, raw, src := ts.exportWorkspace(t, cookie, wsID, "?includeData=true")
	srcRows := len(src.Data.Families[0].Rows)

	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/import",
		map[string]any{"bundle": json.RawMessage(raw), "name": "Imported", "slug": "imported"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST import: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Workspace struct {
			ID     int64  `json:"id"`
			Slug   string `json:"slug"`
			Name   string `json:"name"`
			SpecID *int64 `json:"specId"`
		} `json:"workspace"`
		SpecID           *int64 `json:"specId"`
		SpecCreated      bool   `json:"specCreated"`
		EntitiesRestored int    `json:"entitiesRestored"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Workspace.Slug != "imported" || out.Workspace.Name != "Imported" {
		t.Errorf("workspace = %+v", out.Workspace)
	}
	if out.SpecID == nil || *out.SpecID != specID || out.SpecCreated || out.Workspace.SpecID == nil || *out.Workspace.SpecID != specID {
		t.Errorf("spec resolution: specId %v created %v workspace.specId %v; want the source's spec %d by hash, not created",
			out.SpecID, out.SpecCreated, out.Workspace.SpecID, specID)
	}
	if out.EntitiesRestored != 1 {
		t.Errorf("entitiesRestored = %d, want 1 family", out.EntitiesRestored)
	}

	// The copy exports to the same layers with the same row count, and its
	// history starts with the import's own baseline checkpoint.
	_, _, dst := ts.exportWorkspace(t, cookie, out.Workspace.ID, "?includeData=true")
	if len(dst.Overrides) != 1 || len(dst.Endpoints) != 1 || len(dst.Resources) != 1 || dst.Data == nil || len(dst.Data.Families[0].Rows) != srcRows {
		t.Errorf("imported layers = overrides %d endpoints %d resources %d rows %v; want 1/1/1/%d",
			len(dst.Overrides), len(dst.Endpoints), len(dst.Resources), dst.Data, srcRows)
	}
	_, list := ts.listResourceEntities(t, cookie, out.Workspace.ID, testspec.FamilyWidgets, "limit=500")
	if len(list.Rows) != srcRows {
		t.Errorf("imported family holds %d rows, want %d", len(list.Rows), srcRows)
	}
	ckReq := jsonRequest(t, http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/checkpoints", out.Workspace.ID), nil, cookie, "")
	ckRec := ts.do(ckReq)
	if ckRec.Code != http.StatusOK || !strings.Contains(ckRec.Body.String(), `"импорт"`) {
		t.Errorf("checkpoints of the import: status %d body %s; want one labelled «импорт»", ckRec.Code, ckRec.Body.String())
	}

	// The same slug again is the create route's own 409.
	req = jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/import",
		map[string]any{"bundle": json.RawMessage(raw), "name": "Again", "slug": "imported"}, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusConflict {
		t.Errorf("duplicate slug: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_importWorkspace_refusals(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Source")
	_, raw, _ := ts.exportWorkspace(t, cookie, wsID, "")

	post := func(body map[string]any) (int, string, string) {
		t.Helper()
		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/import", body, cookie, csrfToken)
		rec := ts.do(req)
		code := ""
		if rec.Code >= 400 {
			code = errorCode(t, rec)
		}
		return rec.Code, code, rec.Body.String()
	}

	// A v3 document is refused by the format's own validator, with its words.
	if st, code, body := post(map[string]any{"bundle": map[string]any{"mockerBundle": 3}, "name": "x"}); st != http.StatusBadRequest || code != "invalid_bundle" || !strings.Contains(body, "mockerBundle 3") {
		t.Errorf("v3 document: status %d code %q body %s; want 400 invalid_bundle naming the version", st, code, body)
	}
	// A hash this installation lacks, with no inline copy: 409 with details.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["spec"] = map[string]any{"hash": "deadbeef", "name": "elsewhere", "inline": nil}
	if st, code, body := post(map[string]any{"bundle": doc, "name": "x"}); st != http.StatusConflict || code != "spec_not_found" || !strings.Contains(body, "deadbeef") {
		t.Errorf("unknown hash: status %d code %q body %s; want 409 spec_not_found with the hash in details", st, code, body)
	}
	// An explicit specId that does not exist: the attach route's own 404.
	if st, _, _ := post(map[string]any{"bundle": doc, "name": "x", "specId": 999999}); st != http.StatusNotFound {
		t.Errorf("unknown specId: status %d, want 404", st)
	}
	// No bundle at all.
	if st, _, _ := post(map[string]any{"name": "x"}); st != http.StatusBadRequest {
		t.Errorf("no bundle: status %d, want 400", st)
	}
}

func TestHandler_importWorkspace_inlineSpecIsImportedOnce(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Source")
	_, raw, _ := ts.exportWorkspace(t, cookie, wsID, "")

	// A document naming a spec this server has never seen — the nested
	// derivation document, inlined as the export would inline it — imports
	// the spec on the first call and finds it by hash on the second.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	inline := string(testspec.NestedDerivationDoc())
	doc["spec"] = map[string]any{"hash": "not-this-installations", "name": "nested", "inline": inline}
	// The overrides and resources of the source name /widgets routes the
	// nested spec does not have; they import as rows regardless (an import
	// is a restore, not a validation against the spec), which is the drift
	// report's business afterwards.
	call := func(slug string) (specID int64, created bool) {
		t.Helper()
		req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/import",
			map[string]any{"bundle": doc, "name": "Inline " + slug, "slug": slug}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("import with inline spec: status = %d; body = %s", rec.Code, rec.Body.String())
		}
		var out struct {
			SpecID      *int64 `json:"specId"`
			SpecCreated bool   `json:"specCreated"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.SpecID == nil {
			t.Fatalf("decode: %v body %s", err, rec.Body.String())
		}
		return *out.SpecID, out.SpecCreated
	}
	first, created := call("inline-a")
	if !created {
		t.Errorf("first import: specCreated = false, want true")
	}
	// The second call carries the same inline bytes; the hash the document
	// NAMES still resolves to nothing, so the inline copy is hashed again —
	// and deduplicated onto the spec the first call created.
	second, created := call("inline-b")
	if created || second != first {
		t.Errorf("second import: specId %d created %v; want %d, not created", second, created, first)
	}
}

func TestHandler_forkWorkspace_copiesEverythingAndLeavesTheSourceAlone(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID, wsID := ts.configuredWorkspace(t, "Original")
	_, _, before := ts.exportWorkspace(t, cookie, wsID, "?includeData=true")
	srcRows := len(before.Data.Families[0].Rows)
	srcRevision := ts.workspaceRevision(t, cookie, wsID)

	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/fork", wsID), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST fork: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var fork struct {
		ID         int64  `json:"id"`
		Slug       string `json:"slug"`
		Name       string `json:"name"`
		SpecID     *int64 `json:"specId"`
		ForkedFrom *int64 `json:"forkedFrom"`
		Revision   int64  `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fork); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fork.ID == wsID || fork.ForkedFrom == nil || *fork.ForkedFrom != wsID || fork.SpecID == nil || *fork.SpecID != specID {
		t.Errorf("fork = %+v; want a new id, forkedFrom %d, specId %d", fork, wsID, specID)
	}
	if !strings.HasSuffix(fork.Name, "(копия)") || fork.Slug == "" {
		t.Errorf("fork name %q slug %q; want the source's name with the copy suffix and a fresh slug", fork.Name, fork.Slug)
	}

	_, _, after := ts.exportWorkspace(t, cookie, fork.ID, "?includeData=true")
	if len(after.Overrides) != 1 || len(after.Endpoints) != 1 || len(after.Resources) != 1 || after.Data == nil || len(after.Data.Families[0].Rows) != srcRows {
		t.Errorf("fork layers = overrides %d endpoints %d resources %d data %v; want 1/1/1/%d rows", len(after.Overrides), len(after.Endpoints), len(after.Resources), after.Data, srcRows)
	}
	if got := ts.workspaceRevision(t, cookie, wsID); got != srcRevision {
		t.Errorf("source revision moved %d -> %d on a fork; a fork must not write the source", srcRevision, got)
	}

	// includeData:false forks the configuration alone: the family is
	// confirmed and empty.
	req = jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/fork", wsID),
		map[string]any{"name": "Config only", "includeData": false}, cookie, csrfToken)
	rec = ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST fork (no data): status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var lean struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &lean)
	_, list := ts.listResourceEntities(t, cookie, lean.ID, testspec.FamilyWidgets, "limit=500")
	if len(list.Rows) != 0 {
		t.Errorf("includeData:false fork holds %d rows, want 0", len(list.Rows))
	}
}

// workspaceRevision reads GET /api/workspaces/{id}'s revision.
func (ts *testServer) workspaceRevision(t *testing.T, cookie *http.Cookie, wsID int64) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workspace %d: status = %d", wsID, rec.Code)
	}
	var out struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Revision
}

// TestHandler_importWorkspace_refusesAnExecutableMediaType pins the
// 2026-09-03 audit finding: every other writer of a pinned variant refuses
// a media type the browser executes before the row is written, and the
// import did not — it stored the variant (the serve path still refused it,
// so it silently never served). The hard rule's "neither stored" half now
// holds on this door too.
func TestHandler_importWorkspace_refusesAnExecutableMediaType(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, _, wsID := ts.configuredWorkspace(t, "Source")
	_, raw, _ := ts.exportWorkspace(t, cookie, wsID, "")

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	overrides, _ := doc["overrides"].([]any)
	if len(overrides) != 1 {
		t.Fatalf("export carries %d overrides, want 1", len(overrides))
	}
	overrides[0].(map[string]any)["responses"] = map[string]any{
		"200": map[string]any{"mode": "pinned", "mediaType": "text/html", "body": "<script>1</script>"},
	}
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces/import",
		map[string]any{"bundle": doc, "name": "Bad import"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "text/html") {
		t.Fatalf("import with a text/html pinned variant: status = %d, body = %s; want 400 naming the type", rec.Code, rec.Body.String())
	}
}
