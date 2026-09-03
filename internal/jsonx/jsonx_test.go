package jsonx_test

import (
	stdjson "encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

// This file is the reason the swap to sonic is safe to keep: it compares the
// backend against encoding/json on exactly the shapes this codebase puts
// through it, using the STANDARD LIBRARY to build the expectation. Nothing
// here would notice a change in sonic's default config — every assertion is
// against what encoding/json actually does.

type custom struct{ N int }

func (c custom) MarshalJSON() ([]byte, error) { return stdjson.Marshal([]int{c.N, c.N}) }

func (c *custom) UnmarshalJSON(b []byte) error {
	var pair []int
	if err := stdjson.Unmarshal(b, &pair); err != nil {
		return err
	}
	if len(pair) > 0 {
		c.N = pair[0]
	}
	return nil
}

type nested struct {
	Map     map[string]int      `json:"map"`
	Raw     jsonx.RawMessage    `json:"raw"`
	Text    string              `json:"text"`
	Ptr     *int                `json:"ptr"`
	Omitted string              `json:"omitted,omitempty"`
	Custom  custom              `json:"custom"`
	Slice   []map[string]string `json:"slice"`
}

func fixture() nested {
	n := 7
	return nested{
		// Deliberately more than eight keys and in scrambled order: Go
		// randomises map iteration per run, so an unsorted encoder fails this
		// only SOMETIMES — which is exactly how it would reach production.
		Map:    map[string]int{"z": 1, "a": 2, "m": 3, "b": 4, "q": 5, "c": 6, "y": 7, "d": 8, "x": 9},
		Raw:    jsonx.RawMessage(`{"inner":[1,2,3]}`),
		Text:   `<script>alert("x") & 'y'</script>`,
		Ptr:    &n,
		Custom: custom{N: 3},
		Slice:  []map[string]string{{"k2": "v", "k1": "v"}},
	}
}

func TestMarshal_byteIdenticalToStdlib(t *testing.T) {
	want, err := stdjson.Marshal(fixture())
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}
	got, err := jsonx.Marshal(fixture())
	if err != nil {
		t.Fatalf("jsonx marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("marshal differs from encoding/json\n std: %s\njsonx: %s", want, got)
	}
}

func TestMarshal_isStableAcrossRuns(t *testing.T) {
	// The property the golden actually depends on. Repeated in one process
	// because map iteration order is re-randomised per map, not per process.
	first, err := jsonx.Marshal(fixture())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		got, err := jsonx.Marshal(fixture())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(first) {
			t.Fatalf("iteration %d differs — map keys are not being sorted\nfirst: %s\n got: %s", i, first, got)
		}
	}
}

func TestMarshal_escapesHTMLLikeStdlib(t *testing.T) {
	// sonic's DEFAULT config leaves these raw; encoding/json escapes them to
	// \u003c\u003e\u0026. A workspace can pin a response body containing them
	// and this plane serves it to a browser, so the two must agree.
	//
	// Compared against the stdlib rather than a literal typed here: the first
	// draft of this test asserted `{"s":"<>&"}` and failed for that reason,
	// which is a small demonstration of why the rule is worth keeping.
	const src = `<>&`
	want, err := stdjson.Marshal(map[string]string{"s": src})
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonx.Marshal(map[string]string{"s": src})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("HTML escaping differs\n std: %s\njsonx: %s", want, got)
	}
	if !strings.Contains(string(got), `\u003c`) {
		t.Errorf("expected the < to be escaped, got %s", got)
	}
}

func TestUnmarshal_roundTripsThroughStdlib(t *testing.T) {
	encoded, err := jsonx.Marshal(fixture())
	if err != nil {
		t.Fatal(err)
	}
	var viaStd nested
	if err := stdjson.Unmarshal(encoded, &viaStd); err != nil {
		t.Fatalf("stdlib could not read what jsonx wrote: %v", err)
	}

	stdEncoded, err := stdjson.Marshal(fixture())
	if err != nil {
		t.Fatal(err)
	}
	var viaJSONX nested
	if err := jsonx.Unmarshal(stdEncoded, &viaJSONX); err != nil {
		t.Fatalf("jsonx could not read what stdlib wrote: %v", err)
	}
}

func TestDecoder_useNumberKeepsTheLiteral(t *testing.T) {
	// internal/openapi reads spec bounds with UseNumber precisely so 1, 1.0
	// and 1e400 stay distinguishable; a backend that normalised them would
	// silently rewrite what a spec said.
	const src = `{"a":1.0,"b":1,"c":1e400,"d":0.1000,"e":123456789012345678901234567890}`

	decode := func(newDec func(io.Reader) interface {
		Decode(any) error
		UseNumber()
	}) map[string]stdjson.Number {
		d := newDec(strings.NewReader(src))
		d.UseNumber()
		var m map[string]stdjson.Number
		if err := d.Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return m
	}

	want := decode(func(r io.Reader) interface {
		Decode(any) error
		UseNumber()
	} {
		return stdjson.NewDecoder(r)
	})
	got := decode(func(r io.Reader) interface {
		Decode(any) error
		UseNumber()
	} {
		return jsonx.NewDecoder(r)
	})

	for k, w := range want {
		if got[k].String() != w.String() {
			t.Errorf("number %q: stdlib %q, jsonx %q", k, w, got[k])
		}
	}
}

func TestDecoder_disallowUnknownFieldsAndMore(t *testing.T) {
	dec := jsonx.NewDecoder(strings.NewReader(`{"a":1} {"a":2}`))
	dec.DisallowUnknownFields()
	var first struct {
		A int `json:"a"`
	}
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !dec.More() {
		t.Error("More() must see the trailing second document — it is how decodeJSON rejects trailing data")
	}

	strict := jsonx.NewDecoder(strings.NewReader(`{"a":1,"nope":2}`))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&first); err == nil {
		t.Error("DisallowUnknownFields accepted an unknown field")
	}
}

func TestValid_agreesWithStdlib(t *testing.T) {
	for _, s := range []string{`{"a":1}`, `{`, `null`, ``, `1.0`, `"x"`, `[1,]`, `{"a":}`} {
		if got, want := jsonx.Valid([]byte(s)), stdjson.Valid([]byte(s)); got != want {
			t.Errorf("Valid(%q): jsonx=%v stdlib=%v", s, got, want)
		}
	}
}

// TestMalformed_recognisesTheBackendsOwnErrors is the regression guard for the
// one divergence that would otherwise be silent: sonic reports a type mismatch
// as *decoder.MismatchTypeError, not *json.UnmarshalTypeError, so the
// errors.As that used to answer this question still compiles and stops
// matching — turning a 400 into a 500 with nothing to notice it.
func TestMalformed_recognisesTheBackendsOwnErrors(t *testing.T) {
	type body struct {
		Name string `json:"name"`
	}

	cases := []struct {
		name string
		src  string
	}{
		{"type mismatch", `{"name":123}`},
		{"truncated", `{"name":`},
		{"empty", ``},
		{"garbage", `not json at all`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var v body
			err := jsonx.NewDecoder(strings.NewReader(tc.src)).Decode(&v)
			if err == nil {
				t.Fatalf("expected a decode error for %q", tc.src)
			}
			if !jsonx.Malformed(err) {
				t.Errorf("Malformed(%T: %v) = false, want true — a client mistake would be answered 500", err, err)
			}
		})
	}

	t.Run("unknown field", func(t *testing.T) {
		dec := jsonx.NewDecoder(strings.NewReader(`{"name":"a","extra":1}`))
		dec.DisallowUnknownFields()
		var v body
		err := dec.Decode(&v)
		if err == nil {
			t.Fatal("expected an unknown-field error")
		}
		if !jsonx.Malformed(err) {
			t.Errorf("Malformed(%v) = false, want true", err)
		}
	})

	t.Run("not a decode failure", func(t *testing.T) {
		if jsonx.Malformed(nil) {
			t.Error("Malformed(nil) must be false")
		}
		if jsonx.Malformed(errors.New("database is down")) {
			t.Error("an unrelated error must not be reported as malformed input — that is how a 500 becomes a 400")
		}
	})
}
