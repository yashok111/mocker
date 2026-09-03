package gen

import (
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

// jsonSize is the byte length jsonx.Marshal would produce for v, computed
// without producing it. It exists because the budget accounting calls
// estimateJSONSize once per scalar property, once per array item and once
// per required-floor value — thousands of times for a 200-row list — and
// each call was a full reflective Marshal, the single largest cost of a
// generated body (measured 2026-09-03: 1.34 s and 3.9 M allocations for
// the acceptance corpus's 130 primary variants, the sizer's share the
// biggest of any one thing).
//
// It must be EXACT, not an estimate: the byte-budget tests pin overshoot to
// the byte, and the golden over 419 bodies would move on a one-byte
// disagreement. So it reproduces encoding/json's own rules — HTML-escaped
// strings (the jsonx backend is stdlib with default escaping, see
// jsonx.go), \ufffd for invalid UTF-8, U+2028/2029 escaped, the 'e' form
// for floats below 1e-6 or at 1e21 and above with the exponent's leading
// zero dropped — and answers ok=false for any type it does not model, so
// the caller falls back to the real Marshal rather than guess. jsonsize_test
// holds it against encoding/json over every shape the walkers emit.
func jsonSize(v any) (int, bool) {
	switch x := v.(type) {
	case nil:
		return len("null"), true
	case bool:
		if x {
			return len("true"), true
		}
		return len("false"), true
	case string:
		return jsonStringSize(x), true
	case jsonx.Number:
		return len(x), true
	case float64:
		return jsonFloatSize(x, 64)
	case float32:
		return jsonFloatSize(float64(x), 32)
	case int:
		return intSize(int64(x)), true
	case int64:
		return intSize(x), true
	case int32:
		return intSize(int64(x)), true
	case uint64:
		return len(strconv.FormatUint(x, 10)), true
	case map[string]any:
		n := 2
		for k, e := range x {
			es, ok := jsonSize(e)
			if !ok {
				return 0, false
			}
			n += jsonStringSize(k) + 1 + es
		}
		if len(x) > 1 {
			n += len(x) - 1
		}
		return n, true
	case []any:
		n := 2
		for _, e := range x {
			es, ok := jsonSize(e)
			if !ok {
				return 0, false
			}
			n += es
		}
		if len(x) > 1 {
			n += len(x) - 1
		}
		return n, true
	default:
		return 0, false
	}
}

func intSize(n int64) int {
	if n == 0 {
		return 1
	}
	size := 0
	if n < 0 {
		size = 1
		if n == math.MinInt64 {
			return len("-9223372036854775808")
		}
		n = -n
	}
	for n > 0 {
		size++
		n /= 10
	}
	return size
}

// jsonFloatSize mirrors encoding/json's floatEncoder: NaN and the
// infinities are unsupported values (Marshal errors), 'f' formatting
// except below 1e-6 or from 1e21 on, where the 'e' form is used and an
// exponent like e-07 is shortened to e-7.
func jsonFloatSize(f float64, bits int) (int, bool) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	var scratch [32]byte
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			format = 'e'
		}
	}
	b := strconv.AppendFloat(scratch[:0], f, format, -1, bits)
	if format == 'e' {
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			return n - 1, true
		}
	}
	return len(b), true
}

// jsonStringSize is 2 quotes plus encoding/json's escaped length of s with
// HTML escaping on (the stdlib default jsonx keeps).
func jsonStringSize(s string) int {
	n := 2
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&':
				n++
			case c == '\\' || c == '"' || c == '\b' || c == '\f' || c == '\n' || c == '\r' || c == '\t':
				n += 2
			default:
				n += len(`\u0000`)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// The encoder writes U+FFFD itself (three bytes), not an
			// escape sequence.
			n += utf8.RuneLen(utf8.RuneError)
		case r == '\u2028' || r == '\u2029':
			n += len(`\u2028`)
		default:
			n += size
		}
		i += size
	}
	return n
}
