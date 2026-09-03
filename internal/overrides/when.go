package overrides

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Input is one request as the when-evaluator sees it. mockplane builds it
// ONCE per request — parsing the query, keeping the header multimap as-is,
// decoding the body at most once — and passes the SAME value to every
// candidate variant it tries; Match itself does no parsing and has no side
// effects, so trying five variants against one Input costs five map/slice
// lookups, not five re-decodes of the request.
type Input struct {
	Query  url.Values  // r.URL.Query()
	Header http.Header // r.Header — looked up with Header.Get, which canonicalises the name
	// Body is the decoded JSON body: map[string]any, []any or a scalar
	// (string, float64, jsonx.Number or bool) exactly as encoding/json
	// produced it; nil when there is none.
	Body any
	// BodyOK is false when the body was absent, unparsable or truncated.
	// Every in:"body" condition then evaluates false — never a match
	// against a partial document a truncated read happened to produce.
	BodyOK bool
}

// Match reports whether c holds against in, per the SEMANTICS DESIGN §12
// ("Условия срабатывания (when)") only sketches and this file has to pin
// down: In is "query" | "header" | "body", Op is "equals" | "contains" |
// "exists", equals/contains compare Op's rendering of the looked-up value
// against Value, and exists asks only whether the name is present — value
// shape and value emptiness never enter into it.
//
// Match is TOTAL: an unrecognised In or Op — a shape [ValidateConditions]
// would reject on a fresh write, but that a row from an older build, a
// hand-run UPDATE or a future version this one cannot fully interpret can
// still carry — never matches, never errors, never panics. That is what
// lets op_overrides' read path (repo.go's scan(), which shares this same
// package's [ValidateResponses]/[ValidateVariant] with the write path — see
// their comments) go on serving every OTHER response variant those checks
// left it able to load, rather than the one bad condition taking the whole
// evaluation down.
func (c Condition) Match(in Input) bool {
	switch c.In {
	case "query":
		return matchQuery(c, in.Query)
	case "header":
		return matchHeader(c, in.Header)
	case "body":
		if !in.BodyOK {
			return false
		}
		return matchBody(c, in.Body)
	default:
		return false
	}
}

// matchQuery looks c.Name up in q EXACT-CASE — unlike a header name, a
// query parameter name is not canonicalised by anything between the client
// and here, so "Verbose" and "verbose" are different parameters, not the
// same one spelled two ways. A parameter repeated with several values
// matches equals/contains when ANY one of them does (DESIGN's own example:
// checking one email among several query values a form might resubmit).
func matchQuery(c Condition, q url.Values) bool {
	values, ok := q[c.Name]
	switch c.Op {
	case "exists":
		return ok // present at all — even a bare "?verbose" (value "") counts
	case "equals":
		return slices.Contains(values, c.Value)
	case "contains":
		for _, v := range values {
			if strings.Contains(v, c.Value) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// matchHeader uses Header.Get exclusively (per Input.Header's own comment),
// which both canonicalises the name ("x-test" finds a header the client
// sent as "X-Test") and collapses a repeated header to its first value —
// unlike matchQuery, there is no ANY-of-several-values rule for headers
// here. The one imprecision this buys: Get("X-Debug") returns "" both when
// the header is absent AND when it is present but sent empty
// ("X-Debug:\r\n"), so exists reads a present-but-empty header as absent.
// That is the same trade DESIGN's own vocabulary for this ("a header that
// is set") leaves open, and an operator who needs to distinguish those two
// cases can test equals against "" instead.
func matchHeader(c Condition, h http.Header) bool {
	v := h.Get(c.Name)
	switch c.Op {
	case "exists":
		return v != ""
	case "equals":
		return v == c.Value
	case "contains":
		return strings.Contains(v, c.Value)
	default:
		return false
	}
}

// matchBody looks c.Name up as a TOP-LEVEL key of body — no dot paths, no
// array indexes, this slice's DESIGN §19 line 1111 asks for equality on
// exactly one field. A body whose top level is not a JSON object (an array
// or a bare scalar) has no field to look up at all, so it never matches
// any in:"body" condition, exists included.
//
// exists asks only presence: a key mapped to JSON null, an object or an
// array still counts as present (DESIGN's own wording: "a body key that is
// present even when its value is null"). equals/contains need a value they
// can RENDER to a string first — renderValue is what draws that line, and
// null/object/array values fail it, so equals/contains never match them
// even though exists, asking a different question, still can.
func matchBody(c Condition, body any) bool {
	obj, ok := body.(map[string]any)
	if !ok {
		return false
	}
	val, present := obj[c.Name]
	if c.Op == "exists" {
		return present
	}
	if !present {
		return false
	}
	rendered, ok := renderValue(val)
	if !ok {
		return false // null, object or array: nothing to compare Value against
	}
	switch c.Op {
	case "equals":
		return rendered == c.Value
	case "contains":
		return strings.Contains(rendered, c.Value)
	default:
		return false
	}
}

// renderValue is the whole of the number question this file has to answer:
// a decoded body can hold a plain float64 (the encoding/json default) or a
// jsonx.Number (a decoder using UseNumber()), and JSON's own 1, 1.0 and
// 1.00 must all render "1" so a condition written against one of those
// spellings still matches a body that happens to use another. 'f' with
// precision -1 is what keeps strconv.FormatFloat from ever falling back to
// exponential notation (1e3 must render "1000", not "1e+03") — the default
// verb %v would not make that promise.
//
// The false branch covers JSON null (Go nil), an object (map[string]any)
// or an array ([]any): none of them has a canonical single-string
// rendering, so equals/contains can never match one — that is a property
// of Op, not of Name's presence, which is why exists does not route
// through this function at all.
func renderValue(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case jsonx.Number:
		return t.String(), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return "", false
	}
}

// MatchAll reports whether EVERY condition in conds holds against in — the
// AND DESIGN §12 describes for one variant's when[]. An EMPTY list returns
// false, not true: a variant with no when[] is the eventual active_status
// fallback, never itself a when[]-matched candidate — SelectWhen relies on
// this to skip such variants without a separate len check of its own.
func MatchAll(conds []Condition, in Input) bool {
	if len(conds) == 0 {
		return false
	}
	for _, c := range conds {
		if !c.Match(in) {
			return false
		}
	}
	return true
}

// SelectWhen returns the responses key of the first variant whose when[]
// is non-empty and matches every condition in in, otherwise ("", false) so
// the caller falls back to active_status exactly as DESIGN §12 describes.
//
// Row.Responses is a Go map, and Go deliberately randomises map iteration
// order. "The first matching variant wins" is only a well-defined rule if
// SOMETHING orders the candidates the same way on every call — DESIGN
// itself does not say what that order is beyond "first", so this file
// fixes it as ascending numeric status, the one ordering that needs no
// extra field on Variant and matches how an operator reads a responses
// map back (200 before 409 before 500). A key that does not parse as a
// decimal status can never legitimately be an operation's status code —
// it sorts last and is simply never a candidate, the same tolerance
// [Condition.Match] extends to a shape it does not recognise.
func SelectWhen(responses map[string]Variant, in Input) (status string, ok bool) {
	type candidate struct {
		key    string
		status int
	}
	candidates := make([]candidate, 0, len(responses))
	for key := range responses {
		n, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{key: key, status: n})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].status < candidates[j].status })

	for _, cand := range candidates {
		v := responses[cand.key]
		if len(v.When) == 0 {
			continue
		}
		if MatchAll(v.When, in) {
			return cand.key, true
		}
	}
	return "", false
}

// ValidateConditions returns a non-nil error for the first condition this
// build cannot evaluate: an unknown In, an unknown Op, an empty Name, or a
// missing Value on equals/contains (exists never needs one — it asks about
// presence, not a comparable rendering). It is the WRITE-time-only gate:
// [ValidateVariant] calls it, so Put/PutMany (via normalizeAndValidate,
// which already runs every Variant through ValidateVariant) reject a
// condition [Condition.Match] could only ever silently treat as "never
// matches" — a 400 at write time beats a when[] an operator wrote that
// quietly never fires. The returned error is NOT wrapped in ErrInvalidRow
// here; the caller that already wraps every other structural failure the
// same way (ValidateVariant) does that, so there is one place, not two,
// that decides what a caller-visible invalid-row error looks like.
func ValidateConditions(conds []Condition) error {
	for _, c := range conds {
		switch c.In {
		case "query", "header", "body":
		default:
			return fmt.Errorf("when: unknown in %q", c.In)
		}
		if c.Name == "" {
			return fmt.Errorf("when: name is empty")
		}
		switch c.Op {
		case "exists":
		case "equals", "contains":
			if c.Value == "" {
				return fmt.Errorf("when: op %q on name %q requires a value", c.Op, c.Name)
			}
		default:
			return fmt.Errorf("when: unknown op %q", c.Op)
		}
	}
	return nil
}
