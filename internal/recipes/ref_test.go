package recipes

import (
	"errors"
	"strings"
	"testing"
)

// This file covers what a LEAF can honestly observe about "ref" with a stub
// resolver: property 9 (Validate refuses a malformed reference, and
// ACCEPTS one naming a family that does not exist — P3c D9) and the decline
// contract refValue itself owns (P3c D7/D10/D15) — the defaulting of an
// absent policy to "generate", the nil-Ref case answering per policy
// without calling a nil func, ok=false from the resolver answering per
// policy, and ok=true passing the resolver's own value through UNCHANGED,
// with no second Coerce (D10: the resolver already did the scalar check
// and the coercion). Anything that needs a real store — a real family, a
// real entity row — belongs to internal/mockplane, not here (D6).

// --- Recipe.Validate ------------------------------------------------------

func TestRefValidate_MalformedShapes(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"data absent", ``},
		{"data not an object", `"nope"`},
		{"family missing", `{"property":"id"}`},
		{"family empty", `{"family":"","property":"id"}`},
		{"family not slash-prefixed", `{"family":"subjects","property":"id"}`},
		{"family over 2 KiB", `{"family":"/` + strings.Repeat("a", 2<<10) + `","property":"id"}`},
		{"property missing", `{"family":"/subjects"}`},
		{"property empty", `{"family":"/subjects","property":""}`},
		{"unrecognized key", `{"family":"/subjects","property":"id","scope":"x"}`},
		{"policy restrict", `{"family":"/subjects","property":"id","policy":"restrict"}`},
		{"policy unknown token", `{"family":"/subjects","property":"id","policy":"setnull"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Recipe{Kind: KindRef, Data: raw(tc.data)}
			if err := r.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want an error for %s", tc.name)
			} else if !errors.Is(err, ErrRecipe) {
				t.Fatalf("Validate() = %v, want it to wrap ErrRecipe", err)
			}
		})
	}
}

func TestRefValidate_AcceptsAFamilyThatDoesNotExist(t *testing.T) {
	// internal/recipes is a LEAF with no database handle at all (D4/D9):
	// Validate checks SHAPE only, never existence. A family that names no
	// resource in any workspace — indistinguishable, from here, from one
	// that names a real family not yet confirmed — must validate cleanly.
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/no-such-family-anywhere","property":"id"}`)}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (existence is a runtime question, D9)", err)
	}
}

func TestRefValidate_AcceptsAnExplicitGenerateAndSetNullPolicy(t *testing.T) {
	for _, policy := range []string{"generate", "set-null"} {
		r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id","policy":"` + policy + `"}`)}
		if err := r.Validate(); err != nil {
			t.Fatalf("Validate() with policy %q = %v, want nil", policy, err)
		}
	}
}

func TestRefValidate_AcceptsFamilyPathCharactersDESIGNDoesNotForbid(t *testing.T) {
	// D3: family is matched EXACT STRING, never delimited — a dot, a "#",
	// or a doubled slash inside it is not this recipe's business to reject.
	for _, family := range []string{"/orgs/{}/subjects", "/a#b", "/a.b", "/a//b"} {
		r := Recipe{Kind: KindRef, Data: raw(`{"family":"` + family + `","property":"id"}`)}
		if err := r.Validate(); err != nil {
			t.Fatalf("Validate() with family %q = %v, want nil", family, err)
		}
	}
}

// --- refValue's decline contract ------------------------------------------

func TestRefValue_AbsentPolicyDefaultsToGenerate(t *testing.T) {
	// D7: an absent policy key means "generate" — the document's own
	// default — and the defaulting happens in refValue, which is the only
	// place in the chain that reads Data at all. Observed here through the
	// RefQuery the stub resolver receives: an empty string would silently
	// disable the traffic mark for every ref written the documented default
	// way (D7's own stated consequence).
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id"}`)}
	var gotPolicy string
	stub := func(q RefQuery) (any, bool) {
		gotPolicy = q.Policy
		return 7, true
	}
	if _, _, err := r.Value(Env{Type: "integer"}, "subjectId", nil, stub); err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if gotPolicy != "generate" {
		t.Fatalf("RefQuery.Policy = %q, want %q (never empty)", gotPolicy, "generate")
	}
}

func TestRefValue_FamilyAndPropertyPassThroughVerbatim(t *testing.T) {
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/orgs/{}/subjects","property":"displayName"}`)}
	var got RefQuery
	stub := func(q RefQuery) (any, bool) {
		got = q
		return "x", true
	}
	if _, _, err := r.Value(Env{Type: "string", Seed: 42}, "name", nil, stub); err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if got.Family != "/orgs/{}/subjects" || got.Property != "displayName" || got.Seed != 42 {
		t.Fatalf("RefQuery = %+v, want family/property from Data and Seed from Env", got)
	}
}

func TestRefValue_NilRef_GeneratePolicyDeclines(t *testing.T) {
	// D15's own refusal matrix: "Request.Ref is nil (confirm, reseed,
	// preview) | n/a | per policy". A nil Ref is not the resolver
	// declining — there is no resolver to ask — and refValue must not call
	// through it.
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id"}`)}
	v, ok, err := r.Value(Env{Type: "integer"}, "subjectId", nil, nil)
	if err != nil || ok || v != nil {
		t.Fatalf("Value() = %v, %v, %v, want nil, false, nil", v, ok, err)
	}
}

func TestRefValue_NilRef_SetNullPolicyEmitsNull(t *testing.T) {
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id","policy":"set-null"}`)}
	v, ok, err := r.Value(Env{Type: "integer"}, "subjectId", nil, nil)
	if err != nil || !ok || v != nil {
		t.Fatalf("Value() = %v, %v, %v, want nil, true, nil (set-null always emits JSON null)", v, ok, err)
	}
}

func TestRefValue_ResolverDeclines_GeneratePolicyFallsThrough(t *testing.T) {
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id"}`)}
	stub := func(RefQuery) (any, bool) { return nil, false }
	v, ok, err := r.Value(Env{Type: "integer"}, "subjectId", nil, stub)
	if err != nil || ok || v != nil {
		t.Fatalf("Value() = %v, %v, %v, want nil, false, nil (ordinary decline)", v, ok, err)
	}
}

func TestRefValue_ResolverDeclines_SetNullPolicyEmitsNull(t *testing.T) {
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id","policy":"set-null"}`)}
	stub := func(RefQuery) (any, bool) { return nil, false }
	v, ok, err := r.Value(Env{Type: "integer"}, "subjectId", nil, stub)
	if err != nil || !ok || v != nil {
		t.Fatalf("Value() = %v, %v, %v, want nil, true, nil", v, ok, err)
	}
}

func TestRefValue_ResolverResolves_ValuePassesThroughUnchanged(t *testing.T) {
	// D10: "The RESOLVER declines a non-scalar value, not refValue" — the
	// resolver already ran the scalar check and the coercion (it may call
	// recipes.Coerce). On ok=true refValue must return the resolver's value
	// AS-IS: applying Coerce a second time here, against env.Type, would be
	// a second decision point the design deliberately puts nowhere but the
	// resolver (D4's "one decision point, in the one place that can mark").
	// A string value handed back against an "integer" env.Type — which a
	// second Coerce("string->integer") could well accept and silently
	// reshape — is the sharpest way to observe that refValue does not
	// reach for Coerce at all.
	r := Recipe{Kind: KindRef, Data: raw(`{"family":"/subjects","property":"id"}`)}
	stub := func(RefQuery) (any, bool) { return "not-coerced", true }
	v, ok, err := r.Value(Env{Type: "integer"}, "subjectId", nil, stub)
	if err != nil || !ok {
		t.Fatalf("Value() = %v, %v, %v, want ok=true, err=nil", v, ok, err)
	}
	if s, isStr := v.(string); !isStr || s != "not-coerced" {
		t.Fatalf("Value() = %v (%T), want the resolver's own value unchanged", v, v)
	}
}

func TestRefValue_MalformedDataAtRuntimeErrors(t *testing.T) {
	// Value is also called directly by tests and by any caller that
	// bypassed Validate (every other kind in this file already takes this
	// defensive posture, e.g. parseSequence's own doc) — a Data that could
	// never have passed Validate must still not panic or silently decline;
	// it is a structurally broken recipe, so Value returns an error.
	r := Recipe{Kind: KindRef, Data: raw(`"not an object"`)}
	_, ok, err := r.Value(Env{Type: "integer"}, "x", nil, nil)
	if ok || !errors.Is(err, ErrRecipe) {
		t.Fatalf("Value() ok=%v err=%v, want ok=false and an ErrRecipe", ok, err)
	}
}
