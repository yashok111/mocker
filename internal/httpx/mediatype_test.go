package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/httpx"
)

// TestBrowserExecutableMediaType is the whole security value of this package's
// media-type rule, so it is a table of the ACTUAL bypasses rather than a couple
// of representative cases. Every string in the "dangerous" half below was found
// by an adversarial pass against the previous implementation — which cut at the
// first ";", lower-cased, trimmed and looked the result up in a three-entry map
// — and every one of them reached the wire.
func TestBrowserExecutableMediaType(t *testing.T) {
	t.Parallel()

	dangerous := []struct{ in, why string }{
		{"text/html", "the plain form"},
		{"TEXT/HTML", "case is not part of a media type's identity"},
		{"text/html; charset=utf-8", "a parameter does not make it safe"},
		{"TEXT/HTML; charset=utf-8", "both at once"},
		{" text/html ", "surrounding whitespace"},
		{"application/xhtml+xml", "the second table entry"},
		{"image/svg+xml", "the third: SVG carries script"},

		// The bypasses. Each of these passed the old prefix-matching check AND
		// the mock plane's own mirrored copy of it, was written to the wire
		// verbatim by Go, and resolved to text/html in a browser.
		{"application/json,text/html", "WHATWG Fetch parses every comma-separated value and keeps the last valid one; Chromium coalesces Content-Type the same way"},
		{"text/html,application/json", "the reversed form, for a first-valid-wins implementation"},
		{";text/html", "unparseable, so the header is ignored and the body is sniffed"},
		{"text/html\x00", "a NUL is not a token character; unparseable, therefore sniffed"},
		{"text/html ;x", "Go parses the essence but rejects the parameter — an error must not be read as safe"},
		{"text/html;", "a bare trailing separator parses fine and the essence is still text/html"},
	}
	for _, tc := range dangerous {
		if !httpx.BrowserExecutableMediaType(tc.in) {
			t.Errorf("BrowserExecutableMediaType(%q) = false, want true — %s", tc.in, tc.why)
		}
	}

	safe := []struct{ in, why string }{
		{"", "the ordinary no-opinion value; the caller resolves the real type elsewhere"},
		{"   ", "whitespace is still no opinion"},
		{"application/json", "the overwhelmingly common case"},
		{"application/json; charset=utf-8", "with a parameter"},
		{"text/plain", "inert"},
		{"text/csv", "the real customer document declares one of these"},
		{"multipart/form-data; boundary=x", "and two of these"},
		{"application/xml", "XML is not XHTML"},
		{"application/json;", "a bare trailing separator: Go parses it cleanly, so it is not the unparseable case and must not be refused"},
		{"image/png", "an image the browser renders but cannot execute"},

		// The case a cruder rule gets wrong. "contains a comma -> refuse" would
		// reject this, and it is a perfectly ordinary quoted parameter.
		{`application/json;x="a,text/html"`, "a comma INSIDE a quoted parameter is not a second media type"},
	}
	for _, tc := range safe {
		if httpx.BrowserExecutableMediaType(tc.in) {
			t.Errorf("BrowserExecutableMediaType(%q) = true, want false — %s", tc.in, tc.why)
		}
	}
}

// TestBrowserExecutableMediaType_matchesWhatGoPutsOnTheWire pins the assumption
// the rule rests on: net/http does NOT normalise, validate or reject a
// Content-Type a handler sets. Whatever string reaches Header().Set is exactly
// what a browser receives, which is why refusing an unparseable value at the
// gate is the only place it can be refused at all.
func TestBrowserExecutableMediaType_matchesWhatGoPutsOnTheWire(t *testing.T) {
	t.Parallel()

	const smuggled = "application/json,text/html"
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", smuggled)
	rec.WriteHeader(http.StatusOK)

	if got := rec.Header().Get("Content-Type"); got != smuggled {
		t.Fatalf("net/http rewrote the Content-Type to %q; this rule assumes it does not, and the assumption is what makes the gate load-bearing", got)
	}
	if !httpx.BrowserExecutableMediaType(smuggled) {
		t.Error("the value Go will hand a browser unchanged is not refused by the gate")
	}
}

// TestIsJSONMediaType pins the moved-byte-for-byte behavior: empty answers
// true (the generator's own no-declared-type fallback), any substring match
// on "json" answers true (including a vendor-suffixed type), and a
// non-JSON declared type answers false. This is deliberately NOT the
// mime.ParseMediaType-based rule BrowserExecutableMediaType uses above —
// see that function's own doc comment for why harmonising them is refused.
func TestIsJSONMediaType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"application/json", true},
		{"application/vnd.api+json", true},
		{"application/JSON", false}, // Contains is case-sensitive, exactly like the original
		{"text/csv", false},
		{"text/plain", false},
		{"application/json,text/html", true}, // a substring match, deliberately not essence-parsed
	}
	for _, c := range cases {
		if got := httpx.IsJSONMediaType(c.in); got != c.want {
			t.Errorf("IsJSONMediaType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
