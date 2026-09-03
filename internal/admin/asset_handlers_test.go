package admin_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

// A6 (A4): the three asset routes through the real handler chain — CSRF
// (the raw-body exemption and what it does NOT exempt), the two size gates,
// the media-type refusal at both gates, confirmSlug on the delete, and the
// url the record reports.

// rawPut builds PUT /api/workspaces/{id}/assets/{name} with the file as the
// body — the one request in this package whose Content-Type is not JSON.
func rawPut(t *testing.T, id float64, name, contentType string, body []byte, cookie *http.Cookie, csrf string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("http://mocker.local/api/workspaces/%d/assets/%s", int64(id), name), bytes.NewReader(body))
	req.Host = "mocker.local"
	req.Header.Set("Origin", "http://mocker.local")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return req
}

func TestAssets_putListDelete(t *testing.T) {
	ts := newTestServerCfg(t, func(c *config.Config) {
		c.MaxAsset = 4096
		c.MaxAssetsTotal = 6000
		c.TrustProxy = config.TrustProxy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	})
	cookie, csrf, id, slug := ts.createWorkspace(t, "alice", "alex")

	// Create: 201, the url is the mock-plane address under a trusted https.
	req := rawPut(t, id, "pic.jpg", "image/jpeg; charset=binary", []byte("JPEGJPEG"), cookie, csrf)
	req.RemoteAddr = "10.1.2.3:4444"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Host = "mocker.local:8443"
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT: status=%d body=%s", rec.Code, rec.Body)
	}
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	wantURL := "https://" + slug + ".mock.local:8443/__mocker/assets/pic.jpg"
	if view["url"] != wantURL || view["mediaType"] != "image/jpeg" || view["sizeBytes"].(float64) != 8 {
		t.Fatalf("view = %v, want url %q", view, wantURL)
	}

	// Replace: 200.
	rec = ts.do(rawPut(t, id, "pic.jpg", "image/webp", []byte("WEBP"), cookie, csrf))
	if rec.Code != http.StatusOK {
		t.Fatalf("second PUT: status=%d body=%s", rec.Code, rec.Body)
	}

	// List: one row, the caps beside the usage.
	rec = ts.do(jsonRequest(t, http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/assets", int64(id)), nil, cookie, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list: status=%d body=%s", rec.Code, rec.Body)
	}
	var list struct {
		Assets        []map[string]any `json:"assets"`
		TotalBytes    int64            `json:"totalBytes"`
		MaxAssetBytes int64            `json:"maxAssetBytes"`
		MaxTotalBytes int64            `json:"maxTotalBytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Assets) != 1 || list.TotalBytes != 4 || list.MaxAssetBytes != 4096 || list.MaxTotalBytes != 6000 {
		t.Fatalf("list = %+v", list)
	}

	// Delete: the slug guard, then 204, then 404.
	del := func(body any) *httptest.ResponseRecorder {
		return ts.do(jsonRequest(t, http.MethodDelete, fmt.Sprintf("http://mocker.local/api/workspaces/%d/assets/pic.jpg", int64(id)), body, cookie, csrf))
	}
	if rec := del(map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE without confirmSlug: status=%d body=%s", rec.Code, rec.Body)
	}
	if rec := del(map[string]string{"confirmSlug": "wrong"}); rec.Code != http.StatusConflict {
		t.Fatalf("DELETE wrong slug: status=%d body=%s", rec.Code, rec.Body)
	}
	if rec := del(map[string]string{"confirmSlug": slug}); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d body=%s", rec.Code, rec.Body)
	}
	if rec := del(map[string]string{"confirmSlug": slug}); rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE again: status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestAssets_putRefusals(t *testing.T) {
	ts := newTestServerCfg(t, func(c *config.Config) {
		c.MaxAsset = 1024
		c.MaxAssetsTotal = 1536
	})
	cookie, csrf, id, _ := ts.createWorkspace(t, "alice", "alex")

	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
		code string
	}{
		{"no csrf token", rawPut(t, id, "a.png", "image/png", []byte("x"), cookie, ""), http.StatusForbidden, ""},
		{"no session", rawPut(t, id, "a.png", "image/png", []byte("x"), nil, csrf), http.StatusUnauthorized, ""},
		{"html at the chain", rawPut(t, id, "a.html", "text/html", []byte("<script>"), cookie, csrf), http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"svg at the chain", rawPut(t, id, "a.svg", "image/svg+xml", []byte("<svg/>"), cookie, csrf), http.StatusUnsupportedMediaType, ""},
		{"no content type", rawPut(t, id, "a.png", "", []byte("x"), cookie, csrf), http.StatusUnsupportedMediaType, ""},
		{"unparseable type", rawPut(t, id, "a.png", "image/png,text/html", []byte("x"), cookie, csrf), http.StatusUnsupportedMediaType, ""},
		{"bad name (the mux itself redirects a dot-segment; a space is what reaches the handler)", rawPut(t, id, "a%20b", "image/png", []byte("x"), cookie, csrf), http.StatusBadRequest, ""},
		{"over the per-file cap", rawPut(t, id, "big.png", "image/png", bytes.Repeat([]byte("x"), 1025), cookie, csrf), http.StatusRequestEntityTooLarge, "asset_too_large"},
	} {
		rec := ts.do(tc.req)
		if rec.Code != tc.want || (tc.code != "" && !strings.Contains(rec.Body.String(), tc.code)) {
			t.Errorf("%s: status=%d body=%s, want %d %s", tc.name, rec.Code, rec.Body, tc.want, tc.code)
		}
	}

	// A wrong Origin is refused even on the raw-body route: check 2 runs.
	req := rawPut(t, id, "a.png", "image/png", []byte("x"), cookie, csrf)
	req.Header.Set("Origin", "http://evil.example")
	if rec := ts.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong origin: status=%d body=%s", rec.Code, rec.Body)
	}

	// POST to the same path is NOT exempt: a non-JSON type is still 415.
	req = rawPut(t, id, "a.png", "image/png", []byte("x"), cookie, csrf)
	req.Method = http.MethodPost
	if rec := ts.do(req); rec.Code != http.StatusUnsupportedMediaType || !strings.Contains(rec.Body.String(), "application/json") {
		t.Fatalf("POST with image/png: status=%d body=%s, want 415 naming application/json", rec.Code, rec.Body)
	}

	// The quota: 1024 + 1024 > 1536.
	if rec := ts.do(rawPut(t, id, "one.png", "image/png", bytes.Repeat([]byte("x"), 1024), cookie, csrf)); rec.Code != http.StatusCreated {
		t.Fatalf("first at the cap: status=%d body=%s", rec.Code, rec.Body)
	}
	if rec := ts.do(rawPut(t, id, "two.png", "image/png", bytes.Repeat([]byte("x"), 1024), cookie, csrf)); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "assets_quota") {
		t.Fatalf("over quota: status=%d body=%s", rec.Code, rec.Body)
	}
}

// A custom endpoint created with bodyRef carries it into its pinned variant
// (A6: §32.3's "or a custom endpoint"), and body/mediaType beside it are
// refused by name.
func TestAssets_customEndpointBodyRef(t *testing.T) {
	ts := newTestServer(t)
	cookie, csrf, id, _ := ts.createWorkspace(t, "alice", "alex")
	base := fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", int64(id))

	rec := ts.do(jsonRequest(t, http.MethodPost, base,
		map[string]any{"method": "GET", "path": "/logo", "bodyRef": "asset:logo.png", "mediaType": "image/png"}, cookie, csrf))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "exclusive") {
		t.Fatalf("bodyRef beside mediaType: status=%d body=%s", rec.Code, rec.Body)
	}
	rec = ts.do(jsonRequest(t, http.MethodPost, base,
		map[string]any{"method": "GET", "path": "/logo", "bodyRef": "logo.png"}, cookie, csrf))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bodyRef without the asset: prefix: status=%d body=%s", rec.Code, rec.Body)
	}
	rec = ts.do(jsonRequest(t, http.MethodPost, base,
		map[string]any{"method": "GET", "path": "/logo", "bodyRef": "asset:logo.png"}, cookie, csrf))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with bodyRef: status=%d body=%s", rec.Code, rec.Body)
	}
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	responses, _ := view["responses"].(map[string]any)
	v200, _ := responses["200"].(map[string]any)
	if v200["bodyRef"] != "asset:logo.png" || v200["mode"] != "pinned" {
		t.Fatalf("stored variant = %v, want a pinned bodyRef", v200)
	}
}
