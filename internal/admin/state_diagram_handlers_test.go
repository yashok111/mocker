package admin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/statediagram"
)

func TestStateDiagramAuthoringAndSimulationThroughMCP(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	src := loopbackTestSrc()
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(ctx, src, method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, out, err)
		}
		return out
	}
	raw := call("POST", "/api/designs", `{"name":"Lifecycle"}`, 201)
	var detail apidesign.Detail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d/state-diagrams", detail.Design.ID)
	call("POST", base, `{"expectedVersion":1,"diagram":{"id":"order","name":"Order","initialStateId":"new","states":[{"id":"new","name":"New","x":0,"y":0,"terminal":false},{"id":"paid","name":"Paid","x":300,"y":0,"terminal":true}],"transitions":[{"id":"pay","name":"Pay","from":"new","to":"paid","patchJSON":"{\"n\":9007199254740993}","responseStatus":200,"guard":{"pointer":"/allowed","equalsJSON":"true"}}]}}`, 201)
	call("GET", base+"/order", "", 200)
	call("POST", base+"/order/validate", `{}`, 200)
	raw = call("POST", base+"/order/simulate", `{"dataJSON":"{\"allowed\":true}","transitionIds":["pay","pay"]}`, 200)
	var run statediagram.Simulation
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	if run.StateID != "paid" || !run.Steps[0].Accepted || run.Steps[1].Accepted || !strings.Contains(run.DataJSON, "9007199254740993") {
		t.Fatalf("%s", raw)
	}
	list := call("GET", base, "", 200)
	if !strings.Contains(string(list), `"version":2`) {
		t.Fatalf("simulation saved a revision: %s", list)
	}
	call("POST", base+"/order/commands", `{"expectedVersion":2,"commands":[{"kind":"settings","name":"Edited by MCP"}]}`, 200)
	call("DELETE", base+"/order", `{"expectedVersion":2}`, 409)
	call("POST", base+"/order/commands", `{"expectedVersion":3,"commands":[{"kind":"settings","name":"Should roll back"},{"kind":"unknown"}]}`, 400)
	raw = call("GET", base+"/order", "", 200)
	if strings.Contains(string(raw), "Should roll back") || !strings.Contains(string(raw), "Edited by MCP") {
		t.Fatal("batch was not atomic")
	}
	call("DELETE", base+"/order", `{"expectedVersion":3}`, 200)
	call("GET", base+"/order", "", 404)
}
