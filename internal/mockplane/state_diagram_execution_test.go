package mockplane

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/statediagram"
	"github.com/yashok111/mocker/internal/workspaces"
)

func stateExecutionPlane(t *testing.T, settings domain.Settings, noBody bool) (*Plane, *resources.Repo, *resources.Resource, int64) {
	return stateExecutionPlaneConfigured(t, settings, noBody, nil)
}

func stateExecutionPlaneConfigured(t *testing.T, settings domain.Settings, noBody bool, configure func(map[string]any)) (*Plane, *resources.Repo, *resources.Resource, int64) {
	t.Helper()
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	var root map[string]any
	if err := jsonx.Unmarshal([]byte(rebindDocA), &root); err != nil {
		t.Fatal(err)
	}
	paths := root["paths"].(map[string]any)
	detail := paths["/widgets/{id}"].(map[string]any)
	status := 200
	for _, action := range []string{"pay", "close"} {
		if noBody {
			status = 204
			paths["/widgets/{id}/"+action] = map[string]any{"post": map[string]any{"responses": map[string]any{"204": map[string]any{"description": "Done"}}}}
		} else {
			paths["/widgets/{id}/"+action] = map[string]any{"post": detail["get"]}
		}
	}
	var diagram any
	rawDiagram := fmt.Sprintf(`{"id":"lifecycle","name":"Lifecycle","initialStateId":"new","entity":{"family":"/widgets","keyParam":"id","stateField":"status"},"states":[{"id":"new","name":"Created","value":"created","x":0,"y":0,"terminal":false},{"id":"ready","name":"Paid","value":"paid","x":200,"y":0,"terminal":false},{"id":"done","name":"Closed","value":"closed","x":400,"y":0,"terminal":true}],"transitions":[{"id":"pay","name":"Pay","from":"new","to":"ready","binding":{"method":"post","path":"/widgets/{id}/pay"},"guard":{"pointer":"/allowed","equalsJSON":"true"},"patchJSON":"{\"name\":\"paid\"}","responseStatus":%d},{"id":"close","name":"Close","from":"ready","to":"done","binding":{"method":"post","path":"/widgets/{id}/close"},"patchJSON":"{}","responseStatus":%d}]}`, status, status)
	if err := jsonx.Unmarshal([]byte(rawDiagram), &diagram); err != nil {
		t.Fatal(err)
	}
	root["x-mocker-state-diagrams-execution"] = map[string]any{"formatVersion": 1, "diagrams": []any{diagram}}
	if configure != nil {
		configure(root)
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	sr := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, sr, "state execution", string(raw))
	wsID := resourceIntegrationWorkspace(t, db, "alex", specID, settings)
	rr := resources.NewRepo(db, sr, cfg.MaxResponse, 256<<10, 1000)
	res, err := rr.Confirm(t.Context(), wsID, "/widgets")
	if err != nil {
		t.Fatal(err)
	}
	p := New(cfg, workspaces.NewRepo(db), sr, runtimeTestLogger())
	p.SetResources(rr)
	p.SetEntities(rr)
	return p, rr, res, wsID
}

func createStateEntity(t *testing.T, repo *resources.Repo, res *resources.Resource, raw string) resources.Entity {
	t.Helper()
	decoder := jsonx.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil {
		t.Fatal(err)
	}
	row, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, data)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestStateExecutionLiveIsolationAndPrecision(t *testing.T) {
	p, repo, res, _ := stateExecutionPlane(t, domain.Settings{Seed: 1, ListSize: 1}, false)
	first := createStateEntity(t, repo, res, `{"allowed":true,"large":9007199254740993,"fraction":0.10000000000000001,"huge":1e999,"zero":-0}`)
	second := createStateEntity(t, repo, res, `{"allowed":true,"status":"created","name":"other"}`)
	path := "/widgets/" + first.EntityKey
	paid := entityRequest(p, t.Context(), "POST", path+"/pay", "", "application/json")
	if paid.Code != 200 || !strings.Contains(paid.Body.String(), `"status":"paid"`) {
		t.Fatalf("pay=%d %s", paid.Code, paid.Body)
	}
	if paid.Header().Get("Content-Type") != "application/json" || paid.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe response headers: %v", paid.Header())
	}
	for _, token := range []string{`"large":9007199254740993`, `"fraction":0.10000000000000001`, `"huge":1e999`, `"zero":-0`, `"id":` + first.EntityKey} {
		if !strings.Contains(paid.Body.String(), token) {
			t.Fatalf("lost %s: %s", token, paid.Body)
		}
	}
	other, found, err := repo.Get(t.Context(), res.ID, "", "", second.EntityKey)
	if err != nil || !found || string(other.Data) != string(second.Data) {
		t.Fatalf("other entity changed: %s err=%v", other.Data, err)
	}
	if repeat := entityRequest(p, t.Context(), "POST", path+"/pay", "", ""); repeat.Code != 409 {
		t.Fatalf("repeat=%d %s", repeat.Code, repeat.Body)
	}
	closed := entityRequest(p, t.Context(), "POST", path+"/close", "", "")
	if closed.Code != 200 || !strings.Contains(closed.Body.String(), `"status":"closed"`) {
		t.Fatalf("close=%d %s", closed.Code, closed.Body)
	}
	if terminal := entityRequest(p, t.Context(), "POST", path+"/pay", "", ""); terminal.Code != 409 {
		t.Fatalf("terminal=%d %s", terminal.Code, terminal.Body)
	}
	read := entityRequest(p, t.Context(), "GET", path, "", "")
	if read.Code != 200 || read.Body.String() != closed.Body.String() {
		t.Fatalf("read=%d %s", read.Code, read.Body)
	}
}

func TestStateExecutionConcurrentTransitionHasOneWinner(t *testing.T) {
	p, repo, res, _ := stateExecutionPlane(t, domain.Settings{Seed: 1, ListSize: 1}, false)
	row := createStateEntity(t, repo, res, `{"allowed":true,"status":"created"}`)
	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { results <- entityRequest(p, t.Context(), "POST", "/widgets/"+row.EntityKey+"/pay", "", "") })
	}
	wg.Wait()
	close(results)
	statuses := map[int]int{}
	for result := range results {
		statuses[result.Code]++
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("concurrent statuses=%v", statuses)
	}
	stored, found, err := repo.Get(t.Context(), res.ID, "", "", row.EntityKey)
	if err != nil || !found || !strings.Contains(string(stored.Data), `"status":"paid"`) {
		t.Fatalf("stored=%s err=%v", stored.Data, err)
	}
}

func TestStateExecutionBlockedRequestsPreserveData(t *testing.T) {
	for _, reason := range []string{"guard", "null state", "unknown state", "missing", "bad key", "accept", "cancel", "session", "override", "route off"} {
		t.Run(reason, func(t *testing.T) {
			settings := domain.Settings{Seed: 1, ListSize: 1}
			if reason == "cancel" {
				settings.DelayMs = 1000
			}
			p, repo, res, wsID := stateExecutionPlane(t, settings, false)
			data := `{"allowed":true,"status":"created"}`
			if reason == "guard" {
				data = `{"allowed":false,"status":"created"}`
			}
			if reason == "null state" {
				data = `{"allowed":true,"status":null}`
			}
			if reason == "unknown state" {
				data = `{"allowed":true,"status":"unknown"}`
			}
			row := createStateEntity(t, repo, res, data)
			key, accept, want := row.EntityKey, "", 409
			ctx := t.Context()
			switch reason {
			case "missing":
				key, want = "99999", 404
			case "bad key":
				key, want = "0007", 400
			case "accept":
				accept, want = "text/plain", 406
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 15*time.Millisecond)
				defer cancel()
				want = 0
			case "session":
				state := livestate.NewStore(0, nil)
				if err := state.Set(wsID, livestate.Directive{Target: livestate.Target{Method: "POST", Path: "/widgets/{id}/pay"}, Action: livestate.ActionFail, Status: 503, Once: true}); err != nil {
					t.Fatal(err)
				}
				p.SetLiveState(state)
				want = 503
			case "override":
				p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{overrides.OpKey("POST", "/widgets/{id}/pay"): wsRow("POST", "/widgets/{id}/pay", true, 418)}})
				want = 418
			case "route off":
				override := wsRow("POST", "/widgets/{id}/pay", true, 200)
				override.RouteOff = true
				p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{overrides.OpKey("POST", "/widgets/{id}/pay"): override}})
				want = 404
			}
			rec := entityRequest(p, ctx, "POST", "/widgets/"+key+"/pay", "", accept)
			if want != 0 && rec.Code != want {
				t.Fatalf("response=%d %s, want%d", rec.Code, rec.Body, want)
			}
			stored, found, err := repo.Get(t.Context(), res.ID, "", "", row.EntityKey)
			if err != nil || !found || string(stored.Data) != string(row.Data) {
				t.Fatalf("blocked request wrote: %s err=%v", stored.Data, err)
			}
			rows, err := repo.List(t.Context(), res.ID, "", "")
			if err != nil || len(rows) != 2 {
				t.Fatalf("blocked request changed rowcount: %d err=%v", len(rows), err)
			}
		})
	}
}

func TestStateExecutionNoBodyStillPersistsState(t *testing.T) {
	p, repo, res, _ := stateExecutionPlane(t, domain.Settings{Seed: 1, ListSize: 1}, true)
	row := createStateEntity(t, repo, res, `{"allowed":true,"status":"created"}`)
	response := entityRequest(p, t.Context(), "POST", "/widgets/"+row.EntityKey+"/pay", "", "text/plain")
	if response.Code != 204 || response.Body.Len() != 0 {
		t.Fatalf("no-body=%d %s", response.Code, response.Body)
	}
	stored, found, err := repo.Get(t.Context(), res.ID, "", "", row.EntityKey)
	if err != nil || !found || !strings.Contains(string(stored.Data), `"status":"paid"`) {
		t.Fatalf("stored=%s err=%v", stored.Data, err)
	}
}

func TestStateExecutionDeleteDoesNotDeleteEntity(t *testing.T) {
	p, repo, res, _ := stateExecutionPlaneConfigured(t, domain.Settings{Seed: 1, ListSize: 1}, false, func(root map[string]any) {
		detail := root["paths"].(map[string]any)["/widgets/{id}"].(map[string]any)
		detail["delete"] = detail["get"]
		diagram := root[statediagram.ExecutionExtension].(map[string]any)["diagrams"].([]any)[0].(map[string]any)
		transition := diagram["transitions"].([]any)[0].(map[string]any)
		transition["binding"] = map[string]any{"method": "delete", "path": "/widgets/{id}"}
		transition["patchJSON"] = `{"id":999,"name":"paid"}`
	})
	row := createStateEntity(t, repo, res, `{"allowed":true,"status":"created"}`)
	response := entityRequest(p, t.Context(), "DELETE", "/widgets/"+row.EntityKey, "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"paid"`) || !strings.Contains(response.Body.String(), `"id":`+row.EntityKey) {
		t.Fatalf("delete transition=%d %s", response.Code, response.Body)
	}
	stored, found, err := repo.Get(t.Context(), res.ID, "", "", row.EntityKey)
	if err != nil || !found || string(stored.Data) != response.Body.String() {
		t.Fatalf("ordinary DELETE ran after transition: found=%v row=%s err=%v", found, stored.Data, err)
	}
}

func TestStateExecutionAcceptsEntityAboveStaticRuleLimit(t *testing.T) {
	p, repo, res, _ := stateExecutionPlane(t, domain.Settings{Seed: 1, ListSize: 1}, false)
	row := createStateEntity(t, repo, res, `{"allowed":true,"status":"created","padding":"`+strings.Repeat("a", 70<<10)+`"}`)
	response := entityRequest(p, t.Context(), "POST", "/widgets/"+row.EntityKey+"/pay", "", "application/json")
	if response.Code != 200 || response.Body.Len() <= 64<<10 {
		t.Fatalf("large resource response=%d bytes=%d", response.Code, response.Body.Len())
	}
	stored, found, err := repo.Get(t.Context(), res.ID, "", "", row.EntityKey)
	if err != nil || !found || string(stored.Data) != response.Body.String() {
		t.Fatalf("committed body differs: %v", err)
	}
}

func TestStateExecutionUsesRequestBaseAndParentScopes(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 1, BasePath: "/tenants/{tenant}", BasePathValues: []string{"one", "two"}}
	p, repo, _, wsID := stateExecutionPlaneConfigured(t, settings, false, func(root map[string]any) {
		paths := root["paths"].(map[string]any)
		for _, suffix := range []string{"", "/{id}", "/{id}/pay", "/{id}/close"} {
			paths["/orgs/{organization}/widgets"+suffix] = paths["/widgets"+suffix]
		}
		paths["/orgs"] = paths["/widgets"]
		paths["/orgs/{org}"] = paths["/widgets/{id}"]
		diagram := root[statediagram.ExecutionExtension].(map[string]any)["diagrams"].([]any)[0].(map[string]any)
		diagram["entity"].(map[string]any)["family"] = "/orgs/{}/widgets"
		for _, raw := range diagram["transitions"].([]any) {
			binding := raw.(map[string]any)["binding"].(map[string]any)
			binding["path"] = "/orgs/{organization}" + binding["path"].(string)
		}
	})
	parent, err := repo.Confirm(t.Context(), wsID, "/orgs")
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.Confirm(t.Context(), wsID, "/orgs/{}/widgets")
	if err != nil {
		t.Fatal(err)
	}
	var siblings []resources.Entity
	for i, tuple := range [][2]string{{"one", "10"}, {"one", "20"}, {"two", "30"}, {"two", "40"}} {
		base := resources.EncodeScope([]string{tuple[0]})
		scope := resources.EncodeScope([]string{tuple[1]})
		if _, _, err := repo.Set(t.Context(), parent.ID, base, "", tuple[1], parent.IDField, parent.Wrapper.IDType, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		row, _, err := repo.Set(t.Context(), child.ID, base, scope, fmt.Sprint(100+i), child.IDField, child.Wrapper.IDType, map[string]any{"allowed": true, "status": "created"})
		if err != nil {
			t.Fatal(err)
		}
		siblings = append(siblings, row)
	}
	response := entityRequest(p, t.Context(), "POST", "/tenants/one/orgs/10/widgets/100/pay", "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"paid"`) {
		t.Fatalf("scoped response=%d %s", response.Code, response.Body)
	}
	for i, before := range siblings {
		stored, found, err := repo.Get(t.Context(), child.ID, resources.ScopeKey(before.BaseScopeKey), resources.ScopeKey(before.ScopeKey), before.EntityKey)
		if err != nil || !found {
			t.Fatalf("scoped get: %v", err)
		}
		if i == 0 {
			if string(stored.Data) != response.Body.String() {
				t.Fatalf("selected entity differs: %s", stored.Data)
			}
		} else if string(stored.Data) != string(before.Data) {
			t.Fatalf("sibling changed: %s", stored.Data)
		}
	}
	for _, path := range []string{"/tenants/one/orgs/20/widgets/100/pay", "/tenants/two/orgs/10/widgets/100/pay", "/tenants/undeclared/orgs/10/widgets/100/pay"} {
		response := entityRequest(p, t.Context(), "POST", path, "", "")
		if response.Code != 404 && response.Code != 400 {
			t.Fatalf("cross-scope %s=%d %s", path, response.Code, response.Body)
		}
	}
	if deleted, err := repo.Delete(t.Context(), parent.ID, "one", "", "20"); err != nil || !deleted {
		t.Fatalf("delete parent: %v %v", deleted, err)
	}
	orphan := entityRequest(p, t.Context(), "POST", "/tenants/one/orgs/20/widgets/101/pay", "", "")
	if orphan.Code != 404 {
		t.Fatalf("orphan transition=%d %s", orphan.Code, orphan.Body)
	}
	stored, found, err := repo.Get(t.Context(), child.ID, "one", "20", "101")
	if err != nil || !found || string(stored.Data) != string(siblings[1].Data) {
		t.Fatalf("orphan transition changed child: %s err=%v", stored.Data, err)
	}
}

func TestStateExecutionManagedRosterUnionsAndKeepsDormantRows(t *testing.T) {
	p, repo, res, wsID := stateExecutionPlane(t, domain.Settings{Seed: 1, ListSize: 1}, false)
	ws, err := p.src.(*workspaces.Repo).ByID(t.Context(), wsID)
	if err != nil {
		t.Fatal(err)
	}
	p.src = entityManagedSource{Source: p.src, designID: 1}
	rt, err := p.buildRuntime(t.Context(), ws, nil)
	if err != nil || rt.resources[res.RouteFamily] == nil {
		t.Fatalf("state-only managed roster=%v err=%v", rt, err)
	}
	active, err := p.executionResources(t.Context(), ws, rt.resources, nil, nil)
	if err != nil || len(active) != 0 {
		t.Fatalf("unapplied roster=%v err=%v", active, err)
	}
	rows, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("dormant rows changed: %v %v", rows, err)
	}
}
