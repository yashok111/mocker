package mockplane

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/workspaces"
)

// AssetStore is the mock plane's view of internal/assets (A6, DESIGN §32):
// two reads and nothing else — the mock plane never creates, replaces or
// deletes an asset, only an authenticated operator does. Same shape as
// [EntityStore]: an interface plus a setter, because this package imports
// internal/store in not one production file, and *assets.Repo satisfies it
// with no adapter.
type AssetStore interface {
	// Meta is what the ETag path and a bodyRef's existence check read;
	// never the bytes.
	Meta(ctx context.Context, workspaceID int64, name string) (assets.Meta, error)
	// Get is the bytes, for a 200 on the route and for a bodyRef body.
	Get(ctx context.Context, workspaceID int64, name string) (assets.Meta, []byte, error)
}

// SetAssets wires store as the Plane's [AssetStore]. Call it once, during
// startup, beside [Plane.SetEntities]; a Plane whose SetAssets is never
// called answers 404 on the asset route and asset_missing on every bodyRef
// — the "feature dead in prod while every test is green" shape CLAUDE.md
// warns about, which is why scripts/smoke.sh's A6 block exercises both
// through the real image rather than trusting main.go by reading it.
func (p *Plane) SetAssets(store AssetStore) {
	p.assets = store
}

// assetLookup is the per-request seam a bodyRef resolves through (A6 D6),
// the shape recipes.Ref already has and for the same reason: it is
// CONSTRUCTED in serveGenerated, where a real request and a real store
// exist, and RECEIVED by assembleResponse, which Preview shares and hands
// nil. ok=false means "serve the variant's status with an empty body" for
// every reason at once — no store wired, no such name, a stored type the
// serve-side gate refuses, a store error — and the closure is the one that
// marks the traffic note, because assembleResponse holds no *http.Request
// and cannot (round-1 #5).
type assetLookup func(name string) (assets.Meta, []byte, bool)

// newAssetLookup builds the closure for one request. The executable-type
// refusal runs here on the STORED type — the second gate §32.6 promises
// beside the upload's own — because a row written by an older build or a
// hand-run UPDATE must not be served under a type a browser would run.
func (p *Plane) newAssetLookup(ctx context.Context, r *http.Request, ws *workspaces.Workspace) assetLookup {
	return func(name string) (assets.Meta, []byte, bool) {
		if p.assets == nil {
			markAssetMissing(r)
			return assets.Meta{}, nil, false
		}
		// Meta first, the BLOB only once the stored type has passed the
		// gate — a refused row costs the same one small read the route
		// pays for a 304, never the bytes it will not serve.
		meta, err := p.assets.Meta(ctx, ws.ID, name)
		switch {
		case errors.Is(err, assets.ErrNotFound):
			markAssetMissing(r)
			return assets.Meta{}, nil, false
		case err != nil:
			p.log.Error("read asset for bodyRef", "workspace", ws.Slug, "asset", name, "err", err)
			markAssetMissing(r)
			return assets.Meta{}, nil, false
		case httpx.BrowserExecutableMediaType(meta.MediaType):
			p.log.Warn("asset carries a browser-executable media type; refusing to serve it",
				"workspace", ws.Slug, "asset", name, "mediaType", meta.MediaType)
			markAssetMissing(r)
			return assets.Meta{}, nil, false
		}
		meta, data, err := p.assets.Get(ctx, ws.ID, name)
		if err != nil {
			// Deleted between the two reads, or a store error: missing.
			if !errors.Is(err, assets.ErrNotFound) {
				p.log.Error("read asset bytes for bodyRef", "workspace", ws.Slug, "asset", name, "err", err)
			}
			markAssetMissing(r)
			return assets.Meta{}, nil, false
		}
		return meta, data, true
	}
}

// assetBase is the absolute prefix an asset_url recipe writes a name after
// (A6 D7): httpx.WorkspaceURL — the ONE construction of a workspace's
// public URL, shared with the admin API's own workspace record — plus the
// reserved prefix and "/assets/". Computed per request from the two
// guarded reads that function makes; Preview computes the same string from
// the ADMIN request it is serving, through the same function.
func (p *Plane) assetBase(r *http.Request, ws *workspaces.Workspace) string {
	return httpx.WorkspaceURL(r, p.cfg, ws.Slug) + p.cfg.ReservedPrefix + "/assets/"
}

// serveAsset answers GET|HEAD {prefix}/assets/{name} — the third control
// route beside /health and /state and the first that serves a body of any
// size (DESIGN §32.3). Reached at dispatch step 4: CORS was set at step 2,
// preflight answered at step 3, and the session layer (step 5) never runs
// for it — a forced status, a delay or a pause do not apply to a picture,
// because a control route is not a mock; nor is it recorded in traffic.
//
// Meta first, bytes only on a miss: a frontend loads the same avatar sixty
// times a screen, and a matching If-None-Match answers 304 without moving
// the BLOB through the reader pool. The ETag is the sha256, strong (the
// bytes are the identity), and there is NO Cache-Control — §32.3's "no
// cache header beyond the ETag": with neither Cache-Control nor
// Last-Modified a browser has no heuristic freshness to apply, so every
// load revalidates against the tag, and a re-upload under the same name is
// visible on the next request. nosniff denies the browser a third opinion
// on the type (§32.6).
func (p *Plane) serveAsset(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, name string) {
	if p.assets == nil || !assets.ValidName(name) {
		httpx.Err(w, http.StatusNotFound, "asset_not_found", "no such asset")
		return
	}
	meta, err := p.assets.Meta(r.Context(), ws.ID, name)
	switch {
	case errors.Is(err, assets.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, "asset_not_found", "no such asset")
		return
	case err != nil:
		p.log.Error("read asset", "workspace", ws.Slug, "asset", name, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	case httpx.BrowserExecutableMediaType(meta.MediaType):
		// The second gate (§32.6): the upload refused this type, and a row
		// that carries it anyway is not served — same 404 as absent, so
		// nothing leaks about why.
		p.log.Warn("asset carries a browser-executable media type; refusing to serve it",
			"workspace", ws.Slug, "asset", name, "mediaType", meta.MediaType)
		httpx.Err(w, http.StatusNotFound, "asset_not_found", "no such asset")
		return
	}

	etag := `"` + meta.SHA256 + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ifNoneMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	_, data, err := p.assets.Get(r.Context(), ws.ID, name)
	if err != nil {
		// Deleted between Meta and Get, or a store error: absent is the
		// honest answer for the first and the logged one for the second.
		if !errors.Is(err, assets.ErrNotFound) {
			p.log.Error("read asset bytes", "workspace", ws.Slug, "asset", name, "err", err)
		}
		w.Header().Del("ETag")
		httpx.Err(w, http.StatusNotFound, "asset_not_found", "no such asset")
		return
	}
	w.Header().Set("Content-Type", meta.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	// G705: the bytes are an operator's own upload, refused at upload AND
	// just above when their type is one a browser would execute.
	if _, werr := w.Write(data); werr != nil { //nolint:gosec
		p.log.Debug("write asset", "workspace", ws.Slug, "asset", name, "err", werr)
	}
}

// ifNoneMatch reports whether an If-None-Match header names etag: a
// comma-separated list of tags, each possibly weak (W/"…"), or "*" — the
// weak comparison RFC 9110 §13.1.2 prescribes for If-None-Match, which is
// what a browser or a cache sends back. An exact-string compare would
// answer a full 200 to a list or a weak tag and cost the BLOB read for
// nothing.
func ifNoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimSpace(tag)
		tag = strings.TrimPrefix(tag, "W/")
		if tag == etag {
			return true
		}
	}
	return false
}

// markAssetMissing records that a bodyRef named an asset this request
// could not serve (A6 D10): the response went out with the variant's
// status and an empty body, and the traffic row is the one place an
// operator sees why the picture is blank. A no-op when no TrafficSink is
// wired, like every mark.
func markAssetMissing(r *http.Request) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	tm.assetMissing = true
}
