package overrides_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// TestValidateVariant_functionIsExclusivePerVariant is acceptance clause 16.
// Every one of the five conflicting fields is exercised, because "one producer
// per variant" is a claim about a SET and a test that checks `body` alone
// passes over an implementation that forgot `recipes`.
func TestValidateVariant_functionIsExclusivePerVariant(t *testing.T) {
	const src = `return 200, {ok = true}`
	for _, tc := range []struct {
		name string
		with func(*overrides.Variant)
		want string
	}{
		{"body", func(v *overrides.Variant) { v.Body = jsonx.RawMessage(`{"a":1}`) }, "function and body are exclusive"},
		{"bodyEncoding", func(v *overrides.Variant) { v.BodyEncoding = "base64" }, "function and body are exclusive"},
		{"bodyRef", func(v *overrides.Variant) { v.BodyRef = "asset:logo.png" }, "function and bodyRef are exclusive"},
		{"recipes", func(v *overrides.Variant) {
			v.Recipes = map[string]recipes.Recipe{"$.id": {Kind: "uuid"}}
		}, "function and recipes are exclusive"},
		{"schemaPatch", func(v *overrides.Variant) { v.SchemaPatch = jsonx.RawMessage(`[{"op":"add"}]`) }, "function and schemaPatch are exclusive"},
		{"mediaType", func(v *overrides.Variant) { v.MediaType = "text/plain" }, "function takes no mediaType"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := overrides.Variant{Function: src}
			tc.with(&v)
			err := overrides.ValidateVariant(v)
			if !errors.Is(err, overrides.ErrInvalidRow) {
				t.Fatalf("err = %v, want ErrInvalidRow — the pair must be REFUSED, not accepted with one of the two silently winning", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestValidateVariant_functionAloneIsAccepted is the other direction, and it
// is the one that catches an exclusivity check written too wide: a criterion
// that only ever observes refusals passes against an implementation that
// refuses everything.
func TestValidateVariant_functionAloneIsAccepted(t *testing.T) {
	v := overrides.Variant{
		Function: `if req.body.password == "hunter2" then return 200, {token = "t"} end
			return 401, {error = "bad credentials"}`,
		When: []overrides.Condition{{In: "query", Name: "mode", Op: "equals", Value: "login"}},
	}
	if err := overrides.ValidateVariant(v); err != nil {
		t.Fatalf("a function with when[] was refused: %v — selection is unchanged and the function runs only when its variant is selected (D5)", err)
	}
}

// TestValidateVariant_functionIsCompiledAtWriteTime is acceptance clause 37:
// this plane always answers, so a deferred parse is a 500 nobody asked for.
func TestValidateVariant_functionIsCompiledAtWriteTime(t *testing.T) {
	err := overrides.ValidateVariant(overrides.Variant{Function: "return 200, }"})
	if !errors.Is(err, overrides.ErrInvalidRow) {
		t.Fatalf("err = %v, want ErrInvalidRow", err)
	}
	// The parser's OWN words, not a summary of them: an author navigates by
	// the line and the offending token.
	if !strings.Contains(err.Error(), "line:1") || !strings.Contains(err.Error(), "near '}'") {
		t.Fatalf("err = %q, want the parser's own line and token", err)
	}
}
