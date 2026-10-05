package api

import (
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestBackendInteractionsContract(t *testing.T) {
	schema, err := BackendSchema("CreateBackendDiagramRequest")
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://mocker.invalid/interactions", schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("https://mocker.invalid/interactions")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../internal/backendmodel/testdata/diagrams/interaction.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if err = compiled.Validate(map[string]any{"document": doc, "idempotencyKey": "test"}); err != nil {
		t.Fatal(err)
	}
	doc["payload"].(map[string]any)["unknown"] = true
	if compiled.Validate(map[string]any{"document": doc, "idempotencyKey": "test"}) == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err = BackendSchema("BuildBackendInteractionsRequest"); err != nil {
		t.Fatal(err)
	}
}
