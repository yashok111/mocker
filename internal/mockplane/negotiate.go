// Content negotiation and header safety on the mock plane: the Accept gate,
// the media-type checks both planes share, the response-header allowlist and
// the envelope. Split out of respond.go 2026-09-03; the text is unchanged.
package mockplane

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/httpx"
)

// acceptable implements DESIGN §9's "Accept-negotiation с явным fallback и
// 406" for ONE declared media type — operation_responses stores exactly one
// media_type per (operation, status), so there is never more than one
// candidate to negotiate against.
//
// What is actually implemented, not the full RFC 9110 algorithm: header is
// split on "," into media ranges, each optionally carrying a ";q=" weight
// (default 1, malformed values ignored back to the default rather than
// rejected). A range matches mediaType if its type/subtype equals
// mediaType's own (ignoring any ";charset=..." etc. mediaType itself might
// carry — split at the first ";"), or either half is "*". Among every
// matching range, the MOST SPECIFIC one wins (exact > type/* > */*), and
// its q decides the outcome: q=0 is an explicit refusal (406) even if a
// less specific range would otherwise have accepted it; any q>0 accepts.
// Ties in specificity take the highest q seen. A missing/empty Accept, or
// mediaType itself being "" (nothing declared to negotiate), always
// accepts — matching the "явный fallback" DESIGN calls for.
func acceptable(accept, mediaType string) bool {
	if mediaType == "" {
		return true
	}
	accept = strings.TrimSpace(accept)
	if accept == "" {
		return true
	}

	typ, sub, ok := splitMediaType(mediaType)
	if !ok {
		return true // an undeclared-shape media type: nothing sane to negotiate against
	}

	bestSpec := -1
	bestQ := 0.0
	for part := range strings.SplitSeq(accept, ",") {
		rtyp, rsub, q, ok := parseAcceptRange(part)
		if !ok {
			continue
		}
		if rtyp != "*" && rtyp != typ {
			continue
		}
		if rsub != "*" && rsub != sub {
			continue
		}
		spec := 0
		if rtyp != "*" {
			spec++
		}
		if rsub != "*" {
			spec++
		}
		if spec > bestSpec || (spec == bestSpec && q > bestQ) {
			bestSpec, bestQ = spec, q
		}
	}
	if bestSpec == -1 {
		return false
	}
	return bestQ > 0
}

// splitMediaType lower-cases and splits mt (stripping any ";parameter"
// suffix first) into its type and subtype. ok is false when mt has no "/"
// at all — not a real media type, so negotiation has nothing to compare.
func splitMediaType(mt string) (typ, sub string, ok bool) {
	mt, _, _ = strings.Cut(mt, ";")
	mt = strings.ToLower(strings.TrimSpace(mt))
	typ, sub, ok = strings.Cut(mt, "/")
	if !ok || typ == "" || sub == "" {
		return "", "", false
	}
	return typ, sub, true
}

// dangerousResolvedMediaType is [httpx.BrowserExecutableMediaType] at this
// plane's SERVE boundary, applied to the EFFECTIVE media type — AFTER
// serveGenerated's own variant.MediaType fallback, which the write-time check
// in internal/admin can never see. An operator can leave a response's own
// mediaType blank and still inherit a spec-declared text/html straight out of
// an imported document: the write-time gate only ever inspects the field the
// operator themselves typed, so that fallback sails through it untouched.
// This is the second, request-time gate over the value that is ACTUALLY about
// to become the response's Content-Type.
//
// It delegates rather than mirroring. It used to hold its own copy of the
// table and its own ";"-splitting normalisation, "kept in step by hand" with
// admin's — and the two stayed in step right up to the point where both missed
// the same bypass, which is what a hand-kept copy buys you.
func dangerousResolvedMediaType(mediaType string) bool {
	return httpx.BrowserExecutableMediaType(mediaType)
}

// parseAcceptRange parses one comma-separated Accept entry into its
// type/subtype and q weight. ok is false for an entry with no "/" at all
// (not a media range, e.g. stray whitespace from a trailing comma).
func parseAcceptRange(raw string) (typ, sub string, q float64, ok bool) {
	parts := strings.Split(raw, ";")
	typ, sub, ok = splitMediaType(parts[0])
	if !ok {
		return "", "", 0, false
	}
	q = 1.0
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		val, found := strings.CutPrefix(p, "q=")
		if !found {
			continue
		}
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			q = parsed
		}
	}
	return typ, sub, q, true
}

// unsafeResponseHeaderName denies header names a declared/pinned header has
// no legitimate reason to set on this open, unauthenticated plane (round-1
// finding #10). Content-Length/Transfer-Encoding would fight the framing
// this function's own caller computes and writes exactly once, further
// down; Content-Type would fight the resolved media type this function's
// caller already decided. Set-Cookie/Set-Cookie2 are the load-bearing
// ones: DESIGN §16's path-routing mode puts the admin plane and every
// workspace's mock plane on ONE origin, and auth.Manager.SetCookie's own
// admin cookie carries no Domain — an override that sets Set-Cookie with an
// explicit Domain=<the registrable domain> is accepted by the browser and
// silently swaps out (or empties) whichever admin teammate merely OPENS a
// shared mocked URL, no admin-plane request involved. name is matched
// case-insensitively (HTTP header names are), same as net/http's own
// canonicalization treats them.
var unsafeResponseHeaderName = map[string]bool{
	"set-cookie":        true,
	"set-cookie2":       true,
	"transfer-encoding": true,
	"content-length":    true,
	"content-type":      true,
}

// headerIsSafe is the predicate [setSafeHeader] and [addSafeHeader] both
// enforce before a generated header reaches anywhere the client can see it:
// false when the value contains a CR or LF (a header value can originate
// from the workspace's own uploaded OpenAPI document — an example or a
// pinned string — which DESIGN §15's threat model treats as
// attacker-controllable input, and a raw CR/LF in a header value is a
// classic response-splitting vector; net/http's own header writer already
// neutralizes embedded newlines by replacing them with spaces before they
// reach the wire, but silently mangling a value is worse than dropping a
// header that was never going to be well-formed anyway) or the name itself
// is empty or in [unsafeResponseHeaderName] (a stored header NAME is exactly
// as attacker-reachable as its value). Pulled out as its own function so
// [Plane.assembleResponse] — which has no [http.ResponseWriter] to write
// straight to, per D1 — drops exactly the same headers a real request would,
// through one shared rule rather than two hand-kept copies (CLAUDE.md
// records this exact failure shape for the media-type rule already).
func headerIsSafe(name, value string) bool {
	if name == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	return !unsafeResponseHeaderName[strings.ToLower(strings.TrimSpace(name))]
}

// setSafeHeader sets a generated header on w, dropping it entirely rather
// than setting it when [headerIsSafe] refuses it. custom.go's own pinned-header
// loop calls this directly (it still writes straight to a ResponseWriter);
// [Plane.assembleResponse] calls [addSafeHeader] instead, the same predicate
// aimed at a map because it returns a value rather than writing one.
func setSafeHeader(w http.ResponseWriter, name, value string) {
	if !headerIsSafe(name, value) {
		return
	}
	w.Header().Set(name, value)
}

// addSafeHeader adds name/value into dst — a map that becomes
// [domain.AssembledResponse.Headers] — under exactly the rule [setSafeHeader]
// enforces for the [http.ResponseWriter] it writes straight to: a header the
// writer would have dropped is dropped here too, so it never survives into a
// preview response either.
func addSafeHeader(dst map[string]string, name, value string) {
	if headerIsSafe(name, value) {
		dst[name] = value
	}
}

// wrapEnvelope wraps body under key, per settings.Envelope (DESIGN's
// {"<envelope>": ...} wrapping). Called only when [httpx.IsJSONMediaType]
// already gated the media type, this is the belt-and-suspenders check that body
// really is the complete, valid JSON value [gen.Generator.Body] is
// documented to hand back for a JSON media type: returning an error instead
// of wrapping on anything else, rather than trusting that contract blindly.
func wrapEnvelope(key string, body []byte) ([]byte, error) {
	// Compact validates and copies in one pass and keeps the bytes the
	// old map marshal produced (a RawMessage inside a map is compacted by
	// the encoder too); the map marshal itself was a second full scan of
	// a body of up to MOCKER_MAX_RESPONSE through reflection.
	keyBytes, err := jsonx.Marshal(key)
	if err != nil {
		return nil, fmt.Errorf("mockplane: envelope: marshal: %w", err)
	}
	var buf bytes.Buffer
	buf.Grow(len(body) + len(keyBytes) + 3)
	buf.WriteByte('{')
	buf.Write(keyBytes)
	buf.WriteByte(':')
	if err := jsonx.Compact(&buf, body); err != nil {
		return nil, fmt.Errorf("mockplane: envelope: body is not valid JSON")
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
