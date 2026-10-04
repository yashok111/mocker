package admin

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func backendAnnotationListInput(r *http.Request) (backendmodel.AnnotationListInput, error) {
	var in backendmodel.AnnotationListInput
	bad := &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Use single-valued annotation filters and limit 1–500"}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return in, bad
	}
	for key, values := range query {
		if len(values) != 1 {
			return in, bad
		}
		value := values[0]
		switch key {
		case "annotationId", "targetId", "revisionId":
			if !backendmodel.ValidID(value) {
				return in, bad
			}
		case "recordType":
			if value != "node" && value != "edge" {
				return in, bad
			}
		case "orphaned":
			if value != "true" && value != "false" {
				return in, bad
			}
			in.Orphaned = new(value == "true")
		case "limit":
			in.Limit, err = strconv.Atoi(value)
			if err != nil || in.Limit < 1 || in.Limit > backendmodel.MaxAnnotationPageSize {
				return in, bad
			}
		case "cursor":
			if len(value) > 2048 {
				return in, bad
			}
		default:
			return in, bad
		}
	}
	in.AnnotationID = query.Get("annotationId")
	in.TargetID = query.Get("targetId")
	in.RevisionID = query.Get("revisionId")
	in.RecordType = query.Get("recordType")
	in.Cursor = query.Get("cursor")
	return in, nil
}
func (s *Server) handleListBackendAnnotations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, err := backendAnnotationListInput(r)
	if err != nil {
		s.backendError(w, err)
		return
	}
	page, err := s.backendRepo.ListAnnotations(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}
