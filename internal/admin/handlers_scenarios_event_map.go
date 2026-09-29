package admin

import (
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleGetDesignScenarioEventMap(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	var revisionID int64
	if raw, present := r.URL.Query()["revisionId"]; present {
		var err error
		if len(raw) != 1 {
			httpx.Err(w, 400, httpx.CodeBadRequest, "Укажите один revisionId")
			return
		}
		revisionID, err = strconv.ParseInt(raw[0], 10, 64)
		if err != nil || revisionID <= 0 {
			httpx.Err(w, 400, httpx.CodeBadRequest, "Укажите положительный revisionId")
			return
		}
	}
	report, err := s.designScenariosRepo.EventMap(r.Context(), id, revisionID)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}

func (s *Server) handleAnalyzeDesignScenarioEventMap(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	detail, err := s.designScenariosRepo.Detail(r.Context(), id)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	var body *struct {
		Document *designscenario.Document `json:"document"`
	}
	if !s.designScenarioBody(w, r, &body) {
		return
	}
	if body == nil || body.Document == nil {
		httpx.Err(w, 400, httpx.CodeBadRequest, "Укажите document")
		return
	}
	analysis, err := designscenario.AnalyzeEventMap(r.Context(), *body.Document)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, designscenario.EventMapReport{
		EventMapAnalysis: analysis, ScenarioID: id, Version: detail.Scenario.Version, Proposed: true,
	})
}
