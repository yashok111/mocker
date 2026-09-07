// respond_pinned_test.go: pinned mode's own serve-time gates (round-1
// findings #3/#8/#9/#10) — body/media-type/headers/envelope, base64
// decoding, HEAD, the oversized-body and dangerous-media-type refusals, the
// unsafe-header-name drop, and the two malformed/non-JSON degrade cases.
// Split out of the former respond_test.go (package mockplane, not
// mockplane_test — see helpers_test.go's own comment for why).
package mockplane

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

// TestServeGenerated_PinnedMode_JSONBodyMediaTypeHeadersEnvelope covers mode
// "pinned" end to end: the row's own literal body served verbatim, its own
// MediaType (not the document's), its own Headers layered on top of the
// declared ones, and the workspace's envelope still applied — a pinned body
// is not exempt from the wire-shape contract the rest of the workspace
// agreed to.
func TestServeGenerated_PinnedMode_JSONBodyMediaTypeHeadersEnvelope(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope

	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:      "pinned",
					Body:      json.RawMessage(`{"id":7,"name":"pinned-item"}`),
					MediaType: "application/vnd.custom+json",
					Headers:   map[string]string{"X-Pinned": "yes"},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.custom+json" {
		t.Errorf("Content-Type = %q, want the row's own pinned media type", ct)
	}
	if h := rec.Header().Get("X-Pinned"); h != "yes" {
		t.Errorf("X-Pinned = %q, want %q (the row's own header, layered on top of the declared ones)", h, "yes")
	}
	want := `{"data":{"id":7,"name":"pinned-item"}}`
	if rec.Body.String() != want {
		t.Errorf("body = %s, want %s (pinned body served verbatim, still enveloped)", rec.Body, want)
	}
}

// TestServeGenerated_PinnedMode_Base64Body covers the other body shape:
// BodyEncoding "base64" carries bytes that are not themselves valid JSON —
// decoded and served verbatim, and (since the media type is not JSON) never
// enveloped even though the workspace has one configured.
func TestServeGenerated_PinnedMode_Base64Body(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope // must NOT apply — non-JSON pinned media type

	const raw = "hello, pinned world"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	bodyJSON, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}

	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:         "pinned",
					Body:         json.RawMessage(bodyJSON),
					BodyEncoding: "base64",
					MediaType:    "text/plain",
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if rec.Body.String() != raw {
		t.Errorf("body = %q, want the base64-decoded literal %q", rec.Body.String(), raw)
	}
}

// TestServeGenerated_PinnedMode_HEADKeepsHeadersButSuppressesBody is the
// pinned-mode twin of TestServeGenerated_HEADKeepsHeadersButSuppressesBody:
// HEAD against a pinned variant still reports the pinned Content-Type and a
// non-zero Content-Length, with zero body bytes actually written.
func TestServeGenerated_PinnedMode_HEADKeepsHeadersButSuppressesBody(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:      "pinned",
					Body:      json.RawMessage(`{"id":1,"name":"x"}`),
					MediaType: "application/json",
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, http.MethodHead, "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodHead, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	hw := &headWriter{ResponseWriter: rec}
	p.serveGenerated(hw, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (the pinned variant's own type)", ct)
	}
	cl := rec.Header().Get("Content-Length")
	if cl == "" || cl == "0" {
		t.Errorf("Content-Length = %q, want the non-zero length a GET would have sent", cl)
	}
}

// TestServeGenerated_PinnedMode_OversizedBodyRefused is round-1 findings
// #3/#8: a pinned body is written ONCE but served on every subsequent
// unauthenticated request, with nothing downstream re-measuring it unlike
// a generated body (bounded on every call by gen.Options.MaxBytes). This
// fixture attaches the override row directly (fixtureRuntimeWithOverrides
// is white-box and bypasses overrides.Repo.Put's own write-time cap
// entirely — see overrides.TestValidation_pinnedBodyOverLimit for that
// gate), so what is under test here is specifically respond.go's own
// serve-time re-check against the LIVE cfg.MaxResponse.
func TestServeGenerated_PinnedMode_OversizedBodyRefused(t *testing.T) {
	body := `{"id":7,"name":"` + strings.Repeat("a", 200) + `"}`
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {Mode: "pinned", Body: json.RawMessage(body), MediaType: "application/json"},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	// A live ceiling well under the pinned body's own size, proving the
	// CHECK is what refused it, not an empty body to begin with.
	p := New(runtimeTestConfig(64, 32), nil, nil, runtimeTestLogger())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %d bytes, want 0 — a pinned body over the live MaxResponse must be refused, not served", rec.Body.Len())
	}
}

// TestServeGenerated_PinnedMode_DangerousResolvedMediaTypeRefused is
// round-1 finding #9: admin's own write-time guard
// (dangerousMediaType) only ever inspects the OPERATOR's own
// MediaType — leaving the pinned variant's MediaType blank falls through
// to the document's declared type, which can itself be browser-executable.
// This must be refused at the EFFECTIVE (post-fallback) type, the one
// respond.go is actually about to send as Content-Type.
func TestServeGenerated_PinnedMode_DangerousResolvedMediaTypeRefused(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/page", CanonicalPath: "/page", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "text/html",
		OpPointer: "#/paths/~1page/get",
	}
	rows := map[string]*overrides.Row{
		overrides.OpKey("GET", "/page"): {
			Method: "GET", Path: "/page", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				// MediaType intentionally left blank: an operator relying on
				// the spec-declared type above, exactly the gap the write-time
				// guard cannot see.
				"200": {Mode: "pinned", Body: json.RawMessage(`"<script>alert(1)</script>"`)},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, blankDoc, []router.Route{route},
		map[int64][]gen.ResponseVariant{1: {variant}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/page")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/page", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "text/html" {
		t.Errorf("Content-Type = %q, must never resolve to a browser-executable type for a pinned response with no mediaType of its own", ct)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty — a pinned response resolving to a browser-executable media type must not be served", rec.Body.String())
	}
}

// TestServeGenerated_PinnedMode_UnsafeHeaderNameDropped is round-1 finding
// #10: a declared/pinned header's VALUE was already sanitized for CR/LF,
// but its NAME was unrestricted — letting an override set Set-Cookie and
// (on DESIGN §16's one-origin path-routing mode) silently swap out or
// empty whichever admin teammate merely opens a shared mocked URL. An
// ordinary header name must still pass through untouched.
func TestServeGenerated_PinnedMode_UnsafeHeaderNameDropped(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:      "pinned",
					Body:      json.RawMessage(`{"id":7,"name":"pinned-item"}`),
					MediaType: "application/json",
					Headers: map[string]string{
						"Set-Cookie": "mocker_session=hijacked; Domain=mock.local; Path=/",
						"X-Pinned":   "yes",
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Errorf("Set-Cookie = %q, want empty — a pinned/declared header must never be able to set Set-Cookie", sc)
	}
	if h := rec.Header().Get("X-Pinned"); h != "yes" {
		t.Errorf("X-Pinned = %q, want %q — an ordinary header name must still pass through", h, "yes")
	}
}

// TestServeGenerated_PinnedMode_MalformedBase64AnswersEmptyBodyNot500 is
// pinnedBody's own malformed-base64 case (already unit-tested by
// TestPinnedBody), but reached through serveGenerated: the `case pinned:`
// arm's `genErr != nil` branch (respond.go's decode-pinned-response-body
// Error log, body set to nil) had no serveGenerated-level test — every
// existing pinned-mode test in this file decodes cleanly.
func TestServeGenerated_PinnedMode_MalformedBase64AnswersEmptyBodyNot500(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:         "pinned",
					Body:         json.RawMessage(`"not-valid-base64!!"`),
					BodyEncoding: "base64",
					MediaType:    "application/json",
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the declared status, never a 500); body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty — a pinned body that fails to decode must never be served", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want the declared type even though decoding the pinned body failed", ct)
	}
}

// TestServeGenerated_PinnedMode_EnvelopeSkippedWhenDecodedBodyIsNotJSON is
// wrapEnvelope's own error path (respond.go's "skip envelope: body is not
// valid JSON" Debug log), reached through serveGenerated: a pinned base64
// body under a JSON-ish media type that decodes to bytes which are NOT
// themselves valid JSON. Every existing envelope test either has a JSON
// mediaType with genuinely-JSON bytes (wraps) or a non-JSON mediaType
// (httpx.IsJSONMediaType false, never calls wrapEnvelope at all) — this is the
// third combination: httpx.IsJSONMediaType true, wrapEnvelope itself refuses.
func TestServeGenerated_PinnedMode_EnvelopeSkippedWhenDecodedBodyIsNotJSON(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope

	const raw = "not json at all"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	bodyJSON, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}

	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:         "pinned",
					Body:         json.RawMessage(bodyJSON),
					BodyEncoding: "base64",
					MediaType:    "application/json", // httpx.IsJSONMediaType true — wrapEnvelope IS attempted
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.String() != raw {
		t.Errorf("body = %q, want the un-wrapped literal %q — a JSON-media-typed body that is not actually valid JSON must be left exactly as decoded, never corrupted by a failed wrap attempt", rec.Body.String(), raw)
	}
}
