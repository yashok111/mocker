package admin

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
)

func TestStateDiagramExecutionRoutesStrictCAS(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, func(cfg *config.Config) { cfg.CheckpointDebounce = 300 })
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Execution", Document: stateExecutionTestDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d", d.Design.ID)
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, base+path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, out, err)
		}
		return out
	}
	if out := call("GET", "/state-diagram-execution", "", 200); !strings.Contains(string(out), `"diagrams":[]`) {
		t.Fatalf("initial: %s", out)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		for _, body := range []string{`{}`, `null`, `[]`, `{"expectedVersion":null}`, `{"expectedVersion":0}`, `{"expectedVersion":-1}`, `{"expectedVersion":1.0}`, `{"expectedVersion":1e0}`, `{"expectedVersion":"1"}`, `{"expectedVersion":1,"extra":true}`} {
			call(method, "/state-diagrams/lifecycle/execution", body, 400)
		}
	}
	before := responseRuleRows(t, s)
	call("PUT", "/state-diagrams/lifecycle/execution", `{"expectedVersion":9007199254740993}`, 409)
	if !reflect.DeepEqual(before, responseRuleRows(t, s)) {
		t.Fatal("stale exact version changed storage")
	}
	call("PUT", "/state-diagrams/lifecycle/execution", `{"expectedVersion":1}`, 200)
	if out := call("GET", "/state-diagram-execution", "", 200); !strings.Contains(string(out), `"state":"current"`) {
		t.Fatalf("current: %s", out)
	}
	call("DELETE", "/state-diagrams/lifecycle/execution", `{"expectedVersion":1}`, 409)
	call("DELETE", "/state-diagrams/lifecycle/execution", `{"expectedVersion":2}`, 200)
	call("DELETE", "/state-diagrams/lifecycle/execution", `{"expectedVersion":3}`, 200)
	if rows, err := s.checkpointsRepo.List(t.Context(), d.Design.DraftWorkspaceID); err != nil || len(rows) != 0 {
		t.Fatalf("API history leaked to checkpoints: %+v %v", rows, err)
	}
}

const stateExecutionTestDocument = `{"openapi":"3.1.0","info":{"title":"State execution","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"status":{"type":"string"}}}}}}}}}},"/orders/{id}":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"},"status":{"type":"string"}}}}}}}},"post":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"},"status":{"type":"string"}}}}}}}},"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}]}},"x-mocker-state-diagrams":{"formatVersion":1,"diagrams":[{"id":"lifecycle","name":"Order","initialStateId":"created","entity":{"family":"/orders","keyParam":"id","stateField":"status"},"states":[{"id":"created","name":"Created","x":0,"y":0,"terminal":false},{"id":"paid","name":"Paid","x":200,"y":0,"terminal":true}],"transitions":[{"id":"pay","name":"Pay","from":"created","to":"paid","binding":{"method":"post","path":"/orders/{id}"},"responseStatus":200,"patchJSON":"{}"}]}]}}`
