package gen

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

// TestJSONSize_matchesEncodingJSON holds jsonSize to the byte against the
// real encoder over every shape the walkers emit and every escaping rule
// the encoder has — this package's budget tests and the 419-body golden
// both depend on the two never disagreeing.
func TestJSONSize_matchesEncodingJSON(t *testing.T) {
	t.Parallel()
	values := []any{
		nil, true, false, "", "plain", `quote " backslash \`, "tab\tnl\ncr\r bs\b ff\f", "ctl\x01\x1f",
		"<b>&amp;</b>", "ünïcödé ☃ 𝄞", "line sep ", "bad\xffutf8\xc0", "\x7f",
		0, 1, -1, 42, int64(math.MaxInt64), int64(math.MinInt64), int32(-7), uint64(math.MaxUint64),
		0.0, math.Copysign(0, -1), 1.0, 0.5, -2.25, 1e20, 1e21, 123456789012345678901234.0, 1e-6, 9.99e-7, 1e-7, 1.5e-10, 1e300, -1e-300,
		float32(0.1), jsonx.Number("9007199254740993"), jsonx.Number("1.0"), jsonx.Number("-0"),
		[]any{}, []any{1}, []any{"a", nil, 2.5, true}, []any{[]any{}, map[string]any{}},
		map[string]any{}, map[string]any{"k": "v"}, map[string]any{"a": 1, "b<": "&", "c": []any{map[string]any{"z": nil}}},
	}
	for _, v := range values {
		want, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("Marshal(%#v): %v", v, err)
		}
		got, ok := jsonSize(v)
		if !ok {
			t.Errorf("jsonSize(%#v): not modelled, want %d", v, len(want))
			continue
		}
		if got != len(want) {
			t.Errorf("jsonSize(%#v) = %d, want %d (%s)", v, got, len(want), want)
		}
	}
	for _, v := range []any{math.Inf(1), math.NaN(), struct{}{}, []string{"x"}} {
		if n, ok := jsonSize(v); ok && v != nil {
			if _, err := json.Marshal(v); err == nil {
				t.Errorf("jsonSize(%#v) = %d claims to model a type the fallback must handle", v, n)
			}
		}
	}
}
