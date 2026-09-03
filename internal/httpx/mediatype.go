package httpx

import (
	"mime"
	"strings"
)

// browserExecutableMediaType names the media types a browser navigated to a
// response DIRECTLY renders and runs, rather than downloads or displays as
// inert text — the only ones dangerous here, since a mocked response is
// otherwise no more executable than any other stored string.
//
// This matters because DESIGN §16's path-routing mode (server.go's servePath)
// puts the admin plane and every workspace's mock plane on ONE origin, and the
// admin session cookie has Path=/ and no Domain (auth.Manager.SetCookie): it is
// sent to a mocked URL exactly as it is sent to /api/*. A colleague who merely
// OPENS a mocked URL a teammate shared — an entirely ordinary action — would
// run whatever script an HTML or SVG body carried, same origin as their own
// live admin session.
var browserExecutableMediaType = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
	"image/svg+xml":         true,
}

// BrowserExecutableMediaType reports whether a body served under mediaType
// could be rendered and executed by a browser. An empty mediaType is NOT
// dangerous: it is the ordinary "no opinion" value, and the caller resolves the
// real type elsewhere.
//
// # Why this parses instead of splitting on ";"
//
// It used to do the obvious thing — cut at the first ";", lower-case, trim,
// look the result up in the table — and that is not a media type parser, it is
// a prefix matcher. It let this through, verified end to end against a live
// handler:
//
//	Content-Type: application/json,text/html
//
// Neither gate objected (the whole string is not one of the three names), Go
// wrote the header verbatim, and a browser resolved it to text/html: WHATWG
// Fetch's "extract a MIME type" parses every comma-separated value and keeps
// the last valid one, and Chromium coalesces Content-Type the same way. So did
// ";text/html" and "text/html\x00", which are unparseable and therefore
// sniffed. That is a same-origin stored XSS against the admin session in path
// mode — the exact thing this table exists to prevent — reachable through an
// ordinary PUT.
//
// [mime.ParseMediaType] settles all of them at once, because "," and NUL are
// not token characters: it fails, and a value this package cannot parse is a
// value it cannot reason about, so it is refused. That is strictly better than
// enumerating tricks. It also keeps the legitimate case working — a comma
// inside a quoted parameter, application/json;x="a,text/html", parses cleanly
// to application/json and is allowed, which a "contains a comma" rule would
// have wrongly refused.
//
// # Why it lives here rather than in each plane
//
// It had two copies, one in internal/admin (checking what an operator typed at
// write time) and one in internal/mockplane (checking the EFFECTIVE type at
// serve time, after the spec-declared fallback the write gate cannot see).
// Those two call sites are genuinely different and both are still needed — but
// the RULE they apply is one rule, and two copies of a rule are two things to
// get wrong. internal/httpx is the leaf both planes already import.
func BrowserExecutableMediaType(mediaType string) bool {
	if strings.TrimSpace(mediaType) == "" {
		return false
	}
	essence, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		// Unparseable. A browser will still make something of it — coalescing
		// a comma-separated list, or ignoring the header and sniffing the
		// bytes — and this package cannot predict which. Refuse rather than
		// guess: nothing legitimate needs a Content-Type Go's own parser
		// rejects.
		return true
	}
	return browserExecutableMediaType[essence]
}

// IsJSONMediaType reports whether mediaType is a shape gen.Generator's own
// finalize would serialize as JSON: empty (the generator's own fallback) or
// containing "json" anywhere (matching e.g. "application/vnd.api+json" as
// well as the plain form).
//
// # Why this is strings.Contains, not [mime.ParseMediaType] like its neighbour
//
// [BrowserExecutableMediaType] above parses on purpose — it is a security
// gate, and an unparseable value there is refused rather than guessed
// about. This function is not a gate: it decides which media types the
// generator's own body-shaping logic (envelope wrapping, JSON-shaped
// coercion at derivation and serve time) already treats as JSON, and that
// decision was made — and is exercised by every spec already imported —
// with a substring match. Switching it to essence-based parsing would
// silently change the envelope decision for every existing spec (a real
// spec occasionally declares a media type [mime.ParseMediaType] rejects
// outright, e.g. a stray trailing ";" some generators emit), and no bar in
// this tree would go red over that change — it would just quietly start
// wrapping or coercing bodies it used to leave alone. Harmonising the two
// predicates is therefore deliberately NOT done here.
//
// Promoted (P3a) out of internal/mockplane, which had the only copy, into
// this package beside BrowserExecutableMediaType so that response serving
// AND resource derivation (internal/specs, at import time) share the one
// definition rather than each carrying its own.
func IsJSONMediaType(mediaType string) bool {
	return mediaType == "" || strings.Contains(mediaType, "json")
}
