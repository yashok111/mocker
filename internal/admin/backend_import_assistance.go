package admin

import (
	"net/http"
	"net/url"
	"slices"

	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/guide"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleValidateBackendImportBatch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.ImportBatchValidationInput
	if !s.backendImportBody(w, r, &in, min(s.cfg.MaxBody, int64(backendmodel.MaxImportBatchBytes))) {
		return
	}
	out, err := s.backendRepo.ValidateImportBatch(r.Context(), r.PathValue("id"), r.PathValue("iid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleQueryBackendCoverage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.CoverageQueryInput
	if !s.backendImportBody(w, r, &in, s.cfg.MaxBody) {
		return
	}
	out, err := s.backendRepo.QueryCoverage(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleBackendImportSchema(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		s.backendError(w, backendQueryError())
		return
	}
	for key, values := range q {
		if len(values) != 1 || !slices.Contains([]string{"profile", "recordType", "kind"}, key) {
			s.backendError(w, backendQueryError())
			return
		}
	}
	profile, recordType, kind := q.Get("profile"), q.Get("recordType"), q.Get("kind")
	if !slices.Contains([]string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}, profile) {
		s.backendError(w, backendQueryError())
		return
	}
	if recordType == "node" && !slices.Contains(backendmodel.SupportedNodeKindsForProfile(profile), kind) || recordType == "edge" && !slices.Contains(backendmodel.SupportedEdgeKindsForProfile(profile), kind) {
		s.backendError(w, backendQueryError())
		return
	}
	schema, err := api.BackendImportRecordSchema(profile == backendmodel.ComposedProfile, recordType, kind)
	if err != nil {
		s.backendError(w, backendQueryError())
		return
	}
	httpx.JSON(w, 200, map[string]any{"scope": "wire-record-v1", "profile": profile, "recordType": recordType, "kind": kind, "schema": schema, "guideSetId": guide.CurrentGuideSetID(), "semanticValidationTool": "validate_backend_import_batch", "examplesTopic": "backend-examples"})
}
