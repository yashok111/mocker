package admin

import (
	"net/http"
	"net/url"
	"slices"

	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func backendAnalysisQuery(r *http.Request, mode string) (url.Values, error) {
	if r.ContentLength != 0 || r.TransferEncoding != nil {
		return nil, backendQueryError()
	}
	if !backendmodel.ValidID(r.PathValue("id")) || (mode != "list" && !backendmodel.ValidID(r.PathValue("aid"))) {
		return nil, backendQueryError()
	}
	allowed := []string{}
	if mode == "list" {
		allowed = []string{"status", "kind", "cursor", "limit", "order"}
	}
	if mode == "results" {
		allowed = []string{"resultVersion", "section", "cursor", "service", "kind", "certainty", "direction", "depth", "limit"}
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, backendQueryError()
	}
	for k, v := range q {
		if len(v) != 1 || !slices.Contains(allowed, k) {
			return nil, backendQueryError()
		}
	}
	if !backendAnalysisNumbers(q) {
		return nil, backendQueryError()
	}
	if len(q.Get("cursor")) > 1024 {
		return nil, backendQueryError()
	}
	if !backendAnalysisSelectionQuery(q, mode) {
		return nil, backendQueryError()
	}
	return q, nil
}
func backendAnalysisNumbers(q url.Values) bool {
	for _, key := range []string{"resultVersion", "depth", "limit"} {
		if q.Has(key) {
			n, err := backendPositiveDecimal(q.Get(key))
			if key == "depth" && q.Get(key) == "0" {
				n = 0
				err = nil
			}
			if err != nil || (key == "depth" && n > 32) || (key == "limit" && n > 500) {
				return false
			}
		}
	}
	return true
}
func backendAnalysisOptionalSelector(q url.Values, key string, values []string) bool {
	return !q.Has(key) || slices.Contains(values, q.Get(key))
}
func backendAnalysisSelectionQuery(q url.Values, mode string) bool {
	switch mode {
	case "list":
		return backendAnalysisOptionalSelector(q, "order", []string{"asc", "desc"}) && backendAnalysisOptionalSelector(q, "status", []string{"queued", "running", "completed", "failed", "cancelled", "interrupted"}) && backendAnalysisOptionalSelector(q, "kind", backendanalysis.Kinds())
	case "results":
		return q.Has("resultVersion") && slices.Contains([]string{"changes", "findings", "witnesses", "checks", "gaps"}, q.Get("section")) && backendAnalysisOptionalSelector(q, "certainty", []string{"confirmed", "possible", "unknown"}) && backendAnalysisOptionalSelector(q, "direction", []string{"upstream", "downstream", "both"})
	default:
		return true
	}
}

// backendAnalysisResultQuery maps an admitted results query. DepthSet keeps
// an explicit depth=0 (review 2026-10-06, F25): analysisQueryNumber returns
// 0 for "0" and for an absent depth alike, and the filter read both as unset.
func backendAnalysisResultQuery(q url.Values) backendanalysis.ResultQuery {
	return backendanalysis.ResultQuery{ResultVersion: analysisQueryNumber(q, "resultVersion"), Section: q.Get("section"), Cursor: q.Get("cursor"), Service: q.Get("service"), Kind: q.Get("kind"), Certainty: q.Get("certainty"), Direction: q.Get("direction"), Depth: int(analysisQueryNumber(q, "depth")), DepthSet: q.Has("depth"), Limit: int(analysisQueryNumber(q, "limit"))}
}
func analysisQueryNumber(q url.Values, k string) int64 {
	if !q.Has(k) {
		return 0
	}
	n, _ := backendPositiveDecimal(q.Get(k))
	return n
}
func (s *Server) backendAnalysisBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if !backendmodel.ValidID(r.PathValue("id")) || (r.PathValue("aid") != "" && !backendmodel.ValidID(r.PathValue("aid"))) {
		s.backendError(w, backendQueryError())
		return false
	}
	return s.backendNoQuery(w, r) && s.backendProjectionBody(w, r, out, min(s.cfg.MaxBody, int64(2<<20)))
}
func (s *Server) handleStartBackendAnalysis(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendanalysis.StartInput
	if !s.backendAnalysisBody(w, r, &in) {
		return
	}
	ctx := r.Context()
	if s.backendArtifacts != nil {
		ctx = s.backendArtifacts.DiagramContext(ctx)
	}
	out, err := s.backendAnalysis.Start(ctx, r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 202, out)
}
func (s *Server) handleListBackendAnalysis(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, err := backendAnalysisQuery(r, "list")
	if err != nil {
		s.backendError(w, err)
		return
	}
	// Review 2026-10-06, F13: List reads backend_analysis_jobs alone, so an
	// unknown project was a 200 with an empty page; every sibling read
	// (replay, observations, a job by id) answers 404 first.
	if _, err = s.backendRepo.Get(r.Context(), r.PathValue("id")); err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendAnalysisRepo.List(r.Context(), r.PathValue("id"), backendanalysis.ListQuery{Order: q.Get("order"), Status: q.Get("status"), Kind: q.Get("kind"), Cursor: q.Get("cursor"), Limit: int(analysisQueryNumber(q, "limit"))})
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendAnalysis(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if _, err := backendAnalysisQuery(r, "detail"); err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendAnalysisRepo.Detail(r.Context(), r.PathValue("id"), r.PathValue("aid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCancelBackendAnalysis(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendanalysis.CancelInput
	if !s.backendAnalysisBody(w, r, &in) {
		return
	}
	out, err := s.backendAnalysis.Cancel(r.Context(), r.PathValue("id"), r.PathValue("aid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleRetryBackendAnalysis(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendanalysis.RetryInput
	if !s.backendAnalysisBody(w, r, &in) {
		return
	}
	out, err := s.backendAnalysis.Retry(r.Context(), r.PathValue("id"), r.PathValue("aid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 202, out)
}
func (s *Server) handleGetBackendAnalysisResults(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, err := backendAnalysisQuery(r, "results")
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendAnalysisRepo.Results(r.Context(), r.PathValue("id"), r.PathValue("aid"), backendAnalysisResultQuery(q))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
