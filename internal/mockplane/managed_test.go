package mockplane_test

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestManagedRuntimePublicationAndControlGuard(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	repo := apidesign.NewRepo(db, cfg)
	wsRepo := workspaces.NewRepo(db)
	doc := `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/one":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"string","enum":["one"]}}}}}}}}}`
	d, err := repo.Create(ctx, apidesign.CreateInput{Name: "API", Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	plane := mockplane.New(cfg, wsRepo, specs.NewRepo(db, cfg), testLogger())
	plane.SetLiveState(livestate.NewStore(0, nil))
	request := func(wsID int64, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		ws, err := wsRepo.ByID(ctx, wsID)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		plane.ServeHTTP(rec, httptest.NewRequest(method, fmt.Sprintf("http://%s.mock.local%s", ws.Slug, path), strings.NewReader(body)))
		return rec
	}
	if got := request(d.Design.DraftWorkspaceID, "GET", "/one", ""); got.Code != 200 {
		t.Fatalf("draft: %d %s", got.Code, got.Body)
	}
	if got := request(d.Design.PublishedWorkspaceID, "GET", "/one", ""); got.Code != 404 {
		t.Fatalf("unpublished: %d %s", got.Code, got.Body)
	}
	review, err := repo.RequestReview(ctx, d.Design.ID, 1, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Publish(ctx, d.Design.ID, review.ID, 1, "ui"); err != nil {
		t.Fatal(err)
	}
	if got := request(d.Design.PublishedWorkspaceID, "GET", "/one", ""); got.Code != 200 {
		t.Fatalf("published: %d %s", got.Code, got.Body)
	}
	_, err = repo.Save(ctx, d.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: strings.ReplaceAll(doc, "one", "two"), Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if got := request(d.Design.DraftWorkspaceID, "GET", "/two", ""); got.Code != 200 {
		t.Fatalf("new draft: %d %s", got.Code, got.Body)
	}
	if got := request(d.Design.PublishedWorkspaceID, "GET", "/one", ""); got.Code != 200 {
		t.Fatalf("stable published: %d %s", got.Code, got.Body)
	}
	if got := request(d.Design.PublishedWorkspaceID, "GET", "/two", ""); got.Code != 404 {
		t.Fatalf("draft leaked: %d %s", got.Code, got.Body)
	}
	for _, id := range []int64{d.Design.DraftWorkspaceID, d.Design.PublishedWorkspaceID} {
		for _, method := range []string{"POST", "DELETE"} {
			for _, body := range []string{`{"target":"*","action":"status","value":503}`, `{"scenario":"hidden"}`} {
				got := request(id, method, "/__mocker/state", body)
				if got.Code != 409 || !strings.Contains(got.Body.String(), "managed_workspace") {
					t.Fatalf("%s: %d %s", method, got.Code, got.Body)
				}
			}
		}
	}
}
