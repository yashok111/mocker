package overrides_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

// decodeBody is a small helper that runs real encoding/json over s so the
// number-rendering tests exercise what a genuine decode produces (float64
// for every plain JSON number) rather than a hand-built any value that
// might not match the shapes a real request body actually decodes to.
func decodeBody(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decodeBody(%q): %v", s, err)
	}
	return v
}

// TestCondition_Match covers every In x Op combination, an unknown In, an
// unknown Op, and the value-shape edge cases (null/object/array/bool) the
// task calls out individually.
func TestCondition_Match(t *testing.T) {
	tests := []struct {
		name string
		cond overrides.Condition
		in   overrides.Input
		want bool
	}{
		// --- query -----------------------------------------------------
		{
			name: "query exists matches a present key with a value",
			cond: overrides.Condition{In: "query", Name: "verbose", Op: "exists"},
			in:   overrides.Input{Query: url.Values{"verbose": {"true"}}},
			want: true,
		},
		{
			name: "query exists matches a present key with an EMPTY value",
			cond: overrides.Condition{In: "query", Name: "verbose", Op: "exists"},
			in:   overrides.Input{Query: url.Values{"verbose": {""}}},
			want: true,
		},
		{
			name: "query exists does not match an absent key",
			cond: overrides.Condition{In: "query", Name: "verbose", Op: "exists"},
			in:   overrides.Input{Query: url.Values{}},
			want: false,
		},
		{
			name: "query equals matches an exact value",
			cond: overrides.Condition{In: "query", Name: "verbose", Op: "equals", Value: "true"},
			in:   overrides.Input{Query: url.Values{"verbose": {"true"}}},
			want: true,
		},
		{
			name: "query equals does not match a different value",
			cond: overrides.Condition{In: "query", Name: "verbose", Op: "equals", Value: "true"},
			in:   overrides.Input{Query: url.Values{"verbose": {"false"}}},
			want: false,
		},
		{
			name: "query equals is exact-case on the parameter NAME",
			cond: overrides.Condition{In: "query", Name: "Verbose", Op: "equals", Value: "true"},
			in:   overrides.Input{Query: url.Values{"verbose": {"true"}}},
			want: false,
		},
		{
			name: "query contains matches a substring",
			cond: overrides.Condition{In: "query", Name: "q", Op: "contains", Value: "cat"},
			in:   overrides.Input{Query: url.Values{"q": {"concatenate"}}},
			want: true,
		},
		{
			name: "query contains does not match when the substring is absent",
			cond: overrides.Condition{In: "query", Name: "q", Op: "contains", Value: "dog"},
			in:   overrides.Input{Query: url.Values{"q": {"concatenate"}}},
			want: false,
		},
		{
			name: "query equals over several values matches when ONLY THE SECOND matches",
			cond: overrides.Condition{In: "query", Name: "tag", Op: "equals", Value: "bar"},
			in:   overrides.Input{Query: url.Values{"tag": {"foo", "bar"}}},
			want: true,
		},
		{
			name: "query equals over several values, none matching",
			cond: overrides.Condition{In: "query", Name: "tag", Op: "equals", Value: "baz"},
			in:   overrides.Input{Query: url.Values{"tag": {"foo", "bar"}}},
			want: false,
		},

		// --- header ------------------------------------------------------
		{
			name: "header exists matches a set header",
			cond: overrides.Condition{In: "header", Name: "X-Debug", Op: "exists"},
			in:   overrides.Input{Header: http.Header{"X-Debug": {"1"}}},
			want: true,
		},
		{
			name: "header exists does not match an absent header",
			cond: overrides.Condition{In: "header", Name: "X-Debug", Op: "exists"},
			in:   overrides.Input{Header: http.Header{}},
			want: false,
		},
		{
			name: "header equals matches",
			cond: overrides.Condition{In: "header", Name: "X-Env", Op: "equals", Value: "staging"},
			in:   overrides.Input{Header: http.Header{"X-Env": {"staging"}}},
			want: true,
		},
		{
			name: "header equals does not match a different value",
			cond: overrides.Condition{In: "header", Name: "X-Env", Op: "equals", Value: "staging"},
			in:   overrides.Input{Header: http.Header{"X-Env": {"prod"}}},
			want: false,
		},
		{
			name: "header lookup is case-INsensitive: x-test finds X-Test",
			cond: overrides.Condition{In: "header", Name: "x-test", Op: "equals", Value: "1"},
			in:   overrides.Input{Header: http.Header{"X-Test": {"1"}}},
			want: true,
		},
		{
			name: "header contains matches a substring",
			cond: overrides.Condition{In: "header", Name: "User-Agent", Op: "contains", Value: "curl"},
			in:   overrides.Input{Header: http.Header{"User-Agent": {"curl/8.0"}}},
			want: true,
		},
		{
			name: "header contains does not match when the substring is absent",
			cond: overrides.Condition{In: "header", Name: "User-Agent", Op: "contains", Value: "wget"},
			in:   overrides.Input{Header: http.Header{"User-Agent": {"curl/8.0"}}},
			want: false,
		},

		// --- body ----------------------------------------------------------
		{
			name: "body exists matches a present top-level field",
			cond: overrides.Condition{In: "body", Name: "email", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `{"email":"a@b.com"}`), BodyOK: true},
			want: true,
		},
		{
			name: "body exists does not match an absent field",
			cond: overrides.Condition{In: "body", Name: "email", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `{}`), BodyOK: true},
			want: false,
		},
		{
			name: "body equals matches a string field",
			cond: overrides.Condition{In: "body", Name: "email", Op: "equals", Value: "a@b.com"},
			in:   overrides.Input{Body: decodeBody(t, `{"email":"a@b.com"}`), BodyOK: true},
			want: true,
		},
		{
			name: "body equals does not match a different value",
			cond: overrides.Condition{In: "body", Name: "email", Op: "equals", Value: "a@b.com"},
			in:   overrides.Input{Body: decodeBody(t, `{"email":"other@b.com"}`), BodyOK: true},
			want: false,
		},
		{
			name: "body contains matches a substring",
			cond: overrides.Condition{In: "body", Name: "email", Op: "contains", Value: "@b.com"},
			in:   overrides.Input{Body: decodeBody(t, `{"email":"a@b.com"}`), BodyOK: true},
			want: true,
		},
		{
			name: "body: a non-object top level (array) never matches, exists included",
			cond: overrides.Condition{In: "body", Name: "email", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `["a","b"]`), BodyOK: true},
			want: false,
		},
		{
			name: "body: a non-object top level (scalar) never matches, exists included",
			cond: overrides.Condition{In: "body", Name: "email", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `"just a string"`), BodyOK: true},
			want: false,
		},

		// --- BodyOK=false: every in:"body" condition is false, exists too ---
		{
			name: "BodyOK=false: exists is false even though the field would be present",
			cond: overrides.Condition{In: "body", Name: "email", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `{"email":"a@b.com"}`), BodyOK: false},
			want: false,
		},
		{
			name: "BodyOK=false: equals is false even on a value that would match",
			cond: overrides.Condition{In: "body", Name: "email", Op: "equals", Value: "a@b.com"},
			in:   overrides.Input{Body: decodeBody(t, `{"email":"a@b.com"}`), BodyOK: false},
			want: false,
		},

		// --- number rendering ------------------------------------------------
		{
			name: "number rendering: JSON 1 renders \"1\"",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: "1"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":1}`), BodyOK: true},
			want: true,
		},
		{
			name: "number rendering: JSON 1.0 also renders \"1\"",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: "1"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":1.0}`), BodyOK: true},
			want: true,
		},
		{
			name: "number rendering: JSON 1.00 also renders \"1\"",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: "1"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":1.00}`), BodyOK: true},
			want: true,
		},
		{
			name: "number rendering: 1e3 renders \"1000\", not exponential notation",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: "1000"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":1e3}`), BodyOK: true},
			want: true,
		},
		{
			name: "number rendering: a plain float64 renders the same as the equivalent json.Number",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: "1"},
			in:   overrides.Input{Body: map[string]any{"n": json.Number("1")}, BodyOK: true},
			want: true,
		},

		// --- bool ------------------------------------------------------------
		{
			name: "bool true renders \"true\"",
			cond: overrides.Condition{In: "body", Name: "ok", Op: "equals", Value: "true"},
			in:   overrides.Input{Body: decodeBody(t, `{"ok":true}`), BodyOK: true},
			want: true,
		},
		{
			name: "bool false renders \"false\"",
			cond: overrides.Condition{In: "body", Name: "ok", Op: "equals", Value: "false"},
			in:   overrides.Input{Body: decodeBody(t, `{"ok":false}`), BodyOK: true},
			want: true,
		},

		// --- null / object / array values: equals and contains never match ---
		{
			name: "a null-valued field never matches equals",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: ""},
			in:   overrides.Input{Body: decodeBody(t, `{"n":null}`), BodyOK: true},
			want: false,
		},
		{
			name: "an object-valued field never matches equals",
			cond: overrides.Condition{In: "body", Name: "n", Op: "equals", Value: "[object Object]"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":{"x":1}}`), BodyOK: true},
			want: false,
		},
		{
			name: "an array-valued field never matches contains",
			cond: overrides.Condition{In: "body", Name: "n", Op: "contains", Value: "1"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":[1,2,3]}`), BodyOK: true},
			want: false,
		},
		{
			// SEMANTICS: "a body key that is present even when its value is
			// null" — exists asks presence, not renderability, so a null
			// value (unlike equals/contains above) still counts as present.
			name: "a null-valued field STILL matches exists (presence, not rendering)",
			cond: overrides.Condition{In: "body", Name: "n", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":null}`), BodyOK: true},
			want: true,
		},
		{
			name: "an object-valued field still matches exists",
			cond: overrides.Condition{In: "body", Name: "n", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":{"x":1}}`), BodyOK: true},
			want: true,
		},
		{
			name: "an array-valued field still matches exists",
			cond: overrides.Condition{In: "body", Name: "n", Op: "exists"},
			in:   overrides.Input{Body: decodeBody(t, `{"n":[1,2,3]}`), BodyOK: true},
			want: true,
		},

		// --- unknown In / unknown Op: never match, no error, no panic --------
		{
			name: "unknown In never matches, for any op",
			cond: overrides.Condition{In: "cookie", Name: "session", Op: "exists"},
			in:   overrides.Input{Query: url.Values{"session": {"x"}}, Header: http.Header{"Session": {"x"}}},
			want: false,
		},
		{
			name: "unknown Op never matches, for a recognised In",
			cond: overrides.Condition{In: "query", Name: "verbose", Op: "startsWith", Value: "t"},
			in:   overrides.Input{Query: url.Values{"verbose": {"true"}}},
			want: false,
		},
		{
			name: "unknown In AND unknown Op together: still just false",
			cond: overrides.Condition{In: "cookie", Name: "x", Op: "startsWith", Value: "t"},
			in:   overrides.Input{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Match panicked: %v", r)
					}
				}()
				if got := tt.cond.Match(tt.in); got != tt.want {
					t.Errorf("Match() = %v, want %v", got, tt.want)
				}
			}()
		})
	}
}

// TestMatchAll_emptyListNeverMatches proves the "no when[] is the fallback,
// never a candidate" rule: MatchAll([]) must be false, not vacuously true.
func TestMatchAll_emptyListNeverMatches(t *testing.T) {
	t.Parallel()
	if overrides.MatchAll(nil, overrides.Input{}) {
		t.Error("MatchAll(nil, ...) = true, want false")
	}
	if overrides.MatchAll([]overrides.Condition{}, overrides.Input{}) {
		t.Error("MatchAll([]Condition{}, ...) = true, want false")
	}
}

// TestMatchAll_isAND proves two conditions must BOTH hold.
func TestMatchAll_isAND(t *testing.T) {
	t.Parallel()
	conds := []overrides.Condition{
		{In: "query", Name: "verbose", Op: "equals", Value: "true"},
		{In: "header", Name: "X-Debug", Op: "exists"},
	}

	tests := []struct {
		name string
		in   overrides.Input
		want bool
	}{
		{
			name: "both hold",
			in: overrides.Input{
				Query:  url.Values{"verbose": {"true"}},
				Header: http.Header{"X-Debug": {"1"}},
			},
			want: true,
		},
		{
			name: "only the first holds",
			in: overrides.Input{
				Query:  url.Values{"verbose": {"true"}},
				Header: http.Header{},
			},
			want: false,
		},
		{
			name: "only the second holds",
			in: overrides.Input{
				Query:  url.Values{"verbose": {"false"}},
				Header: http.Header{"X-Debug": {"1"}},
			},
			want: false,
		},
		{
			name: "neither holds",
			in:   overrides.Input{Query: url.Values{}, Header: http.Header{}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := overrides.MatchAll(conds, tt.in); got != tt.want {
				t.Errorf("MatchAll() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSelectWhen_skipsVariantsWithNoWhen proves a variant with an empty (or
// absent) When is never a when[]-candidate, even though its status would
// otherwise sort first.
func TestSelectWhen_skipsVariantsWithNoWhen(t *testing.T) {
	t.Parallel()
	responses := map[string]overrides.Variant{
		"200": {Mode: "generated"}, // no When at all: never a candidate
		"409": {
			Mode: "pinned",
			When: []overrides.Condition{{In: "query", Name: "verbose", Op: "equals", Value: "true"}},
		},
	}
	in := overrides.Input{Query: url.Values{"verbose": {"true"}}}

	status, ok := overrides.SelectWhen(responses, in)
	if !ok || status != "409" {
		t.Fatalf("SelectWhen() = (%q, %v), want (\"409\", true)", status, ok)
	}
}

// TestSelectWhen_noCandidateMatches proves the (\"\", false) fallback signal
// when nothing with a non-empty When[] matches — the caller is expected to
// fall back to active_status in that case.
func TestSelectWhen_noCandidateMatches(t *testing.T) {
	t.Parallel()
	responses := map[string]overrides.Variant{
		"200": {Mode: "generated"},
		"409": {
			Mode: "pinned",
			When: []overrides.Condition{{In: "query", Name: "verbose", Op: "equals", Value: "true"}},
		},
	}
	status, ok := overrides.SelectWhen(responses, overrides.Input{Query: url.Values{}})
	if ok || status != "" {
		t.Fatalf("SelectWhen() = (%q, %v), want (\"\", false)", status, ok)
	}
}

// TestSelectWhen_unparseableKeyIsSkipped proves a responses key that is not
// a decimal status (never legitimately reachable through Put/PutMany, but
// SelectWhen must still not choke on one built by hand) is never a
// candidate, rather than sorting arbitrarily or panicking.
func TestSelectWhen_unparseableKeyIsSkipped(t *testing.T) {
	t.Parallel()
	responses := map[string]overrides.Variant{
		"2XX": {
			Mode: "pinned",
			When: []overrides.Condition{{In: "query", Name: "verbose", Op: "equals", Value: "true"}},
		},
		"409": {
			Mode: "pinned",
			When: []overrides.Condition{{In: "query", Name: "verbose", Op: "equals", Value: "true"}},
		},
	}
	in := overrides.Input{Query: url.Values{"verbose": {"true"}}}

	status, ok := overrides.SelectWhen(responses, in)
	if !ok || status != "409" {
		t.Fatalf("SelectWhen() = (%q, %v), want (\"409\", true) — the unparseable key must never win", status, ok)
	}
}

// TestSelectWhen_ascendingNumericOrderIsDeterministic is the property the
// task calls "easy to get wrong and impossible to spot later": Row.Responses
// is a Go map, and Go deliberately randomises map iteration. Two variants
// that BOTH match must resolve to the lower status EVERY time, not just on
// whichever run got lucky with map order — so this rebuilds the map fresh
// on every iteration and runs enough iterations that a random-order
// implementation (map iteration order, unsorted) would fail with
// overwhelming probability.
func TestSelectWhen_ascendingNumericOrderIsDeterministic(t *testing.T) {
	t.Parallel()
	cond := []overrides.Condition{{In: "query", Name: "verbose", Op: "equals", Value: "true"}}
	in := overrides.Input{Query: url.Values{"verbose": {"true"}}}

	for i := range 50 {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			// A fresh map literal per call: Go re-randomises iteration
			// order per range, but building a new map each time removes
			// any doubt that the SAME underlying bucket layout is being
			// reused across iterations.
			responses := map[string]overrides.Variant{
				"500": {Mode: "pinned", When: cond},
				"200": {Mode: "pinned", When: cond},
				"409": {Mode: "pinned", When: cond},
			}
			status, ok := overrides.SelectWhen(responses, in)
			if !ok || status != "200" {
				t.Fatalf("iteration %d: SelectWhen() = (%q, %v), want (\"200\", true) — the lowest matching status must always win", i, status, ok)
			}
		})
	}
}

// TestValidateConditions_acceptsTheThreeExistingFixtures proves the write
// gate accepts exactly the shapes already stored by earlier phases:
// internal/overrides/repo_test.go's fullVariant() and
// internal/admin/override_handlers_test.go's PUT fixture. If either of
// these newly failed ValidateConditions, Put() would start rejecting rows
// that round-tripped cleanly before this file existed.
func TestValidateConditions_acceptsTheThreeExistingFixtures(t *testing.T) {
	t.Parallel()
	fixtures := []overrides.Condition{
		{In: "query", Name: "verbose", Op: "equals", Value: "true"}, // repo_test.go fullVariant()
		{In: "header", Name: "X-Debug", Op: "exists"},               // repo_test.go fullVariant()
		{In: "header", Name: "X-Test", Op: "exists"},                // admin/override_handlers_test.go
	}
	for _, c := range fixtures {
		if err := overrides.ValidateConditions([]overrides.Condition{c}); err != nil {
			t.Errorf("ValidateConditions(%+v) = %v, want nil", c, err)
		}
	}
	// And together, since that is how fullVariant() actually stores them.
	if err := overrides.ValidateConditions(fixtures[:2]); err != nil {
		t.Errorf("ValidateConditions(fullVariant's two conditions) = %v, want nil", err)
	}
}

// TestValidateConditions_rejectsWhatMatchCannotEvaluate is the write-time
// mirror of TestCondition_Match's tolerant cases: everything Match quietly
// treats as "never matches" must be a hard rejection here instead, so an
// operator gets a 400 at write time rather than a when[] that silently
// never fires.
func TestValidateConditions_rejectsWhatMatchCannotEvaluate(t *testing.T) {
	tests := []struct {
		name string
		cond overrides.Condition
	}{
		{name: "unknown in", cond: overrides.Condition{In: "cookie", Name: "session", Op: "exists"}},
		{name: "unknown op", cond: overrides.Condition{In: "query", Name: "q", Op: "startsWith", Value: "x"}},
		{name: "empty name", cond: overrides.Condition{In: "query", Name: "", Op: "exists"}},
		{name: "equals with no value", cond: overrides.Condition{In: "query", Name: "q", Op: "equals"}},
		{name: "contains with no value", cond: overrides.Condition{In: "query", Name: "q", Op: "contains"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := overrides.ValidateConditions([]overrides.Condition{tt.cond}); err == nil {
				t.Errorf("ValidateConditions(%+v) = nil, want an error", tt.cond)
			}
		})
	}
}

// TestValidateConditions_existsNeedsNoValue proves exists is exempt from
// the missing-Value rejection equals/contains are subject to.
func TestValidateConditions_existsNeedsNoValue(t *testing.T) {
	t.Parallel()
	cond := overrides.Condition{In: "header", Name: "X-Debug", Op: "exists"}
	if err := overrides.ValidateConditions([]overrides.Condition{cond}); err != nil {
		t.Errorf("ValidateConditions(exists, no value) = %v, want nil", err)
	}
}

// TestValidateVariant_wrapsConditionErrorsInErrInvalidRow proves the wiring
// this file's whole write-time gate depends on: a Variant carrying a
// condition ValidateConditions rejects must fail ValidateVariant too,
// wrapped in the SAME ErrInvalidRow sentinel every other structural
// rejection in this package already uses.
func TestValidateVariant_wrapsConditionErrorsInErrInvalidRow(t *testing.T) {
	t.Parallel()
	v := overrides.Variant{
		Mode: "generated",
		When: []overrides.Condition{{In: "cookie", Name: "session", Op: "exists"}},
	}
	err := overrides.ValidateVariant(v)
	if !errors.Is(err, overrides.ErrInvalidRow) {
		t.Fatalf("ValidateVariant() error = %v, want it to wrap ErrInvalidRow", err)
	}
}

// TestValidateVariant_acceptsAValidWhen is ValidateVariant's happy path for
// When specifically — every other Mode/BodyEncoding/recipe case is already
// covered by overrides_test.go's TestValidation_* suite, unchanged.
func TestValidateVariant_acceptsAValidWhen(t *testing.T) {
	t.Parallel()
	v := overrides.Variant{
		Mode: "generated",
		When: []overrides.Condition{{In: "query", Name: "verbose", Op: "equals", Value: "true"}},
	}
	if err := overrides.ValidateVariant(v); err != nil {
		t.Errorf("ValidateVariant() = %v, want nil", err)
	}
}
