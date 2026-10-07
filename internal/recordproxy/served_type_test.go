package recordproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/workspaces"
)

// proxyService saves a config for mode/upstream and returns a ServeProxy
// caller bound to it, so each case below states only what it is about.
func proxyService(t *testing.T, mode, upstream string) (*Repo, int64, func(path string, capture func([]byte) (int, error)) *httptest.ResponseRecorder) {
	t.Helper()
	repo, id := fixture(t)
	c := DefaultConfig()
	c.Mode = mode
	c.Upstream = upstream
	c.CaptureEntities = true
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{ProxyAllowlist: []string{u.Scheme + "://" + u.Host}, MaxBody: 1024, MaxResponse: 4096})
	ws := &workspaces.Workspace{ID: id, Slug: "proxy-test"}
	return repo, id, func(path string, capture func([]byte) (int, error)) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		if !s.ServeProxy(w, httptest.NewRequest(http.MethodGet, path, nil), ws, "GET "+path, false, capture) {
			t.Fatal("proxy not handled")
		}
		return w
	}
}

// review 2026-10-06, F178: passthrough and record copied the upstream
// Content-Type verbatim, so an allowlisted upstream answering text/html (or
// the comma-smuggled application/json,text/html a browser resolves to HTML)
// was served on the mock plane — same origin as the admin session in path
// mode. Every other mock-plane serving path runs the one gate in httpx.
func TestProxyRefusesBrowserExecutableUpstreamType(t *testing.T) {
	for _, mode := range []string{"passthrough", "record"} {
		for _, ct := range []string{"text/html", "application/json,text/html", "image/svg+xml", "text/html; x=json"} {
			t.Run(mode+" "+ct, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header()["Content-Type"] = []string{ct}
					_, _ = w.Write([]byte(`{"a":"<img src=x onerror=alert(1)>"}`))
				}))
				defer upstream.Close()
				repo, id, call := proxyService(t, mode, upstream.URL)
				w := call("/page", nil)
				if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "onerror") {
					t.Fatalf("served executable upstream type: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
				}
				if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
					t.Fatalf("nosniff = %q", got)
				}
				if list, err := repo.List(t.Context(), id); err != nil || len(list) != 0 {
					t.Fatalf("executable type recorded: %+v %v", list, err)
				}
			})
		}
	}
}

// review 2026-10-06, F178: an UNTYPED upstream body was written with no
// Content-Type, and net/http then sniffs one itself (DetectContentType),
// which names text/html for an HTML body — the gate must see that type too.
func TestProxyRefusesUntypedHTMLUpstreamBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = w.Write([]byte(`<html><script>alert(1)</script></html>`))
	}))
	defer upstream.Close()
	_, _, call := proxyService(t, "passthrough", upstream.URL)
	w := call("/page", nil)
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "script") {
		t.Fatalf("served sniffed html: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
}

// The gate must not cost ordinary traffic: JSON and plain text still pass,
// now with nosniff, and an untyped non-HTML body keeps the type net/http
// would have sniffed for it.
func TestProxyPassthroughKeepsSafeTypesWithNosniff(t *testing.T) {
	for _, tc := range []struct{ ct, body, want string }{
		{"application/json", `{"ok":true}`, "application/json"},
		{"text/plain; charset=utf-8", "hello", "text/plain; charset=utf-8"},
		{"", "hello", "text/plain; charset=utf-8"},
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.ct == "" {
				w.Header()["Content-Type"] = nil
			} else {
				w.Header().Set("Content-Type", tc.ct)
			}
			_, _ = w.Write([]byte(tc.body))
		}))
		_, _, call := proxyService(t, "passthrough", upstream.URL)
		w := call("/ok", nil)
		upstream.Close()
		if w.Code != http.StatusOK || w.Body.String() != tc.body || w.Header().Get("Content-Type") != tc.want || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%q: %d %q %q %s", tc.ct, w.Code, w.Header().Get("Content-Type"), w.Header().Get("X-Content-Type-Options"), w.Body)
		}
	}
}

// review 2026-10-06, F178: record accepted any type CONTAINING "json", so
// text/plain; x=json was stored (and traffic.RedactBody then routed the body
// to the text redactor, leaving a JSON "password" in clear). Recording now
// requires a parsed essence whose subtype names JSON.
func TestProxyRecordRequiresApplicationJSONEssence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; x=json")
		_, _ = w.Write([]byte(`{"password":"secret"}`))
	}))
	defer upstream.Close()
	repo, id, call := proxyService(t, "record", upstream.URL)
	w := call("/text", nil)
	if got := w.Header().Get("X-Mocker-Recording"); got != "skipped-non-json" {
		t.Fatalf("recording = %q", got)
	}
	if list, err := repo.List(t.Context(), id); err != nil || len(list) != 0 {
		t.Fatalf("non-json essence recorded: %+v %v", list, err)
	}
}

// review 2026-10-06, F178: replay served the stored Content-Type with no
// gate and no nosniff. A recording made before the record-time gate existed
// (or written by anything else) must still not reach a browser as HTML.
func TestProxyReplayRefusesExecutableStoredType(t *testing.T) {
	repo, id := fixture(t)
	c := DefaultConfig()
	c.Mode = "record"
	c.Upstream = "http://api.test"
	c, err := repo.Save(t.Context(), id, c)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(repo, &config.Config{MaxBody: 1024, MaxResponse: 1024})
	key := func(r *http.Request) string {
		return RequestKey("http://api.test|auth=false|cookies=false", r, []byte{})
	}
	for path, ct := range map[string]string{"/html": "application/json,text/html", "/json": "application/json"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if _, err := repo.Record(t.Context(), id, c, Recording{Key: key(r), Method: http.MethodGet, Path: path, Status: 200, ContentType: ct, Body: []byte(`{"a":"<script>x</script>"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	c.Mode = "replay"
	if _, err := repo.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	serve := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeProxy(w, httptest.NewRequest(http.MethodGet, path, nil), &workspaces.Workspace{ID: id}, "", false, nil)
		return w
	}
	if w := serve("/html"); w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "script") {
		t.Fatalf("replayed executable type: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if w := serve("/json"); w.Code != http.StatusOK || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("json replay: %d %q", w.Code, w.Header().Get("X-Content-Type-Options"))
	}
}

// review 2026-10-06, F180: entity capture was fed the REDACTED body, so a
// field named like a secret (sort_key, api_key, …) was upserted as the
// literal "[redacted]" and the response said `saved`. Redacted traffic is
// not real data; capture is skipped and says so.
func TestProxyEntityCaptureSkipsRedactedBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"a","sort_key":"a1"}]`))
	}))
	defer upstream.Close()
	_, _, call := proxyService(t, "record", upstream.URL)
	var captured []byte
	w := call("/accounts", func(body []byte) (int, error) {
		captured = body
		return 1, nil
	})
	if captured != nil || w.Header().Get("X-Mocker-Entities-Result") != "skipped-redacted" || w.Header().Get("X-Mocker-Entities-Imported") != "0" {
		t.Fatalf("captured %s result=%q imported=%q", captured, w.Header().Get("X-Mocker-Entities-Result"), w.Header().Get("X-Mocker-Entities-Imported"))
	}
	if w.Header().Get("X-Mocker-Recording") != "saved-redacted" {
		t.Fatalf("recording = %q", w.Header().Get("X-Mocker-Recording"))
	}
}

// review 2026-10-06, F179 at the mock plane: a dot-segment path is the
// caller's error, answered 400 before anything reaches the upstream.
func TestProxyAnswersDotSegmentPathAs400(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	_, _, call := proxyService(t, "passthrough", upstream.URL+"/v1/team-a")
	if w := call("/../../admin", nil); w.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("dot segment: %d calls=%d %s", w.Code, calls, w.Body)
	}
}
