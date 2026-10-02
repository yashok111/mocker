package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/recordproxy"
	"github.com/yashok111/mocker/internal/workspaces"
)

func (s *Server) proxyWorkspace(w http.ResponseWriter, r *http.Request) (*workspaces.Workspace, bool) {
	if _, ok := s.requireUser(w, r); !ok {
		return nil, false
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return nil, false
	}
	return s.loadWorkspace(w, r, id)
}
func (s *Server) proxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, recordproxy.ErrConfirmation):
		httpx.Err(w, http.StatusBadRequest, "confirmation_required", err.Error())
	case errors.Is(err, recordproxy.ErrConflict):
		httpx.Err(w, http.StatusConflict, "proxy_conflict", err.Error())
	case errors.Is(err, recordproxy.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, "proxy_recording_not_found", err.Error())
	default:
		s.log.Error("proxy storage", "err", err)
		httpx.Err(w, http.StatusInternalServerError, "proxy_storage_failed", "Не удалось прочитать или сохранить данные прокси")
	}
}
func (s *Server) proxyView(w http.ResponseWriter, c recordproxy.Config) {
	origins := s.cfg.ProxyAllowlist
	if origins == nil {
		origins = []string{}
	}
	httpx.JSON(w, http.StatusOK, struct {
		Config         recordproxy.Config `json:"config"`
		AllowedOrigins []string           `json:"allowedOrigins"`
	}{c, origins})
}
func (s *Server) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.proxyWorkspace(w, r)
	if !ok {
		return
	}
	c, err := s.proxyRepo.Get(r.Context(), ws.ID)
	if err != nil {
		s.proxyError(w, err)
		return
	}
	s.proxyView(w, c)
}
func (s *Server) handlePutProxy(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.proxyWorkspace(w, r)
	if !ok {
		return
	}
	c := recordproxy.DefaultConfig()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !decodeBody(w, r, &c) {
		return
	}
	if err := c.Validate(s.cfg.ProxyAllowlist); err != nil {
		httpx.Err(w, http.StatusBadRequest, "proxy_config_invalid", err.Error())
		return
	}
	c, err := s.proxyRepo.Save(r.Context(), ws.ID, c)
	if err != nil {
		s.proxyError(w, err)
		return
	}
	s.proxyView(w, c)
}
func (s *Server) handleListProxyRecordings(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.proxyWorkspace(w, r)
	if !ok {
		return
	}
	items, err := s.proxyRepo.List(r.Context(), ws.ID)
	if err != nil {
		s.proxyError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, struct {
		Items []recordproxy.Recording `json:"items"`
	}{items})
}
func (s *Server) handleDeleteProxyRecording(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.proxyWorkspace(w, r)
	if !ok {
		return
	}
	rid, err := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if err != nil || rid <= 0 {
		httpx.Err(w, http.StatusBadRequest, "invalid_id", "Некорректный ID записи")
		return
	}
	var in struct {
		Version *int64 `json:"version"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Version == nil {
		httpx.Err(w, http.StatusBadRequest, "version_required", "Укажите version настроек прокси")
		return
	}
	if err := s.proxyRepo.Delete(r.Context(), ws.ID, rid, *in.Version); err != nil {
		s.proxyError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) handleClearProxyRecordings(w http.ResponseWriter, r *http.Request) {
	ws, ok := s.proxyWorkspace(w, r)
	if !ok {
		return
	}
	var in struct {
		Version     *int64 `json:"version"`
		ConfirmSlug string `json:"confirmSlug"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Version == nil || in.ConfirmSlug != ws.Slug {
		httpx.Err(w, http.StatusBadRequest, "confirmation_required", "Укажите version и confirmSlug воркспейса")
		return
	}
	if err := s.proxyRepo.Clear(r.Context(), ws.ID, *in.Version, in.ConfirmSlug); err != nil {
		s.proxyError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
