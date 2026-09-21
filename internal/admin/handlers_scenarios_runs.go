package admin

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

type RunDesignScenarioRequest struct {
	RunID      string                         `json:"runId"`
	RevisionID int64                          `json:"revisionId"`
	Variables  designscenario.ExecutionValues `json:"variables,omitempty"`
	Name       string                         `json:"name,omitempty"`
}

var scenarioRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (input RunDesignScenarioRequest) validate() error {
	if !scenarioRunIDPattern.MatchString(input.RunID) || input.RevisionID <= 0 || utf8.RuneCountInString(input.Name) > 200 {
		return fmt.Errorf("%w: укажите runId [A-Za-z0-9_-]{1,100}, положительный revisionId и имя до 200 символов", designscenario.ErrInvalid)
	}
	return nil
}

func (input *RunDesignScenarioRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"runId", "revisionId"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("missing %s", key)
		}
	}
	for key, raw := range fields {
		switch key {
		case "runId", "revisionId", "variables", "name":
		default:
			return fmt.Errorf("unknown field %q", key)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s cannot be null", key)
		}
	}
	type wire RunDesignScenarioRequest
	var out wire
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*input = RunDesignScenarioRequest(out)
	return nil
}

func (s *Server) scenarioRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, designscenario.ErrRunConflict):
		httpx.Err(w, http.StatusConflict, "design_scenario_run_conflict", err.Error())
	case errors.Is(err, designscenario.ErrRunBusy):
		httpx.Err(w, http.StatusConflict, "design_scenario_run_busy", "Достигнут лимит одновременных прогонов")
	case errors.Is(err, designscenario.ErrRunGone):
		httpx.Err(w, http.StatusGone, "design_scenario_run_gone", "Отчёт больше не хранится; этот runId уже использован")
	case errors.Is(err, errScenarioRunsUnavailable):
		httpx.Err(w, http.StatusServiceUnavailable, "design_scenario_execution_unavailable", "Исполнение сценариев недоступно")
	case errors.Is(err, designscenario.ErrInvalid):
		httpx.Err(w, http.StatusBadRequest, "design_scenario_invalid", err.Error())
	default:
		s.designScenarioError(w, err)
	}
}

func (s *Server) handleRunDesignScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	var input RunDesignScenarioRequest
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	report, err := s.scenarioRuns.start(r.Context(), id, input, designActor(r))
	if err != nil {
		s.scenarioRunError(w, err)
		return
	}
	status := http.StatusOK
	if report.Status == "running" {
		status = http.StatusAccepted
	}
	httpx.JSON(w, status, report)
}

func (s *Server) handleListDesignScenarioRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	runs, err := s.scenarioRuns.list(r.Context(), id)
	if err != nil {
		s.scenarioRunError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetDesignScenarioRun(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	report, err := s.scenarioRuns.get(r.Context(), id, r.PathValue("runId"))
	if err != nil {
		s.scenarioRunError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}

func (s *Server) handleCancelDesignScenarioRun(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	var input *struct{}
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	if input == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "Ожидался пустой JSON-объект")
		return
	}
	report, err := s.scenarioRuns.cancel(r.Context(), id, r.PathValue("runId"))
	if err != nil {
		s.scenarioRunError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}
