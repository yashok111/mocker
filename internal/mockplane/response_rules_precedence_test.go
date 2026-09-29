package mockplane

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

type executionCustomSource []*customep.Row

func (s executionCustomSource) ForWorkspace(context.Context, int64) ([]*customep.Row, error) {
	return s, nil
}

func TestResponseRuleExecution_CustomRouteKeepsPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		on, off    bool
		wantStatus int
		wantBody   string
	}{
		{"active custom", true, false, 202, `{"custom":true}`},
		{"disabled custom exposes spec", false, false, 200, `{"number":1e0}`},
		{"routeOff custom does not expose spec", true, true, 404, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, sink, _ := executionTestPlane(t, executionTestRule("GET"))
			p.SetCustomEndpoints(executionCustomSource{&customep.Row{
				ID: 44, Method: "GET", Path: "/widgets", CanonicalPath: "/widgets", OverrideOn: tc.on, RouteOff: tc.off,
				ActiveStatus: 202, Responses: map[string]overrides.Variant{"202": {Mode: "pinned", MediaType: "application/json", Body: jsonx.RawMessage(`{"custom":true}`)}},
			}})
			rec, notes := executionRequest(t, p, sink, httptest.NewRequest("GET", "http://alex.mock.local/widgets", nil))
			if rec.Code != tc.wantStatus || tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Fatalf("response = %d %s, want %d %s", rec.Code, rec.Body, tc.wantStatus, tc.wantBody)
			}
			if tc.on && strings.Contains(notes, "response_rule") {
				t.Fatalf("custom route evaluated spec graph: %q", notes)
			}
		})
	}
}

func TestResponseRuleExecution_StaticPostSkipsResourceWriteButFallbackPreservesIt(t *testing.T) {
	for _, tc := range []struct {
		name, accept   string
		fallback       bool
		writes, status int
	}{
		{"static", "", false, 0, 200},
		{"static unacceptable", "text/plain", false, 0, 406},
		{"fallback", "", true, 1, 200},
		{"fallback unacceptable", "text/plain", true, 0, 406},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := executionTestRule("POST")
			if tc.fallback {
				rule.Nodes[3] = responserules.Node{ID: "no", Type: "fallback", Name: "Fallback"}
			}
			p, sink, _ := executionTestPlane(t, rule)
			res := itemsResource(new("bare"), "id", specs.Wrapper{})
			res.RouteFamily = "/widgets"
			p.SetResources(previewFakeResourceSource{res: res})
			store := &fakeEntityStore{createFn: func(_ context.Context, id int64, _, _ resources.ScopeKey, field, _ string, data map[string]any) (resources.Entity, error) {
				if id != 42 || field != "id" || data["name"] != "request" {
					t.Fatalf("wrong entity write: id=%d field=%q data=%v", id, field, data)
				}
				return resources.Entity{Data: jsonx.RawMessage(`{"id":17,"stored":true}`)}, nil
			}}
			p.SetEntities(store)
			req := httptest.NewRequest("POST", "http://alex.mock.local/widgets", strings.NewReader(`{"name":"request"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", tc.accept)
			rec, _ := executionRequest(t, p, sink, req)
			if rec.Code != tc.status || store.createCalls != tc.writes {
				t.Fatalf("status=%d writes=%d body=%s", rec.Code, store.createCalls, rec.Body)
			}
			if tc.writes == 1 && rec.Body.String() != `{"id":17,"stored":true}` {
				t.Fatalf("fallback lost store output: %s", rec.Body)
			}
		})
	}
}

func TestResponseRuleExecution_ActiveFunctionMasksGraph(t *testing.T) {
	p, sink, _ := executionTestPlane(t, executionTestRule("GET"))
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{overrides.OpKey("GET", "/widgets"): functionRow("/widgets", `return 299, {from_function = true}`)}})
	rec, notes := executionRequest(t, p, sink, httptest.NewRequest("GET", "http://alex.mock.local/widgets", nil))
	if rec.Code != 299 || !strings.Contains(rec.Body.String(), "from_function") || !strings.Contains(notes, "response_rule_shadowed_override") || !strings.Contains(notes, noteFunction) {
		t.Fatalf("function layer changed: %d %s notes=%q", rec.Code, rec.Body, notes)
	}
}

func TestResponseRuleExecution_PauseCancellationPrecedesResponseAndDelay(t *testing.T) {
	p, sink, ws := executionTestPlane(t, executionTestRule("GET"))
	store := livestate.NewStore(0, nil)
	target := livestate.Target{Method: "GET", Path: "/widgets"}
	if err := store.Set(ws.ID, livestate.Directive{Target: target, Action: livestate.ActionPause}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ws.ID, livestate.Directive{Target: target, Action: livestate.ActionFail, Status: 503, N: 2}); err != nil {
		t.Fatal(err)
	}
	p.SetLiveState(store)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Millisecond)
	defer cancel()
	rec, _ := executionRequest(t, p, sink, httptest.NewRequestWithContext(ctx, "GET", "http://alex.mock.local/widgets", nil))
	if rec.Body.Len() != 0 {
		t.Fatalf("paused request wrote %s", rec.Body)
	}
	if _, err := store.Delete(ws.ID, target, livestate.ActionPause); err != nil {
		t.Fatal(err)
	}
	rec, _ = executionRequest(t, p, sink, httptest.NewRequest("GET", "http://alex.mock.local/widgets", nil))
	if rec.Code != 503 {
		t.Fatalf("pause consumed fail counter twice: %d %s", rec.Code, rec.Body)
	}
	rec, _ = executionRequest(t, p, sink, httptest.NewRequest("GET", "http://alex.mock.local/widgets", nil))
	if rec.Body.String() != `{"number":1e0}` {
		t.Fatalf("fail counter not exhausted: %d %s", rec.Code, rec.Body)
	}
}

func TestResponseRuleExecution_RevisionCacheAndBasePath(t *testing.T) {
	p, sink, ws := executionTestPlane(t, executionTestRule("GET"))
	ws.Settings.BasePath = "/api/{tenant}"
	source := p.specs.(*fakeRuntimeSource)
	req := func() *http.Request {
		return httptest.NewRequest("GET", "http://alex.mock.local/api/acme/widgets", nil)
	}
	rec, _ := executionRequest(t, p, sink, req())
	if rec.Body.String() != `{"number":1e0}` {
		t.Fatalf("base path lost operation binding: %s", rec.Body)
	}
	updated := executionTestRule("GET")
	updated.Nodes[3].Response.BodyJSON = new(`{"revision":2}`)
	source.normalized = executionTestSource(t, updated).normalized
	rec, _ = executionRequest(t, p, sink, req())
	if rec.Body.String() != `{"number":1e0}` || source.calls != 1 {
		t.Fatalf("cached runtime changed without revision: %s builds=%d", rec.Body, source.calls)
	}
	ws.Revision++
	rec, _ = executionRequest(t, p, sink, req())
	if rec.Body.String() != `{"revision":2}` || source.calls != 2 {
		t.Fatalf("revision did not refresh runtime: %s builds=%d", rec.Body, source.calls)
	}
}

func TestResponseRuleExecution_LexicalBodyAndRepeatedQueryParity(t *testing.T) {
	for _, lexical := range []string{"1", "1.0", "1e0"} {
		rule := executionTestRule("POST")
		rule.Nodes[1].Condition = &overrides.Condition{In: "body", Name: "number", Op: "equals", Value: lexical}
		p, sink, _ := executionTestPlane(t, rule)
		for _, sent := range []string{"1", "1.0", "1e0"} {
			req := httptest.NewRequest("POST", "http://alex.mock.local/widgets", strings.NewReader(`{"number":`+sent+`}`))
			req.Header.Set("Content-Type", "application/json")
			rec, _ := executionRequest(t, p, sink, req)
			if (rec.Header().Get("X-Rule") == "first") != (lexical == sent) {
				t.Fatalf("lexical=%s sent=%s response=%s", lexical, sent, rec.Body)
			}
		}
	}
	rule := executionTestRule("GET")
	rule.Nodes[1].Condition = &overrides.Condition{In: "query", Name: "tag", Op: "equals", Value: "last"}
	p, sink, _ := executionTestPlane(t, rule)
	rec, _ := executionRequest(t, p, sink, httptest.NewRequest("GET", "http://alex.mock.local/widgets?tag=first&tag=last", nil))
	if rec.Header().Get("X-Rule") != "first" {
		t.Fatalf("repeated query did not match any value: %s", rec.Body)
	}
}

func TestResponseRuleExecution_DelayClampsWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		session, settings, graph, want int
	}{
		{"sum", 0, 17, 29, 46},
		{"sum capped", 0, 20000, 20000, 30000},
		{"session replacement", 5, 20000, 20000, 5},
		{"defensive overflow", 0, math.MaxInt, 10, 30000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := responseRuleDelayMs(tc.session, nil, tc.settings, &responserules.Simulation{TotalDelayMs: new(tc.graph)})
			if got != tc.want {
				t.Fatalf("delay=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestResponseRuleExecution_ServeBoundaryRefusesUnsafeHeadersAndMedia(t *testing.T) {
	for _, tc := range []struct {
		name, media string
		headers     []responserules.Field
	}{
		{"browser type", "text/html", []responserules.Field{}},
		{"managed header", "application/json", []responserules.Field{{Name: "Connection", Value: "close"}}},
		{"header control", "application/json", []responserules.Field{{Name: "X-Test", Value: "x\r\ny"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, ws := executionTestPlane(t, executionTestRule("GET"))
			rec := httptest.NewRecorder()
			p.writeResponseRule(rec, httptest.NewRequest("GET", "http://alex.mock.local/widgets", nil), ws, &router.Route{Method: "GET", Path: "/widgets"}, responserules.Response{Status: 200, MediaType: tc.media, Headers: tc.headers, BodyJSON: new(`{"private":true}`)})
			if rec.Code != 500 || strings.Contains(rec.Body.String(), "private") || rec.Header().Get("Connection") != "" {
				t.Fatalf("unsafe response escaped: %d %s %v", rec.Code, rec.Body, rec.Header())
			}
		})
	}
}

func TestResponseRuleExecution_TruncatedCaptureHasBoundedNote(t *testing.T) {
	rule := executionTestRule("POST")
	rule.Nodes[1].Condition = &overrides.Condition{In: "body", Name: "value", Op: "exists"}
	p, sink, _ := executionTestPlane(t, rule)
	p.cfg.TrafficMaxBody = 1
	req := httptest.NewRequest("POST", "http://alex.mock.local/widgets", strings.NewReader(fmt.Sprintf(`{"value":"%s"}`, strings.Repeat("sensitive", 9000))))
	req.Header.Set("Content-Type", "application/json")
	rec, notes := executionRequest(t, p, sink, req)
	if rec.Body.String() != `{"number":1e0}` || !strings.Contains(notes, "response_rule_body_rejected") || strings.Contains(notes, "sensitive") {
		t.Fatalf("truncated input behavior: %s notes=%q", rec.Body, notes)
	}
}
