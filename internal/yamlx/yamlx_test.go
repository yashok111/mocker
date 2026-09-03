package yamlx

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestToJSON_openAPIShape(t *testing.T) {
	t.Parallel()
	raw := []byte(`# a comment first
openapi: 3.0.3
info: {title: t, version: "1.0"}
paths:
  /widgets:
    get:
      responses:
        200:            # an integer key in YAML, a string key in JSON
          description: ok
        "404":
          description: missing
      parameters:
        - name: limit
          in: query
          schema: {type: integer, minimum: 0, maximum: 9007199254740993}
`)
	out, err := ToJSON(raw)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	paths := doc["paths"].(map[string]any)
	get := paths["/widgets"].(map[string]any)["get"].(map[string]any)
	responses := get["responses"].(map[string]any)
	if _, ok := responses["200"]; !ok {
		t.Errorf("integer key 200 was not stringified: %v", responses)
	}
	if _, ok := responses["404"]; !ok {
		t.Errorf("quoted key lost: %v", responses)
	}
	// The big integer survives as a JSON number, not a float that rounds.
	if !contains(string(out), "9007199254740993") {
		t.Errorf("large integer rounded: %s", out)
	}
}

func TestToJSON_refusesNonYAMLAndStreams(t *testing.T) {
	t.Parallel()
	if _, err := ToJSON([]byte("openapi: 3.0.0\n---\nopenapi: 3.0.0\n")); !errors.Is(err, ErrMultipleDocuments) {
		t.Errorf("stream: err = %v, want ErrMultipleDocuments", err)
	}
	if _, err := ToJSON([]byte("a: [unterminated")); !errors.Is(err, ErrNotYAML) {
		t.Errorf("garbage: err = %v, want ErrNotYAML", err)
	}
	if _, err := ToJSON([]byte("? [1, 2]\n: seq-key\n")); !errors.Is(err, ErrNotYAML) {
		t.Errorf("sequence key: err = %v, want ErrNotYAML", err)
	}
}

func TestToJSON_jsonIsYAML(t *testing.T) {
	t.Parallel()
	// JSON is a YAML subset: the converter must not mangle a document that
	// was JSON all along (internal/openapi tries JSON first, but the
	// property still holds and is cheap to pin).
	out, err := ToJSON([]byte(`{"openapi":"3.1.0","x":[1,true,null,"s"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil || v["openapi"] != "3.1.0" {
		t.Errorf("round trip: %v %v", err, v)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestToJSON_secondDocumentThatDoesNotParseIsStillRefused: a parse error on
// the second document used to be swallowed (only a CLEAN second decode
// counted as "multiple documents"), importing the first half of a file the
// author meant as one.
func TestToJSON_secondDocumentThatDoesNotParseIsStillRefused(t *testing.T) {
	t.Parallel()
	if _, err := ToJSON([]byte("openapi: 3.0.0\n---\n{bad\n")); !errors.Is(err, ErrMultipleDocuments) {
		t.Errorf("stream with a broken second document: err = %v, want ErrMultipleDocuments", err)
	}
}

// TestToJSON_keepsScalarTextJSONCares About: yaml.v3's own resolution of a
// plain scalar is YAML's, and the converter used to inherit it — `1.0`
// became `1`, an unquoted date became an RFC 3339 instant. Walking the
// node tree keeps the author's text where JSON can carry it.
func TestToJSON_keepsScalarText(t *testing.T) {
	t.Parallel()
	out, err := ToJSON([]byte("openapi: 3.0.0\nfloat: 1.0\ndate: 2024-01-01\nhex: 0x1F\nbig: 9007199254740993\nflag: true\nnothing: ~\nbase: &base\n  own: 0\n  inherited: 2\nmerge:\n  <<: *base\n  own: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"float":1.0`, `"date":"2024-01-01"`, `"hex":31`, `"big":9007199254740993`, `"flag":true`, `"nothing":null`, `"own":1`, `"inherited":2`} {
		if !contains(string(out), want) {
			t.Errorf("output lacks %s: %s", want, out)
		}
	}
	if _, err := ToJSON([]byte("openapi: 3.0.0\nx: .inf\n")); !errors.Is(err, ErrNotYAML) {
		t.Errorf(".inf: err = %v, want ErrNotYAML", err)
	}
}
