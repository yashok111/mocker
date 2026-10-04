package admin

import (
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) backendArtifactBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if !s.backendNoQuery(w, r) {
		return false
	}
	limit := min(s.cfg.MaxBody, int64(backendmodel.MaxAPIPinBodyBytes))
	if r.ContentLength > limit {
		s.backendError(w, &backendmodel.FaultError{Status: 413, Code: "backend_too_large", Message: "Request exceeds artifact body limit"})
		return false
	}
	return s.backendProjectionBody(w, r, out, limit)
}

func (s *Server) handleQueryBackendArtifacts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.ArtifactQueryInput
	if !s.backendArtifactBody(w, r, &in) {
		return
	}
	out, err := s.backendArtifacts.Query(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handlePreviewBackendArtifactPins(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.PreviewArtifactPinsInput
	if !s.backendArtifactBody(w, r, &in) {
		return
	}
	out, err := s.backendArtifacts.Preview(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleApplyBackendArtifactPins(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.ApplyArtifactPinsInput
	if !s.backendArtifactBody(w, r, &in) {
		return
	}
	out, err := s.backendArtifacts.Apply(r.Context(), r.PathValue("id"), in)
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

// DesignScenarioArtifactSnapshot is the qualified exact-owner wire contract.
// Stored digests remain metadata until the complete owner envelope verifies.
type DesignScenarioArtifactSnapshot struct {
	ScenarioID           string `json:"scenarioId"`
	RevisionID           string `json:"revisionId"`
	Version              string `json:"version"`
	StoredContentHash    string `json:"storedContentHash"`
	ContentHash          string `json:"contentHash,omitempty"`
	DocumentHash         string `json:"documentHash"`
	DocumentJSON         string `json:"documentJSON"`
	FormDraftsJSON       string `json:"formDraftsJSON"`
	HashPolicy           string `json:"hashPolicy"`
	TypedStatus          string `json:"typedStatus"`
	EnvelopeVerification string `json:"envelopeVerification"`
}

func (s *Server) handleGetDesignScenarioArtifactSnapshot(w http.ResponseWriter, r *http.Request) {
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
	out, err := s.scenarioArtifactSnapshots.ArtifactInspectionSnapshot(r.Context(), id, rid)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	wire := DesignScenarioArtifactSnapshot{
		ScenarioID: strconv.FormatInt(out.ScenarioID, 10), RevisionID: strconv.FormatInt(out.RevisionID, 10), Version: strconv.FormatInt(out.Version, 10),
		StoredContentHash: out.StoredContentHash, DocumentHash: out.DocumentHash,
		DocumentJSON: out.DocumentJSON, FormDraftsJSON: out.FormDraftsJSON,
		HashPolicy: "design-scenario-envelope-v1", TypedStatus: out.TypedStatus, EnvelopeVerification: out.EnvelopeVerification,
	}
	if out.EnvelopeVerification == "verified" {
		wire.ContentHash = out.ContentHash
	}
	httpx.JSON(w, http.StatusOK, wire)
}
