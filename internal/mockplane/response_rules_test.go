package mockplane

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/workspaces"
)

// The same-status leaves make choosing a status instead of traversing the
// copied graph visibly wrong, including lexical JSON numbers on the wire.
func executionTestRule(method string) responserules.Rule {
	return responserules.Rule{
		ID: "branch", Name: "Branch", Binding: &responserules.Binding{Method: method, Path: "/widgets"},
		Nodes: []responserules.Node{
			{ID: "start", Type: "start", Name: "Start"},
			{ID: "condition", Type: "condition", Name: "Condition", Condition: &overrides.Condition{In: "header", Name: "X-Branch", Op: "equals", Value: "first"}},
			{ID: "yes", Type: "response", Name: "First", Response: &responserules.Response{Status: 200, MediaType: "application/json", Headers: []responserules.Field{{Name: "X-Rule", Value: "first"}}, BodyJSON: new(`{ "number": 1.0, "large": 9007199254740993 }`)}},
			{ID: "no", Type: "response", Name: "Second", Response: &responserules.Response{Status: 200, MediaType: "application/json", Headers: []responserules.Field{}, BodyJSON: new(`{"number":1e0}`)}},
		},
		Edges: []responserules.Edge{
			{ID: "start-condition", From: "start", Port: "next", To: "condition"},
			{ID: "condition-yes", From: "condition", Port: "true", To: "yes"},
			{ID: "condition-no", From: "condition", Port: "false", To: "no"},
		},
	}
}

func executionTestSource(t *testing.T, rule responserules.Rule) *fakeRuntimeSource {
	t.Helper()
	var root map[string]any
	if err := jsonx.Unmarshal([]byte(widgetsDoc), &root); err != nil {
		t.Fatal(err)
	}
	item := root["paths"].(map[string]any)["/widgets"].(map[string]any)
	operation := item["get"]
	delete(item, "get")
	item[strings.ToLower(rule.Binding.Method)] = operation
	root["x-mocker-response-rules-execution"] = responserules.Envelope{FormatVersion: 1, Rules: []responserules.Rule{rule}}
	data, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	route := widgetsRoute()
	route.Method = rule.Binding.Method
	variant := widgetsVariant()
	variant.SchemaPtr = strings.ReplaceAll(variant.SchemaPtr, "/get/", "/"+strings.ToLower(rule.Binding.Method)+"/")
	return &fakeRuntimeSource{normalized: data, routes: []router.Route{route}, variants: map[int64][]gen.ResponseVariant{widgetsOpRowID: {variant}}}
}

func executionTestPlane(t *testing.T, rule responserules.Rule) (*Plane, *fakeTrafficSink, *workspaces.Workspace) {
	t.Helper()
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	sink := &fakeTrafficSink{}
	return trafficPlane(t, 1<<20, executionTestSource(t, rule), sink, ws), sink, ws
}

func executionRequest(t *testing.T, p *Plane, sink *fakeTrafficSink, req *http.Request) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	events := sink.all()
	if len(events) == 0 {
		t.Fatal("request produced no traffic event")
	}
	return rec, events[len(events)-1].Notes
}

func TestResponseRuleExecution_BranchesPreserveBytesAndSkipEnvelope(t *testing.T) {
	p, sink, ws := executionTestPlane(t, executionTestRule("GET"))
	ws.Settings.Envelope = new("wrapped")
	for _, tc := range []struct{ header, body string }{
		{"first", `{ "number": 1.0, "large": 9007199254740993 }`},
		{"second", `{"number":1e0}`},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
		req.Header.Set("X-Branch", tc.header)
		req.Header.Set("Origin", "http://client.local")
		rec, notes := executionRequest(t, p, sink, req)
		if rec.Code != 200 || rec.Body.String() != tc.body {
			t.Fatalf("response = %d %s, want exact %s", rec.Code, rec.Body, tc.body)
		}
		if rec.Header().Get("Content-Length") != fmt.Sprint(len(tc.body)) || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("missing transport headers: %v", rec.Header())
		}
		if rec.Header().Get("Access-Control-Allow-Origin") == "" || !strings.Contains(notes, "response_rule_response") {
			t.Fatalf("missing CORS or outcome note: headers=%v notes=%q", rec.Header(), notes)
		}
	}
}

func TestResponseRuleExecution_BindsInheritedAlias(t *testing.T) {
	rule := executionTestRule("GET")
	rule.Binding.Path = "/one"
	source := executionTestSource(t, rule)
	var root map[string]any
	if err := jsonx.Unmarshal(source.normalized, &root); err != nil {
		t.Fatal(err)
	}
	paths := root["paths"].(map[string]any)
	shared := paths["/widgets"]
	delete(paths, "/widgets")
	paths["/one"] = map[string]any{"$ref": "#/components/pathItems/Shared"}
	paths["/two"] = map[string]any{"$ref": "#/components/pathItems/Shared"}
	root["components"] = map[string]any{"pathItems": map[string]any{"Shared": shared}}
	data, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	source.normalized = data
	first, second := widgetsRoute(), widgetsRoute()
	first.Path, first.CanonicalPath = "/one", "/one"
	second.Path, second.CanonicalPath, second.OpRowID = "/two", "/two", 2
	source.routes = []router.Route{first, second}
	variant := widgetsVariant()
	variant.OpPointer = "#/components/pathItems/Shared/get"
	variant.SchemaPtr = "#/components/pathItems/Shared/get/responses/200/content/application~1json/schema"
	other := variant
	other.OpRowID = 2
	source.variants = map[int64][]gen.ResponseVariant{1: {variant}, 2: {other}}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	sink := &fakeTrafficSink{}
	p := trafficPlane(t, 1<<20, source, sink, ws)
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/one", nil)
	req.Header.Set("X-Branch", "first")
	rec, _ := executionRequest(t, p, sink, req)
	if rec.Code != 200 || rec.Header().Get("X-Rule") != "first" || rec.Body.String() != `{ "number": 1.0, "large": 9007199254740993 }` {
		t.Fatalf("inherited response rule = %d %q headers=%v", rec.Code, rec.Body.String(), rec.Header())
	}
}

func TestResponseRuleExecution_AcceptHEADAndBodylessStatuses(t *testing.T) {
	for _, tc := range []struct {
		name, method, accept string
		status, wantStatus   int
		body                 *string
	}{
		{"reject unacceptable type", "GET", "text/plain", 200, 406, new(`true`)},
		{"HEAD routes through GET rule", "HEAD", "application/json", 200, 200, new(`true`)},
		{"204", "GET", "text/plain", 204, 204, nil},
		{"205", "GET", "text/plain", 205, 205, nil},
		{"304", "GET", "text/plain", 304, 304, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := executionTestRule("GET")
			rule.Nodes[3].Response.Status, rule.Nodes[3].Response.BodyJSON = tc.status, tc.body
			p, sink, _ := executionTestPlane(t, rule)
			req := httptest.NewRequest(tc.method, "http://alex.mock.local/widgets", nil)
			req.Header.Set("Accept", tc.accept)
			rec, _ := executionRequest(t, p, sink, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body, tc.wantStatus)
			}
			if tc.wantStatus != 406 && rec.Body.Len() != 0 {
				t.Fatalf("bodyless response wrote %s", rec.Body)
			}
		})
	}
}

func TestResponseRuleExecution_MaxResponseUsesLiveLimit(t *testing.T) {
	p, sink, _ := executionTestPlane(t, executionTestRule("GET"))
	p.cfg.MaxResponse = 2
	rec, notes := executionRequest(t, p, sink, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil))
	if rec.Code != 500 || !strings.Contains(notes, "response_rule_too_large") {
		t.Fatalf("response = %d %s notes=%q, want bounded refusal", rec.Code, rec.Body, notes)
	}
}

func TestResponseRuleExecution_OverridesAndScenarioMaskSpecGraph(t *testing.T) {
	for _, tc := range []struct {
		name       string
		row        *overrides.Row
		scenario   *bundle.OverrideEntry
		wantRule   bool
		wantStatus int
	}{
		{"active status", wsRow("GET", "/widgets", true, 418), nil, false, 418},
		{"disabled", wsRow("GET", "/widgets", false, 418), nil, true, 200},
		{"delay-only row", &overrides.Row{Method: "GET", Path: "/widgets", OverrideOn: true, DelayMs: new(0)}, nil, false, 200},
		{"scenario disabled exposes spec", wsRow("GET", "/widgets", true, 418), new(entry("GET", "/widgets", false, 503)), true, 200},
		{"scenario active masks spec", wsRow("GET", "/widgets", false, 418), new(entry("GET", "/widgets", true, 503)), false, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, sink, ws := executionTestPlane(t, executionTestRule("GET"))
			p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{overrides.OpKey("GET", "/widgets"): tc.row}})
			if tc.scenario != nil {
				ws.ScenarioID = new(int64(42))
				p.SetScenarios(&fakeScenarioSource{byID: map[int64]*scenarios.Scenario{42: {Bundle: snapshotOf(ws.Settings, *tc.scenario)}}})
			}
			rec, notes := executionRequest(t, p, sink, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil))
			if rec.Code != tc.wantStatus || (rec.Body.String() == `{"number":1e0}`) != tc.wantRule {
				t.Fatalf("response = %d %s; want status=%d rule=%v", rec.Code, rec.Body, tc.wantStatus, tc.wantRule)
			}
			if !tc.wantRule && !strings.Contains(notes, "response_rule_shadowed_override") {
				t.Fatalf("missing shadow reason: %q", notes)
			}
		})
	}
}

func TestResponseRuleExecution_FailNextConsumedOnceAndRouteOffConsumesNothing(t *testing.T) {
	p, sink, ws := executionTestPlane(t, executionTestRule("GET"))
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{Target: livestate.Target{Method: "GET", Path: "/widgets"}, Action: livestate.ActionFail, Status: 503, Once: true}); err != nil {
		t.Fatal(err)
	}
	p.SetLiveState(store)
	row := &overrides.Row{Method: "GET", Path: "/widgets", OverrideOn: true, RouteOff: true}
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{overrides.OpKey("GET", "/widgets"): row}})
	rec, routeOffNotes := executionRequest(t, p, sink, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil))
	if rec.Code != 404 {
		t.Fatalf("routeOff = %d", rec.Code)
	}
	if !strings.Contains(routeOffNotes, "response_rule_shadowed_route_off") {
		t.Fatalf("missing routeOff shadow reason: %q", routeOffNotes)
	}
	row.OverrideOn = false
	ws.Revision++
	rec, notes := executionRequest(t, p, sink, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil))
	if rec.Code != 503 || !strings.Contains(notes, "response_rule_shadowed_session") {
		t.Fatalf("armed fail = %d notes=%q", rec.Code, notes)
	}
	rec, _ = executionRequest(t, p, sink, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil))
	if rec.Body.String() != `{"number":1e0}` {
		t.Fatalf("next request missed graph: %d %s", rec.Code, rec.Body)
	}
}

func TestResponseRuleExecution_LiveInputNumbersAndCapturePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, method, contentType, body string
		wantTrue, rejected              bool
	}{
		{"large exact", "POST", "application/json", `{"value":9007199254740993}`, true, false},
		{"rounded different", "POST", "application/json", `{"value":9007199254740992}`, false, false},
		{"text plain", "POST", "text/plain", `{"value":9007199254740993}`, true, false},
		{"absent type", "POST", "", `{"value":9007199254740993}`, true, false},
		{"unsupported type", "POST", "application/octet-stream", `{"value":9007199254740993}`, false, false},
		{"multipart", "POST", "multipart/form-data; boundary=test", `{"value":9007199254740993}`, false, false},
		{"GET body ignored", "GET", "application/json", `{"value":9007199254740993}`, false, false},
		{"DELETE body ignored", "DELETE", "application/json", `{"value":9007199254740993}`, false, false},
		{"duplicate", "POST", "application/json", `{"value":0,"value":9007199254740993}`, false, true},
		{"malformed", "POST", "application/json", `{`, false, true},
		{"oversized", "POST", "application/json", `{"value":"` + strings.Repeat("x", 65536) + `"}`, false, true},
		{"too deep", "POST", "application/json", strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := executionTestRule(tc.method)
			rule.Nodes[1].Condition = &overrides.Condition{In: "body", Name: "value", Op: "equals", Value: "9007199254740993"}
			p, sink, _ := executionTestPlane(t, rule)
			req := httptest.NewRequest(tc.method, "http://alex.mock.local/widgets", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			rec, notes := executionRequest(t, p, sink, req)
			want := `{"number":1e0}`
			if tc.wantTrue {
				want = `{ "number": 1.0, "large": 9007199254740993 }`
			}
			if rec.Code != 200 || rec.Body.String() != want {
				t.Fatalf("response = %d %s, want %s", rec.Code, rec.Body, want)
			}
			if strings.Contains(notes, "response_rule_body_rejected") != tc.rejected {
				t.Fatalf("notes=%q rejected=%v", notes, tc.rejected)
			}
		})
	}
}

func TestResponseRuleExecution_HTTPInputsDoNotUseFixtureLimits(t *testing.T) {
	rule := executionTestRule("GET")
	rule.Nodes[1].Condition = &overrides.Condition{In: "header", Name: "X-Branch", Op: "contains", Value: "first"}
	p, sink, _ := executionTestPlane(t, rule)
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	for i := range 110 {
		req.Header.Add(fmt.Sprintf("X-Extra-%d", i), strings.Repeat("x", 5000))
	}
	req.Header.Add("X-Branch", "first\tvalue")
	req.Header.Add("X-Branch", "second")
	rec, _ := executionRequest(t, p, sink, req)
	if rec.Header().Get("X-Rule") != "first" {
		t.Fatalf("HTTP fixture constraints changed selection: %d %s", rec.Code, rec.Body)
	}
}

func TestResponseRuleExecution_DelayAndFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			rule := executionTestRule("GET")
			if fallback {
				rule.Nodes[3] = responserules.Node{ID: "no", Type: "fallback", Name: "Fallback"}
			}
			rule.Nodes = append(rule.Nodes, responserules.Node{ID: "delay", Type: "delay", Name: "Delay", DelayMs: new(40)})
			rule.Edges[2].To = "delay"
			rule.Edges = append(rule.Edges, responserules.Edge{ID: "delay-no", From: "delay", Port: "next", To: "no"})
			p, sink, ws := executionTestPlane(t, rule)
			ws.Settings.DelayMs = 40
			started := time.Now()
			rec, notes := executionRequest(t, p, sink, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil))
			if elapsed := time.Since(started); elapsed < 75*time.Millisecond {
				t.Fatalf("combined delay missing: %v", elapsed)
			}
			if rec.Code != 200 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if fallback && (!strings.Contains(notes, "response_rule_fallback") || !strings.HasPrefix(rec.Body.String(), "[")) {
				t.Fatalf("fallback response=%s notes=%q", rec.Body, notes)
			}
			store := livestate.NewStore(0, nil)
			if err := store.Set(ws.ID, livestate.Directive{Target: livestate.Target{Method: "GET", Path: "/widgets"}, Action: livestate.ActionDelay, Ms: 1}); err != nil {
				t.Fatal(err)
			}
			p.SetLiveState(store)
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Millisecond)
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, "GET", "http://alex.mock.local/widgets", nil)
			rec, _ = executionRequest(t, p, sink, req)
			if rec.Body.Len() == 0 {
				t.Fatal("session delay did not replace combined graph/workspace delay")
			}
		})
	}
}

func TestResponseRuleExecution_CanceledGraphDelayWritesNothing(t *testing.T) {
	rule := executionTestRule("GET")
	rule.Nodes = append(rule.Nodes, responserules.Node{ID: "delay", Type: "delay", Name: "Delay", DelayMs: new(1000)})
	rule.Edges[2].To = "delay"
	rule.Edges = append(rule.Edges, responserules.Edge{ID: "delay-no", From: "delay", Port: "next", To: "no"})
	p, sink, _ := executionTestPlane(t, rule)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Millisecond)
	defer cancel()
	rec, _ := executionRequest(t, p, sink, httptest.NewRequestWithContext(ctx, "GET", "http://alex.mock.local/widgets", nil))
	if rec.Body.Len() != 0 || rec.Flushed {
		t.Fatalf("canceled delay wrote response %s", rec.Body)
	}
}
