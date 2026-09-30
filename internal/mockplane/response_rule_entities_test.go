package mockplane

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

func liveEntityRule(kind, method, path string) responserules.Rule {
	op := &responserules.EntityOperation{Family: "/widgets"}
	port := "next"
	status := 200
	switch kind {
	case "entity_read":
		op.Operation = "get"
		op.Key = &responserules.ValueRef{Source: "path", Name: "id"}
		port = "found"
	case "entity_create":
		op.Data = &responserules.ValueRef{Source: "body"}
		status = 201
	case "entity_update":
		op.Key = &responserules.ValueRef{Source: "path", Name: "id"}
		op.Data = &responserules.ValueRef{Source: "body"}
	}
	if kind == "entity_update" {
		port = "found"
	}
	rule := responserules.Rule{
		ID: kind, Name: kind, Binding: &responserules.Binding{Method: method, Path: path},
		Nodes: []responserules.Node{
			{ID: "start", Type: "start", Name: "Start"},
			{ID: "entity", Type: kind, Name: "Entity", Entity: op},
			{ID: "response", Type: "response", Name: "Response", Response: &responserules.Response{
				Status: status, MediaType: "application/json", Headers: []responserules.Field{}, BodyFrom: &responserules.ValueRef{Source: "result", NodeID: "entity"},
			}},
		},
		Edges: []responserules.Edge{
			{ID: "begin", From: "start", Port: "next", To: "entity"},
			{ID: "finish", From: "entity", Port: port, To: "response"},
		},
	}
	if port == "found" {
		rule.Nodes = append(rule.Nodes, responserules.Node{ID: "missing", Type: "response", Name: "Missing", Response: &responserules.Response{
			Status: 404, MediaType: "application/json", Headers: []responserules.Field{}, BodyJSON: new(`{"missing":true}`),
		}})
		rule.Edges = append(rule.Edges, responserules.Edge{ID: "absent", From: "entity", Port: "missing", To: "missing"})
	}
	return rule
}

type entityManagedSource struct {
	Source
	designID int64
	err      error
}

type entityResourceSource struct{ rows []*resources.Resource }

func (s entityResourceSource) ForWorkspace(context.Context, int64) ([]*resources.Resource, error) {
	return s.rows, nil
}

func (s entityManagedSource) ManagedDesign(context.Context, int64) (int64, error) {
	return s.designID, s.err
}

func TestEntityRulesManagedRuntimeDormantResources(t *testing.T) {
	for _, applied := range []bool{true, false} {
		t.Run(map[bool]string{true: "applied", false: "unapplied"}[applied], func(t *testing.T) {
			rules := []responserules.Rule{}
			if applied {
				rules = []responserules.Rule{liveEntityRule("entity_create", "POST", "/widgets")}
			}
			p, repo, res, wsID := liveEntityPlane(t, rules, domain.Settings{Seed: 1, ListSize: 1})
			ws, err := p.src.(*workspaces.Repo).ByID(t.Context(), wsID)
			if err != nil {
				t.Fatal(err)
			}
			for _, managed := range []bool{false, true} {
				p.src = entityManagedSource{Source: p.src, designID: map[bool]int64{false: 0, true: 1}[managed]}
				rt, err := p.buildRuntime(t.Context(), ws, nil)
				if err != nil {
					t.Fatal(err)
				}
				wantActive := !managed || applied
				if (rt.resources[res.RouteFamily] != nil) != wantActive {
					t.Fatalf("managed=%v applied=%v roster=%v", managed, applied, rt.resources)
				}
			}
			rows, err := repo.List(t.Context(), res.ID, "", "")
			if err != nil || len(rows) != 1 {
				t.Fatalf("runtime filtering changed stored rows: rows=%v err=%v", rows, err)
			}
			lookupErr := errors.New("membership lookup failed")
			p.src = entityManagedSource{Source: p.src, err: lookupErr}
			if _, err := p.buildRuntime(t.Context(), ws, nil); !errors.Is(err, lookupErr) {
				t.Fatalf("membership failure was ignored: %v", err)
			}
		})
	}
}

func TestEntityRulesManagedRuntimeIncludesAncestorClosure(t *testing.T) {
	rule := liveEntityRule("entity_create", "POST", "/widgets")
	rule.Nodes[1].Entity.Family = "/orgs/{}/teams/{}/widgets"
	p, _, _, wsID := liveEntityPlane(t, []responserules.Rule{rule}, domain.Settings{Seed: 1, ListSize: 1})
	ws, err := p.src.(*workspaces.Repo).ByID(t.Context(), wsID)
	if err != nil {
		t.Fatal(err)
	}
	p.src = entityManagedSource{Source: p.src, designID: 1}
	p.resources = entityResourceSource{rows: []*resources.Resource{
		{RouteFamily: "/orgs"}, {RouteFamily: "/orgs/{}/teams"},
		{RouteFamily: "/orgs/{}/teams/{}/widgets"}, {RouteFamily: "/widgets"},
	}}
	rt, err := p.buildRuntime(t.Context(), ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.resources) != 3 || rt.resources["/widgets"] != nil {
		t.Fatalf("ancestor roster=%v", rt.resources)
	}
	for _, family := range []string{"/orgs", "/orgs/{}/teams", "/orgs/{}/teams/{}/widgets"} {
		if rt.resources[family] == nil {
			t.Fatalf("missing ancestor family %s", family)
		}
	}
}

func liveEntityPlane(t *testing.T, rules []responserules.Rule, settings domain.Settings) (*Plane, *resources.Repo, *resources.Resource, int64) {
	t.Helper()
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	var root map[string]any
	if err := jsonx.Unmarshal([]byte(rebindDocA), &root); err != nil {
		t.Fatal(err)
	}
	paths := root["paths"].(map[string]any)
	detail := paths["/widgets/{id}"].(map[string]any)
	detail["patch"] = detail["get"]
	root[responserules.ExecutionExtension] = responserules.Envelope{FormatVersion: 1, Rules: rules}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	sr := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, sr, "entity rules", string(raw))
	wsID := resourceIntegrationWorkspace(t, db, "alex", specID, settings)
	rr := resources.NewRepo(db, sr, cfg.MaxResponse, 64<<10, 1000)
	res, err := rr.Confirm(t.Context(), wsID, "/widgets")
	if err != nil {
		t.Fatal(err)
	}
	p := New(cfg, workspaces.NewRepo(db), sr, runtimeTestLogger())
	p.SetResources(rr)
	p.SetEntities(rr)
	return p, rr, res, wsID
}

func entityRequest(p *Plane, ctx context.Context, method, path, body, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, method, "http://alex.mock.local"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func TestEntityRulesLiveCreateReadUpdatePreserveData(t *testing.T) {
	rules := []responserules.Rule{
		liveEntityRule("entity_create", "POST", "/widgets"),
		liveEntityRule("entity_read", "GET", "/widgets/{id}"),
		liveEntityRule("entity_update", "PATCH", "/widgets/{id}"),
	}
	p, repo, res, _ := liveEntityPlane(t, rules, domain.Settings{Seed: 1, ListSize: 1})
	created := entityRequest(p, t.Context(), "POST", "/widgets", `{"id":999,"large":9007199254740993,"fraction":0.10000000000000001,"huge":1e999,"zero":-0,"name":"created"}`, "application/json")
	if created.Code != 201 || !strings.Contains(created.Body.String(), `"id":2`) {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	updated := entityRequest(p, t.Context(), "PATCH", "/widgets/2", `{"id":100,"name":"updated"}`, "")
	if updated.Code != 200 || !strings.Contains(updated.Body.String(), `"name":"updated"`) {
		t.Fatalf("update = %d %s", updated.Code, updated.Body)
	}
	read := entityRequest(p, t.Context(), "GET", "/widgets/2", "", "")
	if read.Code != 200 || read.Body.String() != updated.Body.String() {
		t.Fatalf("read = %d %s, updated = %s", read.Code, read.Body, updated.Body)
	}
	for _, exact := range []string{`"large":9007199254740993`, `"fraction":0.10000000000000001`, `"huge":1e999`, `"zero":-0`, `"id":2`} {
		if !strings.Contains(read.Body.String(), exact) {
			t.Fatalf("lost %s: %s", exact, read.Body)
		}
	}
	for _, method := range []string{"GET", "PATCH"} {
		missing := entityRequest(p, t.Context(), method, "/widgets/999", `{"name":"absent"}`, "")
		if missing.Code != 404 || missing.Body.String() != `{"missing":true}` {
			t.Fatalf("%s missing = %d %s", method, missing.Code, missing.Body)
		}
	}
	rows, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("entity count=%d err=%v; create must write once and missing update must not insert", len(rows), err)
	}
}

func TestEntityRulesRefusedAndCanceledRequestsDoNotWrite(t *testing.T) {
	for _, reason := range []string{"accept", "base delay", "graph delay", "session", "override"} {
		t.Run(reason, func(t *testing.T) {
			rule := liveEntityRule("entity_create", "POST", "/widgets")
			settings := domain.Settings{Seed: 1, ListSize: 1}
			if reason == "base delay" {
				settings.DelayMs = 1000
			}
			if reason == "graph delay" {
				rule.Nodes = append(rule.Nodes, responserules.Node{ID: "delay", Type: "delay", Name: "Delay", DelayMs: new(1000)})
				rule.Edges[0].To = "delay"
				rule.Edges = append(rule.Edges, responserules.Edge{ID: "wait", From: "delay", Port: "next", To: "entity"})
			}
			p, repo, res, wsID := liveEntityPlane(t, []responserules.Rule{rule}, settings)
			ctx := t.Context()
			accept := ""
			wantStatus := 0
			switch reason {
			case "accept":
				accept, wantStatus = "text/plain", 406
			case "base delay", "graph delay":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 15*time.Millisecond)
				defer cancel()
			case "session":
				state := livestate.NewStore(0, nil)
				if err := state.Set(wsID, livestate.Directive{Target: livestate.Target{Method: "POST", Path: "/widgets"}, Action: livestate.ActionFail, Status: 503, Once: true}); err != nil {
					t.Fatal(err)
				}
				p.SetLiveState(state)
				wantStatus = 503
			case "override":
				p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{overrides.OpKey("POST", "/widgets"): wsRow("POST", "/widgets", true, 418)}})
				wantStatus = 418
			}
			rec := entityRequest(p, ctx, "POST", "/widgets", `{"name":"must not be stored"}`, accept)
			if wantStatus != 0 && rec.Code != wantStatus {
				t.Fatalf("status=%d body=%s want=%d", rec.Code, rec.Body, wantStatus)
			}
			rows, err := repo.List(t.Context(), res.ID, "", "")
			if err != nil || len(rows) != 1 {
				t.Fatalf("refused request changed data: rows=%d err=%v", len(rows), err)
			}
		})
	}
}

func TestEntityRuleHostScopesAndRoster(t *testing.T) {
	res := &resources.Resource{ID: 42, RouteFamily: "/orgs/{}/teams/{}/widgets", IDField: "id", ScopeParams: []string{"org", "team"}, Wrapper: specs.Wrapper{IDType: "integer"}}
	store := &fakeEntityStore{createFn: func(_ context.Context, id int64, base, scope resources.ScopeKey, _, _ string, _ map[string]any) (resources.Entity, error) {
		if id != 42 || base != resources.EncodeScope([]string{"tenant1"}) || scope != resources.EncodeScope([]string{"org/7", "team1"}) {
			t.Fatalf("wrong target: resource=%d base=%s scope=%s", id, base, scope)
		}
		return resources.Entity{Data: jsonx.RawMessage(`{"id":3,"precise":9007199254740993}`)}, nil
	}}
	rt := &runtime{resources: map[string]*resources.Resource{res.RouteFamily: res}, settings: domain.Settings{BasePath: "/tenants/{tenant}", BasePathValues: []string{"tenant1"}}}
	p := &Plane{entities: store}
	host := &responseRuleEntityHost{resolver: luaHost{p: p, rt: rt, base: resources.EncodeScope([]string{"tenant1"}), outer: []string{"org/7", "team1", "detail"}}}
	for _, scope := range [][]string{nil, {"org/7", "team1"}} {
		result, err := host.Create(t.Context(), responserules.EntityTarget{Family: res.RouteFamily, Scope: scope}, map[string]any{"name": "created"})
		if err != nil || !result.Found || result.Value.(map[string]any)["precise"] != jsonx.Number("9007199254740993") {
			t.Fatalf("scoped create=%+v error=%v", result, err)
		}
	}
	for _, target := range []responserules.EntityTarget{
		{Family: "/other-workspace"},
		{Family: res.RouteFamily, Scope: []string{}},
		{Family: res.RouteFamily, Scope: []string{"one"}},
	} {
		if _, err := host.Create(t.Context(), target, map[string]any{}); err == nil {
			t.Fatalf("accepted invalid target: %+v", target)
		}
	}
	host.resolver.base = resources.EncodeScope([]string{"undeclared"})
	if _, err := host.Create(t.Context(), responserules.EntityTarget{Family: res.RouteFamily}, map[string]any{}); err == nil {
		t.Fatal("accepted undeclared base scope")
	}
	if store.createCalls != 2 {
		t.Fatalf("invalid target touched storage: create calls=%d", store.createCalls)
	}
}

func TestEntityRulesListAndRequestPathResponse(t *testing.T) {
	list := liveEntityRule("entity_create", "GET", "/widgets")
	list.ID = "list"
	list.Nodes[1].Type = "entity_read"
	list.Nodes[1].Entity = &responserules.EntityOperation{Family: "/widgets", Operation: "list"}
	list.Nodes[2].Response.Status = 200
	path := liveEntityRule("entity_create", "GET", "/widgets/{id}")
	path.ID = "path"
	path.Nodes = []responserules.Node{path.Nodes[0], path.Nodes[2]}
	path.Nodes[1].Response.Status = 200
	path.Nodes[1].Response.BodyFrom = &responserules.ValueRef{Source: "path", Name: "id"}
	path.Edges = []responserules.Edge{{ID: "end", From: "start", Port: "next", To: "response"}}
	p, _, _, _ := liveEntityPlane(t, []responserules.Rule{list, path}, domain.Settings{Seed: 1, ListSize: 2})
	rec := entityRequest(p, t.Context(), "GET", "/widgets", "", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "[") || strings.Count(rec.Body.String(), `"id":`) != 2 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	rec = entityRequest(p, t.Context(), "GET", "/widgets/2", "", "")
	if rec.Code != 200 || rec.Body.String() != `"2"` {
		t.Fatalf("path body = %d %s", rec.Code, rec.Body)
	}
}

func TestEntityRuleSessionDelayReplacesGraphDelay(t *testing.T) {
	rule := liveEntityRule("entity_create", "POST", "/widgets")
	rule.Nodes = append(rule.Nodes, responserules.Node{ID: "delay", Type: "delay", Name: "Delay", DelayMs: new(1000)})
	rule.Edges[0].To = "delay"
	rule.Edges = append(rule.Edges, responserules.Edge{ID: "wait", From: "delay", Port: "next", To: "entity"})
	p, _, _, wsID := liveEntityPlane(t, []responserules.Rule{rule}, domain.Settings{Seed: 1, ListSize: 1, DelayMs: 1000})
	state := livestate.NewStore(0, nil)
	if err := state.Set(wsID, livestate.Directive{Target: livestate.Target{Method: "POST", Path: "/widgets"}, Action: livestate.ActionDelay, Ms: 1}); err != nil {
		t.Fatal(err)
	}
	p.SetLiveState(state)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	rec := entityRequest(p, ctx, "POST", "/widgets", `{"name":"quick"}`, "")
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"name":"quick"`) {
		t.Fatalf("session delay failed to replace graph/workspace delay: %d %s", rec.Code, rec.Body)
	}
}
