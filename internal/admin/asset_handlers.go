package admin

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/workspaces"
)

// A6 (DESIGN §32.5, decisions.md mocker-a6-assets D3): the three asset
// routes under /api/workspaces/{id}/assets — the file goes in as the RAW
// body of a PUT (never multipart: §8's "multipart we do not touch" stands,
// and `curl -T photo.jpg -H 'Content-Type: image/jpeg'` is the whole
// client), the list answers metadata only, and the delete is guarded by
// confirmSlug like every verb that destroys workspace data no checkpoint
// restores. Agent-primary: each ships with its MCP tool and no screen.

const (
	codeAssetTooLarge = "asset_too_large"
	codeAssetsQuota   = "assets_quota"
	codeAssetNotFound = "asset_not_found"
)

// assetView is one asset on the wire. url is the mock-plane URL a frontend
// will fetch, built through the ONE constructor of a workspace's public
// address (httpx.WorkspaceURL) so an agent reads back exactly the string
// an asset_url recipe would write.
type assetView struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	URL       string `json:"url"`
}

// assetListView carries the ceilings beside the usage — the shape P6c's
// {open, cap} set — so an agent that reads the list knows how much room a
// workspace has left without a second call.
type assetListView struct {
	Assets        []assetView `json:"assets"`
	TotalBytes    int64       `json:"totalBytes"`
	MaxAssetBytes int64       `json:"maxAssetBytes"`
	MaxTotalBytes int64       `json:"maxTotalBytes"`
}

func (s *Server) newAssetView(r *http.Request, ws *workspaces.Workspace, m assets.Meta) assetView {
	return assetView{
		Name: m.Name, MediaType: m.MediaType, SizeBytes: m.SizeBytes, SHA256: m.SHA256,
		CreatedAt: m.CreatedAt.Unix(), UpdatedAt: m.UpdatedAt.Unix(),
		URL: httpx.WorkspaceURL(r, s.cfg, ws.Slug) + s.cfg.ReservedPrefix + "/assets/" + m.Name,
	}
}

// handlePutAsset answers PUT /api/workspaces/{id}/assets/{name}: the body
// is the file, Content-Type is its media type. 201 on create, 200 on
// replace.
//
// Two size gates before a byte is stored, both answering 413 by name: a
// Content-Length over MOCKER_MAX_ASSET is refused before the body is read
// at all, and the read itself runs under its own http.MaxBytesReader at
// the cap plus one — never a bare io.ReadAll under the dispatcher's 10 MB
// MaxBody, which would buffer ten megabytes for a four-kilobyte cap
// (round-1 #11). The media type is refused unparseable, empty or
// browser-executable HERE as well as at the CSRF chain (rawBodyRoute): two
// gates, like every media type in this tree, so the MCP loopback — which
// bypasses the chain by construction — meets the same refusal.
func (s *Server) handlePutAsset(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !assets.ValidName(name) {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"asset name must be one path segment of [A-Za-z0-9._-], at most 128 characters")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		httpx.Err(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType,
			"Content-Type must name the file's media type")
		return
	}
	if dangerousMediaType(mediaType) {
		httpx.Err(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType,
			"a media type a browser would execute is not stored")
		return
	}
	if r.ContentLength > s.cfg.MaxAsset {
		httpx.Err(w, http.StatusRequestEntityTooLarge, codeAssetTooLarge,
			fmt.Sprintf("asset is %d bytes, over MOCKER_MAX_ASSET (%d)", r.ContentLength, s.cfg.MaxAsset))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxAsset+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Err(w, http.StatusRequestEntityTooLarge, codeAssetTooLarge,
				fmt.Sprintf("asset exceeds MOCKER_MAX_ASSET (%d)", s.cfg.MaxAsset))
			return
		}
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "could not read the request body")
		return
	}
	if int64(len(data)) > s.cfg.MaxAsset {
		httpx.Err(w, http.StatusRequestEntityTooLarge, codeAssetTooLarge,
			fmt.Sprintf("asset is %d bytes, over MOCKER_MAX_ASSET (%d)", len(data), s.cfg.MaxAsset))
		return
	}

	meta, created, err := s.assetsRepo.Put(r.Context(), ws.ID, name, mediaType, data)
	switch {
	case err == nil:
	case errors.Is(err, assets.ErrTooLarge):
		httpx.Err(w, http.StatusRequestEntityTooLarge, codeAssetTooLarge, err.Error())
		return
	case errors.Is(err, assets.ErrQuota):
		httpx.Err(w, http.StatusRequestEntityTooLarge, codeAssetsQuota,
			fmt.Sprintf("the workspace's assets would exceed MOCKER_MAX_ASSETS_TOTAL (%d)", s.assetsRepo.MaxTotalBytes))
		return
	case errors.Is(err, assets.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
		return
	case errors.Is(err, assets.ErrInvalidName):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	default:
		s.log.Error("put asset", "workspace", ws.ID, "asset", name, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, s.newAssetView(r, ws, meta))
}

// handleListAssets answers GET /api/workspaces/{id}/assets.
func (s *Server) handleListAssets(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	list, err := s.assetsRepo.List(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list assets", "workspace", ws.ID, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}
	total, err := s.assetsRepo.TotalBytes(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("sum assets", "workspace", ws.ID, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}
	view := assetListView{
		Assets:        make([]assetView, 0, len(list)),
		TotalBytes:    total,
		MaxAssetBytes: s.assetsRepo.MaxAssetBytes,
		MaxTotalBytes: s.assetsRepo.MaxTotalBytes,
	}
	for _, m := range list {
		view.Assets = append(view.Assets, s.newAssetView(r, ws, m))
	}
	httpx.JSON(w, http.StatusOK, view)
}

// deleteAssetRequest is DELETE's JSON body: the slug guard reset-data and a
// decline already use, required by the server and checked inside the
// delete's own transaction.
type deleteAssetRequest struct {
	ConfirmSlug string `json:"confirmSlug"`
}

// handleDeleteAsset answers DELETE /api/workspaces/{id}/assets/{name}: 204,
// or 404 for a name that is not there (confirmSlug was typed for
// something), 409 for a wrong slug. Nothing that references the asset is
// touched or refused (§32.5): a bodyRef or an asset_url naming it starts
// answering asset_missing / a 404 URL, and the repair is a re-upload under
// the same name.
func (s *Server) handleDeleteAsset(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var body deleteAssetRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if body.ConfirmSlug == "" {
		httpx.Err(w, http.StatusBadRequest, codeResourceConfirmSlugRequired,
			"confirmSlug is required: pass the workspace's slug exactly")
		return
	}
	switch err := s.assetsRepo.Delete(r.Context(), ws.ID, name, body.ConfirmSlug); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, assets.ErrConfirmSlug):
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugMismatch,
			"confirmSlug does not name this workspace; nothing was deleted")
	case errors.Is(err, assets.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, codeAssetNotFound, "no such asset: "+strconv.Quote(name))
	case errors.Is(err, assets.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	default:
		s.log.Error("delete asset", "workspace", ws.ID, "asset", name, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
	}
}
