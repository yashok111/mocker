package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestAPIDesignRoutesAndManagedGuards(t *testing.T) {
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	src := loopbackTestSrc()
	status, body, err := s.CallAsMCP(ctx, src, "POST", "/api/designs", []byte(`{"name":"Orders"}`))
	if err != nil || status != 201 {
		t.Fatalf("create: %d %s %v", status, body, err)
	}
	var d apidesign.Detail
	if err = json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if d.Draft.Source != "mcp" || d.Design.DraftURL == "" || len(d.Revisions) != 1 {
		t.Fatalf("detail: %+v", d)
	}
	for _, wsID := range []int64{d.Design.DraftWorkspaceID, d.Design.PublishedWorkspaceID} {
		for _, route := range s.routes() {
			method, path, _ := strings.Cut(route.pattern, " ")
			if method == "GET" || !strings.HasPrefix(path, "/api/workspaces/{id}") {
				continue
			}
			switch path {
			case "/api/workspaces/{id}/fork", "/api/workspaces/{id}/preview", "/api/workspaces/{id}/endpoints/preview", "/api/workspaces/{id}/probe":
				continue
			}
			t.Run(fmt.Sprintf("%d_%s", wsID, route.pattern), func(t *testing.T) {
				path = strings.NewReplacer("{id}", fmt.Sprint(wsID), "{cid}", "1", "{sid}", "1", "{eid}", "1", "{tid}", "1", "{opKey}", "GET%20%2Forders", "{family}", "orders", "{key}", "1", "{aid}", "1").Replace(path)
				status, body, err := s.CallAsMCP(ctx, src, method, path, []byte(`{}`))
				if err != nil || status != 409 || !strings.Contains(string(body), `"managed_workspace"`) {
					t.Fatalf("guard: %d %s %v", status, body, err)
				}
			})
		}
	}
	// An ordinary workspace remains writable through exactly the same wrapper.
	ws, err := s.ws.Create(ctx, workspaces.CreateInput{Name: "ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	status, body, err = s.CallAsMCP(ctx, src, "DELETE", fmt.Sprintf("/api/workspaces/%d", ws.ID), nil)
	if err != nil || status >= 300 {
		t.Fatalf("ordinary delete: %d %s %v", status, body, err)
	}
	// Foreign-key ownership also refuses deletion through the repository seam.
	if err = s.ws.Delete(ctx, d.Design.DraftWorkspaceID); err == nil {
		t.Fatal("managed workspace deleted")
	}
}

func TestAPIDesignDirectPublishRequiresSession(t *testing.T) {
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	d, err := s.designsRepo.Create(ctx, apidesign.CreateInput{Name: "API", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.designsRepo.RequestReview(ctx, d.Design.ID, 1, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.mcpIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"expectedVersion":1}`))
	req.SetPathValue("id", fmt.Sprint(d.Design.ID))
	req.SetPathValue("rid", fmt.Sprint(review.ID))
	req = req.WithContext(withAuthContext(ctx, nil, user))
	rec := httptest.NewRecorder()
	s.handlePublishAPIDesignReview(rec, req)
	if rec.Code != 403 {
		t.Fatalf("direct MCP publication: %d %s", rec.Code, rec.Body)
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"expectedVersion":1}`))
	req.SetPathValue("id", fmt.Sprint(d.Design.ID))
	req.SetPathValue("rid", fmt.Sprint(review.ID))
	req = req.WithContext(withAuthContext(ctx, &auth.Session{}, user))
	rec = httptest.NewRecorder()
	s.handlePublishAPIDesignReview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("human publication: %d %s", rec.Code, rec.Body)
	}
}

func TestAPIDesignManagedExportAndBodyLimit(t *testing.T) {
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	src := loopbackTestSrc()
	document := `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"x-kept":{"n":9007199254740993}}`
	d, err := s.designsRepo.Create(ctx, apidesign.CreateInput{Name: "API", Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	status, body, err := s.CallAsMCP(ctx, src, "GET", fmt.Sprintf("/api/workspaces/%d/openapi.json", d.Design.DraftWorkspaceID), nil)
	if err != nil || status != 200 || string(body) != d.Draft.Document {
		t.Fatalf("authored export: %d %s %v", status, body, err)
	}
	status, body, err = s.CallAsMCP(ctx, src, "GET", fmt.Sprintf("/api/workspaces/%d/openapi.json", d.Design.PublishedWorkspaceID), nil)
	if err != nil || status != 404 {
		t.Fatalf("unpublished export: %d %s %v", status, body, err)
	}
	s.cfg.MaxBody = 30
	status, body, err = s.CallAsMCP(ctx, src, "POST", "/api/designs", []byte(`{"name":"`+strings.Repeat("x", 100)+`"}`))
	if err != nil || status != 413 {
		t.Fatalf("body limit: %d %s %v", status, body, err)
	}
}

func TestAPIDesignImportsIndependentWorkspace(t *testing.T) {
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	src := loopbackTestSrc()
	original, err := s.ws.Create(ctx, workspaces.CreateInput{Name: "Source API"})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(`{"name":"New project","workspaceId":%d}`, original.ID))
	status, raw, err := s.CallAsMCP(ctx, src, "POST", "/api/designs", body)
	if err != nil || status != 201 {
		t.Fatalf("import: %d %s %v", status, raw, err)
	}
	var detail apidesign.Detail
	if err = json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Design.DraftWorkspaceID == original.ID || detail.Design.PublishedWorkspaceID == original.ID {
		t.Fatal("source adopted as managed")
	}
	after, err := s.ws.ByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != original.Revision || after.SpecID != nil {
		t.Fatal("source changed")
	}
	if owner, err := s.ws.ManagedDesign(ctx, original.ID); err != nil || owner != 0 {
		t.Fatalf("source membership: %d %v", owner, err)
	}
}
