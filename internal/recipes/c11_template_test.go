package recipes

import (
	"errors"
	"testing"
)

// C11: "The template rules are enforced where an operator meets them" —
// two functions, four assertions (gates/p2e/artifacts/context.md §J/C11):
// Recipe.Validate rejects an unknown placeholder and an unmatched "{{" at
// WRITE time (D8(1)); Recipe.Value produces a placeholder-free template's
// literal text unconditionally (D8(2)) and DECLINES an {{index}}-bearing
// template evaluated at a path with no array position at all (D7(3)).
//
// This file is new — it does not touch value_test.go, which an earlier
// section (production) already edited for the Value(env, dataPath, faker)
// signature change.

// --- Recipe.Validate ----------------------------------------------------

// Fails against the silent-passthrough reading of D8(1): a build that lets
// an unrecognised "{{…}}" through as literal text would accept this recipe
// and ship a body containing the literal "{{idex}}". Production line this
// pins: internal/recipes/value.go's validateTemplate, the
// `if placeholder != "index"` branch.
func TestTemplateValidate_RejectsUnknownPlaceholder(t *testing.T) {
	r := Recipe{Kind: KindTemplate, Data: raw(`"Order #{{idex}}"`)}
	err := r.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error naming the unknown placeholder \"{{idex}}\"")
	}
	if !errors.Is(err, ErrRecipe) {
		t.Fatalf("Validate() = %v, want it to wrap ErrRecipe", err)
	}
}

// Same production branch, the other malformed shape D8(1) names by name:
// an opener with no matching closer must not be treated as ordinary text.
func TestTemplateValidate_RejectsUnmatchedOpener(t *testing.T) {
	r := Recipe{Kind: KindTemplate, Data: raw(`"prefix {{ suffix, no closer"`)}
	err := r.Validate()
	if err == nil {
		t.Fatal(`Validate() = nil, want an error for the unmatched "{{"`)
	}
	if !errors.Is(err, ErrRecipe) {
		t.Fatalf("Validate() = %v, want it to wrap ErrRecipe", err)
	}
}

// A bare "}}" with no opener is ordinary text (D8(1)'s own carve-out) and
// must NOT be rejected — the negative control for the two tests above, so
// a Validate that rejects every "}}" on sight cannot pass this file by
// accident.
func TestTemplateValidate_AcceptsBareCloserAsLiteralText(t *testing.T) {
	r := Recipe{Kind: KindTemplate, Data: raw(`"score }} out of 10"`)}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil: a lone \"}}\" with no opener is ordinary text", err)
	}
}

// --- Recipe.Value --------------------------------------------------------

// Fails against the reading of D7(3) that makes EVERY template decline
// outside an array, including one that never asked for a position (the
// second wrong reading C11 exists to catch). D5 says an empty/plain
// template legally produces its own text; D8(2) says that holds even at a
// path with no array index, because nothing in it asked for one.
// Production line this pins: internal/recipes/value.go's templateValue,
// the `if hasIndex { … }` branch — a build that gated the whole function on
// indexFromPath's ok, rather than on hasIndex, fails this.
func TestTemplateValue_NoPlaceholderProducesLiteralTextOutsideArray(t *testing.T) {
	r := Recipe{Kind: KindTemplate, Data: raw(`"just plain text"`)}
	// dataPath is a detail body's top-level field name: no "[N]" segment
	// anywhere in it.
	v, ok, err := r.Value(Env{Type: "string"}, "status", nil, nil)
	if err != nil {
		t.Fatalf("Value() err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("Value() ok = false, want true: a placeholder-free template always produces (D8(2))")
	}
	if v != "just plain text" {
		t.Fatalf("Value() = %q, want the literal template text unchanged", v)
	}
}

// Fails against the silent-emission reading of D7(3): a build that
// substitutes a default index (0, or "") when dataPath carries no "[N]"
// would ship a value nobody asked for at that path. Production line this
// pins: internal/recipes/value.go's templateValue, the
// `idx, ok := indexFromPath(dataPath); if !ok { return nil, false, nil }`
// branch — and indexFromPath itself, the innermost-"[N]" scan.
func TestTemplateValue_IndexPlaceholderDeclinesOutsideArray(t *testing.T) {
	r := Recipe{Kind: KindTemplate, Data: raw(`"Widget #{{index}}"`)}
	// Same detail-body top-level path as above: no array position exists
	// here for {{index}} to substitute (D7(3)).
	v, ok, err := r.Value(Env{Type: "string"}, "status", nil, nil)
	if err != nil {
		t.Fatalf("Value() err = %v, want nil", err)
	}
	if ok {
		t.Fatalf("Value() = %v, ok = true, want a decline: {{index}} outside an array has no position to substitute", v)
	}
	if v != nil {
		t.Fatalf("Value() = %v on a decline, want nil", v)
	}
}

// The positive control for the test above: the SAME template, at a path
// that DOES carry an array position, must produce rather than decline —
// otherwise the decline test above could be passing for the wrong reason
// (e.g. an implementation that always declines "template" outright).
func TestTemplateValue_IndexPlaceholderProducesInsideArray(t *testing.T) {
	r := Recipe{Kind: KindTemplate, Data: raw(`"Widget #{{index}}"`)}
	v, ok, err := r.Value(Env{Type: "string"}, "items[10].status", nil, nil)
	if err != nil {
		t.Fatalf("Value() err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("Value() ok = false, want true: dataPath carries an array position here")
	}
	if v != "Widget #10" {
		t.Fatalf("Value() = %q, want %q", v, "Widget #10")
	}
}
