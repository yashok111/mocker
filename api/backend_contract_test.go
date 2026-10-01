package api

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendSchemaPreservesExactBoundsAndPrivateCopies(t *testing.T) {
	first, err := BackendSchema("BeginBackendImportRequest")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jsonx.Marshal(first)
	if err != nil || !strings.Contains(string(raw), "9223372036854775807") {
		t.Fatalf("schema int64 bound rounded: %s %v", raw, err)
	}
	first["properties"].(map[string]any)["expectedVersion"] = false
	second, err := BackendSchema("BeginBackendImportRequest")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second["properties"].(map[string]any)["expectedVersion"].(map[string]any); !ok {
		t.Fatal("caller mutated the shared contract")
	}
}

func TestBackendSchemaRefusesMissingExternalCyclicAndDeepReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		root any
		defs map[string]any
	}{
		{"missing", map[string]any{"$ref": "#/components/schemas/Missing"}, map[string]any{}},
		{"external", map[string]any{"$ref": "https://unrelated.invalid/schema"}, map[string]any{}},
		{"cycle", map[string]any{"$ref": "#/components/schemas/A"}, map[string]any{"A": map[string]any{"$ref": "#/components/schemas/A"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := expandBackendSchema(tc.root, tc.defs); err == nil {
				t.Fatal("unsafe reference accepted")
			}
		})
	}
	var deep any = true
	for range 80 {
		deep = map[string]any{"nested": deep}
	}
	if _, err := expandBackendSchema(deep, nil); err == nil {
		t.Fatal("unbounded recursion accepted")
	}
}
