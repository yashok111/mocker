package admin

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
)

func TestResponseRuleExecutionRoutesStrictCAS(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, func(cfg *config.Config) { cfg.CheckpointDebounce = 300 })
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Execution", Document: responseRuleTestDocument, Source: "ui"})
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
	if out := call("GET", "/response-rule-execution", "", 200); !strings.Contains(string(out), `"rules":[]`) {
		t.Fatalf("initial: %s", out)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		for _, body := range []string{`{}`, `null`, `[]`, `{"expectedVersion":null}`, `{"expectedVersion":0}`, `{"expectedVersion":-1}`, `{"expectedVersion":1.0}`, `{"expectedVersion":1e0}`, `{"expectedVersion":"1"}`, `{"expectedVersion":1,"extra":true}`} {
			call(method, "/response-rules/r/execution", body, 400)
		}
	}
	before := responseRuleRows(t, s)
	call("PUT", "/response-rules/r/execution", `{"expectedVersion":9007199254740993}`, 409)
	if !reflect.DeepEqual(before, responseRuleRows(t, s)) {
		t.Fatal("stale exact version changed storage")
	}
	call("PUT", "/response-rules/r/execution", `{"expectedVersion":1}`, 200)
	if out := call("GET", "/response-rule-execution", "", 200); !strings.Contains(string(out), `"state":"current"`) {
		t.Fatalf("current: %s", out)
	}
	call("DELETE", "/response-rules/r/execution", `{"expectedVersion":1}`, 409)
	call("DELETE", "/response-rules/r/execution", `{"expectedVersion":2}`, 200)
	call("DELETE", "/response-rules/r/execution", `{"expectedVersion":3}`, 200)
	if rows, err := s.checkpointsRepo.List(t.Context(), d.Design.DraftWorkspaceID); err != nil || len(rows) != 0 {
		t.Fatalf("API history leaked to checkpoints: %+v %v", rows, err)
	}
}
