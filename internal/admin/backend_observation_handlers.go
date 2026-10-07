package admin

import (
	"context"
	"net/http"
	"strconv"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	ob "github.com/yashok111/mocker/internal/backendobservations"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) SetBackendObservations(service *ob.Service) { s.backendObservations = service }
func (s *Server) observationRequest(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	if _, ok := s.requireUser(w, r); !ok {
		return nil, false
	}
	if s.backendObservations == nil {
		s.backendError(w, &bm.FaultError{Status: 503, Code: "backend_observation_unavailable", Message: "Observations unavailable"})
		return nil, false
	}
	if !bm.ValidID(r.PathValue("id")) || r.PathValue("sid") != "" && !bm.ValidID(r.PathValue("sid")) {
		s.backendError(w, backendQueryError())
		return nil, false
	}
	if _, e := s.backendRepo.Get(r.Context(), r.PathValue("id")); e != nil {
		s.backendError(w, e)
		return nil, false
	}
	q := r.URL.Query()
	for key, values := range q {
		if len(values) != 1 || r.Method != "GET" || (key != "limit" && key != "cursor") {
			s.backendError(w, backendQueryError())
			return nil, false
		}
	}
	if r.Method == "GET" && (r.ContentLength != 0 || r.TransferEncoding != nil) {
		s.backendError(w, backendQueryError())
		return nil, false
	}
	ctx := r.Context()
	if s.backendArtifacts != nil {
		ctx = s.backendArtifacts.DiagramContext(ctx)
	}
	return ctx, true
}
func observationPage(r *http.Request) (int, string, error) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 1 || v > 500 {
			return 0, "", backendQueryError()
		}
		limit = v
	}
	return limit, r.URL.Query().Get("cursor"), nil
}
func (s *Server) handleImportBackendObservations(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	var in ob.ImportInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(ob.BatchBytes))) {
		return
	}
	out, e := s.backendObservations.Repo.Import(ctx, r.PathValue("id"), in)
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleListBackendObservations(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	limit, c, e := observationPage(r)
	if e != nil {
		s.backendError(w, e)
		return
	}
	out, e := s.backendObservations.Repo.List(ctx, r.PathValue("id"), limit, c)
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendObservationVersion(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		s.backendError(w, backendQueryError())
		return
	}
	v, e := backendPositiveDecimal(r.PathValue("v"))
	if e != nil {
		s.backendError(w, e)
		return
	}
	out, e := s.backendObservations.Repo.Version(ctx, r.PathValue("id"), r.PathValue("sid"), v)
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendObservationRecords(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	v, e := backendPositiveDecimal(r.PathValue("v"))
	if e != nil {
		s.backendError(w, e)
		return
	}
	limit, c, e := observationPage(r)
	if e != nil {
		s.backendError(w, e)
		return
	}
	out, e := s.backendObservations.Repo.Records(ctx, r.PathValue("id"), r.PathValue("sid"), v, limit, c)
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleAdaptBackendObservations(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	var in ob.AdaptInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(32<<20))) {
		return
	}
	out, e := ob.Adapt(ctx, r.PathValue("id"), in, s.backendObservations.Replay)
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCorrelateBackendObservations(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	var in ob.CorrelateInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(ob.BatchBytes))) {
		return
	}
	out, e := s.backendObservations.Correlate(ctx, r.PathValue("id"), r.PathValue("sid"), in)
	if e == nil {
		out, e = s.backendObservations.Repo.CorrelationPage(ctx, r.PathValue("id"), r.PathValue("sid"), out.Version, 100, "")
	}
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendObservationCorrelation(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.observationRequest(w, r)
	if !ok {
		return
	}
	v, e := backendPositiveDecimal(r.PathValue("v"))
	if e != nil {
		s.backendError(w, e)
		return
	}
	limit, c, e := observationPage(r)
	if e != nil {
		s.backendError(w, e)
		return
	}
	out, e := s.backendObservations.Repo.CorrelationPage(ctx, r.PathValue("id"), r.PathValue("sid"), v, limit, c)
	if e != nil {
		s.backendError(w, e)
		return
	}
	httpx.JSON(w, 200, out)
}
