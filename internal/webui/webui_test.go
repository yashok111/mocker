package webui_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/webui"
)

const (
	indexBody = "<html>SPA-INDEX</html>"
	assetBody = "console.log('app')"
)

// discardLog is the logger every test hands Handler: nothing here asserts on
// log output, so it goes nowhere rather than cluttering `go test -v`.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// builtFS is a minimal but real-shaped build: an index.html plus one
// content-hashed asset, exactly what `make ui` produces.
func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(indexBody)},
		"assets/app.js": &fstest.MapFile{Data: []byte(assetBody)},
	}
}

func testCfg(baseDomain string) *config.Config {
	return &config.Config{BaseDomain: baseDomain}
}

// teapotNext answers every request it receives with 418, a status nothing
// else in this package ever produces — a request reaching it is therefore
// unambiguous, unlike checking for the ABSENCE of some SPA-shaped response.
func teapotNext() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

func TestHandler_UnmatchedPath_ServesIndexWithNoCache(t *testing.T) {
	t.Parallel()
	h := webui.Handler(builtFS(), teapotNext(), testCfg("mocks.example.com"), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.local/w/7", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != indexBody {
		t.Errorf("body = %q, want index.html body %q", rec.Body.String(), indexBody)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestHandler_AssetPath_ImmutableCacheAndContentType(t *testing.T) {
	t.Parallel()
	h := webui.Handler(builtFS(), teapotNext(), testCfg("mocks.example.com"), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.local/assets/app.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != assetBody {
		t.Errorf("body = %q, want asset body %q", rec.Body.String(), assetBody)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want it to contain %q", cc, "immutable")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want it to contain %q", ct, "javascript")
	}
}

func TestHandler_HeadRequest_NoBody(t *testing.T) {
	t.Parallel()
	h := webui.Handler(builtFS(), teapotNext(), testCfg("mocks.example.com"), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "http://admin.local/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %d bytes, want 0", rec.Body.Len())
	}
}

// TestHandler_AdminSurface_PassesToNext pins the whole contract's condition:
// Handler answers ONLY GET/HEAD outside its reserved paths. Every row here
// must reach teapotNext, not the SPA — a regression that widens the reserved
// set into a mux pattern (the defect the task's own gate measured and
// rejected) would instead start answering one of these with index.html.
func TestHandler_AdminSurface_PassesToNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"GET healthz", http.MethodGet, "/healthz"},
		{"GET readyz", http.MethodGet, "/readyz"},
		{"GET mcp (an MCP client probe, never the SPA shell)", http.MethodGet, "/mcp"},
		{"GET bare api", http.MethodGet, "/api"},
		{"GET api subpath", http.MethodGet, "/api/definitely-nope"},
		{"POST healthz", http.MethodPost, "/healthz"},
		{"PUT api workspaces", http.MethodPut, "/api/workspaces"},
		{"POST arbitrary path", http.MethodPost, "/whatever"},
		{"DELETE root", http.MethodDelete, "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := webui.Handler(builtFS(), teapotNext(), testCfg("mocks.example.com"), discardLog())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, "http://admin.local"+tt.path, nil))
			if rec.Code != http.StatusTeapot {
				t.Errorf("%s %s: status = %d, want 418 (reached next)", tt.method, tt.path, rec.Code)
			}
		})
	}
}

func TestHandler_NotBuilt_Answers503NamingMakeUI(t *testing.T) {
	t.Parallel()
	empty := fstest.MapFS{} // a fresh clone: dist/ holds only .gitkeep, no index.html
	h := webui.Handler(empty, teapotNext(), testCfg("mocks.example.com"), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.local/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if !strings.Contains(rec.Body.String(), "make ui") {
		t.Errorf("body = %q, want it to name `make ui`", rec.Body.String())
	}
}

// TestHandler_PathTraversal_NeverEscapesFsys covers the raw ".." form, its
// %2e%2e-encoded twin (net/http decodes both to the identical r.URL.Path —
// verified separately against net/url — so the safety check must catch both
// the same way) and an embedded NUL byte, which fs.ValidPath alone does not
// reject. Every case must land on the ordinary unmatched-path answer
// (index.html, 200) rather than erroring, hanging, or reading anything a
// legitimate route wouldn't.
func TestHandler_PathTraversal_NeverEscapesFsys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{"raw dot-dot", "http://admin.local/../../etc/passwd"},
		{"percent-encoded dot-dot", "http://admin.local/%2e%2e/%2e%2e/etc/passwd"},
		{"doubled leading slash", "http://admin.local//etc/passwd"},
		{"embedded NUL", "http://admin.local/foo%00bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := webui.Handler(builtFS(), teapotNext(), testCfg("mocks.example.com"), discardLog())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (safe fallback)", rec.Code)
			}
			if rec.Body.String() != indexBody {
				t.Errorf("body = %q, want the index.html fallback %q", rec.Body.String(), indexBody)
			}
		})
	}
}

func TestHandler_CSP_WithBaseDomain(t *testing.T) {
	t.Parallel()
	h := webui.Handler(builtFS(), teapotNext(), testCfg("mocks.example.com"), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.local/", nil))

	want := "default-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; " +
		"object-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
		"connect-src 'self' http://*.mocks.example.com:* https://*.mocks.example.com:*" +
		" ws://*.mocks.example.com:* wss://*.mocks.example.com:*"
	if got := rec.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("CSP =\n%q\nwant\n%q", got, want)
	}
}

func TestHandler_CSP_EmptyBaseDomain_OmitsHostSources(t *testing.T) {
	t.Parallel()
	h := webui.Handler(builtFS(), teapotNext(), testCfg(""), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.local/", nil))

	got := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(got, "connect-src 'self'") {
		t.Errorf("CSP = %q, want it to contain a bare connect-src 'self'", got)
	}
	if strings.Contains(got, "*.") {
		t.Errorf("CSP = %q, empty BaseDomain must not produce a wildcard host source", got)
	}
}

func TestHandler_CSP_OnNotBuiltResponseToo(t *testing.T) {
	t.Parallel()
	empty := fstest.MapFS{}
	h := webui.Handler(empty, teapotNext(), testCfg("mocks.example.com"), discardLog())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://admin.local/", nil))

	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("503 response carries no Content-Security-Policy header, want one on every response Handler serves")
	}
}

func TestBuilt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fsys fstest.MapFS
		want bool
	}{
		{"index.html present", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(indexBody)}}, true},
		{"only .gitkeep", fstest.MapFS{".gitkeep": &fstest.MapFile{Data: []byte{}}}, false},
		{"empty fs", fstest.MapFS{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := webui.Built(tt.fsys); got != tt.want {
				t.Errorf("Built() = %v, want %v", got, tt.want)
			}
		})
	}
}
