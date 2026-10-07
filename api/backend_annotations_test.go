package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendAnnotationSchemasStrictCommandsAndFilters(t *testing.T) {
	for _, name := range []string{"BackendAnnotationTarget", "BackendAnnotation", "BackendAnnotationPage", "CreateBackendAnnotationCommand", "UpdateBackendAnnotationCommand", "RemoveBackendAnnotationCommand", "ListBackendAnnotationsQuery"} {
		if _, err := BackendSchema(name); err != nil {
			t.Error(err)
		}
	}
	schema, err := BackendSchema("ApplyBackendProjectCommandsRequest")
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/annotations"
	if err := compiler.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	const id = "0197aaf9-5555-7000-8000-000000000001"
	const valid = `{"expectedVersion":9007199254740993,"idempotencyKey":"note","commands":[{"type":"create_annotation","annotationId":"` + id + `","target":{"recordType":"node","id":"` + id + `"},"body":"note"}]}`
	validate := func(raw string) error {
		d := jsonx.NewDecoder(bytes.NewBufferString(raw))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return compiled.Validate(v)
	}
	if err := validate(valid); err != nil {
		t.Fatalf("annotation union: %v", err)
	}
	for _, raw := range []string{strings.Replace(valid, `"body":"note"`, `"body":"note","name":"mixed"`, 1), strings.Replace(valid, `"body":"note"`, `"body":null`, 1), strings.Replace(valid, `"recordType":"node"`, `"recordType":"node","revisionId":null`, 1), strings.Replace(valid, `"create_annotation"`, `"remove_annotation"`, 1), strings.Replace(valid, `"body":"note"`, `"body":"note","author":"forged"`, 1)} {
		if err := validate(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
