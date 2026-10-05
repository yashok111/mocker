package mcp

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendDiagramToolRoutesAndInt64(t *testing.T) {
	const id = backendTestID
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct{ name, args, path string }{
		{"get_backend_diagram", `{"projectId":"` + id + `","diagramId":"` + id + `","version":9007199254740993,"hash":"` + hash + `"}`, "/diagrams/" + id + "/versions/9007199254740993?hash=" + hash},
		{"get_backend_diagram_view", `{"projectId":"` + id + `","viewId":"` + id + `","version":9007199254740993}`, "/diagram-views/" + id + "/versions/9007199254740993"},
		{"list_backend_diagrams", `{"projectId":"` + id + `","limit":500}`, "/diagrams?limit=500"},
		{"list_backend_diagram_views", `{"projectId":"` + id + `","kind":"architecture"}`, "/diagram-views?kind=architecture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{"version":9007199254740995}`)}
			out, msg := callTool(t, calls, tc.name, tc.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != "GET" || calls.path != "/api/backend-projects/"+id+tc.path {
				t.Fatal(calls.method, calls.path)
			}
			if !strings.Contains(string(out), "9007199254740995") {
				t.Fatal("rounded result")
			}
		})
	}
}

func TestBackendDiagramSDKExample(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	fixture := newToolFixture(server)
	call := func(name string, in any, out any) []byte {
		t.Helper()
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		body, message := fixture.Call(t, name, string(raw))
		if message != "" {
			t.Fatal(name, message)
		}
		if out != nil {
			if err = json.Unmarshal(body, out); err != nil {
				t.Fatal(err)
			}
		}
		return body
	}
	var project backendmodel.Project
	call("create_backend_project", map[string]any{"name": "Diagram SDK", "idempotencyKey": "diagram-sdk-project"}, &project)
	id := project.ID
	document := map[string]any{"format": "backend-diagram-v1", "kind": "architecture", "target": map[string]any{"revisionId": project.CurrentRevisionID}, "payload": map[string]any{"primarySystemId": id, "elements": []any{map[string]any{"id": id, "label": "Orders", "role": "software_system", "responsibility": "Orders", "technology": "Go", "origin": map[string]any{"kind": "authored", "reason": "Reviewed mapping"}, "refs": []any{}}}, "links": []any{}}}
	create := map[string]any{"projectId": id, "document": document, "idempotencyKey": "diagram-sdk-create"}
	var diagram backendmodel.DiagramVersion
	receipt := call("create_backend_diagram", create, &diagram)
	call("query_backend_diagram", map[string]any{"projectId": id, "pin": diagram.Pin, "level": "context", "rootId": id, "search": "", "origin": "all", "section": "elements", "limit": 100}, nil)
	state := backendmodel.DiagramViewState{Diagram: diagram.Pin, Level: "context", RootID: id, Origin: "all", Positions: []backendmodel.DiagramPosition{}, CollapsedIDs: []string{}}
	var view backendmodel.DiagramView
	call("create_backend_diagram_view", map[string]any{"projectId": id, "name": "Historical", "state": state, "idempotencyKey": "sdk-view"}, &view)
	call("save_backend_diagram_view", map[string]any{"projectId": id, "viewId": view.ID, "name": "Historical layout", "state": state, "expectedVersion": view.Version, "idempotencyKey": "sdk-layout"}, nil)
	call("get_backend_diagram_view", map[string]any{"projectId": id, "viewId": view.ID, "version": view.Version}, nil)
	call("save_backend_diagram", map[string]any{"projectId": id, "diagramId": diagram.Pin.ID, "document": document, "expectedVersion": diagram.Pin.Version, "idempotencyKey": "sdk-noop"}, nil)
	call("fork_backend_diagram", map[string]any{"projectId": id, "source": diagram.Pin, "target": document["target"], "reason": "Separate reviewed mapping", "idempotencyKey": "sdk-fork"}, nil)
	call("compare_backend_diagrams", map[string]any{"projectId": id, "before": diagram.Pin, "after": diagram.Pin, "limit": 100}, nil)
	call("list_backend_diagrams", map[string]any{"projectId": id}, nil)
	call("list_backend_diagram_views", map[string]any{"projectId": id}, nil)
	if got := call("get_backend_diagram", map[string]any{"projectId": id, "diagramId": diagram.Pin.ID, "version": diagram.Pin.Version, "hash": diagram.Pin.ContentHash}, nil); string(got) != string(receipt) {
		t.Fatal("historical SDK receipt differs")
	}
	if got := call("create_backend_diagram", create, nil); string(got) != string(receipt) {
		t.Fatal("SDK replay differs")
	}
}
