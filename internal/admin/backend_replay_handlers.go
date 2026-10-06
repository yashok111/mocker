package admin

import (
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/backendreplay"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) replayRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return "", false
	}
	if !backendmodel.ValidID(r.PathValue("id")) || (r.PathValue("rid") != "" && !backendmodel.ValidID(r.PathValue("rid"))) || (r.PathValue("item") != "" && !backendmodel.ValidID(r.PathValue("item"))) || r.URL.RawQuery != "" {
		s.backendError(w, backendQueryError())
		return "", false
	}
	if r.Method == "GET" && (r.ContentLength != 0 || r.TransferEncoding != nil) {
		s.backendError(w, backendQueryError())
		return "", false
	}
	if s.backendReplay == nil {
		s.backendError(w, &backendmodel.FaultError{Status: 503, Code: "backend_replay_unavailable", Message: "Replay is not configured"})
		return "", false
	}
	// Resolve the project through the existing owner-aware repository even for template/registry reads.
	if _, err := s.backendRepo.Get(r.Context(), r.PathValue("id")); err != nil {
		s.backendError(w, err)
		return "", false
	}
	return strconv.FormatInt(user.ID, 10), true
}
func (s *Server) handleListBackendReplayTargets(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, 200, s.backendReplay.Targets())
}
func (s *Server) handleGetBackendReplayTemplate(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	v := backendreplay.Template()
	httpx.JSON(w, 200, map[string]any{"format": v.Format, "fixtureHash": v.FixtureHash, "failurePoint": v.FailurePoint, "steps": v.Steps, "assertions": v.Assertions, "artifactPins": v.ArtifactPins, "excludedIds": v.ExcludedIDs})
}
func (s *Server) handleConnectBackendReplayProfile(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in backendreplay.ConnectInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}

	out, err := s.backendReplay.Connect(r.Context(), r.PathValue("id"), actor, in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleListBackendReplayProfiles(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}

	out, err := s.backendReplay.Profiles(r.Context(), r.PathValue("id"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleRevokeBackendReplayAuthorization(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in backendreplay.RevokeInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}
	if err := s.backendReplay.Revoke(r.Context(), r.PathValue("id"), actor, in); err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]bool{"revoked": true})
}
func (s *Server) handleSaveBackendReplayPackage(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in backendreplay.SavePackageInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}
	ctx := r.Context()
	if s.backendArtifacts != nil {
		ctx = s.backendArtifacts.DiagramContext(ctx)
	}
	out, err := s.backendReplay.SavePackage(ctx, r.PathValue("id"), actor, in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleListBackendReplayPackages(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}

	out, err := s.backendReplay.Packages(r.Context(), r.PathValue("id"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendReplayProfile(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	version, err := backendPositiveDecimal(r.PathValue("v"))
	if err != nil {
		s.backendError(w, backendQueryError())
		return
	}
	out, err := s.backendReplay.GetProfile(r.Context(), r.PathValue("id"), r.PathValue("item"), version)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendReplayPackage(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	version, err := backendPositiveDecimal(r.PathValue("v"))
	if err != nil {
		s.backendError(w, backendQueryError())
		return
	}
	out, err := s.backendReplay.GetPackage(r.Context(), r.PathValue("id"), r.PathValue("item"), version)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleStartBackendReplay(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in backendreplay.StartInput
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}

	out, err := s.backendReplay.Start(r.Context(), r.PathValue("id"), actor, in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 202, out)
}
func (s *Server) handleListBackendReplayRuns(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}

	out, err := s.backendReplay.Runs(r.Context(), r.PathValue("id"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendReplayRun(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}

	out, err := s.backendReplay.Get(r.Context(), r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCancelBackendReplayRun(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in struct{}
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}

	out, err := s.backendReplay.Cancel(r.Context(), r.PathValue("id"), actor, r.PathValue("rid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCompareBackendReplayRuns(w http.ResponseWriter, r *http.Request) {
	_, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in struct {
		LeftRunID  string `json:"leftRunId"`
		RightRunID string `json:"rightRunId"`
	}
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}

	out, err := s.backendReplay.Compare(r.Context(), r.PathValue("id"), in.LeftRunID, in.RightRunID)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleReconcileBackendReplayRun(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.replayRequest(w, r)
	if !ok {
		return
	}
	var in struct{}
	if !s.backendBodyLimit(w, r, &in, min(s.cfg.MaxBody, int64(2<<20))) {
		return
	}

	out, err := s.backendReplay.Reconcile(r.Context(), r.PathValue("id"), actor, r.PathValue("rid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

// handleResolveBackendDiagramScope exposes the existing pure scope resolver for
// explicit replay preparation; it never connects to a fixture.
func (s *Server) handleResolveBackendDiagramScope(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	if !backendmodel.ValidID(r.PathValue("id")) {
		s.backendError(w, backendQueryError())
		return
	}
	var in backendmodel.DiagramScopeInput
	if !s.diagramBody(w, r, &in, 2<<20) {
		return
	}
	out, err := s.backendRepo.ResolveDiagramScope(ctx, r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
