package mcp

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendLifecycleSDKBuilder(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{"targetHash":"test"}`)}
	fixture := newToolFixture(calls)
	id := backendTestID
	ref := backendmodel.DiagramRef{Kind: "record", RecordType: "node", ID: id}
	in := backendmodel.DiagramLifecycleBuildInput{Target: backendmodel.BackendReadTarget{RevisionID: id}, Entity: ref, StateFields: []backendmodel.DiagramRef{ref}, StateDiagram: backendmodel.LifecycleArtifactSelection{RowID: "state-diagram", Locator: backendmodel.ArtifactProjectionLocator{Pin: backendmodel.ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}, View: "states", Owner: backendmodel.ArtifactOwnerAddress{Pointer: "/x-mocker-state-diagrams/diagrams/0", DiagramID: "orders"}}}}
	raw, _ := json.Marshal(in)
	raw = append([]byte(`{"projectId":"`+id+`",`), raw[1:]...)
	if _, msg := fixture.Call(t, "build_backend_lifecycle", string(raw)); msg != "" {
		t.Fatal(msg)
	}
	if calls.path != "/api/backend-projects/"+id+"/diagrams/lifecycle/build" || calls.method != "POST" {
		t.Fatal(calls.path, calls.method)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"stateFields":[`, `"stateFields":null,"ignored":[`, 1), strings.Replace(string(raw), `"stateDiagram":`, `"unknown":true,"stateDiagram":`, 1), strings.Replace(string(raw), `"target":`, `"target":null,"unused":`, 1)} {
		calls.method = ""
		if _, msg := fixture.Call(t, "build_backend_lifecycle", bad); msg == "" || calls.method != "" {
			t.Fatal("invalid request dispatched", bad, msg)
		}
	}
}
