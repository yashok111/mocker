package api

import (
	"bytes"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendDiagramClosedCanonicalContract(t *testing.T) {
	for _, name := range []string{"CreateBackendDiagramRequest", "SaveBackendDiagramRequest", "ForkBackendDiagramRequest", "QueryBackendDiagramRequest", "CompareBackendDiagramsRequest", "CreateBackendDiagramViewRequest", "SaveBackendDiagramViewRequest", "BackendDiagramVersion", "BackendDiagramPage", "BackendDiagramView"} {
		schema, err := BackendSchema(name)
		if err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		uri := "https://mocker.invalid/" + name
		if err = compiler.AddResource(uri, schema); err != nil {
			t.Fatal(err)
		}
		if _, err = compiler.Compile(uri); err != nil {
			t.Fatal(name, err)
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("free-form envelope %s", name)
		}
	}
	schema, err := BackendSchema("CreateBackendDiagramRequest")
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/diagram"
	if err = compiler.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	const id = "10000000-0000-4000-8000-000000000001"
	valid := `{"document":{"format":"backend-diagram-v1","kind":"architecture","target":{"revisionId":"` + id + `"},"payload":{"primarySystemId":"` + id + `","elements":[{"id":"` + id + `","label":"Orders","origin":{"kind":"authored","reason":"Explicit boundary"},"refs":[],"role":"software_system","responsibility":"Orders","technology":"Go"}],"links":[]}},"idempotencyKey":"create"}`
	validate := func(raw string) error {
		var value any
		decoder := jsonx.NewDecoder(bytes.NewBufferString(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return compiled.Validate(value)
	}
	if err = validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{`"kind":"architecture"`, `"kind":"lifecycle"`}, {`"refs":[]`, `"refs":null`}, {`"reason":"Explicit boundary"`, `"reason":"Explicit boundary","author":"forged"`}, {`"links":[]`, `"links":[],"unknown":true`}} {
		if validate(strings.Replace(valid, pair[0], pair[1], 1)) == nil {
			t.Fatal("invalid schema admitted", pair[1])
		}
	}
	var contract struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err = json.Unmarshal(backendContract, &contract); err != nil {
		t.Fatal(err)
	}
	count := 0
	for path, methods := range contract.Paths {
		if strings.Contains(path, "/diagrams") || strings.Contains(path, "/diagram-views") {
			for method := range methods {
				if method == "get" || method == "post" {
					count++
				}
			}
		}
	}
	// 13 at f30edb2 (lifecycle build); 5579099 (B5.2) added
	// GET .../diagram-views/{vid}/versions/{v}/svg and 41ca3c6 (B6.1) added
	// POST .../diagrams/resolve-scope, both deliberate diagram routes.
	if count != 15 {
		t.Fatalf("diagram REST operation count=%d, want 15", count)
	}
}
