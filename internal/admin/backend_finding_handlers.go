package admin

import (
	"net/http"
	"net/url"
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleListBackendFindings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.ContentLength != 0 || r.TransferEncoding != nil || !backendmodel.ValidID(r.PathValue("id")) {
		s.diagramError(w, diagramAdmissionError())
		return
	}
	for k, v := range q {
		if len(v) != 1 || !slices.Contains([]string{"jobId", "resultVersion", "cursor", "limit"}, k) {
			s.diagramError(w, diagramAdmissionError())
			return
		}
	}
	version, err := backendPositiveDecimal(q.Get("resultVersion"))
	if err != nil {
		s.diagramError(w, diagramAdmissionError())
		return
	}
	limit := int64(100)
	if q.Has("limit") {
		limit, err = backendPositiveDecimal(q.Get("limit"))
		if err != nil || limit > 100 {
			s.diagramError(w, diagramAdmissionError())
			return
		}
	}
	out, err := s.backendRepo.ListBackendFindings(r.Context(), r.PathValue("id"), backendmodel.FindingAnalysisRef{JobID: q.Get("jobId"), ResultVersion: version}, q.Get("cursor"), int(limit))
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleReviewBackendFinding(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	var in backendmodel.FindingReviewInput
	if !s.diagramBody(w, r, &in, 16<<10) {
		return
	}
	out, err := s.backendRepo.ReviewFinding(ctx, r.PathValue("id"), r.PathValue("fingerprint"), in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
