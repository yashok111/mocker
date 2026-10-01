package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
	"net/http"
	"net/url"
	"strconv"
)

func (s *Server) backendSavedViewBody(w http.ResponseWriter, r *http.Request, out any) bool {
	var raw jsontext.Value
	if !s.backendNoQuery(w, r) || !s.backendBodyLimit(w, r, &raw, min(s.cfg.MaxBody, int64(backendmodel.MaxSavedViewBodyBytes))) {
		return false
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok {
			s.backendError(w, fault)
		} else {
			s.backendError(w, backendmodelInvalidSavedBody())
		}
		return false
	}
	return true
}
func backendmodelInvalidSavedBody() *backendmodel.FaultError {
	return &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Request must match the saved view schema"}
}

func backendSavedViewQuery(r *http.Request, detail bool) (backendmodel.SavedViewListInput, backendmodel.GetSavedViewInput, error) {
	var list backendmodel.SavedViewListInput
	var get backendmodel.GetSavedViewInput
	if r.ContentLength != 0 || r.TransferEncoding != nil {
		return list, get, backendQueryError()
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return list, get, backendQueryError()
	}
	for key, values := range q {
		if len(values) != 1 {
			return list, get, backendQueryError()
		}
		switch key {
		case "version":
			if !detail {
				return list, get, backendQueryError()
			}
			get.Version, err = strconv.ParseInt(values[0], 10, 64)
			if err != nil || get.Version <= 0 {
				return list, get, backendQueryError()
			}
		case "kind":
			if detail || values[0] != "flow" && values[0] != "database" {
				return list, get, backendQueryError()
			}
			list.Kind = values[0]
		case "limit":
			if detail {
				return list, get, backendQueryError()
			}
			list.Limit, err = strconv.Atoi(values[0])
			if err != nil || list.Limit < 1 || list.Limit > 100 {
				return list, get, backendQueryError()
			}
		case "cursor":
			if detail {
				return list, get, backendQueryError()
			}
			list.Cursor = values[0]
		default:
			return list, get, backendQueryError()
		}
	}
	return list, get, nil
}
func (s *Server) handleListBackendSavedViews(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, _, err := backendSavedViewQuery(r, false)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.ListSavedViews(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendSavedView(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	_, in, err := backendSavedViewQuery(r, true)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.GetSavedView(r.Context(), r.PathValue("id"), r.PathValue("vid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCreateBackendSavedView(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.CreateSavedViewInput
	if !s.backendSavedViewBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.CreateSavedView(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleSaveBackendSavedView(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.SaveSavedViewInput
	if !s.backendSavedViewBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.SaveSavedView(r.Context(), r.PathValue("id"), r.PathValue("vid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
