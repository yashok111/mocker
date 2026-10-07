package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendSavedViewSchemaStrictVariantsAndBounds(t *testing.T) {
	schema, err := BackendSchema("CreateBackendSavedViewRequest")
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/saved"
	if err := compiler.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	const id = "00000000-0000-4000-8000-000000000001"
	const valid = `{"name":"View","target":{"revisionId":"` + id + `"},"state":{"kind":"flow","scope":{},"filters":{"search":"","accessKind":"","reverseAccessKind":""},"selection":null,"positions":[],"collapsedGroupIds":[]},"idempotencyKey":"key"}`
	validate := func(raw string) error {
		dec := jsonx.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return compiled.Validate(value)
	}
	if err := validate(valid); err != nil {
		t.Fatal("required empty filters rejected", err)
	}
	for _, tc := range []struct{ name, old, next string }{
		{"mixed-target", `"revisionId":"` + id + `"`, `"revisionId":"` + id + `","proposal":{"proposalId":"` + id + `","proposalRevisionId":"` + id + `"}`},
		{"mixed-scope", `"scope":{}`, `"scope":{"datastoreId":"` + id + `"}`},
		{"missing-filter", `"search":"",`, ``},
		{"unknown-root", `"name":"View"`, `"name":"View","other":true`},
		{"unknown-position", `"positions":[]`, `"positions":[{"nodeId":"` + id + `","x":0,"y":0,"css":"red"}]`},
		{"null-array", `"positions":[]`, `"positions":null`},
		{"range-position", `"positions":[]`, `"positions":[{"nodeId":"` + id + `","x":1000001,"y":0}]`},
		{"zero-id", id, `00000000-0000-0000-0000-000000000000`},
		{"uppercase-id", id, `00000000-0000-4000-8000-00000000000A`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate(strings.Replace(valid, tc.old, tc.next, 1)); err == nil {
				t.Fatal("malformed saved-view request accepted")
			}
		})
	}
}

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
