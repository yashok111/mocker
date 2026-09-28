package mcp

import (
	"net/http"
	"strings"
	"testing"
)

func TestStateDiagramToolsUseSharedAdminRoutes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, method, suffix, args string }{
		{"list_state_diagrams", "GET", "", `{"designId":7}`},
		{"get_state_diagram", "GET", "/order", `{"designId":7,"diagramId":"order"}`},
		{"create_state_diagram", "POST", "", `{"designId":7,"expectedVersion":3,"diagram":{"id":"order","name":"Order","initialStateId":"","states":[],"transitions":[]}}`},
		{"save_state_diagram", "PUT", "/order", `{"designId":7,"diagramId":"order","expectedVersion":3}`},
		{"delete_state_diagram", "DELETE", "/order", `{"designId":7,"diagramId":"order","expectedVersion":3}`},
		{"apply_state_diagram_commands", "POST", "/order/commands", `{"designId":7,"diagramId":"order","expectedVersion":3,"commands":[{"kind":"settings","name":"New"}]}`},
		{"validate_state_diagram", "POST", "/order/validate", `{"designId":7,"diagramId":"order"}`},
		{"simulate_state_diagram", "POST", "/order/simulate", `{"designId":7,"diagramId":"order","dataJSON":"{\"n\":9007199254740993}","transitionIds":["pay"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":3,"dataJSON":"9007199254740993"}`)}
			raw, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" || calls.method != tt.method || calls.path != "/api/designs/7/state-diagrams"+tt.suffix || !strings.Contains(string(raw), "9007199254740993") {
				t.Fatalf("%s %s %s %s", msg, calls.method, calls.path, raw)
			}
		})
	}
}
