package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
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
		if len(values) != 1 || !savedViewQueryParam(key, values[0], detail, &list, &get) {
			return list, get, backendQueryError()
		}
	}
	return list, get, nil
}

// savedViewQueryParam applies one query member to the list or detail input
// and reports whether it is admitted: version belongs to a detail read only,
// kind/limit/cursor to a list only, and every value is checked here.
func savedViewQueryParam(key, value string, detail bool, list *backendmodel.SavedViewListInput, get *backendmodel.GetSavedViewInput) bool {
	var err error
	switch key {
	case "version":
		if !detail {
			return false
		}
		get.Version, err = strconv.ParseInt(value, 10, 64)
		return err == nil && get.Version > 0
	case "kind":
		if detail || value != "flow" && value != "database" {
			return false
		}
		list.Kind = value
		return true
	case "limit":
		if detail {
			return false
		}
		list.Limit, err = strconv.Atoi(value)
		return err == nil && list.Limit >= 1 && list.Limit <= 100
	case "cursor":
		if detail {
			return false
		}
		list.Cursor = value
		return true
	}
	return false
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
