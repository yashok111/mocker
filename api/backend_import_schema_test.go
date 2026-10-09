package api

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestFocusedImportSchemaKeepsOnlySelectedWireKind(t *testing.T) {
	for _, composed := range []bool{false, true} {
		schema, err := BackendImportRecordSchema(composed, "node", "job")
		if err != nil {
			t.Fatal(err)
		}
		properties := schema["properties"].(map[string]any)
		if properties["kind"].(map[string]any)["const"] != "job" {
			t.Fatal("kind discriminator lost")
		}
		_, ref := properties["parentRef"]
		_, key := properties["parentKey"]
		if ref != composed || key == composed {
			t.Fatal("source5/source6 reference modes conflated")
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"$ref"`) || strings.Contains(string(raw), `"upsert_edge"`) {
			t.Fatal("schema is not independently usable/focused")
		}
	}
	if _, err := BackendImportRecordSchema(false, "node", "not-a-kind"); err == nil {
		t.Fatal("unknown kind admitted")
	}
}

func TestImportEvidenceKeysRejectDuplicates(t *testing.T) {
	for _, name := range []string{"BackendNodeInput", "BackendEdgeInput", "BackendComposedNodeInput", "BackendComposedEdgeInput", "BackendImportIdentityMap", "BackendSourceClaimIdentity"} {
		t.Run(name, func(t *testing.T) {
			schema, err := BackendSchema(name)
			if err != nil {
				t.Fatal(err)
			}
			checked := 0
			var visit func(any)
			visit = func(value any) {
				switch v := value.(type) {
				case map[string]any:
					if props, ok := v["properties"].(map[string]any); ok {
						if keys, exists := props["evidenceKeys"]; exists {
							checked++
							compiler := jsonschema.NewCompiler()
							const uri = "https://mocker.invalid/evidence-keys"
							if err := compiler.AddResource(uri, keys); err != nil {
								t.Fatal(err)
							}
							compiled, err := compiler.Compile(uri)
							if err != nil {
								t.Fatal(err)
							}
							if err := compiled.Validate([]any{"proof"}); err != nil {
								t.Fatal(err)
							}
							if compiled.Validate([]any{"proof", "proof"}) == nil {
								t.Errorf("evidenceKeys branch %d accepted duplicate proof keys", checked)
							}
						}
					}
					for _, child := range v {
						visit(child)
					}
				case []any:
					for _, child := range v {
						visit(child)
					}
				}
			}
			visit(schema)
			if checked == 0 {
				t.Fatal("no evidence-key contract exercised")
			}
		})
	}
}
