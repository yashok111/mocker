package recordproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestPatch_passthroughServesThePatchedBody(t *testing.T) {
	repo, id := fixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Alice"}`))
	}))
	defer upstream.Close()
	c := DefaultConfig()
	c.Mode = "passthrough"
	c.Upstream = upstream.URL
	c.OperationPatches = map[string]string{"GET /users/{id}": `body.token = "p"; return body`}
	if err := c.Validate([]string{upstream.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{upstream.URL}, MaxBody: 1024, MaxResponse: 1024})
	w := httptest.NewRecorder()
	if !s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/users/1", nil), &workspaces.Workspace{ID: id}, "GET /users/{id}", false, nil) {
		t.Fatal("proxy not handled")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"token":"p"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("X-Mocker-Patch"); got != "applied" {
		t.Fatalf("X-Mocker-Patch = %q", got)
	}
}

func TestPatch_recordStoresTheOriginalAndCapturesTheServed(t *testing.T) {
	repo, id := fixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Alice"}`))
	}))
	defer upstream.Close()
	c := DefaultConfig()
	c.Mode = "record"
	c.Upstream = upstream.URL
	c.CaptureEntities = true
	c.OperationPatches = map[string]string{"GET /users/{id}": `body.served = true; return body`}
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{upstream.URL}, MaxBody: 1024, MaxResponse: 1024})
	var captured string
	w := httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/users/1", nil), &workspaces.Workspace{ID: id}, "GET /users/{id}", false, func(body []byte) (int, error) {
		captured = string(body)
		return 1, nil
	})
	if !strings.Contains(w.Body.String(), `"served":true`) {
		t.Fatalf("served body lost the patch: %s", w.Body)
	}
	list, err := repo.List(t.Context(), id)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if strings.Contains(list[0].BodyText, `"served"`) || !strings.Contains(list[0].BodyText, `"name":"Alice"`) {
		t.Fatalf("recording is not the original: %s", list[0].BodyText)
	}
	if !strings.Contains(captured, `"served":true`) {
		t.Fatalf("capture did not see the served body: %s", captured)
	}
}

func TestPatch_replayPatchesTheStoredOriginal(t *testing.T) {
	repo, id := fixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Alice"}`))
	}))
	defer upstream.Close()
	c := DefaultConfig()
	c.Mode = "record"
	c.Upstream = upstream.URL
	c, err := repo.Save(t.Context(), id, c)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{upstream.URL}, MaxBody: 1024, MaxResponse: 1024})
	ws := &workspaces.Workspace{ID: id}
	w := httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/users/1", nil), ws, "GET /users/{id}", false, nil)
	upstream.Close()

	c.Mode = "replay"
	c.OperationPatches = map[string]string{"GET /users/{id}": `body.replayed = true; return body`}
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/users/1", nil), ws, "GET /users/{id}", false, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"replayed":true`) || !strings.Contains(w.Body.String(), `"name":"Alice"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Mocker-Patch") != "applied" || w.Header().Get("X-Mocker-Recording") != "replayed" {
		t.Fatalf("headers: %v", w.Header())
	}
}

func TestPatch_aFailedPatchServesTheOriginalUnderAFailedMark(t *testing.T) {
	repo, id := fixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Alice"}`))
	}))
	defer upstream.Close()
	c := DefaultConfig()
	c.Mode = "passthrough"
	c.Upstream = upstream.URL
	c.OperationPatches = map[string]string{"GET /users/{id}": `error("boom")`}
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{upstream.URL}, MaxBody: 1024, MaxResponse: 1024})
	w := httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/users/1", nil), &workspaces.Workspace{ID: id}, "GET /users/{id}", false, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Alice"`) || strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Mocker-Patch") != "failed" {
		t.Fatalf("X-Mocker-Patch = %q", w.Header().Get("X-Mocker-Patch"))
	}
}

func TestPatch_aNonJSONBodyIsNeverPatched(t *testing.T) {
	repo, id := fixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain"))
	}))
	defer upstream.Close()
	c := DefaultConfig()
	c.Mode = "passthrough"
	c.Upstream = upstream.URL
	c.OperationPatches = map[string]string{"GET /text": `return "patched"`}
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{upstream.URL}, MaxBody: 1024, MaxResponse: 1024})
	w := httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/text", nil), &workspaces.Workspace{ID: id}, "GET /text", false, nil)
	if w.Body.String() != "plain" || w.Header().Get("X-Mocker-Patch") != "" {
		t.Fatalf("%d %s %q", w.Code, w.Body, w.Header().Get("X-Mocker-Patch"))
	}
}

func TestPatch_configValidationCompilesAtWrite(t *testing.T) {
	c := DefaultConfig()
	c.OperationPatches = map[string]string{"GET nope": `return body`}
	if err := c.Validate(nil); err == nil || !strings.Contains(err.Error(), "invalid operation key") {
		t.Fatalf("err = %v", err)
	}
	c.OperationPatches = map[string]string{"GET /x": `return body..(`}
	if err := c.Validate(nil); err == nil || !strings.Contains(err.Error(), "does not compile") {
		t.Fatalf("err = %v", err)
	}
	c.OperationPatches = map[string]string{"GET /x": `return body`}
	if err := c.Validate(nil); err != nil {
		t.Fatalf("err = %v", err)
	}
}
