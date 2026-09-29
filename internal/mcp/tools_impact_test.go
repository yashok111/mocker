package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestImpactToolPreservesExactInputAndOutput(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"fromRevisionId":3,"document":" {\"n\":9007199254740993,\"v\":null} "}`, `{"fromRevisionId":3,"toRevisionId":5}`} {
		response := `{"changes":[{"beforeJSON":"null","afterJSON":"9007199254740993"}],"complete":false}`
		calls := &recordingCaller{status: http.StatusOK, body: []byte(response)}
		raw, msg := callTool(t, calls, "analyze_api_design_impact", `{"designId":7,`+body[1:])
		if msg != "" || calls.method != "POST" || calls.path != "/api/designs/7/impact" || string(calls.sent) != body || string(raw) != response {
			t.Fatalf("dispatch: %s %s %s output=%s error=%s", calls.method, calls.path, calls.sent, raw, msg)
		}
	}
}

func TestImpactToolStrictSchemaAndAnnotations(t *testing.T) {
	t.Parallel()
	for _, args := range []string{`{"designId":7}`, `{"designId":7,"fromRevisionId":1}`, `{"designId":7,"fromRevisionId":0,"document":"{}"}`, `{"designId":7,"fromRevisionId":1,"document":null}`, `{"designId":7,"fromRevisionId":1,"document":"{}","toRevisionId":null}`, `{"designId":7,"fromRevisionId":1,"document":"{}","toRevisionId":2}`, `{"designId":7,"fromRevisionId":1,"document":"{}","extra":1}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "analyze_api_design_impact", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid input reached route: %s %s", args, msg)
		}
	}
	ep := newTestEndpoint(t)
	w := doMCP(t, ep.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string
				Annotations struct {
					ReadOnlyHint   bool
					IdempotentHint bool
				}
				InputSchema jsonx.RawMessage
			}
		}
	}
	if err := jsonx.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, tool := range env.Result.Tools {
		if tool.Name == "analyze_api_design_impact" {
			if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || !strings.Contains(string(tool.InputSchema), `"oneOf"`) {
				t.Fatalf("bad tool contract: %+v", tool)
			}
			return
		}
	}
	t.Fatal("impact tool missing")
}
