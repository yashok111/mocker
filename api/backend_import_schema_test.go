package api

import (
	"encoding/json/v2"
	"strings"
	"testing"
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
