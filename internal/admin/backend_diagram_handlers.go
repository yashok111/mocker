package admin

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func diagramAdmissionError() error {
	return &backendmodel.FaultError{Status: 422, Code: "backend_invalid", Message: "Request must match the closed diagram schema"}
}
func (s *Server) diagramError(w http.ResponseWriter, err error) {
	if f, ok := errors.AsType[*backendmodel.FaultError](err); ok && f.Status == 400 {
		fault := *f
		fault.Status = 422
		s.backendError(w, &fault)
		return
	}
	s.backendError(w, err)
}
func (s *Server) diagramContext(w http.ResponseWriter, ctx context.Context, r *http.Request) (context.Context, bool) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return nil, false
	}
	ctx = backendmodel.WithDiagramActor(ctx, user.Name)
	if s.backendArtifacts != nil {
		ctx = s.backendArtifacts.DiagramContext(ctx)
	}
	return ctx, true
}
func (s *Server) diagramBody(w http.ResponseWriter, r *http.Request, out any, limit int64) bool {
	if r.URL.RawQuery != "" {
		s.diagramError(w, diagramAdmissionError())
		return false
	}
	var raw jsontext.Value
	// Review 2026-10-06, F171: this stage wrote its own 400 and bypassed
	// diagramError, so `[]` or unparsable text answered 400 while a
	// schema-invalid object on the same route answers 422.
	if err := backendReadBody(w, r, &raw, min(s.cfg.MaxBody, limit)); err != nil {
		s.diagramError(w, err)
		return false
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		// Scope admission is a domain refusal, not malformed JSON. Keep its
		// public code while other wire failures retain the closed-schema error.
		if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok && fault.Code == "backend_unsupported_scope" {
			s.diagramError(w, fault)
		} else {
			s.diagramError(w, diagramAdmissionError())
		}
		return false
	}
	return true
}
func diagramQuery(r *http.Request, mode string) (backendmodel.DiagramListInput, string, int64, error) {
	var in backendmodel.DiagramListInput
	if r.ContentLength != 0 || r.TransferEncoding != nil {
		return in, "", 0, diagramAdmissionError()
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return in, "", 0, diagramAdmissionError()
	}
	if err = diagramQueryFields(q, mode, &in); err != nil {
		return in, "", 0, err
	}
	if mode == "diagram" && !backendSHA256(q.Get("hash")) {
		return in, "", 0, diagramAdmissionError()
	}
	var version int64
	if mode != "list" {
		raw := r.PathValue("v")
		version, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || version <= 0 || strconv.FormatInt(version, 10) != raw {
			return in, "", 0, diagramAdmissionError()
		}
	}
	return in, q.Get("hash"), version, nil
}

func (s *Server) handleCreateBackendDiagram(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramCreateInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.CreateDiagram(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleSaveBackendDiagram(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramSaveInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.SaveDiagram(ctx, pid, r.PathValue("did"), in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleForkBackendDiagram(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramForkInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.ForkDiagram(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleListBackendDiagrams(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	in, hash, version, err := diagramQuery(r, "list")
	if err != nil {
		s.diagramError(w, err)
		return
	}
	_ = in
	_ = hash
	_ = version
	out, err := s.backendRepo.ListDiagrams(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendDiagram(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	in, hash, version, err := diagramQuery(r, "diagram")
	if err != nil {
		s.diagramError(w, err)
		return
	}
	_ = in
	_ = hash
	_ = version
	out, err := s.backendRepo.GetDiagram(ctx, pid, backendmodel.DiagramPin{ID: r.PathValue("did"), Version: version, ContentHash: hash})
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleQueryBackendDiagram(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramQueryInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.QueryDiagram(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleCompareBackendDiagrams(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramCompareInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.CompareDiagrams(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleCreateBackendDiagramView(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramCreateViewInput
	if !s.diagramBody(w, r, &in, 128<<10) {
		return
	}
	out, err := s.backendRepo.CreateDiagramView(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleSaveBackendDiagramView(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	var in backendmodel.DiagramSaveViewInput
	if !s.diagramBody(w, r, &in, 128<<10) {
		return
	}
	out, err := s.backendRepo.SaveDiagramView(ctx, pid, r.PathValue("vid"), in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleListBackendDiagramViews(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	in, hash, version, err := diagramQuery(r, "list")
	if err != nil {
		s.diagramError(w, err)
		return
	}
	_ = in
	_ = hash
	_ = version
	out, err := s.backendRepo.ListDiagramViews(ctx, pid, in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendDiagramView(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	pid := r.PathValue("id")
	in, hash, version, err := diagramQuery(r, "view")
	if err != nil {
		s.diagramError(w, err)
		return
	}
	_ = in
	_ = hash
	_ = version
	out, err := s.backendRepo.GetDiagramView(ctx, pid, r.PathValue("vid"), version)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func diagramQueryFields(q url.Values, mode string, in *backendmodel.DiagramListInput) error {
	for key, values := range q {
		if len(values) != 1 {
			return diagramAdmissionError()
		}
		if err := diagramQueryField(key, values[0], mode, in); err != nil {
			return err
		}
	}
	return nil
}
func diagramQueryField(key, value, mode string, in *backendmodel.DiagramListInput) error {
	if key == "hash" {
		if mode != "diagram" || !backendSHA256(value) {
			return diagramAdmissionError()
		}
		return nil
	}
	if mode != "list" {
		return diagramAdmissionError()
	}
	switch key {
	case "subjectId":
		if !backendmodel.ValidID(value) {
			return diagramAdmissionError()
		}
		in.SubjectID = value
	case "targetHash":
		if !backendSHA256(value) {
			return diagramAdmissionError()
		}
		in.TargetHash = value
	case "order":
		if !slices.Contains([]string{"asc", "desc"}, value) {
			return diagramAdmissionError()
		}
		in.Order = value
	case "kind":
		if !slices.Contains([]string{"architecture", "interactions", "lifecycle", "business_map"}, value) {
			return diagramAdmissionError()
		}
		in.Kind = value
	case "limit":
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			return diagramAdmissionError()
		}
		in.Limit = limit
	case "cursor":
		if len(value) > 4096 {
			return diagramAdmissionError()
		}
		in.Cursor = value
	default:
		return diagramAdmissionError()
	}
	return nil
}

func (s *Server) handleBuildBackendInteractions(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	var in backendmodel.DiagramInteractionBuildInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.BuildInteractions(ctx, r.PathValue("id"), in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handlePreviewBackendArchitecture(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	var in backendmodel.ArchitecturePreviewInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.PreviewArchitecture(ctx, r.PathValue("id"), in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleBuildBackendLifecycle(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	var in backendmodel.DiagramLifecycleBuildInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendRepo.BuildLifecycle(ctx, r.PathValue("id"), in)
	if err != nil {
		s.diagramError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
