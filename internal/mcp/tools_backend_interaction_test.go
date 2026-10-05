package mcp

import (
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendInteractionsBuilderRoute(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{"targetHash":"test"}`)}
	_, message := callTool(t, calls, "build_backend_interactions", `{"projectId":"`+backendTestID+`","target":{"revisionId":"`+backendTestID+`"},"entrypointId":"`+backendTestID+`","maxSteps":1}`)
	if message != "" {
		t.Fatal(message)
	}
	if calls.method != "POST" || calls.path != "/api/backend-projects/"+backendTestID+"/diagrams/interactions/build" {
		t.Fatal(calls.method, calls.path)
	}
}

func TestBackendInteractionsSDKHistoricalPins(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	fixture := newToolFixture(server)
	call := func(name string, input any, out any) []byte {
		t.Helper()
		raw, err := json.Marshal(input)
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
	var p backendmodel.Project
	call("create_backend_project", map[string]any{"name": "Interactions SDK", "idempotencyKey": "p"}, &p)
	raw, err := os.ReadFile("../backendmodel/testdata/diagrams/interaction.json")
	if err != nil {
		t.Fatal(err)
	}
	var d backendmodel.DiagramDocument
	if err = json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Target = backendmodel.BackendReadTarget{RevisionID: p.CurrentRevisionID}
	create := map[string]any{"projectId": p.ID, "document": d, "idempotencyKey": "create"}
	var v backendmodel.DiagramVersion
	receipt := call("create_backend_diagram", create, &v)
	var page struct {
		Items []struct {
			RowType string `json:"rowType"`
			Data    struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"items"`
	}
	call("query_backend_diagram", map[string]any{"projectId": p.ID, "pin": v.Pin, "section": "links", "search": "", "origin": "all", "limit": 100}, &page)
	if len(page.Items) != 2 || page.Items[0].Data.ID != d.Interactions.Order[0].ID {
		t.Fatal("SDK lost order IDs")
	}
	state := backendmodel.DiagramViewState{Diagram: v.Pin, Origin: "all", Selection: &backendmodel.DiagramSelection{Type: "link", ID: d.Interactions.Order[0].ID}, Positions: []backendmodel.DiagramPosition{}, CollapsedIDs: []string{}}
	var view backendmodel.DiagramView
	call("create_backend_diagram_view", map[string]any{"projectId": p.ID, "state": state, "name": "Old order", "idempotencyKey": "view"}, &view)
	raw, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Interactions.Order = d.Interactions.Order[1:]
	var next backendmodel.DiagramVersion
	call("save_backend_diagram", map[string]any{"projectId": p.ID, "diagramId": v.Pin.ID, "expectedVersion": 1, "document": d, "idempotencyKey": "save"}, &next)
	if string(call("create_backend_diagram", create, nil)) != string(receipt) {
		t.Fatal("receipt changed")
	}
	var old backendmodel.DiagramView
	call("get_backend_diagram_view", map[string]any{"projectId": p.ID, "viewId": view.ID, "version": view.Version}, &old)
	if old.State.Diagram != v.Pin {
		t.Fatal("historical view advanced")
	}
	for _, bad := range []string{`{"projectId":"` + p.ID + `","target":null,"entrypointId":"` + p.ID + `"}`, `{"projectId":"` + p.ID + `","target":{"revisionId":"` + p.CurrentRevisionID + `"},"entrypointId":"` + p.ID + `","maxSteps":0}`} {
		_, message := fixture.Call(t, "build_backend_interactions", bad)
		if message == "" {
			t.Fatal("invalid SDK builder accepted")
		}
	}
}
