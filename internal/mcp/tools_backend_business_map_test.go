package mcp

import (
	"encoding/json/v2"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendBusinessMapSDKExample(t *testing.T) {
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
	rawFixture, err := os.ReadFile("../backendmodel/testdata/diagrams/business_map.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(rawFixture, &document); err != nil {
		t.Fatal(err)
	}
	document["target"] = map[string]any{"revisionId": project.CurrentRevisionID}

	create := map[string]any{"projectId": id, "document": document, "idempotencyKey": "diagram-sdk-create"}
	var diagram backendmodel.DiagramVersion
	receipt := call("create_backend_diagram", create, &diagram)
	call("query_backend_diagram", map[string]any{"projectId": id, "pin": diagram.Pin, "search": "", "origin": "all", "section": "elements", "limit": 100}, nil)
	state := backendmodel.DiagramViewState{Diagram: diagram.Pin, Origin: "all", Positions: []backendmodel.DiagramPosition{}, CollapsedIDs: []string{}}
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

func TestBackendBusinessMapSDKForkArchitecture(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	fixture := newToolFixture(calls)
	pin := backendmodel.DiagramPin{ID: backendTestID, Version: 1, ContentHash: strings.Repeat("a", 64)}
	raw, err := json.Marshal(map[string]any{"projectId": backendTestID, "source": pin, "target": map[string]string{"revisionId": backendTestID}, "architecture": pin, "reason": "Exact C4 dependency", "idempotencyKey": "fork-c4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, message := fixture.Call(t, "fork_backend_diagram", string(raw)); message != "" {
		t.Fatal(message)
	}
	if calls.path != "/api/backend-projects/"+backendTestID+"/diagrams/fork" {
		t.Fatal(calls.path)
	}
}
