package gen

import (
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- priority order (DESIGN §9 "Приоритет источника значения") -----------

func TestLeafValueRecipeHookAlwaysDeclines(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	if v, ok := recipeValue(w, map[string]any{"const": "x"}, "field"); ok {
		t.Fatalf("recipeValue must always decline in P1b, got (%v, true)", v)
	}
}

func TestLeafValuePriorityExampleBeatsEverythingBelow(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{
		"type":     "string",
		"examples": []any{"from-example"},
		"const":    "from-const",
		"default":  "from-default",
		"enum":     []any{"a", "b"},
	}
	v, ok := leafValue(w, schema, "field")
	if !ok || v != "from-example" {
		t.Fatalf("expected schema-level example to win, got (%v, %v)", v, ok)
	}
}

func TestLeafValuePriorityConstBeatsDefaultAndEnum(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{
		"type":    "string",
		"const":   "from-const",
		"default": "from-default",
		"enum":    []any{"a", "b"},
	}
	v, ok := leafValue(w, schema, "field")
	if !ok || v != "from-const" {
		t.Fatalf("expected const to win over default/enum, got (%v, %v)", v, ok)
	}
}

func TestLeafValuePriorityDefaultBeatsEnum(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{
		"type":    "string",
		"default": "from-default",
		"enum":    []any{"a", "b"},
	}
	v, ok := leafValue(w, schema, "field")
	if !ok || v != "from-default" {
		t.Fatalf("expected default to win over enum, got (%v, %v)", v, ok)
	}
}

// TestLeafValueConstNilIsHonored: const:null is a legal, distinct-from-
// absent JSON Schema construct — leafValue must report (nil, true) ("the
// field IS null, a source applied"), never (nil, false) ("no source
// applied, keep looking").
func TestLeafValueConstNilIsHonored(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{"type": []any{"string", "null"}, "const": nil}
	v, ok := leafValue(w, schema, "field")
	if !ok || v != nil {
		t.Fatalf("expected (nil, true) for const:null, got (%v, %v)", v, ok)
	}
}

func TestEnumValueDeterministicAndVariesByPath(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{"enum": []any{"a", "b", "c", "d", "e"}}

	v1, ok1 := enumValue(w, schema, "field.one")
	v2, ok2 := enumValue(w, schema, "field.one")
	if !ok1 || !ok2 || v1 != v2 {
		t.Fatalf("enumValue must be deterministic for the same dataPath, got %v vs %v", v1, v2)
	}

	differs := false
	for i := range 20 {
		vi, ok := enumValue(w, schema, "field"+strconv.Itoa(i))
		if !ok {
			t.Fatalf("enumValue declined unexpectedly at i=%d", i)
		}
		if vi != v1 {
			differs = true
			break
		}
	}
	if !differs {
		t.Fatalf("enumValue never varied across 20 distinct dataPaths")
	}
}

func TestEnumValueEmptyOrAbsentDeclines(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	if _, ok := enumValue(w, map[string]any{}, "field"); ok {
		t.Fatalf("expected decline with no enum key")
	}
	if _, ok := enumValue(w, map[string]any{"enum": []any{}}, "field"); ok {
		t.Fatalf("expected decline with an empty enum array")
	}
}

// --- time split (DESIGN §9 "Время" / the seed contract) -------------------

func TestOrdinaryTimestampFieldIgnoresNow(t *testing.T) {
	schema := map[string]any{"type": "string", "format": "date-time"}
	now1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now2 := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
	w1 := newTestWalker(Options{Seed: 1, Now: func() time.Time { return now1 }}, nil)
	w2 := newTestWalker(Options{Seed: 1, Now: func() time.Time { return now2 }}, nil)

	v1, ok1 := leafValue(w1, schema, "created_at")
	v2, ok2 := leafValue(w2, schema, "created_at")
	if !ok1 || !ok2 {
		t.Fatalf("expected leafValue to produce a timestamp")
	}
	if v1 != v2 {
		t.Fatalf("an ORDINARY time field must not depend on Options.Now(): got %v vs %v", v1, v2)
	}
	if _, err := time.Parse(time.RFC3339, v1.(string)); err != nil {
		t.Fatalf("not a valid RFC3339 timestamp: %v (%q)", err, v1)
	}
}

func TestDeadlineFieldTracksNow(t *testing.T) {
	schema := map[string]any{"type": "string", "format": "date-time"}
	now1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now2 := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
	w1 := newTestWalker(Options{Seed: 1, Now: func() time.Time { return now1 }}, nil)
	w2 := newTestWalker(Options{Seed: 1, Now: func() time.Time { return now2 }}, nil)

	v1, ok1 := leafValue(w1, schema, "token_expires_at")
	v2, ok2 := leafValue(w2, schema, "token_expires_at")
	if !ok1 || !ok2 {
		t.Fatalf("expected leafValue to produce a value")
	}
	if v1 == v2 {
		t.Fatalf("a DEADLINE field must track Options.Now(), got the SAME value for two different Nows")
	}
	ts1, err := time.Parse(time.RFC3339, v1.(string))
	if err != nil {
		t.Fatalf("invalid timestamp: %v", err)
	}
	if !ts1.After(now1) {
		t.Fatalf("expected token_expires_at AFTER now1, got %v <= %v", ts1, now1)
	}
}

func TestExpIntegerFieldIsUnixSecondsAfterNow(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWalker(Options{Seed: 3, Now: func() time.Time { return now }}, nil)
	schema := map[string]any{"type": "integer"}

	v, ok := leafValue(w, schema, "exp")
	if !ok {
		t.Fatalf("expected a value for exp")
	}
	n, ok := v.(int64)
	if !ok {
		t.Fatalf("exp must be an int64 unix timestamp, got %T (%v)", v, v)
	}
	if n <= now.Unix() {
		t.Fatalf("exp must be in the future relative to now, got %d <= %d", n, now.Unix())
	}
}

func TestExpiresInIsRelativeDurationNotAnInstant(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := newTestWalker(Options{Seed: 1, Now: func() time.Time { return now }}, nil)
	schema := map[string]any{"type": "integer", "format": "int32"}

	v, ok := leafValue(w, schema, "expires_in")
	if !ok {
		t.Fatalf("expected leafValue to produce a value for expires_in")
	}
	n, ok := v.(int64)
	if !ok || n <= 0 {
		t.Fatalf("expires_in must be a positive relative duration in seconds, got %v (%T)", v, v)
	}
	if n > 24*3600 {
		t.Fatalf("expires_in unexpectedly large: %d seconds", n)
	}
}

// --- format realism ---------------------------------------------------

func TestFormatUUIDShape(t *testing.T) {
	w := newTestWalker(Options{Seed: 5}, nil)
	schema := map[string]any{"type": "string", "format": "uuid"}
	v, ok := leafValue(w, schema, "trace_id")
	if !ok {
		t.Fatalf("expected a uuid value")
	}
	parts := strings.Split(v.(string), "-")
	if len(parts) != 5 {
		t.Fatalf("expected 5 hyphen-separated groups, got %q", v)
	}
	wantLens := []int{8, 4, 4, 4, 12}
	for i, p := range parts {
		if len(p) != wantLens[i] {
			t.Fatalf("group %d wrong length in %q", i, v)
		}
	}
}

func TestFormatURIIsParseable(t *testing.T) {
	w := newTestWalker(Options{Seed: 9}, nil)
	schema := map[string]any{"type": "string", "format": "uri"}
	v, ok := leafValue(w, schema, "callback_url")
	if !ok {
		t.Fatalf("expected a uri value")
	}
	u, err := url.Parse(v.(string))
	if err != nil || u.Scheme == "" || u.Host == "" {
		t.Fatalf("format:uri must produce a parseable absolute URL, got %q (err=%v)", v, err)
	}
}

func TestFieldNameImageURLProducesURLEvenWithoutFormat(t *testing.T) {
	w := newTestWalker(Options{Seed: 2}, nil)
	schema := map[string]any{"type": "string"} // no format declared at all
	v, ok := leafValue(w, schema, "user.avatar_url")
	if !ok {
		t.Fatalf("expected avatar_url to be realized without a declared format")
	}
	s := v.(string)
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		t.Fatalf("avatar_url must be a parseable URL, got %q", s)
	}
	if !strings.HasSuffix(s, ".jpg") && !strings.HasSuffix(s, ".png") && !strings.HasSuffix(s, ".webp") {
		t.Fatalf("an image-ish field name should produce an image-flavored URL, got %q", s)
	}
}

func TestFieldNameEmailProducesAtAndDot(t *testing.T) {
	w := newTestWalker(Options{Seed: 4}, nil)
	schema := map[string]any{"type": "string"}
	v, ok := leafValue(w, schema, "contact_email")
	if !ok {
		t.Fatalf("expected contact_email to be realized")
	}
	s := v.(string)
	at := strings.IndexByte(s, '@')
	if at <= 0 || !strings.Contains(s[at:], ".") {
		t.Fatalf("email-shaped field must contain @ and a dot after it, got %q", s)
	}
}

func TestFieldNameFirstLastVsFullName(t *testing.T) {
	w := newTestWalker(Options{Seed: 7}, nil)
	schema := map[string]any{"type": "string"}

	first, ok := leafValue(w, schema, "first_name")
	if !ok || strings.Contains(first.(string), " ") {
		t.Fatalf("first_name must be a single token, got %q", first)
	}
	last, ok := leafValue(w, schema, "last_name")
	if !ok || strings.Contains(last.(string), " ") {
		t.Fatalf("last_name must be a single token, got %q", last)
	}
	full, ok := leafValue(w, schema, "name")
	if !ok || !strings.Contains(full.(string), " ") {
		t.Fatalf("a bare \"name\" field should read as a full name (two tokens), got %q", full)
	}
}

func TestFieldNameCodeShape(t *testing.T) {
	w := newTestWalker(Options{Seed: 6}, nil)
	v, ok := leafValue(w, map[string]any{"type": "string"}, "invite_code")
	if !ok {
		t.Fatalf("expected a code value")
	}
	s := v.(string)
	if len(s) != 6 {
		t.Fatalf("expected a 6-char code, got %q", s)
	}
	for _, r := range s {
		if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", r) {
			t.Fatalf("unexpected character %q in code %q", r, s)
		}
	}
}

func TestFieldNameColorShape(t *testing.T) {
	w := newTestWalker(Options{Seed: 6}, nil)
	v, ok := leafValue(w, map[string]any{"type": "string"}, "theme_color")
	if !ok {
		t.Fatalf("expected a color value")
	}
	s := v.(string)
	if len(s) != 7 || s[0] != '#' {
		t.Fatalf("expected a #RRGGBB hex color, got %q", s)
	}
}

func TestFieldNameStatusFromCorpus(t *testing.T) {
	w := newTestWalker(Options{Seed: 6}, nil)
	v, ok := leafValue(w, map[string]any{"type": "string"}, "order_status")
	if !ok {
		t.Fatalf("expected a status value")
	}
	for _, s := range statusCorpus {
		if s == v {
			return
		}
	}
	t.Fatalf("expected a value from statusCorpus, got %q", v)
}

func TestFieldNameSlugShape(t *testing.T) {
	w := newTestWalker(Options{Seed: 6}, nil)
	v, ok := leafValue(w, map[string]any{"type": "string"}, "url_slug")
	if !ok {
		t.Fatalf("expected a slug value")
	}
	s := v.(string)
	if strings.ContainsAny(s, " _") || strings.ToLower(s) != s {
		t.Fatalf("expected a lowercase, hyphenated slug, got %q", s)
	}
}

func TestFieldNamePhoneShape(t *testing.T) {
	w := newTestWalker(Options{Seed: 6}, nil)
	v, ok := leafValue(w, map[string]any{"type": "string"}, "phone")
	if !ok {
		t.Fatalf("expected a phone value")
	}
	if !strings.HasPrefix(v.(string), "+1-555-") {
		t.Fatalf("expected a +1-555-... phone number, got %q", v)
	}
}

// --- numeric realism --------------------------------------------------

func TestFormatUintNeverNegative(t *testing.T) {
	schema := map[string]any{"type": "integer", "format": "uint"}
	for seed := range int64(50) {
		w := newTestWalker(Options{Seed: seed}, nil)
		v, ok := leafValue(w, schema, "broadcast_id")
		if !ok {
			t.Fatalf("seed %d: expected a value for format:uint", seed)
		}
		n, ok := v.(int64)
		if !ok || n < 0 {
			t.Fatalf("seed %d: format:uint must never be negative, got %v", seed, v)
		}
	}
}

func TestFieldNameCountLimitNeverNegativeWithNoSchemaBounds(t *testing.T) {
	schema := map[string]any{"type": "integer"}
	for _, name := range []string{"count", "limit", "item_count", "page_limit"} {
		w := newTestWalker(Options{Seed: 11}, nil)
		v, ok := leafValue(w, schema, name)
		if !ok {
			t.Fatalf("%s: expected a generated value", name)
		}
		n, ok := v.(int64)
		if !ok || n < 0 {
			t.Fatalf("%s: expected non-negative, got %v", name, v)
		}
	}
}

// TestFieldNameCountRespectsExplicitSchemaMinimum proves the schema's OWN
// bounds always win over the name-driven "count/limit >= 0" default, even
// when the schema explicitly declares a negative range.
func TestFieldNameCountRespectsExplicitSchemaMinimum(t *testing.T) {
	schema := map[string]any{"type": "integer", "minimum": json.Number("-5"), "maximum": json.Number("-1")}
	w := newTestWalker(Options{Seed: 1}, nil)
	v, ok := leafValue(w, schema, "count")
	if !ok {
		t.Fatalf("expected a value")
	}
	n := v.(int64)
	if n < -5 || n > -1 {
		t.Fatalf("expected value within the EXPLICIT schema bounds [-5,-1], got %d", n)
	}
}

func TestMultipleOfIntegerRespected(t *testing.T) {
	schema := map[string]any{
		"type": "integer", "multipleOf": json.Number("5"),
		"minimum": json.Number("10"), "maximum": json.Number("40"),
	}
	for seed := range int64(30) {
		w := newTestWalker(Options{Seed: seed}, nil)
		v, ok := leafValue(w, schema, "amount")
		if !ok {
			t.Fatalf("seed %d: expected multipleOf to be handled", seed)
		}
		n := v.(int64)
		if n%5 != 0 || n < 10 || n > 40 {
			t.Fatalf("seed %d: value %d violates multipleOf=5 or the [10,40] bounds", seed, n)
		}
	}
}

func TestMultipleOfNumberRespected(t *testing.T) {
	schema := map[string]any{
		"type": "number", "multipleOf": json.Number("0.25"),
		"minimum": json.Number("0"), "maximum": json.Number("10"),
	}
	for seed := range int64(30) {
		w := newTestWalker(Options{Seed: seed}, nil)
		v, ok := leafValue(w, schema, "score")
		if !ok {
			t.Fatalf("seed %d: expected multipleOf to be handled for number", seed)
		}
		f := v.(float64)
		ratio := f / 0.25
		if math.Abs(ratio-math.Round(ratio)) > 1e-6 {
			t.Fatalf("seed %d: value %v is not a multiple of 0.25", seed, f)
		}
		if f < 0 || f > 10 {
			t.Fatalf("seed %d: value %v out of [0,10] bounds", seed, f)
		}
	}
}

// --- schema constraints on realistic (format/name-driven) strings --------

func TestGeneratedStringRespectsDeclaredMaxLength(t *testing.T) {
	schema := map[string]any{"type": "string", "maxLength": json.Number("10")}
	w := newTestWalker(Options{Seed: 3}, nil)
	v, ok := leafValue(w, schema, "user.description")
	if !ok {
		t.Fatalf("expected description realism to apply")
	}
	if len(v.(string)) > 10 {
		t.Fatalf("expected result truncated to maxLength=10, got %q (%d chars)", v, len(v.(string)))
	}
}

func TestGeneratedStringRespectsDeclaredMinLength(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("40")}
	w := newTestWalker(Options{Seed: 3}, nil)
	v, ok := leafValue(w, schema, "slug")
	if !ok {
		t.Fatalf("expected slug realism to apply")
	}
	if len(v.(string)) < 40 {
		t.Fatalf("expected result padded to minLength=40, got %q (%d chars)", v, len(v.(string)))
	}
}

// TestDeclaredStringLengthCapsHostileMinLength is the P1b round-1 review's
// finding 10, values.go's own half: an email/URL/name-realistic field with
// an attacker-controlled minLength (e.g. {"email":{"minLength":50000000}})
// reaches fitStringLength's strings.Repeat padding via declaredStringLength,
// a SEPARATE code path from schema.go's bare-fallback genString — it needs
// the identical clamp independently.
func TestDeclaredStringLengthCapsHostileMinLength(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("50000000")}
	minLen, _, hasMin, _ := declaredStringLength(schema)
	if !hasMin {
		t.Fatalf("expected hasMin=true")
	}
	if minLen > maxGenStringLength {
		t.Fatalf("declaredStringLength(minLength=5e7) = %d, want capped at maxGenStringLength (%d)", minLen, maxGenStringLength)
	}
}

// TestGeneratedStringHostileMinLengthDoesNotBlowBudget is the end-to-end
// reproduction through a realism-shaped field name (an "email" reaches
// generatedString's own fit()/fitStringLength path, not the bare fallback).
func TestGeneratedStringHostileMinLengthDoesNotBlowBudget(t *testing.T) {
	schema := map[string]any{"type": "string", "minLength": json.Number("50000000")}
	w := newTestWalker(Options{Seed: 1}, nil)
	v, ok := leafValue(w, schema, "email")
	if !ok {
		t.Fatalf("expected email realism to apply")
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T", v)
	}
	if len(s) > maxGenStringLength {
		t.Fatalf("email with minLength=5e7 generated %d bytes, want capped at maxGenStringLength (%d)", len(s), maxGenStringLength)
	}
}

func TestDeclaredStringLengthMinGreaterThanMaxDeclines(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{
		"type": "string", "format": "uuid",
		"minLength": json.Number("10"), "maxLength": json.Number("2"),
	}
	if _, ok := generatedString(w, schema, "x", "uuid", fieldKindGeneric); ok {
		t.Fatalf("expected decline when minLength > maxLength, leaving genScalar to raise ErrUnsatisfiable")
	}
}

// --- pattern (rule 5: cheap-only, documented limitation otherwise) -------

func TestAnchoredLiteralPatternHonoredExactly(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	schema := map[string]any{"type": "string", "pattern": "^published$"}
	v, ok := leafValue(w, schema, "status_literal")
	if !ok || v != "published" {
		t.Fatalf("expected the anchored literal pattern honored exactly, got (%v, %v)", v, ok)
	}
}

func TestNonCheapPatternDeclinesRatherThanGuessing(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)
	// A generic regex, on a field name with no other realism hint: cannot
	// be satisfied cheaply (rule 5), so leafValue must decline rather than
	// emit a value that likely violates the pattern.
	schema := map[string]any{"type": "string", "pattern": "^[a-z]{3,8}$"}
	if v, ok := leafValue(w, schema, "widget_token_xyz"); ok {
		t.Fatalf("expected leafValue to decline an unsatisfiable-cheaply pattern with no other source, got %v", v)
	}
}

// --- field-name classification regressions -----------------------------

func TestFieldNameOfExtractsLeafName(t *testing.T) {
	cases := []struct{ dataPath, want string }{
		{"name", "name"},
		{"user.profile.avatar_url", "avatar_url"},
		{"items[3].name", "name"},
		{"roles[2]", "roles"},
		{"", ""},
	}
	for _, c := range cases {
		if got := fieldNameOf(c.dataPath); got != c.want {
			t.Errorf("fieldNameOf(%q) = %q, want %q", c.dataPath, got, c.want)
		}
	}
}

// TestClassifyFieldNameTable doubles as the regression test for the
// anchoring rules in names.go: a plain substring match on "id" or "count"
// would misfire on "valid"/"discount", which is exactly why those rules use
// mExact/mSuffix, not mContains.
func TestClassifyFieldNameTable(t *testing.T) {
	cases := []struct {
		name string
		want fieldKind
	}{
		{"email", fieldKindEmail},
		{"contact_email", fieldKindEmail},
		{"avatar_url", fieldKindURL},
		{"photo", fieldKindURL},
		{"phone", fieldKindPhone},
		{"created_at", fieldKindTimestamp},
		{"first_name", fieldKindPersonName},
		{"organization_id", fieldKindID},
		{"id", fieldKindID},
		{"count", fieldKindCount},
		{"discount_amount", fieldKindGeneric}, // must NOT misfire on the "count" substring
		{"valid", fieldKindGeneric},           // must NOT misfire on the "id" substring
		{"widget", fieldKindGeneric},
	}
	for _, c := range cases {
		if got := classifyFieldName(c.name); got != c.want {
			t.Errorf("classifyFieldName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// --- determinism (the seed contract, applied to leafValue specifically) --

func TestGeneratedValueDeterministicPerDataPath(t *testing.T) {
	schema := map[string]any{"type": "string"}
	w1 := newTestWalker(Options{Seed: 42}, nil)
	w2 := newTestWalker(Options{Seed: 42}, nil)
	v1, ok1 := leafValue(w1, schema, "user.email")
	v2, ok2 := leafValue(w2, schema, "user.email")
	if !ok1 || !ok2 || v1 != v2 {
		t.Fatalf("expected identical seed+dataPath to produce identical values, got %v vs %v", v1, v2)
	}
}

func TestGeneratedValueVariesByDataPath(t *testing.T) {
	schema := map[string]any{"type": "string"}
	w := newTestWalker(Options{Seed: 42}, nil)
	a, _ := leafValue(w, schema, "user.email")
	b, _ := leafValue(w, schema, "admin.email")
	if a == b {
		t.Fatalf("expected different dataPaths to produce different emails, got the same for both: %v", a)
	}
}

// --- end to end, through the real Generator/openapi resolver -------------

// TestBodyFieldNameRealismEndToEnd proves the wiring, not just the unit:
// leafValue's field-name realism must actually be reachable from
// Generator.Body through the real walkSchema/walkObject path, not only when
// called directly.
func TestBodyFieldNameRealismEndToEnd(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/users/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"email":      map[string]any{"type": "string"},
										"avatar_url": map[string]any{"type": "string"},
									},
									"required": []any{"email", "avatar_url"},
								},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1})

	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		SchemaPtr: "#/paths/~1users~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1users~1{id}/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/users/{}", PathParams: map[string]string{"id": "1"}, Status: 200}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, b)
	}

	email, _ := got["email"].(string)
	if !strings.Contains(email, "@") {
		t.Fatalf("expected the email field to look like an email, got %q", email)
	}
	avatar, _ := got["avatar_url"].(string)
	if _, err := url.Parse(avatar); err != nil || !strings.HasPrefix(avatar, "https://") {
		t.Fatalf("expected the avatar_url field to look like a URL, got %q", avatar)
	}
}
