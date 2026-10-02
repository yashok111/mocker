package admin

import (
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) backendAPIArtifactBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if !s.backendNoQuery(w, r) {
		return false
	}
	limit := min(s.cfg.MaxBody, int64(backendmodel.MaxAPIPinBodyBytes))
	if r.ContentLength > limit {
		s.backendError(w, &backendmodel.FaultError{Status: 413, Code: "backend_too_large", Message: "Request exceeds API artifact body limit"})
		return false
	}
	return s.backendBodyLimit(w, r, out, limit)
}

func (s *Server) handleQueryBackendAPIArtifacts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.APIArtifactQueryInput
	if !s.backendAPIArtifactBody(w, r, &in) {
		return
	}
	out, err := s.backendAPIArtifacts.Query(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handlePreviewBackendAPIPins(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.PreviewAPIPinsInput
	if !s.backendAPIArtifactBody(w, r, &in) {
		return
	}
	out, err := s.backendAPIArtifacts.Preview(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleApplyBackendAPIPins(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.ApplyAPIPinsInput
	if !s.backendAPIArtifactBody(w, r, &in) {
		return
	}
	out, err := s.backendAPIArtifacts.Apply(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	// The durable receipt is the response, including on replay before any CAS
	// or owner lookup. Re-encoding it would lose the original wire bytes.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.ReceiptBytes())
}

func (s *Server) handleGetAPIArtifactSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	if r.ContentLength != 0 || r.TransferEncoding != nil {
		s.backendError(w, backendQueryError())
		return
	}
	for _, key := range []string{"id", "rid"} {
		if !backendmodel.ValidAPIArtifactID(r.PathValue(key)) {
			s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Artifact IDs must be canonical positive decimal int64 strings"})
			return
		}
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rid, _ := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	out, err := s.designsRepo.ArtifactSnapshot(r.Context(), id, rid)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, struct {
		ArtifactID  string `json:"artifactId"`
		RevisionID  string `json:"revisionId"`
		ContentHash string `json:"contentHash"`
		Name        string `json:"name"`
		Document    string `json:"document"`
	}{r.PathValue("id"), r.PathValue("rid"), out.ContentHash, out.DesignName, out.Document})
}
