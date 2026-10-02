package mockplane

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/recordproxy"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestProxyEntityCaptureCollectionAndDetail(t *testing.T) {
	p, repo, res, id := liveEntityPlane(t, []responserules.Rule{}, domain.Settings{Seed: 1, ListSize: 0})
	// The fixture's workspace slug is obtained by its id through the real source.
	source := p.src.(interface {
		ByID(context.Context, int64) (*workspaces.Workspace, error)
	})
	ws, err := source.ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	m, ok := rt.table.Match("GET", NormalizeSegments("/widgets"))
	if !ok {
		t.Fatal("missing route")
	}
	capture := p.proxyEntityCapture(request.Context(), request, ws, rt, m, NormalizeSegments("/widgets"))
	if capture == nil {
		t.Fatal("no capture")
	}
	n, err := capture([]byte(`[{"id":42,"name":"real"}]`))
	if err != nil || n != 1 {
		t.Fatalf("capture %d %v", n, err)
	}
	row, found, err := repo.Get(t.Context(), res.ID, "", "", "42")
	if err != nil || !found || !strings.Contains(string(row.Data), "real") {
		t.Fatalf("entity %+v %v", row, err)
	}
	n, err = capture([]byte(`[{"name":"missing-id"}]`))
	if err == nil || n != 0 {
		t.Fatal("accepted missing ID")
	}
}

func TestProxyRealRecordEntityCaptureAndReplayThroughPlane(t *testing.T) {
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	cfg.MaxBody = 1 << 20
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if len(body) != 70000 {
				t.Errorf("forwarded %d bytes", len(body))
			}
		}
		_, _ = w.Write([]byte(`[{"id":42,"name":"upstream","password":"secret"}]`))
	}))
	defer upstream.Close()
	cfg.ProxyAllowlist = []string{upstream.URL}
	sr := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, sr, "proxy entities", rebindDocA)
	id := resourceIntegrationWorkspace(t, db, "alex", specID, domain.Settings{Seed: 1, ListSize: 0})
	rr := resources.NewRepo(db, sr, cfg.MaxResponse, 64<<10, 1000)
	res, err := rr.Confirm(t.Context(), id, "/widgets")
	if err != nil {
		t.Fatal(err)
	}
	wsRepo := workspaces.NewRepo(db)
	p := New(cfg, wsRepo, sr, runtimeTestLogger())
	p.SetResources(rr)
	p.SetEntities(rr)
	pr := recordproxy.NewRepo(db)
	c := recordproxy.DefaultConfig()
	c.Mode = "record"
	c.Upstream = upstream.URL
	c.CaptureEntities = true
	c, err = pr.Save(t.Context(), id, c)
	if err != nil {
		t.Fatal(err)
	}
	p.SetProxy(recordproxy.NewService(pr, cfg))
	w := entityRequest(p, t.Context(), "GET", "/widgets", "", "")
	if w.Code != 200 || w.Header().Get("X-Mocker-Entities-Imported") != "1" {
		t.Fatalf("record: %d %s %+v", w.Code, w.Body, w.Header())
	}
	row, found, err := rr.Get(t.Context(), res.ID, "", "", "42")
	if err != nil || !found || strings.Contains(string(row.Data), "secret") {
		t.Fatalf("entity: %+v %v", row, err)
	}
	w = entityRequest(p, t.Context(), "POST", "/arbitrary", strings.Repeat("x", 70000), "")
	if w.Code != 200 {
		t.Fatalf("large forwarded body: %d %s", w.Code, w.Body)
	}
	upstream.Close()
	c.Mode = "replay"
	c, err = pr.Save(t.Context(), id, c)
	if err != nil {
		t.Fatal(err)
	}
	w = entityRequest(p, t.Context(), "GET", "/widgets", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "upstream") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	c.Operations = map[string]string{"GET /widgets": "mock"}
	if _, err = pr.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	w = entityRequest(p, t.Context(), "GET", "/widgets", "", "")
	if w.Code != 200 || w.Header().Get("X-Mocker-Proxy-Mode") != "" || !strings.Contains(w.Body.String(), "upstream") {
		t.Fatalf("mock entities after record: %d %s", w.Code, w.Body)
	}
}

type proxyOperationSpy struct{ operation string }

func (s *proxyOperationSpy) ServeProxy(w http.ResponseWriter, _ *http.Request, _ *workspaces.Workspace, operation string, _ bool, _ func([]byte) (int, error)) bool {
	s.operation = operation
	w.WriteHeader(http.StatusOK)
	return true
}
func TestProxyHeadUsesMatchedGetOperationPolicy(t *testing.T) {
	p, _, _, _ := liveEntityPlane(t, []responserules.Rule{}, domain.Settings{Seed: 1, ListSize: 0})
	spy := &proxyOperationSpy{}
	p.SetProxy(spy)
	entityRequest(p, t.Context(), "HEAD", "/widgets/1", "", "")
	if spy.operation != "GET /widgets/{id}" {
		t.Fatalf("HEAD selected %q policy", spy.operation)
	}
}
