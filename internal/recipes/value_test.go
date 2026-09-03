package recipes

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
)

func fullIdentity() domain.Identity {
	return domain.Identity{
		ID:    42,
		Name:  "Ada Lovelace",
		Email: "ada@example.com",
		Roles: []string{"admin", "user"},
		Org:   &domain.Org{ID: 7, Name: "Analytical Engines", Type: "company"},
	}
}

func jsonEq(t *testing.T, got, want any) {
	t.Helper()
	gj, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got %v: %v", got, err)
	}
	wj, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want %v: %v", want, err)
	}
	if string(gj) != string(wj) {
		t.Fatalf("got %s, want %s", gj, wj)
	}
}

// --- IdentityField -----------------------------------------------------

func TestIdentityField(t *testing.T) {
	id := fullIdentity()
	tests := []struct {
		field string
		want  any
		ok    bool
	}{
		{"id", 42, true},
		{"name", "Ada Lovelace", true},
		{"email", "ada@example.com", true},
		{"roles", []string{"admin", "user"}, true},
		{"org.id", 7, true},
		{"org.name", "Analytical Engines", true},
		{"org.type", "company", true},
		{"bogus", nil, false},
		{"", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			got, ok := IdentityField(id, tc.field)
			if ok != tc.ok {
				t.Fatalf("IdentityField(%q) ok = %v, want %v (got %v)", tc.field, ok, tc.ok, got)
			}
			if ok {
				jsonEq(t, got, tc.want)
			}
		})
	}
}

func TestIdentityField_NoOrgDeclinesOrgFields(t *testing.T) {
	id := domain.Identity{ID: 1, Name: "x"}
	for _, f := range []string{"org.id", "org.name", "org.type"} {
		if _, ok := IdentityField(id, f); ok {
			t.Errorf("IdentityField(%q) on an identity with no org = ok, want declined", f)
		}
	}
}

func TestIdentityField_NilID(t *testing.T) {
	id := domain.Identity{Name: "x"}
	if _, ok := IdentityField(id, "id"); ok {
		t.Errorf("IdentityField(id) with a nil ID = ok, want declined")
	}
}

// TestIdentityFieldOK_MatchesIdentityField proves Validate's accepted field
// names and IdentityField's own switch cannot silently drift apart — a
// field Validate lets through must actually be servable, and vice versa.
func TestIdentityFieldOK_MatchesIdentityField(t *testing.T) {
	id := fullIdentity()
	known := []string{"id", "name", "email", "roles", "org.id", "org.name", "org.type"}
	for _, f := range known {
		if !identityFieldOK(f) {
			t.Errorf("identityFieldOK(%q) = false, want true", f)
		}
		if _, ok := IdentityField(id, f); !ok {
			t.Errorf("IdentityField(%q) on a full identity = declined, want ok", f)
		}
		if err := (Recipe{Kind: KindIdentity, Field: f}).Validate(); err != nil {
			t.Errorf("Validate() for field %q = %v, want nil", f, err)
		}
	}
	if identityFieldOK("bogus") {
		t.Errorf("identityFieldOK(bogus) = true, want false")
	}
}

// --- Coerce --------------------------------------------------------------

func TestCoerce(t *testing.T) {
	tests := []struct {
		name     string
		v        any
		jsonType string
		wantOK   bool
		want     any
	}{
		{"number into string", 42.0, "string", true, "42"},
		{"int64 into string", int64(1765900000), "string", true, "1765900000"},
		{"bool into string", true, "string", true, "true"},
		{"fractional number into string", 3.5, "string", true, "3.5"},

		{"numeric string into integer", "42", "integer", true, int64(42)},
		{"whole float into integer", 42.0, "integer", true, int64(42)},
		{"fractional float into integer declines", 3.5, "integer", false, nil},
		{"fractional numeric string into integer declines", "3.5", "integer", false, nil},
		{"non-numeric string into integer declines", "abc", "integer", false, nil},
		{"jwt-shaped string into integer declines", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig", "integer", false, nil},
		{"bool into integer declines", true, "integer", false, nil},

		{"numeric string into number", "3.5", "number", true, 3.5},
		{"non-numeric string into number declines", "abc", "number", false, nil},

		{"bool into boolean ok", true, "boolean", true, true},
		{"string into boolean declines", "true", "boolean", false, nil},

		{"array into scalar declines", []any{1, 2}, "string", false, nil},
		{"object into scalar declines", map[string]any{"a": 1}, "integer", false, nil},

		{"[]any into array ok", []any{1, 2}, "array", true, []any{1, 2}},
		{"[]string into array ok", []string{"a", "b"}, "array", true, []any{"a", "b"}},
		{"scalar into array declines", "x", "array", false, nil},

		{"map into object ok", map[string]any{"a": 1}, "object", true, map[string]any{"a": 1}},
		{"scalar into object declines", "x", "object", false, nil},
		{"array into object declines", []any{1}, "object", false, nil},

		{"no declared type passes anything through", map[string]any{"a": 1}, "", true, map[string]any{"a": 1}},

		{"nil always passes for any type", nil, "string", true, nil},
		{"nil always passes for integer", nil, "integer", true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Coerce(tc.v, tc.jsonType, "")
			if ok != tc.wantOK {
				t.Fatalf("Coerce(%v, %q) ok = %v, want %v (got %v)", tc.v, tc.jsonType, ok, tc.wantOK, got)
			}
			if ok {
				jsonEq(t, got, tc.want)
			}
		})
	}
}

// --- NowValue --------------------------------------------------------------

func TestNowValue(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, offset, format string
		want                 any
	}{
		{"bare epoch", "", "epoch", now.Unix()},
		{"epoch_ms", "", "epoch_ms", now.UnixMilli()},
		{"iso", "", "iso", now.Format(time.RFC3339)},
		{"default format is epoch seconds", "", "", now.Unix()},
		{"plus seconds", "+3600s", "epoch", now.Add(time.Hour).Unix()},
		{"minus days", "-7d", "epoch", now.Add(-7 * 24 * time.Hour).Unix()},
		{"plus minutes", "+90m", "epoch", now.Add(90 * time.Minute).Unix()},
		{"plus hours, iso", "+2h", "iso", now.Add(2 * time.Hour).Format(time.RFC3339)},
		{"unsigned offset defaults positive", "3600s", "epoch", now.Add(time.Hour).Unix()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NowValue(tc.offset, tc.format, now)
			if err != nil {
				t.Fatalf("NowValue(%q, %q) error: %v", tc.offset, tc.format, err)
			}
			jsonEq(t, got, tc.want)
		})
	}
}

// TestNowValue_MillisecondsAreNeverTheDefault pins DESIGN §10's load-bearing
// unit rule directly, not just as a byproduct of the table above.
func TestNowValue_MillisecondsAreNeverTheDefault(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	got, err := NowValue("", "", now)
	if err != nil {
		t.Fatal(err)
	}
	sec, ok := got.(int64)
	if !ok {
		t.Fatalf("NowValue default type = %T, want int64", got)
	}
	if sec != now.Unix() {
		t.Fatalf("NowValue default = %d, want %d (seconds, not %d ms)", sec, now.Unix(), now.UnixMilli())
	}
}

func TestNowValue_Rejections(t *testing.T) {
	fixedNow := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct{ offset, format string }{
		{"banana", "epoch"},
		{"+10x", "epoch"},
		{"+", "epoch"},
		{"-", "epoch"},
		{"", "unixtime"},
		{"3600", "epoch"}, // no unit
	}
	for _, tc := range tests {
		_, err := NowValue(tc.offset, tc.format, fixedNow)
		if err == nil {
			t.Errorf("NowValue(%q, %q) = nil error, want one", tc.offset, tc.format)
			continue
		}
		if !errors.Is(err, ErrRecipe) {
			t.Errorf("NowValue(%q, %q) error = %v, want it to wrap ErrRecipe", tc.offset, tc.format, err)
		}
	}
}

// --- Recipe.Value ----------------------------------------------------------

func TestRecipeValue_Const(t *testing.T) {
	r := Recipe{Kind: KindConst, Data: raw(`"published"`)}
	v, ok, err := r.Value(Env{Type: "string"}, "status", nil, nil)
	if err != nil || !ok {
		t.Fatalf("Value() = %v, %v, %v", v, ok, err)
	}
	jsonEq(t, v, "published")
}

func TestRecipeValue_ConstDeclinesOnTypeMismatch(t *testing.T) {
	r := Recipe{Kind: KindConst, Data: raw(`{"nested":true}`)}
	v, ok, err := r.Value(Env{Type: "string"}, "status", nil, nil)
	if err != nil || ok {
		t.Fatalf("Value() = %v, %v, %v, want ok=false", v, ok, err)
	}
}

func TestRecipeValue_EnumIsSeededDeterministically(t *testing.T) {
	r := Recipe{Kind: KindEnum, Data: raw(`["a","b","c"]`)}
	v0, ok, err := r.Value(Env{Type: "string", Seed: 0}, "x", nil, nil)
	if err != nil || !ok || v0 != "a" {
		t.Fatalf("seed 0 => %v, %v, %v, want a", v0, ok, err)
	}
	v1, ok, err := r.Value(Env{Type: "string", Seed: 1}, "x", nil, nil)
	if err != nil || !ok || v1 != "b" {
		t.Fatalf("seed 1 => %v, %v, %v, want b", v1, ok, err)
	}

	// Same seed, same dataPath: must be repeatable, not just "some member".
	again, _, _ := r.Value(Env{Type: "string", Seed: 12345}, "x", nil, nil)
	again2, _, _ := r.Value(Env{Type: "string", Seed: 12345}, "x", nil, nil)
	if again != again2 {
		t.Fatalf("enum not deterministic for the same seed: %v != %v", again, again2)
	}
}

func TestRecipeValue_Identity(t *testing.T) {
	r := Recipe{Kind: KindIdentity, Field: "email"}
	v, ok, err := r.Value(Env{Identity: fullIdentity(), Type: "string"}, "user.email", nil, nil)
	if err != nil || !ok || v != "ada@example.com" {
		t.Fatalf("Value() = %v, %v, %v", v, ok, err)
	}
}

func TestRecipeValue_IdentityDeclinesWhenNotCarried(t *testing.T) {
	r := Recipe{Kind: KindIdentity, Field: "org.id"}
	v, ok, err := r.Value(Env{Identity: domain.Identity{ID: 1}, Type: "integer"}, "org_id", nil, nil)
	if err != nil || ok {
		t.Fatalf("Value() = %v, %v, %v, want ok=false, err=nil", v, ok, err)
	}
}

func TestRecipeValue_Now(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r := Recipe{Kind: KindNow, Offset: "+3600s"}
	v, ok, err := r.Value(Env{Type: "integer", Now: now}, "expires_at", nil, nil)
	if err != nil || !ok {
		t.Fatalf("Value() err=%v ok=%v", err, ok)
	}
	jsonEq(t, v, now.Add(time.Hour).Unix())
}

func TestRecipeValue_NowDefaultsToISOForAStringTarget(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r := Recipe{Kind: KindNow}
	v, ok, err := r.Value(Env{Type: "string", Now: now}, "created_at", nil, nil)
	if err != nil || !ok {
		t.Fatalf("Value() err=%v ok=%v", err, ok)
	}
	jsonEq(t, v, now.Format(time.RFC3339))
}

func TestRecipeValue_NowDefaultsToEpochForANumericTarget(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r := Recipe{Kind: KindNow}
	v, ok, err := r.Value(Env{Type: "integer", Now: now}, "created_at", nil, nil)
	if err != nil || !ok {
		t.Fatalf("Value() err=%v ok=%v", err, ok)
	}
	jsonEq(t, v, now.Unix())
}

func TestRecipeValue_Null(t *testing.T) {
	r := Recipe{Kind: KindNull}
	v, ok, err := r.Value(Env{Type: "string"}, "x", nil, nil)
	if err != nil || !ok || v != nil {
		t.Fatalf("Value() = %v, %v, %v, want nil, true, nil", v, ok, err)
	}
}

func TestRecipeValue_DeferredKindsAlwaysDecline(t *testing.T) {
	fixedNow := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for _, k := range []Kind{KindCopy, KindOmit, KindListSize} {
		r := Recipe{Kind: k, Field: "$.id", Data: raw(`3`)}
		v, ok, err := r.Value(Env{Identity: fullIdentity(), Type: "string", Now: fixedNow}, "x", nil, nil)
		if v != nil || ok || err != nil {
			t.Fatalf("Kind %s: Value() = %v, %v, %v; want nil, false, nil", k, v, ok, err)
		}
	}
}

func TestRecipeValue_UnknownKind(t *testing.T) {
	r := Recipe{Kind: Kind("bogus")}
	_, ok, err := r.Value(Env{}, "x", nil, nil)
	if ok || !errors.Is(err, ErrRecipe) {
		t.Fatalf("Value() ok=%v err=%v, want ok=false and an ErrRecipe", ok, err)
	}
}

func TestRecipeValue_JWT(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	auth := domain.AuthSettings{JWTTTLSec: 3600, Alg: "HS256", SigningKey: "k"}
	r := Recipe{Kind: KindJWT}
	v, ok, err := r.Value(Env{Type: "string", Auth: auth, Identity: fullIdentity(), Now: now}, "access_token", nil, nil)
	if err != nil || !ok {
		t.Fatalf("Value() err=%v ok=%v", err, ok)
	}
	s, isStr := v.(string)
	if !isStr || strings.Count(s, ".") != 2 {
		t.Fatalf("Value() = %v, want a 3-segment compact JWS", v)
	}
}
