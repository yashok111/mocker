package mockplane

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/recordproxy"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// proxyTemplatePlane is workspace "alex" (spec rebindDocA: /widgets and
// /widgets/{id} only) whose proxy runs in the given mode against an upstream
// that answers every request with {"n":1}, with the given per-operation
// policies and patches.
// Each patch stamps which key it came from, so a response says which key
// the request resolved to.
func proxyTemplatePlane(t *testing.T, mode string, operations map[string]string, patchKeys ...string) *Plane {
	t.Helper()
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"n":1}`))
	}))
	t.Cleanup(upstream.Close)
	cfg.ProxyAllowlist = []string{upstream.URL}
	sr := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, sr, "proxy templates", rebindDocA)
	id := resourceIntegrationWorkspace(t, db, "alex", specID, domain.Settings{Seed: 1})
	pr := recordproxy.NewRepo(db)
	c := recordproxy.DefaultConfig()
	c.Mode = mode
	c.Upstream = upstream.URL
	c.Operations = operations
	for _, key := range patchKeys {
		c.OperationPatches[key] = "body.key = " + luaString(key) + "\nreturn body"
	}
	if err := c.Validate(cfg.ProxyAllowlist); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	p := New(cfg, workspaces.NewRepo(db), sr, runtimeTestLogger())
	p.SetProxy(recordproxy.NewService(pr, cfg))
	return p
}

func luaString(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// patchedBy proxies one request and returns the key whose patch ran, or ""
// when none did.
func patchedBy(t *testing.T, p *Plane, method, path string) string {
	t.Helper()
	w := entityRequest(p, t.Context(), method, path, "", "")
	if w.Code != http.StatusOK || w.Header().Get("X-Mocker-Proxy-Mode") != "record" {
		t.Fatalf("%s %s: %d %s %v", method, path, w.Code, w.Body, w.Header())
	}
	if w.Header().Get("X-Mocker-Patch") != "applied" {
		return ""
	}
	_, key, _ := strings.Cut(w.Body.String(), `"key":"`)
	key, _, _ = strings.Cut(key, `"`)
	return key
}

func TestProxyPatchTemplateKeyMatchesAnUnmatchedPath(t *testing.T) {
	const budget = "POST /api/v1/budgeting-campaigns/{budgetingCampaignId}/{budgetingUnitId}/budget"
	p := proxyTemplatePlane(t, "record", nil, budget, "GET /api/literal/7", "GET /{any}", "GET /files/{}", "GET /files/name.{ext}")
	if got := patchedBy(t, p, "GET", "/solo"); got != "GET /{any}" {
		t.Fatalf("one-segment template: patched by %q", got)
	}
	if got := patchedBy(t, p, "GET", "/files/name.{ext}"); got != "GET /files/name.{ext}" {
		t.Fatalf("a brace pair inside a segment is literal: patched by %q", got)
	}
	if got := patchedBy(t, p, "POST", "/api/v1/budgeting-campaigns/7/24/budget"); got != budget {
		t.Fatalf("template key on an unmatched path: patched by %q", got)
	}
	if got := patchedBy(t, p, "GET", "/api/literal/7"); got != "GET /api/literal/7" {
		t.Fatalf("literal key: patched by %q", got)
	}
	for _, miss := range [][2]string{
		{"POST", "/api/v1/budgeting-campaigns/7/budget"},         // one segment short
		{"POST", "/api/v1/budgeting-campaigns/7/24/25/budget"},   // one segment over
		{"POST", "/api/v1/budgeting-campaigns/7/24/allocations"}, // literal differs
		{"PUT", "/api/v1/budgeting-campaigns/7/24/budget"},       // method differs
		{"GET", "/api/literal/8"},
		{"GET", "/"},              // "{any}" needs a non-empty segment
		{"GET", "/files/x"},       // "{}" names nothing: a literal no request has
		{"GET", "/files/name.js"}, // "name.{ext}" is literal, not a template
	} {
		if got := patchedBy(t, p, miss[0], miss[1]); got != "" {
			t.Fatalf("%s %s: patched by %q, want no patch", miss[0], miss[1], got)
		}
	}
}

func TestProxyPatchTemplateChoiceIsDeterministic(t *testing.T) {
	// "GET /a" matches nothing and "GET /api/{0}/{b}" has fewer literals
	// than the winner on /api/y/7: both sort BEFORE the keys that should
	// win, so a choice that let either replace a better match shows up.
	p := proxyTemplatePlane(t, "record", nil, "GET /a", "GET /api/{a}/7", "GET /api/{c}/{d}", "GET /api/{a}/{b}", "GET /api/{0}/{b}", "GET /api/x/7")
	for path, want := range map[string]string{
		"/api/x/7": "GET /api/x/7",     // the exact key wins outright
		"/api/y/7": "GET /api/{a}/7",   // most literal segments wins
		"/api/q/r": "GET /api/{0}/{b}", // a tie goes to the smaller key
	} {
		// The keys come out of a map, whose order differs per walk: repeat
		// so every relative order is visited with near certainty.
		for range 20 {
			if got := patchedBy(t, p, "GET", path); got != want {
				t.Fatalf("GET %s: patched by %q, want %q", path, got, want)
			}
		}
	}
}

func TestProxyPatchTemplateKeyMatchesASpecRouteByShape(t *testing.T) {
	p := proxyTemplatePlane(t, "record", nil, "GET /widgets/{widgetId}")
	if got := patchedBy(t, p, "GET", "/widgets/1"); got != "GET /widgets/{widgetId}" {
		t.Fatalf("spec route under a renamed parameter: patched by %q", got)
	}
}

func TestProxyOperationPolicyTemplateKeyMatchesAnUnmatchedPath(t *testing.T) {
	p := proxyTemplatePlane(t, "record", map[string]string{"GET /api/mocked/{id}": "mock"})
	w := entityRequest(p, t.Context(), "GET", "/api/mocked/5", "", "")
	if mode := w.Header().Get("X-Mocker-Proxy-Mode"); mode != "" {
		t.Fatalf("a mock policy keyed by a template still proxied (%s): %d %s", mode, w.Code, w.Body)
	}
	if got := patchedBy(t, p, "GET", "/api/mocked/5/extra"); got != "" {
		t.Fatalf("unrelated path patched by %q", got)
	}
}

func TestProxyOperationProxyPolicyTemplateKeyUnderModeOff(t *testing.T) {
	p := proxyTemplatePlane(t, "off", map[string]string{"GET /api/proxied/{id}": "proxy"})
	if w := entityRequest(p, t.Context(), "GET", "/api/proxied/5", "", ""); w.Header().Get("X-Mocker-Proxy-Mode") != "passthrough" {
		t.Fatalf("a proxy policy keyed by a template did not proxy: %d %s %v", w.Code, w.Body, w.Header())
	}
	if w := entityRequest(p, t.Context(), "GET", "/api/other/5", "", ""); w.Header().Get("X-Mocker-Proxy-Mode") != "" {
		t.Fatalf("mode off proxied an unrelated path: %v", w.Header())
	}
}
