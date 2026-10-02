package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendArtifactContractStrictSelectors(t *testing.T) {
	schema, err := BackendSchema("EditorSelector")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/editors"
	if err = c.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(raw string) error {
		var v any
		d := jsonx.NewDecoder(bytes.NewBufferString(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return err
		}
		return compiled.Validate(v)
	}
	valid := []string{`{"kind":"sequence_message","messageId":"opaque / id"}`, `{"kind":"participant","participantId":"same"}`, `{"kind":"event_operation","contractId":"c","operationId":"op"}`, `{"kind":"event_channel","channelId":"ch"}`, `{"kind":"event_message","messageId":"m"}`, `{"kind":"state_diagram","diagramId":"d"}`, `{"kind":"state_transition","diagramId":"d","transitionId":"t","embeddedContractId":"copy"}`, `{"kind":"response_rule","ruleId":"r"}`, `{"kind":"response_node","ruleId":"r","nodeId":"n","embeddedContractId":"linked"}`}
	for _, raw := range valid {
		if err := validate(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if validate(strings.Replace(raw, `}`, `,"extra":true}`, 1)) == nil {
			t.Fatal("extra accepted", raw)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"kind":"event_operation","contractId":"c"}`, `{"kind":"state_transition","diagramId":"d","transitionId":null}`, `{"kind":"sequence_message","messageId":1}`, `{"kind":"participant","participantId":"p","messageId":"m"}`} {
		if validate(raw) == nil {
			t.Fatal("invalid accepted", raw)
		}
	}
}

func TestBackendArtifactContractReadLocatorBounds(t *testing.T) {
	// Readable auxiliary addresses retain escaped paths/keys beyond mutation limits.
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"ArtifactAPIOperationData", map[string]any{"operationKey": strings.Repeat("é/~", 12500), "method": "get", "path": "/x", "summary": "", "documentJSON": "{}"}},
		{"ArtifactOwnerAddress", map[string]any{"pointer": "/contracts/0/document/paths/" + strings.Repeat("~1~0", 1200), "operationKey": strings.Repeat("é/~", 12500), "participantId": strings.Repeat("p", 50000), "eventMap": map[string]any{"pointer": "/" + strings.Repeat("~1", 2200), "pinnedRevisionId": "9007199254740993"}}},
		{"ArtifactContractSource", map[string]any{"designId": "9007199254740993", "revisionId": "9223372036854775807", "version": "9007199254740995"}},
	} {
		schema, err := BackendSchema(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		uri := "https://mocker.invalid/" + tc.name
		if err = c.AddResource(uri, schema); err != nil {
			t.Fatal(err)
		}
		compiled, err := c.Compile(uri)
		if err != nil {
			t.Fatal(err)
		}
		if err = compiled.Validate(tc.value); err != nil {
			t.Fatal(tc.name, err)
		}
	}
}

func TestBackendArtifactContractOwnerScopedCommands(t *testing.T) {
	schema, err := BackendSchema("ArtifactPinCommand")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/command"
	if err = c.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(raw string) error {
		var v any
		d := jsonx.NewDecoder(bytes.NewBufferString(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return err
		}
		return compiled.Validate(v)
	}
	const source = "00000000-0000-4000-8000-000000000001"
	api := `{"type":"set_artifact_pin","artifact":{"kind":"api_design","id":"9007199254740993"},"revisionId":"9223372036854775807","apiBindings":[],"editorBindings":[{"selector":{"kind":"state_diagram","diagramId":"d"},"sourceNodeIds":["` + source + `"]}],"reason":"manual"}`
	scenario := strings.Replace(api, `"kind":"api_design"`, `"kind":"design_scenario"`, 1)
	scenario = strings.Replace(scenario, `"apiBindings":[],`, "", 1)
	scenario = strings.Replace(scenario, `"diagramId":"d"`, `"diagramId":"d","embeddedContractId":"copy"`, 1)
	for _, raw := range []string{api, scenario} {
		if err := validate(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{strings.Replace(api, `"apiBindings":[],`, "", 1), strings.Replace(api, `"diagramId":"d"`, `"diagramId":"d","embeddedContractId":"copy"`, 1), strings.Replace(api, `"kind":"state_diagram","diagramId":"d"`, `"kind":"participant","participantId":"p"`, 1), strings.Replace(scenario, `"editorBindings":`, `"apiBindings":[],"editorBindings":`, 1), strings.Replace(scenario, `,"embeddedContractId":"copy"`, "", 1), strings.Replace(api, `"sourceNodeIds":`, `"objectHash":"`+strings.Repeat("a", 64)+`","sourceNodeIds":`, 1)} {
		if validate(raw) == nil {
			t.Fatal("invalid owner command accepted", raw)
		}
	}
}

func TestBackendArtifactContractSourceVersionInt64(t *testing.T) {
	schema, err := BackendSchema("ArtifactContractSource")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/origin-version"
	if err = c.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"designId": "9007199254740993", "revisionId": "9223372036854775807"}
	for _, version := range []string{"0", "1", "9007199254740993", "9223372036854775807"} {
		value["version"] = version
		if err := compiled.Validate(value); err != nil {
			t.Fatal(version, err)
		}
	}
	for _, version := range []any{"01", "-1", "9223372036854775808", "9999999999999999999", jsonx.Number("1"), nil} {
		value["version"] = version
		if compiled.Validate(value) == nil {
			t.Fatal("invalid source version", version)
		}
	}
}
