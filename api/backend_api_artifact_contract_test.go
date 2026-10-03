package api

import (
	"bytes"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendAPIArtifactContractExactIDsAndOperations(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(backendContract, &doc); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, ops := range doc.Paths {
		for method := range ops {
			if strings.Contains(" get post put patch delete ", " "+method+" ") {
				count++
			}
		}
	}
	if count != 185 {
		t.Fatalf("operation count=%d want185", count)
	}
	schema, err := BackendSchema("APIArtifactID")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/id"
	if err = c.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "9", "99", "9007199254740993", "9223372036854775806", "9223372036854775807"} {
		if err = compiled.Validate(id); err != nil {
			t.Fatal("valid exact ID", id, err)
		}
	}
	for _, id := range []string{"", "0", "01", "+1", " 1", "9223372036854775808", "9999999999999999999", "10000000000000000000"} {
		if compiled.Validate(id) == nil {
			t.Fatal("invalid ID admitted", id)
		}
	}
	if compiled.Validate(jsonx.Number("9007199254740993")) == nil {
		t.Fatal("numeric ID admitted")
	}
}

func TestBackendAPIArtifactContractStrictVariants(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const valid = `{"baseRevisionId":"` + id + `","expectedVersion":9007199254740993,"commands":[{"type":"set_api_pin","artifactId":"9007199254740993","revisionId":"9223372036854775807","reason":"manual","bindings":[{"sourceNodeId":"` + id + `","selector":{"jsonPointer":"/components/schemas/A"}}]}]}`
	schema, err := BackendSchema("PreviewBackendAPIPinsRequest")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/pins"
	if err = c.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(raw string) error {
		var value any
		d := jsonx.NewDecoder(bytes.NewBufferString(raw))
		d.UseNumber()
		if err = d.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return compiled.Validate(value)
	}
	if err = validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ old, next string }{
		{`"jsonPointer":"/components/schemas/A"`, `"jsonPointer":"/components/schemas/A","objectKey":"x"`},
		{`"jsonPointer":"/components/schemas/A"`, `"jsonPointer":"/~3"`},
		{`"jsonPointer":"/components/schemas/A"`, `"jsonPointer":null`},
		{`"reason":"manual"`, `"reason":"manual","unknown":true`},
		{`"commands":[`, `"extra":true,"commands":[`},
		{`"revisionId":"9223372036854775807"`, `"revisionId":9223372036854775807`},
	} {
		if validate(strings.Replace(valid, tc.old, tc.next, 1)) == nil {
			t.Fatal("strict request accepted", tc.next)
		}
	}
}
