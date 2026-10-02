package backendmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Frozen before the single-parse implementation: preserve this independent
// recursive RawValue oracle, including its original validation and errors.
func legacyCanonicalValue(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty JSON value")
	}
	switch raw[0] {
	case '{':
		var m map[string]jsontext.Value
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		keys := []string{}
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		out := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			kb, _ := json.Marshal(k)
			out = append(out, kb...)
			out = append(out, ':')
			vb, err := legacyCanonicalValue(m[k])
			if err != nil {
				return nil, err
			}
			out = append(out, vb...)
		}
		return append(out, '}'), nil
	case '[':
		var a []jsontext.Value
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		out := []byte{'['}
		for i, x := range a {
			if i > 0 {
				out = append(out, ',')
			}
			b, err := legacyCanonicalValue(x)
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
		}
		return append(out, ']'), nil
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return json.Marshal(s)
	default:
		if bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
			return raw, nil
		}
		if !jsontext.Value(raw).IsValid() {
			return nil, fmt.Errorf("invalid JSON number")
		}
		return raw, nil
	}
}

func TestCanonicalValueCompatibility(t *testing.T) {
	cases := []struct {
		name, raw string
		valid     bool
	}{
		{"nested", ` {"z":[{"β":2,"a":1},[false,null,true]],"a":{"b":[],"a":{}}} `, true},
		{"empty_object", `{}`, true}, {"empty_array", `[]`, true}, {"null", `null`, true},
		{"booleans", `[true,false]`, true},
		{"numbers", `[9007199254740993,-9223372036854775808,9223372036854775807,1e+02,1E-02,-0,1.2300,1e9999]`, true},
		{"strings", `{"雪":"é e\u0301 \ud83d\ude00 <>& \u2028\u2029","a":"\u0061\/\b\f\n\r\t\u0000\\\""}`, true},
		{"key_order", `{"雪":1,"é":2,"e\u0301":3,"😀":4,"a":5,"\u0000":6}`, true},
		{"outer_unicode_space", "\u00a0\u2003\u0085{\"a\": -0}\u2028\u3000", true},
		{"outer_number_space", "\u00a01e+02\u3000", true},
		{"duplicate", `{"a":1,"a":2}`, false},
		{"decoded_duplicate", `{"a":1,"\u0061":2}`, false},
		{"nested_duplicate", `[{"x":{"a":1,"\u0061":2}}]`, false},
		{"invalid_utf8", string([]byte{'"', 0xff, '"'}), false},
		{"invalid_key_utf8", string([]byte{'{', '"', 0xff, '"', ':', '0', '}'}), false},
		{"unpaired_surrogate", `"\ud800"`, false},
		{"bad_escape", `"\x20"`, false}, {"control", "\"a\nb\"", false},
		{"unfinished_string", `"x`, false}, {"unfinished_array", `[1`, false},
		{"unfinished_object", `{"a":`, false}, {"trailing_comma", `[1,]`, false},
		{"object_trailing_comma", `{"a":1,}`, false}, {"missing_colon", `{"a" 1}`, false},
		{"leading_zero", `01`, false}, {"plus", `+1`, false}, {"bare_minus", `-`, false},
		{"bad_exponent", `1e+`, false}, {"bad_decimal", `1.`, false}, {"nan", `NaN`, false},
		{"root_values", `{} []`, false}, {"root_numbers", `1 2`, false}, {"root_strings", `"a" "b"`, false},
		{"root_literal_suffix", `nullx`, false}, {"inner_unicode_space", "[\u00a00]", false},
		{"empty", ``, false}, {"space_only", " \t\r\n\u00a0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCanonicalCompatibility(t, []byte(tc.raw), tc.valid) })
	}
}

func assertCanonicalCompatibility(t *testing.T, raw []byte, valid bool) {
	t.Helper()
	want, oldErr := legacyCanonicalValue(raw)
	got, err := canonicalValue(raw)
	if (oldErr == nil) != valid || (err == nil) != valid {
		t.Fatalf("admission: valid=%v old=%v new=%v", valid, oldErr, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical bytes differ: got %q want %q", got, want)
	}
	// The public canonicalJSON entry point always marshals first. Its malformed
	// raw values must keep the exact marshal error, before canonicalValue runs.
	if !valid {
		_, marshalErr := json.Marshal(jsontext.Value(raw), json.Deterministic(true))
		_, publicErr := canonicalJSON(jsontext.Value(raw))
		if fmt.Sprint(publicErr) != fmt.Sprint(marshalErr) || fmt.Sprintf("%T", publicErr) != fmt.Sprintf("%T", marshalErr) {
			t.Fatalf("public error changed: got %T %v want %T %v", publicErr, publicErr, marshalErr, marshalErr)
		}
	}
}

func TestCanonicalValueDepthAndLargeInput(t *testing.T) {
	// Characterized against the old implementation before replacement, including
	// the exact 10000-container default boundary in Go1.27.1. Keep fixed expected
	// bytes here: rerunning the quadratic legacy oracle at this depth adds 12s
	// under race to every suite. The bounded case below still exercises it.
	for _, depth := range []int{9999, 10000, 10001} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			raw := []byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth))
			got, err := canonicalValue(raw)
			if depth > 10000 {
				if err == nil || got != nil {
					t.Fatal("accepted excessive depth")
				}
				return
			}
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("depth %d: changed bytes or admission: %v", depth, err)
			}
		})
	}
	t.Run("nested_objects_oracle", func(t *testing.T) {
		raw := strings.Repeat(`{"z":0,"a":[`, 32) + `"leaf"` + strings.Repeat(`]}`, 32)
		assertCanonicalCompatibility(t, []byte(raw), true)
	})
	t.Run("large_string", func(t *testing.T) {
		value := strings.Repeat("界<>&", 1<<17)
		raw := []byte(`{"z":"` + value + `","a":[1e+02,-0]}`)
		want := []byte(`{"a":[1e+02,-0],"z":"` + value + `"}`)
		got, err := canonicalValue(raw)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("large value changed bytes or admission: %v", err)
		}
	})
	t.Run("wide_graph", func(t *testing.T) { assertCanonicalCompatibility(t, canonicalBenchmarkInput(t), true) })
}

func canonicalBenchmarkInput(tb testing.TB) []byte {
	tb.Helper()
	// Wide graph-like records with nested attributes and retained numeric text;
	// construct once outside the timed loop and keep identical across runs.
	nodes := make([]any, 256)
	for i := range nodes {
		nodes[i] = map[string]any{
			"id": fmt.Sprintf("node-%04d", i), "kind": "column", "name": "Customer <id>",
			"attributes": map[string]any{"native": map[string]any{"precision": jsontext.Value(`9007199254740993`), "scale": jsontext.Value(`1.2300`)}, "nullable": false, "tags": []string{"雪", "customer", "indexed"}},
			"evidence":   []any{map[string]any{"source": map[string]any{"file": "schema.sql", "lines": []int{i, i + 1}}, "status": "explicit"}},
		}
	}
	raw, err := json.Marshal(map[string]any{"nodes": nodes, "edges": []any{}, "metadata": map[string]any{"version": 1, "status": "ready"}}, json.Deterministic(true))
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func BenchmarkCanonicalValueGraph(b *testing.B) {
	raw := canonicalBenchmarkInput(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		if _, err := canonicalValue(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// Fixed bytes and SHA-256 captured from the pre-change implementation, not
// calculated by the oracle. The fixture also anchors the durable import path.
func TestCanonicalValueGoldens(t *testing.T) {
	for _, tc := range []struct{ name, raw, want, hash string }{
		{"numbers", `{"z":[9007199254740993,-9223372036854775808,9223372036854775807,1e+02,1E-02,-0,1.2300],"a":null}`, "{\"a\":null,\"z\":[9007199254740993,-9223372036854775808,9223372036854775807,1e+02,1E-02,-0,1.2300]}", "2378181e55f864b153e1a99fc900682074ee2b9e0c1c91b701164669faf5075b"},
		{"unicode", `{"雪":"é e\u0301 \ud83d\ude00 <>& \u2028\u2029","a":"\u0061\/\b\f\n\r\t\u0000\\\""}`, "{\"a\":\"a/\\b\\f\\n\\r\\t\\u0000\\\\\\\"\",\"雪\":\"é é 😀 <>& \u2028\u2029\"}", "acf9e907e27734a756f4db12329c9896a8398bd7207b4c53b999f1513441b46e"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalValue([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want || fmt.Sprintf("%x", sha256.Sum256(got)) != tc.hash {
				t.Fatalf("golden bytes/hash changed: %q %x", got, sha256.Sum256(got))
			}
		})
	}
}

// UTF-8/Go string ordering differs from UTF-16/JCS ordering here: U+E000
// precedes the emoji. Both spellings must keep the established hash bytes.
func TestCanonicalValueUnicodeKeyOrder(t *testing.T) {
	const want = "{\"\ue000\":2,\"😀\":1}"
	for _, raw := range []string{`{"😀":1,"\ue000":2}`, `{"\ud83d\ude00":1,"\ue000":2}`} {
		t.Run(raw, func(t *testing.T) {
			assertCanonicalCompatibility(t, []byte(raw), true)
			got, err := canonicalValue([]byte(raw))
			if err != nil || string(got) != want {
				t.Fatalf("Unicode key order: got %q want %q err %v", got, want, err)
			}
		})
	}
}

func TestCanonicalImportFixtureGolden(t *testing.T) {
	commands := fixtureCommands(&ImportSession{RepositoryID: "repo-fixed", SnapshotID: "snapshot-fixed"})
	got, err := canonicalJSON(commands)
	if err != nil {
		t.Fatal(err)
	}
	const want = "[{\"node\":{\"attributes\":{},\"evidenceKeys\":[\"proof\"],\"externalKey\":\"handler\",\"kind\":\"handler\",\"name\":\"Handle\"},\"op\":\"upsert_node\"},{\"evidence\":{\"explanation\":\"\",\"externalKey\":\"proof\",\"method\":\"ast\",\"source\":{\"contentHash\":\"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\",\"file\":\"main.go\",\"repositoryId\":\"repo-fixed\",\"snapshotId\":\"snapshot-fixed\"},\"status\":\"explicit\",\"subjectKey\":\"handler\",\"subjectType\":\"node\"},\"op\":\"upsert_evidence\"}]"
	const wantHash = "6dcf611bfb6cb6e2a7cad8f74aa3a5e108563614913627d9bbe40b24e33828e9"
	if string(got) != want || fmt.Sprintf("%x", sha256.Sum256(got)) != wantHash {
		t.Fatalf("fixture bytes/hash changed: %q %x", got, sha256.Sum256(got))
	}
	hash, err := ImportBatchHash(commands)
	if err != nil || hash != wantHash {
		t.Fatalf("batch hash %q %v", hash, err)
	}
}
