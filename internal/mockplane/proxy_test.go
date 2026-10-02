package mockplane_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/workspaces"
)

type proxyStub struct{ calls int }

func (s *proxyStub) ServeProxy(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, operation string, auth bool, capture func([]byte) (int, error)) bool {
	s.calls++
	w.WriteHeader(http.StatusCreated)
	return true
}
func TestProxyDispatchExcludesControlsPreflightAndSnapshots(t *testing.T) {
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()}
	p := newPlane(ws)
	proxy := &proxyStub{}
	p.SetProxy(proxy)
	for _, path := range []string{"/users/1", "/__mocker/health"} {
		w := httptest.NewRecorder()
		p.ServeSlug(w, httptest.NewRequest(http.MethodGet, path, nil), "alex")
		want := 201
		if path == "/__mocker/health" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodOptions, "/users", nil)
	r.Header.Set("Origin", "http://client")
	r.Header.Set("Access-Control-Request-Method", "GET")
	p.ServeSlug(httptest.NewRecorder(), r, "alex")
	p.ServeWorkspace(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users", nil), ws)
	if proxy.calls != 1 {
		t.Fatalf("proxy called %d times", proxy.calls)
	}
}
