package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/resourcemap"
)

func (s *Server) handleGetResourceMap(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.ResourceMap(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	usages, truncated, err := s.designScenariosRepo.ResourceMapUsages(r.Context(), id)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, struct {
		apidesign.ResourceMapDetail
		ScenarioUsages  []designscenario.ResourceMapUsage `json:"scenarioUsages"`
		UsagesTruncated bool                              `json:"usagesTruncated"`
	}{result, usages, truncated})
}

func (s *Server) handlePreviewResourceMap(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var in apidesign.ResourceMapProposal
	if !s.designBody(w, r, &in) {
		return
	}
	result, err := s.designsRepo.PreviewResourceMap(r.Context(), id, in)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (s *Server) handleApplyResourceMapCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var in struct {
		ExpectedVersion *int64                `json:"expectedVersion"`
		Commands        []resourcemap.Command `json:"commands"`
	}
	if !s.designBody(w, r, &in) {
		return
	}
	if in.ExpectedVersion == nil || *in.ExpectedVersion <= 0 {
		httpx.Err(w, http.StatusBadRequest, "design_invalid", "Укажите положительный expectedVersion")
		return
	}
	if len(in.Commands) == 0 {
		httpx.Err(w, http.StatusBadRequest, "design_invalid", "Укажите от 1 до 100 команд")
		return
	}
	result, err := s.designsRepo.ApplyResourceMapCommands(r.Context(), id, *in.ExpectedVersion, designActor(r), in.Commands)
	s.designDetailResponse(w, r, http.StatusOK, result, err)
}
