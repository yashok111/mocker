package api

import (
	"bytes"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/jsonx"
	"strings"
	"testing"
)

func lineageSchemaValidator(t *testing.T, name string) func(string) error {
	t.Helper()
	schema, err := BackendSchema(name)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/lineage"
	if err = c.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	return func(raw string) error {
		d := jsonx.NewDecoder(bytes.NewBufferString(raw))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return compiled.Validate(v)
	}
}
func TestBackendLineageSchemaStrictRefs(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	for _, name := range []string{"BackendLineageValueRef", "BackendImportLineageValueRef"} {
		t.Run(name, func(t *testing.T) {
			v := lineageSchemaValidator(t, name)
			key, value := "nodeId", id
			if strings.Contains(name, "Import") {
				key, value = "nodeKey", "column:amount"
			}
			for _, raw := range []string{`{"kind":"api_field","` + key + `":"` + value + `"}`, `{"kind":"column","` + key + `":"` + value + `","facetKey":"sql"}`, `{"kind":"port","` + key + `":"` + value + `","collection":"parameters","portKey":"opaque/key"}`} {
				if err := v(raw); err != nil {
					t.Fatal(err)
				}
			}
			for _, raw := range []string{`{"kind":"api_field","` + key + `":"` + value + `","facetKey":"sql"}`, `{"kind":"column","` + key + `":"` + value + `"}`, `{"kind":"port","` + key + `":"` + value + `","collection":"parameters","portKey":"has space"}`, `{"kind":"api_field","nodeId":"` + id + `","nodeKey":"key"}`} {
				if v(raw) == nil {
					t.Fatal("invalid reference accepted", raw)
				}
			}
		})
	}
	v := lineageSchemaValidator(t, "QueryBackendLineageRequest")
	base := `{"revisionId":"` + id + `","seed":{"kind":"api_field","nodeId":"` + id + `"},"direction":"forward"`
	if err := v(base + `}`); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{`,"proposal":{}}`, `,"limit":0}`, `,"limit":101}`, `,"maxDepth":33}`, `,"maxDepth":null}`} {
		if v(base+suffix) == nil {
			t.Fatal("invalid query accepted", suffix)
		}
	}
}

func TestBackendLineageMappingAndAPISelectorContracts(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	persisted := `{"kind":"api_field","nodeId":"` + id + `"}`
	imported := `{"kind":"api_field","nodeKey":"request"}`
	for _, tc := range []struct{ name, wire, other string }{{"BackendLineageMappingAttributes", persisted, imported}, {"BackendImportLineageMappingAttributes", imported, persisted}} {
		t.Run(tc.name, func(t *testing.T) {
			v := lineageSchemaValidator(t, tc.name)
			base := `{"sources":[` + tc.wire + `],"destination":` + tc.wire + `,"transform":{"kind":"copy","description":"Copy value","redacted":false},"analysisStatus":"complete","gaps":[]}`
			if err := v(base); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{strings.Replace(base, `"destination":`+tc.wire, `"destination":`+tc.other, 1), strings.Replace(base, `"sources":[`+tc.wire+`]`, `"sources":[]`, 1), strings.Replace(base, `"sources":[`+tc.wire+`]`, `"sources":[`+tc.wire+`,`+tc.wire+`]`, 1), strings.Replace(base, `"redacted":false`, `"redacted":null`, 1), strings.Replace(base, `"kind":"copy"`, `"kind":"unknown_transform"`, 1), strings.Replace(base, `"gaps":[]`, `"gaps":["missing"]`, 1)} {
				if v(bad) == nil {
					t.Fatal("invalid mapping accepted", bad)
				}
			}
			constant := strings.Replace(strings.Replace(base, `"sources":[`+tc.wire+`]`, `"sources":[]`, 1), `"kind":"copy"`, `"kind":"constant"`, 1)
			if err := v(constant); err != nil {
				t.Fatal(err)
			}
		})
	}
	v := lineageSchemaValidator(t, "BackendAPIFieldAttributes")
	body := `{"direction":"response","location":"body","selector":{"kind":"body","path":[{"property":"items"},{"items":true},{"property":"total"}]},"responseStatus":"200","mediaType":"application/json","nativeType":{"status":"unknown","reason":"schema absent"},"analysisStatus":"partial","gaps":["schema absent"]}`
	if err := v(body); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(body, `{"items":true}`, `{"items":false}`, 1), strings.Replace(body, `{"items":true}`, `{"items":true,"property":"x"}`, 1), strings.Replace(body, `"kind":"body","path"`, `"kind":"name","path"`, 1), strings.Replace(body, `"mediaType":"application/json"`, `"mediaType":"Application/json"`, 1), strings.Replace(body, `"direction":"response"`, `"direction":"request"`, 1), strings.Replace(body, `"nativeType":{"status":"unknown","reason":"schema absent"}`, `"nativeType":{"status":"known","value":"string","reason":"extra"}`, 1)} {
		if v(bad) == nil {
			t.Fatal("invalid API field accepted", bad)
		}
	}
}

func TestBackendLineageProfileScopeAndTransition(t *testing.T) {
	scope := lineageSchemaValidator(t, "BackendGraphScope")
	if err := scope(`{"profile":"field-lineage-v1","status":"partial","gaps":["bounded collector"]}`); err != nil {
		t.Fatal(err)
	}
	transition := lineageSchemaValidator(t, "BackendProfileExtension")
	if err := transition(`{"fromProfile":"runtime-flow-v1","toProfile":"field-lineage-v1"}`); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"foundation-graph-v1", "relational-graph-v1", "field-lineage-v1"} {
		if transition(`{"fromProfile":"`+from+`","toProfile":"field-lineage-v1"}`) == nil {
			t.Fatal("invalid extension allowed", from)
		}
	}
}
