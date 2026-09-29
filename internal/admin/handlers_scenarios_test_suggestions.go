package admin

import (
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleSuggestDesignScenarioTests(w http.ResponseWriter, r *http.Request) {
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
	} else {
		detail, err := s.designScenariosRepo.Detail(r.Context(), id)
		if err != nil {
			s.designScenarioError(w, err)
			return
		}
		revisionID = detail.Draft.ID
	}
	revision, err := s.designScenariosRepo.Revision(r.Context(), id, revisionID)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	if s.scenarioRuns == nil || s.scenarioRuns.repo == nil {
		s.scenarioRunError(w, errScenarioRunsUnavailable)
		return
	}
	reports, err := s.scenarioRuns.repo.CoverageReports(r.Context(), id, revisionID)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	result, err := designscenario.SuggestTests(r.Context(), revision, designscenario.BuildCoverage(revision, reports))
	if err != nil {
		s.scenarioRunError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
