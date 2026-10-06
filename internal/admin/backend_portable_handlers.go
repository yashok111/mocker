package admin

import (
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/backendportable"
)

// Registration is owned by the B5.2 integrator; the handler only reads exact pins.
func (s *Server) handleExportBackendViewSVG(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		s.diagramError(w, diagramAdmissionError())
		return
	}
	_, _, version, err := diagramQuery(r, "view")
	if err != nil {
		s.diagramError(w, err)
		return
	}
	artifact, err := backendportable.ExportDiagramSVG(ctx, s.backendRepo, r.PathValue("id"), backendportable.SVGInput{ViewID: r.PathValue("vid"), ViewVersion: version})
	if err != nil {
		s.diagramError(w, err)
		return
	}
	writeBackendSVG(w, artifact)
}
func writeBackendSVG(w http.ResponseWriter, a *backendportable.SVGArtifact) {
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Body)
}

func (s *Server) handleExportBackendProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.ExportInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	if in.Selection.ProjectID != r.PathValue("id") {
		s.diagramError(w, diagramAdmissionError())
		return
	}
	out, err := s.backendPortable.Export(r.Context(), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleBeginBackendPortableImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.BeginInput
	if !s.diagramBody(w, r, &in, 4<<20) {
		return
	}
	out, err := s.backendPortable.Begin(r.Context(), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handlePutBackendPortableChunk(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.PutInput
	if !s.diagramBody(w, r, &in, (2<<20)+(16<<10)) {
		return
	}
	out, err := s.backendPortable.Put(r.Context(), r.PathValue("sid"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handlePreviewBackendPortableImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.PreviewInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendPortable.Preview(r.Context(), r.PathValue("sid"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCommitBackendPortableImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.CommitInput
	if !s.diagramBody(w, r, &in, 16<<10) {
		return
	}
	out, err := s.backendPortable.Commit(r.Context(), r.PathValue("sid"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleAbortBackendPortableImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.SessionInput
	if !s.diagramBody(w, r, &in, 16<<10) {
		return
	}
	out, err := s.backendPortable.Abort(r.Context(), r.PathValue("sid"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendExportChunk(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["manifestHash"]) != 1 || r.ContentLength != 0 || r.TransferEncoding != nil {
		s.diagramError(w, diagramAdmissionError())
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || strconv.Itoa(index) != r.PathValue("index") || !backendmodel.ValidID(r.PathValue("sid")) {
		s.diagramError(w, diagramAdmissionError())
		return
	}
	body, err := s.backendPortable.ExportChunk(r.Context(), r.PathValue("sid"), query.Get("manifestHash"), index)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, struct {
		Index        int    `json:"index"`
		ManifestHash string `json:"manifestHash"`
		Body         string `json:"body"`
	}{index, query.Get("manifestHash"), string(body)})
}

func (s *Server) handleQueryBackendNamespacedArtifact(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.NamespacedArtifactQueryInput
	if !s.diagramBody(w, r, &in, 16<<10) {
		return
	}
	out, err := s.backendArtifacts.QueryNamespaced(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleResolveBackendPortableSelection(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendportable.SelectionInput
	if !s.diagramBody(w, r, &in, 1<<20) {
		return
	}
	out, err := s.backendPortable.ResolveSelection(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
