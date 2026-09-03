package mockplane

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
)

// --- fixtures -------------------------------------------------------------

// fakeAssetStore is an in-memory [AssetStore]: rows by (workspace, name),
// plus an injectable error, mirroring fakeEntityStore's shape.
type fakeAssetStore struct {
	rows map[string]struct {
		meta assets.Meta
		data []byte
	}
	err   error
	calls int
}

func newFakeAssetStore() *fakeAssetStore {
	return &fakeAssetStore{rows: map[string]struct {
		meta assets.Meta
		data []byte
	}{}}
}

func (f *fakeAssetStore) put(ws int64, name, mediaType string, data []byte) {
	f.rows[assetKey(ws, name)] = struct {
		meta assets.Meta
		data []byte
	}{assets.Meta{Name: name, MediaType: mediaType, SizeBytes: int64(len(data)), SHA256: "sha-" + name}, data}
}

func assetKey(ws int64, name string) string { return string(rune(ws)) + "/" + name }

func (f *fakeAssetStore) Meta(_ context.Context, ws int64, name string) (assets.Meta, error) {
	f.calls++
	if f.err != nil {
		return assets.Meta{}, f.err
	}
	row, ok := f.rows[assetKey(ws, name)]
	if !ok {
		return assets.Meta{}, assets.ErrNotFound
	}
	return row.meta, nil
}

func (f *fakeAssetStore) Get(_ context.Context, ws int64, name string) (assets.Meta, []byte, error) {
	f.calls++
	if f.err != nil {
		return assets.Meta{}, nil, f.err
	}
	row, ok := f.rows[assetKey(ws, name)]
	if !ok {
		return assets.Meta{}, nil, assets.ErrNotFound
	}
	return row.meta, row.data, nil
}

// bodyRefRow is one ACTIVE op_overrides row whose 200 is a pinned variant
// referencing an asset by name.
func bodyRefRow(path, name string) *overrides.Row {
	return &overrides.Row{
		Method: http.MethodGet, Path: path, OverrideOn: true,
		Responses: map[string]overrides.Variant{"200": {Mode: "pinned", BodyRef: assets.BodyRefPrefix + name}},
	}
}

// assetURLRow binds an asset_url recipe at pattern on path's generated 200.
func assetURLRow(path, pattern string, data string) *overrides.Row {
	return &overrides.Row{
		Method: http.MethodGet, Path: path, OverrideOn: true,
		Responses: map[string]overrides.Variant{"200": {
			Mode:    "generated",
			Recipes: map[string]recipes.Recipe{pattern: {Kind: recipes.KindAssetURL, Data: jsonx.RawMessage(data)}},
		}},
	}
}

func assetTestPlane(store AssetStore) *Plane {
	p := respondTestPlane()
	if store != nil {
		p.SetAssets(store)
	}
	return p
}

// --- A5: the control route -------------------------------------------------

func TestServeAsset_bytesTypeETagAnd304(t *testing.T) {
	store := newFakeAssetStore()
	store.put(1, "pic.png", "image/png", []byte("PNGBYTES"))
	p := assetTestPlane(store)
	ws := respondTestWorkspace()

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/assets/pic.png", nil)
	rec := httptest.NewRecorder()
	p.serveReserved(rec, req, ws, []string{"assets", "pic.png"})
	if rec.Code != http.StatusOK || rec.Body.String() != "PNGBYTES" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("ETag") != `"sha-pic.png"` || h.Get("Content-Length") != "8" {
		t.Fatalf("headers = %v", h)
	}
	if h.Get("Cache-Control") != "" {
		t.Fatalf("Cache-Control = %q, want none (DESIGN §32.3: no cache header beyond the ETag)", h.Get("Cache-Control"))
	}

	// 304 on a matching tag, and the BLOB is not read: Meta only.
	before := store.calls
	req = httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/assets/pic.png", nil)
	req.Header.Set("If-None-Match", `"sha-pic.png"`)
	rec = httptest.NewRecorder()
	p.serveReserved(rec, req, ws, []string{"assets", "pic.png"})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("If-None-Match: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if store.calls-before != 1 {
		t.Fatalf("a 304 made %d store calls, want 1 (Meta only, never Get)", store.calls-before)
	}

	// HEAD: headers, no body — the headWriter is installed by ServeSlug at
	// step 2, so here the route is exercised through it explicitly.
	req = httptest.NewRequest(http.MethodHead, "http://alex.mock.local/__mocker/assets/pic.png", nil)
	rec = httptest.NewRecorder()
	p.serveReserved(&headWriter{ResponseWriter: rec}, req, ws, []string{"assets", "pic.png"})
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "8" {
		t.Fatalf("HEAD: status=%d body=%q len=%q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
	}
}

func TestServeAsset_refusals(t *testing.T) {
	store := newFakeAssetStore()
	store.put(1, "evil.html", "text/html", []byte("<script>"))
	p := assetTestPlane(store)
	ws := respondTestWorkspace()

	for _, tc := range []struct {
		name string
		rest []string
		want int
	}{
		{"unknown name", []string{"assets", "nope.png"}, http.StatusNotFound},
		{"a stored browser-executable type is refused at serve (the second gate)", []string{"assets", "evil.html"}, http.StatusNotFound},
		{"a nested path is the generic 404", []string{"assets", "a", "b"}, http.StatusNotFound},
		{"an invalid name never reaches the store", []string{"assets", ".."}, http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/"+strings.Join(tc.rest, "/"), nil)
		rec := httptest.NewRecorder()
		p.serveReserved(rec, req, ws, tc.rest)
		if rec.Code != tc.want {
			t.Errorf("%s: status=%d want %d body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<script>") {
			t.Errorf("%s: served the executable body", tc.name)
		}
	}

	// No store wired at all: 404, like a Plane with no entities answers empty.
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/assets/pic.png", nil)
	rec := httptest.NewRecorder()
	assetTestPlane(nil).serveReserved(rec, req, ws, []string{"assets", "pic.png"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no store: status=%d", rec.Code)
	}

	// A store error is a 500, never the bytes and never a 404 that lies.
	store.err = errors.New("injected")
	rec = httptest.NewRecorder()
	p.serveReserved(rec, req, ws, []string{"assets", "pic.png"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("store error: status=%d", rec.Code)
	}
}

// --- A6: bodyRef on a pinned variant --------------------------------------

func TestBodyRef_servesTheAssetVerbatimUnderItsType(t *testing.T) {
	store := newFakeAssetStore()
	store.put(1, "pic.jpg", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0x00})
	settings := domain.Settings{Seed: 1, ListSize: 3}
	// An envelope on the workspace: a JSON body would be wrapped; the asset
	// must not be (§32.3 "verbatim").
	settings.Envelope = ptr("data")
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): bodyRefRow("/order", "pic.jpg")}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	p := assetTestPlane(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	req, tm := attachTrafficMatch(req)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))

	if rec.Code != http.StatusOK || rec.Body.String() != "\xff\xd8\xff\x00" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.Bytes())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want the asset's own", ct)
	}
	if tm.assetMissing {
		t.Fatal("asset_missing marked on a served asset")
	}
}

func TestBodyRef_missingAssetIsEmptyBodyAndNoted(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): bodyRefRow("/order", "gone.jpg")}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)

	for _, tc := range []struct {
		name  string
		store AssetStore
	}{
		{"no such name", newFakeAssetStore()},
		{"no store wired", nil},
		{"store error", &fakeAssetStore{err: errors.New("injected")}},
	} {
		p := assetTestPlane(tc.store)
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
		req, tm := attachTrafficMatch(req)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("%s: status=%d body=%q, want the variant's 200 with an empty body", tc.name, rec.Code, rec.Body.String())
		}
		if !tm.assetMissing {
			t.Errorf("%s: asset_missing not marked", tc.name)
		}
	}
}

func TestBodyRef_previewNeverReadsTheStoreAndNotes(t *testing.T) {
	store := newFakeAssetStore()
	store.put(1, "pic.jpg", "image/jpeg", []byte("x"))
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): bodyRefRow("/order", "pic.jpg")}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	p := assetTestPlane(store)

	rv := resolved{Variant: orderVariants()[1][0], Override: ptr(rows[overrides.OpKey(http.MethodGet, "/order")].Responses["200"]),
		OverrideActive: true, MediaType: "application/json", Status: 200}
	asm := p.assembleResponse(respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order").Route, rows[overrides.OpKey(http.MethodGet, "/order")], rv, nil, nil, 0, nil, nil, "")
	if !asm.AssetMissing || len(asm.Body) != 0 {
		t.Fatalf("Preview-shaped call: AssetMissing=%v body=%q", asm.AssetMissing, asm.Body)
	}
	if store.calls != 0 {
		t.Fatalf("a nil lookup made %d store calls", store.calls)
	}
}

func ptr[T any](v T) *T { return &v }

// --- A7: the asset_url recipe --------------------------------------------

func TestAssetURL_writesTheAbsoluteURLFromTheServingRequest(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): assetURLRow("/order", "subjectId", `"pic.jpg"`)}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	p := assetTestPlane(nil) // no store needed: nothing is looked up

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local:8443/order", nil)
	req.Header.Set("X-Forwarded-Proto", "https") // not trusted: cfg.TrustProxy is off in the fixture
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	got := decodeAny(t, rec.Body.Bytes()).(map[string]any)
	want := "http://alex.mock.local:8443/__mocker/assets/pic.jpg"
	if got["subjectId"] != want {
		t.Fatalf("subjectId = %v, want %q (scheme not upgraded: the forwarded proto is untrusted; port from the request)", got["subjectId"], want)
	}
}

func TestAssetURL_listPicksBySeedAndPreviewUsesItsOwnBase(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): assetURLRow("/order", "subjectId", `["a.png","b.png","c.png"]`)}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	p := assetTestPlane(nil)

	serve := func() string {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
		v, _ := decodeAny(t, rec.Body.Bytes()).(map[string]any)["subjectId"].(string)
		return v
	}
	first := serve()
	if !strings.HasPrefix(first, "http://alex.mock.local/__mocker/assets/") {
		t.Fatalf("subjectId = %q", first)
	}
	if again := serve(); again != first {
		t.Fatalf("same seed, different pick: %q then %q", first, again)
	}

	// Preview: the base it was handed, and an empty one declines.
	row := rows[overrides.OpKey(http.MethodGet, "/order")]
	rv := resolved{Variant: orderVariants()[1][0], Override: ptr(row.Responses["200"]), OverrideActive: true, MediaType: "application/json", Status: 200}
	asm := p.assembleResponse(respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order").Route, row, rv, nil, nil, 0, nil, nil, "https://alex.mock.local:8443/__mocker/assets/")
	if v, _ := decodeAny(t, asm.Body).(map[string]any)["subjectId"].(string); !strings.HasPrefix(v, "https://alex.mock.local:8443/__mocker/assets/") {
		t.Fatalf("preview subjectId = %q", v)
	}
	asm = p.assembleResponse(respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order").Route, row, rv, nil, nil, 0, nil, nil, "")
	if _, isString := decodeAny(t, asm.Body).(map[string]any)["subjectId"].(string); isString {
		t.Fatalf("an empty base still produced a URL: %s", asm.Body)
	}
}

// A spec-declared browser-executable type refuses the whole body BEFORE the
// bodyRef arm: the asset is never read (A6's "existing refusal" clause).
func TestBodyRef_specDeclaredExecutableTypeRefusesBeforeTheLookup(t *testing.T) {
	store := newFakeAssetStore()
	store.put(1, "pic.jpg", "image/jpeg", []byte("x"))
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): bodyRefRow("/order", "pic.jpg")}
	variants := orderVariants()
	variants[1][0].MediaType = "text/html"
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), variants, settings, rows)
	p := assetTestPlane(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatalf("body=%q type=%q, want the refusal's empty body with no type", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if store.calls != 0 {
		t.Fatalf("the asset store was read %d times under a refused spec type", store.calls)
	}
}

func TestBodyRef_zeroByteAssetIsServedNotMissing(t *testing.T) {
	store := newFakeAssetStore()
	store.put(1, "empty.txt", "text/plain", []byte{})
	settings := domain.Settings{Seed: 1, ListSize: 3}
	rows := map[string]*overrides.Row{overrides.OpKey(http.MethodGet, "/order"): bodyRefRow("/order", "empty.txt")}
	rt := fixtureRuntimeWithOverrides(t, orderDoc, orderRoutes(), orderVariants(), settings, rows)
	p := assetTestPlane(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	req, tm := attachTrafficMatch(req)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/order"), resources.ScopeKey(""))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || tm.assetMissing {
		t.Fatalf("status=%d body=%q missing=%v", rec.Code, rec.Body.String(), tm.assetMissing)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("Content-Type = %q, want the asset's own on a zero-byte asset", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff missing on a bodyRef response")
	}
}

func TestIfNoneMatch(t *testing.T) {
	t.Parallel()
	const tag = `"abc"`
	for _, tc := range []struct {
		h    string
		want bool
	}{
		{``, false}, {`"abc"`, true}, {`W/"abc"`, true}, {`*`, true},
		{`"x", "abc"`, true}, {`"x",W/"abc"`, true}, {`"x"`, false}, {`abc`, false},
	} {
		if got := ifNoneMatch(tc.h, tag); got != tc.want {
			t.Errorf("ifNoneMatch(%q) = %v, want %v", tc.h, got, tc.want)
		}
	}
}
