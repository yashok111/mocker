package traffic

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// RedactedValue replaces a secret's real value, in both headers and bodies.
const RedactedValue = "[redacted]"

// redactedHeaderNames matches case-insensitively (the map key is already
// lower-cased); everything not in this set is stored as sent.
var redactedHeaderNames = map[string]struct{}{
	"authorization":       {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"proxy-authorization": {},
}

// RedactHeaders returns h with the credential-carrying header values
// replaced by [RedactedValue], keyed by [http.CanonicalHeaderKey] so the
// output is stable regardless of how the input map's keys were cased —
// http.Header built by net/http is already canonical, but a caller
// constructing one by hand (a test, or a future middleware) is not
// guaranteed to be.
//
// A multi-valued header is comma-joined ("v1, v2"), the same shape
// http.Header.Values would show you if you printed all of it — not
// http.Header.Get's "first value only", because a dropped second value on a
// header like Accept-Language is silent data loss on the traffic screen an
// operator is trying to trust.
func RedactHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for name, vals := range h {
		canon := http.CanonicalHeaderKey(name)
		if _, redact := redactedHeaderNames[strings.ToLower(name)]; redact {
			out[canon] = RedactedValue
			continue
		}
		out[canon] = strings.Join(vals, ", ")
	}
	return out
}

// secretFieldNames matches a JSON field name (already lower-cased) exactly.
// "tokens" (plural) is deliberately NOT in this set and does not match the
// "_token" suffix check either — a plural collection of non-secret items is
// a real, common field name (e.g. a paginated list's "tokens": [...]) and
// redacting it on a name-shape guess would silently corrupt a body an
// operator is trying to read.
var secretFieldNames = map[string]struct{}{
	"password": {},
	"token":    {},
	"secret":   {},
	"passwd":   {},
	"pwd":      {},
}

// isSecretField reports whether a JSON field name identifies a value
// RedactJSONBody must replace: an exact match against secretFieldNames, or a
// name ending in _key, _token or _secret (api_key, refresh_token,
// client_secret, ...).
func isSecretField(name string) bool {
	n := strings.ToLower(name)
	if _, ok := secretFieldNames[n]; ok {
		return true
	}
	return strings.HasSuffix(n, "_key") || strings.HasSuffix(n, "_token") || strings.HasSuffix(n, "_secret")
}

// RedactJSONBody walks a JSON body and replaces the VALUE of any field whose
// name matches [isSecretField], at any depth and inside arrays too. A body
// that is not valid JSON is returned UNCHANGED — no regex redaction is
// attempted on text bodies, because a pattern loose enough to catch a secret
// in arbitrary prose is also loose enough to mangle one that was never
// there (DESIGN §15's field-name list is scoped to JSON/form/text "by
// configurable field name", and this package only implements the JSON case;
// form and text are the caller's problem, if it wants to redact them at
// all — this package's contract from the task list is JSON only).
//
// EXCEPT: a body whose first non-whitespace byte commits it to being a JSON
// object or array (see [looksLikelyTruncatedJSON]) but still fails to parse
// is not "the client sent something other than JSON" — every upstream
// capture that feeds this function (mockplane's own request-body cap, and
// this package's own MaxBody cut before P1c2's redact-before-cut fix)
// truncates at a raw byte offset with no regard for JSON structure, so an
// object cut mid-string parses as garbage even though the original body was
// valid JSON, quite possibly with a secret sitting in the surviving prefix.
// Silently returning THOSE bytes unchanged would store that secret in
// cleartext. Consistent with the marshal-failure case below, this function
// would rather lose the body than leak it: it returns (nil, true) so the
// caller records [NoteRedacted] and the row is treated as redacted, not
// stored.
//
// changed reports whether anything was actually replaced (or the body was
// dropped for the reason above). Discarding it at the call site is what
// would make [NoteRedacted] — and therefore the admin to-override
// conversion's refusal — never fire; every caller of this function in this
// package threads it through.
func RedactJSONBody(body []byte) (out []byte, changed bool) {
	if len(body) == 0 {
		return body, false
	}
	var v any
	if err := jsonx.Unmarshal(body, &v); err != nil {
		if looksLikelyTruncatedJSON(body) {
			return nil, true
		}
		return body, false
	}

	v = redactValue(v, &changed)
	if !changed {
		// Nothing to replace: return the ORIGINAL bytes, not a re-marshaled
		// copy. jsonx.Marshal would reorder nothing (Go map iteration order
		// is randomized on encode too, for map[string]any) and re-format
		// whitespace the source body may have carried on purpose (a caller
		// diffing traffic against what it sent should see its own bytes
		// back, byte for byte, when there was no secret to touch).
		return body, false
	}

	out, err := jsonx.Marshal(v)
	if err != nil {
		// v was just decoded successfully from body, so re-encoding it can
		// only fail for pathological inputs (NaN can't occur — encoding/json
		// never decodes one). Falling back to the original, UNREDACTED bytes
		// here would defeat the whole point of this function, so this is
		// the one case where losing the redaction is worse than losing the
		// body: report the failure by returning nothing rather than
		// silently leaking the secret this call exists to hide.
		return nil, true
	}
	return out, true
}

// RedactBody is round-1 review finding 5's dispatcher: DESIGN §15 requires
// field-name redaction for "JSON/form/text", but before this fix only the
// JSON branch existed — [RedactJSONBody] alone, called directly regardless
// of what the client actually sent. A form-urlencoded or text/plain
// credential (a login POSTed as
// application/x-www-form-urlencoded, or a text/plain body shaped like
// "password: value") was stored, and served back through the admin traffic
// API, in cleartext: everyone who knows the shared admin password could then
// read it off the traffic screen, exactly the risk §15 states this
// redaction exists to close. contentType is read from the SAME header the
// caller already captured (the request's own Content-Type, or the
// response's) — never sniffed from the body itself, so a client cannot
// dodge redaction by mislabeling a JSON body as text/plain (that combination
// still resolves to the JSON branch below, which parses fine) or evade the
// form/text branches by any means other than an actual different body.
//
// Anything other than application/x-www-form-urlencoded or text/plain
// (including a missing Content-Type, exactly the historical behaviour this
// function preserves) falls through to [RedactJSONBody] — the ordinary path
// this package always took before Content-Type was tracked at all, since a
// tolerant JSON parse attempt is harmless against a body that turns out not
// to be JSON.
func RedactBody(body []byte, contentType string) (out []byte, changed bool) {
	switch redactMediaType(contentType) {
	case "application/x-www-form-urlencoded":
		return redactFormBody(body)
	case "text/plain":
		return redactTextBody(body)
	default:
		return RedactJSONBody(body)
	}
}

// redactMediaType strips parameters (";charset=utf-8") and case, mirroring
// internal/mockplane/respond.go's own splitMediaType — duplicated rather
// than imported, since this package is a LEAF (its own header comment: net/
// http and the stdlib only, never internal/mockplane) and the two packages
// each need only this one small piece of RFC 7231 §3.1.1.1 parsing, not a
// shared dependency to get it.
func redactMediaType(contentType string) string {
	mt, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(mt))
}

// redactFormBody redacts an application/x-www-form-urlencoded body by FIELD
// NAME, the same [isSecretField] table [RedactJSONBody] uses. A body that
// fails to parse as a query string is returned unchanged — url.ParseQuery is
// extremely permissive (it happily accepts a body with no "=" in it at all,
// treating the whole thing as a key with an empty value), so a genuine parse
// error here means the bytes are not really form data, not that this
// function should guess at redacting them anyway.
//
// url.Values.Encode() re-serializes in SORTED key order, same trade-off
// [RedactJSONBody] already accepts for a changed JSON body (map iteration
// order is not preserved either): a caller diffing traffic against what it
// sent sees its own bytes back UNCHANGED when nothing needed redacting
// (below), and a re-encoded, differently-ordered but still fully decodable
// form body when something did.
func redactFormBody(body []byte) (out []byte, changed bool) {
	if len(body) == 0 {
		return body, false
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return body, false
	}
	for key, vals := range values {
		if !isSecretField(key) {
			continue
		}
		for i := range vals {
			vals[i] = RedactedValue
		}
		changed = true
	}
	if !changed {
		return body, false
	}
	return []byte(values.Encode()), true
}

// redactTextBody redacts a text/plain body shaped as one "field: value" or
// "field=value" pair per line — the concrete shape DESIGN §15's "text" case
// names (a login form rendered as plain text, a debug dump, ...) — WITHOUT
// the generic regex-over-prose scanning [RedactJSONBody]'s own doc comment
// already rules out for this package: a pattern loose enough to find a
// secret in arbitrary prose is also loose enough to mangle prose that never
// held one. Only a line whose portion before the first ':' or '=' trims down
// to a name [isSecretField] matches is touched; every other line, matching
// or not, rides through byte-for-byte including its own line ending.
func redactTextBody(body []byte) (out []byte, changed bool) {
	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		redacted, lineChanged := redactTextLine(line)
		if lineChanged {
			lines[i] = redacted
			changed = true
		}
	}
	if !changed {
		return body, false
	}
	return bytes.Join(lines, []byte("\n")), true
}

// redactTextLine implements one line of [redactTextBody]'s "field: value" /
// "field=value" rule. line may still carry a trailing "\r" ([redactTextBody]
// splits only on "\n", to leave a CRLF body's line endings otherwise
// untouched) — preserved verbatim on the tail of a redacted line rather than
// stripped and never restored.
func redactTextLine(line []byte) (out []byte, changed bool) {
	idx := bytes.IndexAny(line, ":=")
	if idx < 0 {
		return line, false
	}
	keyPart := line[:idx]
	if !isSecretField(string(bytes.TrimSpace(keyPart))) {
		return line, false
	}

	rest := line[idx+1:]
	cr := bytes.HasSuffix(rest, []byte("\r"))
	if cr {
		rest = rest[:len(rest)-1]
	}
	// Preserve exactly one leading space after the separator ("key: value"),
	// if the original line had one, so a redacted line still reads like a
	// line of this shape rather than losing its own formatting on top of
	// losing its value.
	spacer := ""
	if len(rest) > 0 && rest[0] == ' ' {
		spacer = " "
	}

	out = append(out, keyPart...)
	out = append(out, line[idx])
	out = append(out, spacer...)
	out = append(out, RedactedValue...)
	if cr {
		out = append(out, '\r')
	}
	return out, true
}

// looksLikelyTruncatedJSON reports whether body's first non-whitespace byte
// is '{' or '[' — RFC 8259's only two container shapes, and the only ones
// long enough for a raw byte-offset cut to land mid-token far from the
// start. A bare JSON string, number, true/false or null is at most a few
// bytes; truncating one either still parses or looks nothing like it came
// from JSON in the first place, so this check is deliberately narrow: it
// exists to catch the mid-object cut this finding is about, not to
// reclassify arbitrary text as JSON on the strength of one shared byte (the
// existing "not-json-at-all {{{" test case starts with 'n', not '{', and
// must keep returning changed=false).
func looksLikelyTruncatedJSON(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}

// redactValue mutates maps and slices in place (both are reference types in
// Go, so recursing into a value already reachable from v needs no
// reassignment back into the parent) and returns v unchanged for every other
// type, so the top-level caller can always do `v = redactValue(v, &changed)`
// whether v itself is a map, a slice, or a bare scalar.
func redactValue(v any, changed *bool) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSecretField(k) {
				t[k] = RedactedValue
				*changed = true
				continue
			}
			t[k] = redactValue(val, changed)
		}
		return t
	case []any:
		for i, item := range t {
			t[i] = redactValue(item, changed)
		}
		return t
	default:
		return v
	}
}
