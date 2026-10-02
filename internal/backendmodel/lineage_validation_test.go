package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"
)

func lineageMappingAttrs(t *testing.T) map[string]jsontext.Value {
	return runtimeAttrs(t, map[string]any{"sources": []any{map[string]any{"kind": "port", "nodeKey": "step", "collection": "inputs", "portKey": "opaque/~0"}}, "destination": map[string]any{"kind": "column", "nodeKey": "column", "facetKey": "sql"}, "transform": map[string]any{"kind": "copy", "description": "Copy shape", "redacted": false}, "analysisStatus": "complete", "gaps": []string{}})
}
func lineageAPIAttrs(t *testing.T) map[string]jsontext.Value {
	return runtimeAttrs(t, map[string]any{"direction": "request", "location": "body", "selector": map[string]any{"kind": "body", "path": []any{map[string]any{"property": "имя/~\""}, map[string]any{"items": true}}}, "mediaType": "application/json", "nativeType": known("string"), "analysisStatus": "complete", "gaps": []string{}})
}
func TestLineageImportStrictShapes(t *testing.T) {
	for _, tc := range []struct {
		kind string
		a    map[string]jsontext.Value
	}{{"field_mapping", lineageMappingAttrs(t)}, {"api_field", lineageAPIAttrs(t)}} {
		c := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "subject", Kind: tc.kind, Name: "subject", Attributes: tc.a, EvidenceKeys: []string{}}}
		if err := validateCommand(c, &ImportSession{Profile: "field-lineage-v1"}); err != nil {
			t.Fatal(err)
		}
		if err := validateCommand(c, &ImportSession{Profile: RuntimeProfile}); err == nil {
			t.Fatal("source3 accepts lineage")
		}
	}
}
func TestLineageImportRejectsMalformedMapping(t *testing.T) {
	cases := map[string]jsontext.Value{
		"sources": jsontext.Value(`null`), "destination": jsontext.Value(`{"kind":"column","nodeKey":"c"}`), "transform": jsontext.Value(`{"kind":"copy","description":"x","redacted":null}`), "analysisStatus": jsontext.Value(`"partial"`), "description": jsontext.Value(`null`), "extra": jsontext.Value(`true`),
	}
	for key, v := range cases {
		t.Run(key, func(t *testing.T) {
			a := lineageMappingAttrs(t)
			a[key] = v
			if err := validateCommand(ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "m", Kind: "field_mapping", Name: "m", Attributes: a}}, &ImportSession{Profile: "field-lineage-v1"}); err == nil {
				t.Fatal("accepted malformed mapping")
			}
		})
	}
}
func TestLineageStrictValueRefs(t *testing.T) {
	valid := []string{`{"kind":"column","nodeId":"11111111-1111-4111-8111-111111111111","facetKey":"sql"}`, `{"kind":"port","nodeId":"11111111-1111-4111-8111-111111111111","collection":"results","portKey":"opaque/~0"}`, `{"kind":"api_field","nodeId":"11111111-1111-4111-8111-111111111111"}`}
	for _, s := range valid {
		var v LineageValueRef
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
	}
	bad := []string{`null`, `{}`, `{"kind":"api_field","kind":"api_field","nodeId":"11111111-1111-4111-8111-111111111111"}`, `{"kind":"api_field","nodeKey":"f"}`, `{"kind":"api_field","nodeId":null}`, `{"kind":"column","nodeId":"11111111-1111-4111-8111-111111111111","facetKey":null}`}
	for _, s := range valid {
		bad = append(bad, strings.TrimSuffix(s, "}")+`,"nodeKey":"x"}`, strings.TrimSuffix(s, "}")+`,"extra":false}`)
	}
	for _, s := range bad {
		var v LineageValueRef
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestLineageSelectorsAndTransforms(t *testing.T) {
	for _, change := range []func(map[string]jsontext.Value){
		func(a map[string]jsontext.Value) {
			a["selector"] = jsontext.Value(`{"kind":"body","path":[{"items":false}]}`)
		},
		func(a map[string]jsontext.Value) {
			a["selector"] = jsontext.Value(`{"kind":"body","path":[{"items":true,"property":"x"}]}`)
		},
		func(a map[string]jsontext.Value) { a["selector"] = jsontext.Value(`{"kind":"name","name":"x"}`) },
		func(a map[string]jsontext.Value) { a["mediaType"] = relationalRaw(t, "Application/json") },
		func(a map[string]jsontext.Value) { a["mediaType"] = relationalRaw(t, "application/json; charset=utf8") },
		func(a map[string]jsontext.Value) { a["responseStatus"] = relationalRaw(t, "200") },
		func(a map[string]jsontext.Value) { a["direction"] = relationalRaw(t, "response") },
		func(a map[string]jsontext.Value) { a["description"] = relationalRaw(t, "bad\x01") },
	} {
		a := lineageAPIAttrs(t)
		change(a)
		if err := validateLineageAttributes("api_field", a, false, false); err == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
	for _, kind := range []string{"copy", "rename", "flatten", "enum_map", "aggregate", "compute", "constant", "unknown_transform"} {
		for _, count := range []int{0, 1, 2, 64, 65} {
			a := maps.Clone(lineageMappingAttrs(t))
			refs := []any{}
			for i := range count {
				refs = append(refs, map[string]any{"kind": "api_field", "nodeKey": string(rune('a' + i))})
			}
			a["sources"] = relationalRaw(t, refs)
			a["transform"] = relationalRaw(t, map[string]any{"kind": kind, "description": "description", "redacted": true})
			if kind == "unknown_transform" {
				a["analysisStatus"] = relationalRaw(t, "partial")
				a["gaps"] = relationalRaw(t, []string{"Unresolved inputs"})
			}
			want := count >= 1 && count <= 64
			if kind == "copy" || kind == "rename" {
				want = count == 1
			}
			if kind == "constant" {
				want = count == 0
			}
			if kind == "unknown_transform" {
				want = count <= 64
			}
			// Use ASCII external keys even at the maximum.
			var source []map[string]any
			_ = json.Unmarshal(a["sources"], &source)
			for i := range source {
				source[i]["nodeKey"] = strings.Repeat("x", i+1)
			}
			a["sources"] = relationalRaw(t, source)
			if got := validateLineageAttributes("field_mapping", a, false, false) == nil; got != want {
				t.Fatalf("%s count %d valid=%v want=%v", kind, count, got, want)
			}
		}
	}
}

func TestLineageStrictRecursiveVariants(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"port","nodeKey":"x","collection":"inputs","portKey":"with space"}`,
		`{"kind":"port","nodeKey":"x","collection":"outputs","portKey":""}`,
		`{"kind":"port","nodeKey":"x","collection":"input","portKey":"x"}`,
		`{"kind":"port","nodeKey":"x","collection":"inputs","portKey":"x","facetKey":"sql"}`,
		`{"kind":"column","nodeKey":"x","facetKey":"sql","portKey":"x"}`,
		`{"kind":"api_field","nodeKey":"x","nodeId":"11111111-1111-4111-8111-111111111111"}`,
	} {
		if err := validateLineageRef([]byte(raw), false); err == nil {
			t.Fatalf("accepted mixed ref %s", raw)
		}
	}
	for _, raw := range []string{
		`{"kind":"body","path":null}`, `{"kind":"body","path":[null]}`, `{"kind":"body","path":[{"property":""}]}`,
		`{"kind":"body","path":[{"items":true,"items":true}]}`, `{"kind":"body","path":[],"name":"x"}`,
	} {
		a := lineageAPIAttrs(t)
		a["selector"] = jsontext.Value(raw)
		if err := validateLineageAttributes("api_field", a, false, false); err == nil {
			t.Fatalf("accepted selector %s", raw)
		}
	}
	a := lineageMappingAttrs(t)
	a["sources"] = jsontext.Value(`[{"kind":"api_field","nodeKey":"x"},{"nodeKey":"x","kind":"api_field"}]`)
	a["transform"] = jsontext.Value(`{"kind":"aggregate","description":"x","redacted":false}`)
	if err := validateLineageAttributes("field_mapping", a, false, false); err == nil {
		t.Fatal("duplicate source accepted")
	}
	a = lineageMappingAttrs(t)
	a["description"] = relationalRaw(t, strings.Repeat("я", 2049))
	if err := validateLineageAttributes("field_mapping", a, false, false); err == nil {
		t.Fatal("description UTF8 byte bound ignored")
	}
	for _, status := range []string{"100", "599", "1XX", "5XX", "default"} {
		a := lineageAPIAttrs(t)
		a["direction"] = relationalRaw(t, "response")
		a["responseStatus"] = relationalRaw(t, status)
		if err := validateLineageAttributes("api_field", a, false, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"99", "600", "2xx", "200 OK", ""} {
		a := lineageAPIAttrs(t)
		a["direction"] = relationalRaw(t, "response")
		a["responseStatus"] = relationalRaw(t, status)
		if err := validateLineageAttributes("api_field", a, false, false); err == nil {
			t.Fatalf("accepted %s", status)
		}
	}
	for _, name := range []string{"x-request-id", "X-Request-ID", "not valid"} {
		a := lineageAPIAttrs(t)
		delete(a, "mediaType")
		a["location"] = relationalRaw(t, "header")
		a["selector"] = relationalRaw(t, map[string]any{"kind": "name", "name": name})
		if got := validateLineageAttributes("api_field", a, false, false) == nil; got != (name == "x-request-id") {
			t.Fatalf("header %s valid=%v", name, got)
		}
	}
}

func TestLineageAPIPropertyPreservesNonemptyUTF8(t *testing.T) {
	for _, name := range []string{" ", "~/\"имя", strings.Repeat("я", 200)} {
		a := lineageAPIAttrs(t)
		a["selector"] = relationalRaw(t, map[string]any{"kind": "body", "path": []any{map[string]any{"property": name}}})
		if err := validateLineageAttributes("api_field", a, false, false); err != nil {
			t.Fatalf("valid property %q: %v", name, err)
		}
	}
	a := lineageAPIAttrs(t)
	a["selector"] = relationalRaw(t, map[string]any{"kind": "body", "path": []any{map[string]any{"property": strings.Repeat("я", 201)}}})
	if err := validateLineageAttributes("api_field", a, false, false); err == nil {
		t.Fatal("character bound ignored")
	}
}
