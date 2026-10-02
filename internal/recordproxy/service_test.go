package recordproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestServiceRecordReplayOfflineAndRedaction(t *testing.T) {
	repo, id := fixture(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=secret")
		_, _ = w.Write([]byte(`{"id":9007199254740993,"password":"secret","name":"Alice"}`))
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
	ws := &workspaces.Workspace{ID: id, Slug: "proxy-test", Settings: domain.DefaultSettings()}
	call := func(path string, auth bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if !s.ServeProxy(w, r, ws, "GET /users/{id}", auth, nil) {
			t.Fatal("proxy not handled")
		}
		return w
	}
	live := call("/users/1", false)
	if live.Code != 200 || !strings.Contains(live.Body.String(), "secret") {
		t.Fatalf("live: %d %s", live.Code, live.Body)
	}
	list, err := repo.List(t.Context(), id)
	if err != nil || len(list) != 1 || strings.Contains(string(list[0].Body), "secret") || !strings.Contains(string(list[0].Body), "9007199254740993") {
		t.Fatalf("recording: %+v %v", list, err)
	}
	call("/login", true)
	list, _ = repo.List(t.Context(), id)
	if len(list) != 1 {
		t.Fatal("auth response recorded")
	}
	upstream.Close()
	c.Upstream += "/"
	c.Mode = "replay"
	if _, err = repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	w := call("/users/1", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "redacted") {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	if w := call("/users/2", false); w.Code != 404 {
		t.Fatalf("miss: %d %s", w.Code, w.Body)
	}
	if calls != 2 {
		t.Fatalf("upstream calls %d", calls)
	}
}
func TestServiceMockPolicyAndBodyLimit(t *testing.T) {
	repo, id := fixture(t)
	c := DefaultConfig()
	c.Mode = "passthrough"
	c.Upstream = "http://127.0.0.1:9"
	c.Operations = map[string]string{"GET /local": "mock"}
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{c.Upstream}, MaxBody: 8, MaxResponse: 100})
	ws := &workspaces.Workspace{ID: id}
	if s.ServeProxy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/local", nil), ws, "GET /local", false, nil) {
		t.Fatal("mock became proxy")
	}
	w := httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodPost, "/big", strings.NewReader("too large body")), ws, "POST /big", false, nil)
	if w.Code != 413 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestProxyRefusesOversizedPath(t *testing.T) {
	repo, id := fixture(t)
	c := DefaultConfig()
	c.Mode = "replay"
	c.Upstream = "http://api"
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{MaxBody: 1024, MaxResponse: 1024})
	w := httptest.NewRecorder()
	s.ServeProxy(w, httptest.NewRequest(http.MethodGet, "/"+strings.Repeat("x", 8192), nil), &workspaces.Workspace{ID: id}, "", false, nil)
	if w.Code != 414 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestProxyBodyLimitFromOuterMiddleware(t *testing.T) {
	repo, id := fixture(t)
	c := DefaultConfig()
	c.Mode = "replay"
	c.Upstream = "http://api"
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{MaxBody: 8, MaxResponse: 1024})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("too-large-body"))
	r.Body = http.MaxBytesReader(w, r.Body, 8)
	s.ServeProxy(w, r, &workspaces.Workspace{ID: id}, "", false, nil)
	if w.Code != 413 {
		t.Fatalf("middleware limit: %d %s", w.Code, w.Body)
	}
}
