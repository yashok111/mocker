package jsonpatch

import (
	"errors"
	"strings"
	"testing"
)

// mustParse is a test helper: Parse b and fail the test immediately on error,
// so every test below can read as "given this patch" rather than repeating
// the same three lines of error handling.
func mustParse(t *testing.T, b []byte) Patch {
	t.Helper()
	p, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse(%s) returned unexpected error: %v", b, err)
	}
	return p
}

// TestParse_NilEmptyNullMeanNoPatch covers B4: nil, an empty byte slice, a
// JSON `[]`, and a JSON `null` (with and without surrounding whitespace) are
// all exactly equivalent to "no patch" — Parse succeeds and the result
// reports Empty() true. A naive `len(raw) != 0` presence test would treat
// jsonx.RawMessage's four-byte decoding of a wire `null` as a real patch;
// this is the test that catches that mistake.
func TestParse_NilEmptyNullMeanNoPatch(t *testing.T) {
	cases := map[string][]byte{
		"nil bytes":           nil,
		"empty slice":         {},
		"empty string":        []byte(""),
		"whitespace only":     []byte("   \n\t "),
		"empty array":         []byte("[]"),
		"empty array, spaced": []byte("  [] "),
		"json null":           []byte("null"),
		"json null, spaced":   []byte("  null  \n"),
		"json null, tabs":     []byte("\tnull\t"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", raw, err)
			}
			if !p.Empty() {
				t.Fatalf("Parse(%q).Empty() = false, want true", raw)
			}
		})
	}
}

// TestParse_RejectsUnsupportedOpsByName covers B1: move, copy and test are
// rejected BY NAME, and the error names the offending operation because the
// admin ingress gate renders it verbatim into a 400 body ("unsupported patch
// operation \"move\"") — a generic decode failure would be indistinguishable
// from the decoder's own "unknown field" 400 to any check reading the body.
func TestParse_RejectsUnsupportedOpsByName(t *testing.T) {
	for _, name := range []string{"move", "copy", "test"} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`[{"op":"` + name + `","path":"/x","from":"/y","value":1}]`)
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("Parse accepted unsupported op %q", name)
			}
			if !errors.Is(err, errInvalidPatch) {
				t.Errorf("error does not wrap errInvalidPatch: %v", err)
			}
			want := `unsupported patch operation "` + name + `"`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err.Error(), want)
			}
		})
	}
}

// TestParse_RejectsRootPath covers B5's third clause: the empty pointer (the
// document root) is rejected by Parse for every one of the three supported
// ops, because Apply's map[string]any return type cannot express a root
// replaced wholesale. The message assertion is deliberate, not decoration:
// splitPointer's very next check ("must start with \"/\"") also rejects the
// empty string, for the unrelated reason that it has no leading slash — so a
// bare errors.Is(err, errInvalidPatch) would still pass with the dedicated
// root check (pointer.go's "document root" branch) deleted entirely. Pinning
// the message text is what makes removing that branch fail this test.
func TestParse_RejectsRootPath(t *testing.T) {
	for _, op := range []string{"add", "remove", "replace"} {
		t.Run(op, func(t *testing.T) {
			raw := []byte(`[{"op":"` + op + `","path":"","value":1}]`)
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("Parse accepted the document root as a target for %q", op)
			}
			if !errors.Is(err, errInvalidPatch) {
				t.Errorf("error does not wrap errInvalidPatch: %v", err)
			}
			const want = "document root"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the document root as the specific reason (want substring %q) — "+
					"the generic \"must start with /\" check would also fire here and this test must not accept that", err.Error(), want)
			}
		})
	}
}

// TestParse_StrictEscaping covers B5's first clause: escaping is strict RFC
// 6901 — only "~0" and "~1" decode, and a "~" followed by anything else (or
// nothing) is refused, unlike the tree's own lenient unescapePointerToken
// (internal/openapi/resolver.go), which reads a document this package never
// touches.
func TestParse_StrictEscaping(t *testing.T) {
	t.Run("valid escapes decode and apply against the literal keys", func(t *testing.T) {
		// "~0" -> "~", "~1" -> "/": the token "a~0b" names the property
		// "a~b", and "c~1d" names "c/d" — both are legal, if unusual, JSON
		// object keys.
		p := mustParse(t, []byte(`[{"op":"add","path":"/a~0b/c~1d","value":"ok"}]`))
		doc := map[string]any{"a~b": map[string]any{}}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		inner, ok := out["a~b"].(map[string]any)
		if !ok {
			t.Fatalf("out[%q] is not a map: %#v", "a~b", out["a~b"])
		}
		if inner["c/d"] != "ok" {
			t.Errorf("inner[%q] = %#v, want %q", "c/d", inner["c/d"], "ok")
		}
	})

	invalid := map[string]string{
		"unknown escape ~2": "/a~2b",
		"trailing tilde":    "/a~",
		"bare tilde token":  "/~",
	}
	for name, path := range invalid {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`[{"op":"add","path":"` + path + `","value":1}]`)
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("Parse accepted invalid escape in path %q", path)
			}
			if !errors.Is(err, errInvalidPatch) {
				t.Errorf("error does not wrap errInvalidPatch: %v", err)
			}
		})
	}
}

// TestParse_DashOnlyValidForAdd covers B5's second clause together with its
// own negative space: "-" is how add spells "append", and is refused for
// remove and replace at Parse time — a decision fully determined by the op
// name and the pointer's own text, with no document involved.
func TestParse_DashOnlyValidForAdd(t *testing.T) {
	t.Run("add at - parses", func(t *testing.T) {
		if _, err := Parse([]byte(`[{"op":"add","path":"/required/-","value":"x"}]`)); err != nil {
			t.Fatalf("Parse rejected add at \"-\": %v", err)
		}
	})
	for _, op := range []string{"remove", "replace"} {
		t.Run(op+" at - is rejected", func(t *testing.T) {
			raw := []byte(`[{"op":"` + op + `","path":"/required/-","value":1}]`)
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("Parse accepted %q at \"-\"", op)
			}
			if !errors.Is(err, errInvalidPatch) {
				t.Errorf("error does not wrap errInvalidPatch: %v", err)
			}
		})
	}
}

// TestParse_BoundsPatchBytes covers B6's first bound: a patch over 64 KiB is
// refused identically at Parse regardless of caller (the admin ingress gate
// and the runtime build share this one check).
func TestParse_BoundsPatchBytes(t *testing.T) {
	huge := `[{"op":"add","path":"/x","value":"` + strings.Repeat("a", MaxPatchBytes) + `"}]`
	_, err := Parse([]byte(huge))
	if err == nil {
		t.Fatal("Parse accepted a patch over the byte limit")
	}
	if !errors.Is(err, errInvalidPatch) {
		t.Errorf("error does not wrap errInvalidPatch: %v", err)
	}

	small := []byte(`[{"op":"add","path":"/x","value":"ok"}]`)
	if _, err := Parse(small); err != nil {
		t.Fatalf("Parse rejected a patch well under the byte limit: %v", err)
	}
}

// TestParse_BoundsOperationCount covers B6's second bound: at most 64
// operations, checked exactly at the boundary in both directions so an
// off-by-one in the comparison shows up.
func TestParse_BoundsOperationCount(t *testing.T) {
	build := func(n int) []byte {
		var b strings.Builder
		b.WriteByte('[')
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"op":"add","path":"/x","value":1}`)
		}
		b.WriteByte(']')
		return []byte(b.String())
	}

	if _, err := Parse(build(maxOps)); err != nil {
		t.Fatalf("Parse rejected exactly %d operations: %v", maxOps, err)
	}

	_, err := Parse(build(maxOps + 1))
	if err == nil {
		t.Fatalf("Parse accepted %d operations, one past the limit", maxOps+1)
	}
	if !errors.Is(err, errInvalidPatch) {
		t.Errorf("error does not wrap errInvalidPatch: %v", err)
	}
}

// TestApply_ThreeSupportedOps exercises add, remove and replace end to end,
// including add's "-" append and add-over-an-existing-key's REPLACE
// semantics (B5's fourth clause).
func TestApply_ThreeSupportedOps(t *testing.T) {
	t.Run("add creates a new property", func(t *testing.T) {
		p := mustParse(t, []byte(`[{"op":"add","path":"/properties/x","value":{"type":"string"}}]`))
		doc := map[string]any{"properties": map[string]any{}}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		props := out["properties"].(map[string]any)
		x, ok := props["x"].(map[string]any)
		if !ok || x["type"] != "string" {
			t.Errorf("properties.x = %#v, want {type: string}", props["x"])
		}
	})

	t.Run("add over an existing key replaces it", func(t *testing.T) {
		p := mustParse(t, []byte(`[{"op":"add","path":"/type","value":"integer"}]`))
		doc := map[string]any{"type": "string"}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if out["type"] != "integer" {
			t.Errorf(`out["type"] = %#v, want "integer"`, out["type"])
		}
	})

	t.Run("add at - appends to a non-empty array", func(t *testing.T) {
		p := mustParse(t, []byte(`[{"op":"add","path":"/required/-","value":"z"}]`))
		doc := map[string]any{"required": []any{"x", "y"}}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		got := out["required"].([]any)
		want := []any{"x", "y", "z"}
		if len(got) != len(want) {
			t.Fatalf("required = %#v, want %#v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("required[%d] = %#v, want %#v", i, got[i], want[i])
			}
		}
	})

	t.Run("add at - appends to an empty array", func(t *testing.T) {
		p := mustParse(t, []byte(`[{"op":"add","path":"/required/-","value":"x"}]`))
		doc := map[string]any{"required": []any{}}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		got := out["required"].([]any)
		if len(got) != 1 || got[0] != "x" {
			t.Errorf("required = %#v, want [x]", got)
		}
	})

	t.Run("replace overwrites an existing property", func(t *testing.T) {
		p := mustParse(t, []byte(`[{"op":"replace","path":"/maximum","value":100}]`))
		doc := map[string]any{"maximum": float64(10)}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if out["maximum"] != float64(100) {
			t.Errorf(`out["maximum"] = %#v, want 100`, out["maximum"])
		}
	})

	t.Run("remove deletes an existing property", func(t *testing.T) {
		p := mustParse(t, []byte(`[{"op":"remove","path":"/description"}]`))
		doc := map[string]any{"description": "old", "type": "string"}
		out, err := Apply(p, doc)
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if _, ok := out["description"]; ok {
			t.Errorf("out still has %q after remove: %#v", "description", out["description"])
		}
		if out["type"] != "string" {
			t.Errorf("remove disturbed an unrelated key: %#v", out)
		}
	})
}

// TestApply_DoesNotWriteThroughInputDocument is the check the section brief
// calls out as the one that matters most: build a document with a nested
// map, apply a patch that touches a nested key, and assert the ORIGINAL
// document is unchanged at every level — proving Apply's copy is deep, not a
// shallow top-level copy that still shares the nested maps a concurrent
// request might be reading.
func TestApply_DoesNotWriteThroughInputDocument(t *testing.T) {
	doc := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		},
	}

	p := mustParse(t, []byte(
		`[{"op":"add","path":"/properties/user/properties/email","value":{"type":"string"}}]`,
	))

	out, err := Apply(p, doc)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	// The new property must be visible on the RESULT.
	userOut := out["properties"].(map[string]any)["user"].(map[string]any)
	propsOut := userOut["properties"].(map[string]any)
	if _, ok := propsOut["email"]; !ok {
		t.Fatalf("out is missing the patched property: %#v", propsOut)
	}

	// The ORIGINAL document, at every level the patch walked through, must
	// be exactly as it was before Apply was called.
	userIn := doc["properties"].(map[string]any)["user"].(map[string]any)
	propsIn := userIn["properties"].(map[string]any)
	if _, ok := propsIn["email"]; ok {
		t.Fatalf("Apply wrote the new property into the INPUT document: %#v", propsIn)
	}
	if len(propsIn) != 1 {
		t.Fatalf("input properties map grew: %#v", propsIn)
	}
	if _, ok := propsIn["name"]; !ok {
		t.Fatalf("input document's untouched sibling property was disturbed: %#v", propsIn)
	}

	// Mutating the RESULT's nested maps directly must not reach the input —
	// the strongest proof available that they do not share backing storage.
	propsOut["injected"] = "poison"
	if _, ok := propsIn["injected"]; ok {
		t.Fatalf("mutating the output reached the input document: %#v", propsIn)
	}
}

// TestApply_SchemaDependentErrorsDropTheWholePatch covers B7: everything
// that needs the document — an absent parent, an absent remove/replace
// target, an out-of-range array index, a path that runs through a
// non-container node — is an Apply error, never a Parse error, and Apply's
// atomicity (B2) means every one of these returns a nil map, never a
// partially patched one.
func TestApply_SchemaDependentErrorsDropTheWholePatch(t *testing.T) {
	cases := map[string]struct {
		patch string
		doc   map[string]any
	}{
		"add whose parent is absent": {
			patch: `[{"op":"add","path":"/properties/x/type","value":"string"}]`,
			doc:   map[string]any{"properties": map[string]any{}},
		},
		"replace of an absent key": {
			patch: `[{"op":"replace","path":"/missing","value":1}]`,
			doc:   map[string]any{"present": true},
		},
		"remove of an absent key": {
			patch: `[{"op":"remove","path":"/missing"}]`,
			doc:   map[string]any{"present": true},
		},
		"out-of-range array index": {
			patch: `[{"op":"replace","path":"/items/5","value":"x"}]`,
			doc:   map[string]any{"items": []any{"a", "b"}},
		},
		"path runs into a non-container node": {
			patch: `[{"op":"add","path":"/type/nested","value":1}]`,
			doc:   map[string]any{"type": "string"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := mustParse(t, []byte(tc.patch))
			out, err := Apply(p, tc.doc)
			if err == nil {
				t.Fatalf("Apply accepted a schema-dependent failure: got %#v", out)
			}
			if out != nil {
				t.Errorf("Apply returned a non-nil map alongside an error: %#v", out)
			}
			if !errors.Is(err, errApply) {
				t.Errorf("error does not wrap errApply: %v", err)
			}
		})
	}
}

// TestApply_AtomicOnPartialFailure covers D1(3)/B2 directly: a patch whose
// FIRST operation would succeed and whose SECOND fails must not leave the
// first operation's effect visible anywhere the caller can observe — Apply
// returns only nil and an error, never a map with operation one applied and
// operation two missing.
func TestApply_AtomicOnPartialFailure(t *testing.T) {
	p := mustParse(t, []byte(`[
		{"op":"add","path":"/type","value":"integer"},
		{"op":"remove","path":"/missing"}
	]`))
	doc := map[string]any{"type": "string"}

	out, err := Apply(p, doc)
	if err == nil {
		t.Fatalf("Apply succeeded despite a failing second operation: %#v", out)
	}
	if out != nil {
		t.Errorf("Apply returned a non-nil map on failure: %#v", out)
	}
	if doc["type"] != "string" {
		t.Errorf("input document was mutated despite the failure: %#v", doc)
	}
}

// TestApply_EmptyPatchReturnsAnEquivalentCopy covers B4's other half: Apply
// itself has no special case for an empty Patch (the caller — the runtime
// build in internal/mockplane — is the one that skips calling Apply at all
// for one), but when it IS called with one, the result is a deep copy equal
// to the input, not the input map itself.
func TestApply_EmptyPatchReturnsAnEquivalentCopy(t *testing.T) {
	p := mustParse(t, []byte(`[]`))
	if !p.Empty() {
		t.Fatal("Parse([]) did not report Empty()")
	}

	doc := map[string]any{"type": "object", "properties": map[string]any{"a": 1.0}}
	out, err := Apply(p, doc)
	if err != nil {
		t.Fatalf("Apply returned error for an empty patch: %v", err)
	}
	if out["type"] != "object" {
		t.Errorf("out is missing an unrelated key: %#v", out)
	}

	// Still a distinct map: mutating the result must not reach doc.
	outProps := out["properties"].(map[string]any)
	outProps["b"] = 2.0
	if _, ok := doc["properties"].(map[string]any)["b"]; ok {
		t.Fatal("Apply of an empty patch returned the SAME map as the input, not a copy")
	}
}
