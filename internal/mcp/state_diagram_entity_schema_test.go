package mcp

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestStateDiagramAuthoringPreservesEntityValuesAndVersion(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":9007199254740993}`)}
	args := `{"designId":7,"diagramId":"order","expectedVersion":9007199254740993,"commands":[{"kind":"settings","entity":{"family":"/orders","keyParam":"id","stateField":"status"}},{"kind":"upsert_state","state":{"id":"start","name":"Created","x":0,"y":0,"terminal":false,"value":"created"}}]}`
	_, msg := callTool(t, calls, "apply_state_diagram_commands", args)
	if msg != "" || !strings.Contains(string(calls.sent), `"expectedVersion":9007199254740993`) || !strings.Contains(string(calls.sent), `"stateField":"status"`) || !strings.Contains(string(calls.sent), `"value":"created"`) {
		t.Fatalf("lost config/fence: %s %s", msg, calls.sent)
	}
}
func TestStateDiagramEntityRESTAndMCPRejectUnknownFields(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var api any
	if err := jsonx.Unmarshal(raw, &api); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/state-entity.json"
	if err := compiler.AddResource(uri, api); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri + "#/components/schemas/StateDiagramCommand")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, command string
		valid         bool
	}{
		{"set", `{"kind":"settings","entity":{"family":"/orders","keyParam":"id","stateField":"status"}}`, true},
		{"clear", `{"kind":"settings","clearEntity":true}`, true},
		{"set and clear", `{"kind":"settings","entity":{"family":"/orders","keyParam":"id","stateField":"status"},"clearEntity":true}`, false},
		{"unknown nested", `{"kind":"settings","entity":{"family":"/orders","keyParam":"id","stateField":"status","unknown":true}}`, false},
		{"null config", `{"kind":"settings","entity":null}`, false},
		{"state value", `{"kind":"upsert_state","state":{"id":"start","name":"Created","x":0,"y":0,"terminal":false,"value":"created"}}`, true},
		{"null value", `{"kind":"upsert_state","state":{"id":"start","name":"Created","x":0,"y":0,"terminal":false,"value":null}}`, false},
		{"empty value", `{"kind":"upsert_state","state":{"id":"start","name":"Created","x":0,"y":0,"terminal":false,"value":""}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var command any
			if err := jsonx.Unmarshal([]byte(tt.command), &command); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(command); (err == nil) != tt.valid {
				t.Errorf("REST valid=%t: %v", tt.valid, err)
			}
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			_, msg := callTool(t, calls, "apply_state_diagram_commands", `{"designId":7,"diagramId":"order","expectedVersion":1,"commands":[`+tt.command+`]}`)
			if (msg == "") != tt.valid || !tt.valid && calls.path != "" {
				t.Fatalf("MCP valid=%t reached=%s msg=%s", tt.valid, calls.path, msg)
			}
		})
	}
}
