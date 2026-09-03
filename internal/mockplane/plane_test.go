package mockplane_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/workspaces"
)

// fakeSource is an in-memory [mockplane.Source]: the plane needs no database
// to exercise the request path, only something that answers BySlug.
type fakeSource struct {
	bySlug map[string]*workspaces.Workspace
}

func (f *fakeSource) BySlug(_ context.Context, slug string) (*workspaces.Workspace, error) {
	if ws, ok := f.bySlug[slug]; ok {
		return ws, nil
	}
	return nil, workspaces.ErrNotFound
}

// testConfig builds a config.Config that satisfies every trap documented for
// hand-built test configs: a non-empty ReservedPrefix, host routing with a
// real base domain, and Dev so cookie hardening (irrelevant here, but part of
// the shared minimum) doesn't get in the way.
func testConfig() *config.Config {
	return &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		MaxBody:        10 << 20,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newPlane(wss ...*workspaces.Workspace) *mockplane.Plane {
	src := &fakeSource{bySlug: make(map[string]*workspaces.Workspace, len(wss))}
	for _, ws := range wss {
		src.bySlug[ws.Slug] = ws
	}
	// specs is nil: these tests exercise the plane's pre-P1a behavior, which
	// SpecSource being nil is specifically defined to preserve (routes.go).
	return mockplane.New(testConfig(), src, nil, testLogger())
}

// decodeJSON unmarshals rec's body into a map, failing the test on any
// decode error so a malformed body shows up as a clear failure rather than a
// nil-map false positive further down.
func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode JSON body %q: %v", body, err)
	}
	return m
}

func TestServeHTTP_Health(t *testing.T) {
	specID := int64(42)
	p := newPlane(
		&workspaces.Workspace{Slug: "alex", Revision: 3, Settings: domain.DefaultSettings()},
		&workspaces.Workspace{Slug: "withspec", Revision: 1, SpecID: &specID, Settings: domain.DefaultSettings()},
	)

	t.Run("no spec", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/health", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		body := decodeJSON(t, rec.Body.Bytes())

		wantKeys := []string{"ok", "workspace", "revision", "spec"}
		if len(body) != len(wantKeys) {
			t.Fatalf("body has %d keys (%v), want exactly %v", len(body), keysOf(body), wantKeys)
		}
		for _, k := range wantKeys {
			if _, ok := body[k]; !ok {
				t.Errorf("missing key %q in %v", k, body)
			}
		}
		if body["ok"] != true {
			t.Errorf("ok = %v, want true", body["ok"])
		}
		if body["workspace"] != "alex" {
			t.Errorf("workspace = %v, want %q", body["workspace"], "alex")
		}
		if body["revision"] != float64(3) {
			t.Errorf("revision = %v, want 3", body["revision"])
		}
		if body["spec"] != nil {
			t.Errorf("spec = %v, want nil", body["spec"])
		}
	})

	t.Run("with spec", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://withspec.mock.local/__mocker/health", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)

		body := decodeJSON(t, rec.Body.Bytes())
		if body["spec"] != float64(42) {
			t.Errorf("spec = %v, want 42", body["spec"])
		}
	})
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestServeHTTP_UnknownSlug(t *testing.T) {
	p := newPlane()

	req := httptest.NewRequest(http.MethodGet, "http://ghost.mock.local/__mocker/health", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "ghost") {
		t.Errorf("body %q does not name the slug that was tried", rec.Body.String())
	}
}

func TestServeHTTP_NonBaseDomainHost(t *testing.T) {
	p := newPlane()

	req := httptest.NewRequest(http.MethodGet, "http://totally-unrelated.example/__mocker/health", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "mock.local") {
		t.Errorf("body %q does not name the expected host shape", rec.Body.String())
	}
}

// TestServeHTTP_NotImplementedYet proves the "everything else under the
// reserved prefix is not implemented yet" fallback still holds. It no
// longer targets POST {prefix}/state — P1c2 implements that endpoint (see
// livestate_test.go) — so this now targets a sibling path nothing under the
// prefix has ever claimed, keeping the catch-all itself under test.
func TestServeHTTP_NotImplementedYet(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/__mocker/scenario", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "not_implemented_yet" {
		t.Errorf("code = %v, want %q", errObj["code"], "not_implemented_yet")
	}
}

func TestServeHTTP_CatchAll404(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/users/1", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// TestServeHTTP_EncodedSlashDoesNotReachReserved is the end-to-end regression
// for round-1 review finding 1: GET /__mocker%2Fhealth must 404 like any
// other unmatched path, never be treated as GET /__mocker/health. Before the
// fix, cutReservedPrefix matched against NormalizePath's rejoined string,
// which decodes %2F and glues the result back into a plain "/", making the
// two byte-identical by the time matching saw them.
func TestServeHTTP_EncodedSlashDoesNotReachReserved(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Revision: 3, Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker%2Fhealth", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /__mocker%%2Fhealth: status = %d, want 404 (must not resolve to the reserved health endpoint); body=%s",
			rec.Code, rec.Body)
	}
}

func TestServeHTTP_Preflight(t *testing.T) {
	// DefaultSettings has CORS{Mode: reflect, Credentials: true}.
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodOptions, "http://alex.mock.local/anything", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	req.Header.Set("Access-Control-Request-Headers", "X-Custom, Content-Type")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":      "https://app.example",
		"Access-Control-Allow-Methods":     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Allow-Headers":     "X-Custom, Content-Type",
		"Access-Control-Max-Age":           "600",
	}
	for header, wantVal := range want {
		if got := rec.Header().Get(header); got != wantVal {
			t.Errorf("%s = %q, want %q", header, got, wantVal)
		}
	}
	if vary := rec.Header().Values("Vary"); !slices.Contains(vary, "Origin") {
		t.Errorf("Vary = %v, want to contain Origin", vary)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("preflight body = %q, want empty", rec.Body.String())
	}
}

func TestServeHTTP_PreflightUnknownWorkspace(t *testing.T) {
	p := newPlane()

	req := httptest.NewRequest(http.MethodOptions, "http://ghost.mock.local/anything", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	// domain.DefaultSettings().CORS is {reflect, credentials: true}.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

func TestServeHTTP_CORSOnNotFound(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/no-such-route", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want echoed origin even on a 404", got)
	}
}

func TestServeHTTP_CredentialsNeverWildcard(t *testing.T) {
	s := domain.DefaultSettings()
	s.CORS = domain.CORSSettings{Mode: domain.CORSReflect, Credentials: true}
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: s})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/health", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" || got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the echoed origin, never \"*\"", got)
	}
}

func TestServeHTTP_CORSModeOff(t *testing.T) {
	s := domain.DefaultSettings()
	s.CORS = domain.CORSSettings{Mode: domain.CORSOff}
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: s})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/health", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	for _, header := range []string{
		"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials",
		"Access-Control-Expose-Headers", "Vary",
	} {
		if got := rec.Header().Get(header); got != "" {
			t.Errorf("%s = %q, want empty with cors.mode=off", header, got)
		}
	}
}

func TestServeHTTP_NoOriginNoCORSHeaders(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/health", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty with no Origin header", got)
	}
}

func TestServeHTTP_HEAD(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Revision: 1, Settings: domain.DefaultSettings()})

	req := httptest.NewRequest(http.MethodHead, "http://alex.mock.local/__mocker/health", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
}

func TestServeSlug_Direct(t *testing.T) {
	p := newPlane(&workspaces.Workspace{Slug: "alex", Revision: 7, Settings: domain.DefaultSettings()})

	// Host is deliberately unrelated to the base domain: ServeSlug must not
	// depend on it, since MOCKER_ROUTING=path calls it with the slug already
	// extracted from the URL, not from Host.
	req := httptest.NewRequest(http.MethodGet, "http://ignored.example/__mocker/health", nil)
	rec := httptest.NewRecorder()
	p.ServeSlug(rec, req, "alex")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if body["workspace"] != "alex" || body["revision"] != float64(7) {
		t.Errorf("body = %v, want workspace=alex revision=7", body)
	}
}

func TestServeSlug_UnknownSlugDirect(t *testing.T) {
	p := newPlane()

	req := httptest.NewRequest(http.MethodGet, "http://ignored.example/__mocker/health", nil)
	rec := httptest.NewRecorder()
	p.ServeSlug(rec, req, "nope")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// TestNormalizePath exercises the DISPLAY-ONLY formatting (query-stripping,
// slash-collapsing, per-segment decoding, rejoining). It intentionally
// includes cases where a decoded "/" inside one segment becomes
// indistinguishable, once rejoined, from a real boundary between two
// segments ("encoded slash..." below): that flattening is exactly why
// NormalizePath must never be used for route matching — see
// TestNormalizeSegments and TestServeHTTP_EncodedSlashDoesNotReachReserved
// for the segment-level guarantee matching actually relies on.
func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"root", "/", "/"},
		{"empty", "", "/"},
		{"no leading segments", "///", "/"},
		{"strips query", "/a/b?x=1&y=2", "/a/b"},
		{"strips bare query marker", "/a?", "/a"},
		{"collapses double slash", "/a//b", "/a/b"},
		{"collapses many slashes", "/a////b", "/a/b"},
		{"drops trailing slash", "/a/b/", "/a/b"},
		{"decodes a segment", "/a/%68ello", "/a/hello"},
		{"decodes reserved-looking chars", "/%5Fmocker/health", "/_mocker/health"},
		{
			"encoded slash inside a segment flattens to a display string indistinguishable from a real boundary",
			"/seg%2Fment", "/seg/ment",
		},
		{
			"encoded double slash is decoded, not collapsed",
			"/seg%2F%2Fment", "/seg//ment",
		},
		{"invalid escape kept as-is", "/100%", "/100%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mockplane.NormalizePath(tt.in); got != tt.want {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeSegments is the segment-level twin of TestNormalizePath: it
// proves the property NormalizePath's flattened output cannot — that an
// encoded "/" landing inside one segment stays part of that ONE segment and
// is never mistaken for a boundary between two.
func TestNormalizeSegments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"root", "/", nil},
		{"empty", "", nil},
		{"real boundary", "/__mocker/health", []string{"__mocker", "health"}},
		{
			"encoded slash inside a segment stays inside it, as one segment",
			"/__mocker%2Fhealth", []string{"__mocker/health"},
		},
		{
			"encoded slash does not equal a real boundary at the segment level",
			"/seg%2Fment", []string{"seg/ment"},
		},
		{"collapses double slash", "/a//b", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mockplane.NormalizeSegments(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("NormalizeSegments(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
