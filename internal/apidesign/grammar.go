package apidesign

import (
	"embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Official grammars are bundled so accepting an authored contract never depends
// on remote schema resolution. Runtime import remains deliberately tolerant.
//
//go:embed schemas/*.json
var grammarFiles embed.FS

var loadGrammars = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(noRemoteGrammar{})
	compiler.AssertFormat()
	resources := map[string]string{
		"oas30-schema":  "https://spec.openapis.org/oas/3.0/schema/2024-10-18",
		"oas31-schema":  "https://spec.openapis.org/oas/3.1/schema/2022-10-07",
		"oas31-base":    "https://spec.openapis.org/oas/3.1/schema-base/2022-10-07",
		"oas31-dialect": "https://spec.openapis.org/oas/3.1/dialect/base",
		"oas31-meta":    "https://spec.openapis.org/oas/3.1/meta/base",
	}
	for name, uri := range resources {
		raw, err := grammarFiles.ReadFile("schemas/" + name + ".json")
		if err != nil {
			return nil, err
		}
		var resource any
		if err := jsonx.Unmarshal(raw, &resource); err != nil {
			return nil, err
		}
		if name == "oas31-base" {
			// OAS's schema-base pins its own dialect. JSON Schema 2020-12 is
			// also supported locally; both share the same schema grammar.
			defs := resource.(map[string]any)["$defs"].(map[string]any)
			defs["dialect"] = map[string]any{"enum": []any{
				"https://spec.openapis.org/oas/3.1/dialect/base",
				"https://json-schema.org/draft/2020-12/schema",
			}}
		}
		if err := compiler.AddResource(uri, resource); err != nil {
			return nil, err
		}
	}
	out := make(map[string]*jsonschema.Schema, 2)
	for version, name := range map[string]string{"3.0": "oas30-schema", "3.1": "oas31-base"} {
		schema, err := compiler.Compile(resources[name])
		if err != nil {
			return nil, err
		}
		out[version] = schema
	}
	return out, nil
})

type noRemoteGrammar struct{}

func (noRemoteGrammar) Load(uri string) (any, error) {
	return nil, fmt.Errorf("remote grammar unavailable: %s", uri)
}

func validateGrammar(root map[string]any) ([]Diagnostic, error) {
	grammars, err := loadGrammars()
	if err != nil {
		return nil, fmt.Errorf("load OpenAPI grammars: %w", err)
	}
	version, _ := root["openapi"].(string)
	key := "3.1"
	if strings.HasPrefix(version, "3.0.") {
		key = "3.0"
	}
	if err := grammars[key].Validate(root); err != nil {
		if invalid, ok := errors.AsType[*jsonschema.ValidationError](err); ok {
			diagnostics := []Diagnostic{}
			grammarDiagnostics(invalid, &diagnostics)
			return diagnostics, nil
		}
		return nil, err
	}
	return nil, nil
}

func grammarDiagnostics(err *jsonschema.ValidationError, out *[]Diagnostic) {
	// Bound alternative-branch diagnostics without hiding the primary failure.
	if len(*out) >= 50 {
		return
	}
	if len(err.Causes) != 0 {
		for _, cause := range err.Causes {
			grammarDiagnostics(cause, out)
		}
		return
	}
	var pointer strings.Builder
	for _, token := range err.InstanceLocation {
		pointer.WriteByte('/')
		pointer.WriteString(escape(token))
	}
	*out = append(*out, Diagnostic{Pointer: pointer.String(), Message: err.Error(), Severity: "error"})
}
