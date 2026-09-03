package recipes

import (
	"encoding/json"
	"errors"
	"testing"
)

func raw(s string) json.RawMessage {
	return json.RawMessage(s)
}

func TestRecipe_Validate(t *testing.T) {
	tests := []struct {
		name    string
		recipe  Recipe
		wantErr bool
	}{
		{"const ok", Recipe{Kind: KindConst, Data: raw(`"published"`)}, false},
		{"const number ok", Recipe{Kind: KindConst, Data: raw(`42`)}, false},
		{"const null ok", Recipe{Kind: KindConst, Data: raw(`null`)}, false},
		{"const missing value", Recipe{Kind: KindConst}, true},
		{"const malformed json", Recipe{Kind: KindConst, Data: raw(`{not json`)}, true},

		{"enum ok", Recipe{Kind: KindEnum, Data: raw(`["draft","published"]`)}, false},
		{"enum empty array", Recipe{Kind: KindEnum, Data: raw(`[]`)}, true},
		{"enum not an array", Recipe{Kind: KindEnum, Data: raw(`"x"`)}, true},
		{"enum missing value", Recipe{Kind: KindEnum}, true},

		{"copy ok", Recipe{Kind: KindCopy, Field: "$.id"}, false},
		{"copy missing field", Recipe{Kind: KindCopy}, true},

		{"identity ok id", Recipe{Kind: KindIdentity, Field: "id"}, false},
		{"identity ok org.id", Recipe{Kind: KindIdentity, Field: "org.id"}, false},
		{"identity unknown field", Recipe{Kind: KindIdentity, Field: "nope"}, true},
		{"identity empty field", Recipe{Kind: KindIdentity}, true},

		{"jwt bare ok", Recipe{Kind: KindJWT}, false},
		{"jwt with claims ok", Recipe{Kind: KindJWT, Claims: raw(`{"scope":"admin"}`)}, false},
		{"jwt with ttl ok", Recipe{Kind: KindJWT, TTLSec: 60}, false},
		{"jwt malformed claims", Recipe{Kind: KindJWT, Claims: raw(`[1,2]`)}, true},
		{"jwt negative ttl", Recipe{Kind: KindJWT, TTLSec: -1}, true},

		{"now bare ok", Recipe{Kind: KindNow}, false},
		{"now offset ok", Recipe{Kind: KindNow, Offset: "+3600s"}, false},
		{"now negative offset ok", Recipe{Kind: KindNow, Offset: "-7d"}, false},
		{"now format epoch_ms ok", Recipe{Kind: KindNow, Format: "epoch_ms"}, false},
		{"now format iso ok", Recipe{Kind: KindNow, Format: "iso"}, false},
		{"now bad format", Recipe{Kind: KindNow, Format: "unix"}, true},
		{"now bad offset unit", Recipe{Kind: KindNow, Offset: "+3600x"}, true},
		{"now offset no digits", Recipe{Kind: KindNow, Offset: "+s"}, true},

		{"null ok", Recipe{Kind: KindNull}, false},
		{"omit ok", Recipe{Kind: KindOmit}, false},

		{"listSize fixed ok", Recipe{Kind: KindListSize, Data: raw(`10`)}, false},
		{"listSize range ok", Recipe{Kind: KindListSize, Data: raw(`[5,50]`)}, false},
		{"listSize inverted range", Recipe{Kind: KindListSize, Data: raw(`[50,5]`)}, true},
		{"listSize negative fixed", Recipe{Kind: KindListSize, Data: raw(`-1`)}, true},
		{"listSize negative lo", Recipe{Kind: KindListSize, Data: raw(`[-1,5]`)}, true},
		{"listSize missing value", Recipe{Kind: KindListSize}, true},
		{"listSize wrong shape", Recipe{Kind: KindListSize, Data: raw(`[1,2,3]`)}, true},
		{"listSize not numeric", Recipe{Kind: KindListSize, Data: raw(`"ten"`)}, true},

		{"unknown kind", Recipe{Kind: Kind("bogus")}, true},
		{"zero value recipe", Recipe{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.recipe.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want an error")
				}
				if !errors.Is(err, ErrRecipe) {
					t.Fatalf("Validate() = %v, want it to wrap ErrRecipe", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestRecipe_Validate_NeverPanics stands in for HARD RULE-adjacent guidance:
// a malformed override row is user input reaching an unauthenticated mock
// plane, so Validate must return an error, never panic, on adversarial
// input — including bytes that are not valid JSON at all.
func TestRecipe_Validate_NeverPanics(t *testing.T) {
	adversarial := []Recipe{
		{Kind: KindConst, Data: raw(`{not json`)},
		{Kind: KindEnum, Data: raw(`{not json`)},
		{Kind: KindEnum, Data: raw(``)},
		{Kind: KindListSize, Data: raw(`{"lo":1}`)},
		{Kind: KindListSize, Data: raw(``)},
		{Kind: KindJWT, Claims: raw(`{not json`)},
		{Kind: KindJWT, Claims: raw(`"just a string"`)},
		{Kind: KindNow, Offset: "banana"},
		{Kind: KindNow, Offset: "+"},
		{Kind: KindIdentity, Field: ""},
		{Kind: Kind("")},
		{Kind: Kind("\x00weird")},
		{},
	}
	for i, r := range adversarial {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("case %d: Validate() panicked on %+v: %v", i, r, p)
				}
			}()
			_ = r.Validate()
		}()
	}
}

func TestKind_Deferred(t *testing.T) {
	want := map[Kind]bool{
		KindConst:     false,
		KindEnum:      false,
		KindCopy:      true,
		KindIdentity:  false,
		KindJWT:       false,
		KindNow:       false,
		KindNull:      false,
		KindOmit:      true,
		KindListSize:  true,
		Kind("bogus"): false,
	}
	for k, want := range want {
		if got := k.Deferred(); got != want {
			t.Errorf("Kind(%q).Deferred() = %v, want %v", k, got, want)
		}
	}
}
