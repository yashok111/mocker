package mcp

import (
	"net/http"
	"strings"
	"testing"
)

func TestStateDiagramExecutionToolRoutes(t *testing.T) {
	for _, tc := range []struct{ name, method, path, input string }{
		{"get_state_diagram_execution", "GET", "/api/designs/7/state-diagram-execution", `{"designId":7}`},
		{"apply_state_diagram", "PUT", "/api/designs/7/state-diagrams/r/execution", `{"designId":7,"diagramId":"r","expectedVersion":9007199254740993}`},
		{"unapply_state_diagram", "DELETE", "/api/designs/7/state-diagrams/r/execution", `{"designId":7,"diagramId":"r","expectedVersion":9007199254740993}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":9007199254740993}`)}
			result, message := callTool(t, caller, tc.name, tc.input)
			if message != "" || caller.method != tc.method || caller.path != tc.path {
				t.Fatalf("%s %s: %s", caller.method, caller.path, message)
			}
			if !strings.Contains(string(result), "9007199254740993") {
				t.Fatalf("rounded output %s", result)
			}
			if tc.method != "GET" && string(caller.sent) != `{"expectedVersion":9007199254740993}` {
				t.Fatalf("invalid body %s", caller.sent)
			}
		})
	}
}

func TestStateDiagramExecutionToolsRejectInvalidInput(t *testing.T) {
	for _, input := range []string{
		`{"designId":7,"diagramId":"r"}`,
		`{"designId":7,"diagramId":"r","expectedVersion":null}`,
		`{"designId":7,"diagramId":"r","expectedVersion":0}`,
		`{"designId":7,"diagramId":"../r","expectedVersion":1}`,
		`{"designId":7,"diagramId":"r","expectedVersion":1,"document":"{}"}`,
	} {
		caller := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
		_, message := callTool(t, caller, "apply_state_diagram", input)
		if message == "" || caller.method != "" {
			t.Fatalf("invalid input reached handler: %s", input)
		}
	}
}
