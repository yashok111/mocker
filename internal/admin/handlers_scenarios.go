package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
)

type designScenarioService interface {
	List(context.Context) ([]designscenario.Scenario, error)
	Create(context.Context, designscenario.CreateInput) (*designscenario.Detail, error)
	Detail(context.Context, int64) (*designscenario.Detail, error)
	Save(context.Context, int64, designscenario.SaveInput) (*designscenario.Detail, error)
	Apply(context.Context, int64, designscenario.CommandsInput) (*designscenario.Detail, error)
	Revision(context.Context, int64, int64) (designscenario.Revision, error)
	Diff(context.Context, int64, int64, int64) (*designscenario.Diff, error)
	Restore(context.Context, int64, designscenario.RestoreInput) (*designscenario.Detail, error)
	Validate(context.Context, designscenario.Document) ([]designscenario.Diagnostic, error)
}

func (s *Server) designScenarioError(w http.ResponseWriter, err error) {
	if invalid, ok := errors.AsType[*designscenario.InvalidError](err); ok {
		httpx.ErrDetails(w, http.StatusBadRequest, "design_scenario_invalid", err.Error(), invalid)
		return
	}
	if conflict, ok := errors.AsType[*designscenario.LinkedConflictError](err); ok {
		httpx.ErrDetails(w, http.StatusConflict, "design_scenario_conflict", err.Error(), conflict)
		return
	}
	if conflict, ok := errors.AsType[*designscenario.ConflictError](err); ok {
		httpx.ErrDetails(w, http.StatusConflict, "design_scenario_conflict", err.Error(), conflict)
		return
	}
	switch {
	case errors.Is(err, designscenario.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "Сценарий или версия не найдены")
	case errors.Is(err, designscenario.ErrInvalid):
		httpx.Err(w, http.StatusBadRequest, "design_scenario_invalid", "Сценарий содержит ошибки")
	case errors.Is(err, designscenario.ErrConflict):
		httpx.Err(w, http.StatusConflict, "design_scenario_conflict", "Сценарий изменился; загрузите текущую версию")
	case errors.Is(err, designscenario.ErrTooLarge):
		httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "Сценарий превышает допустимый размер")
	default:
		s.log.Error("design scenario", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "Не удалось выполнить действие со сценарием")
	}
}

func designScenarioPathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "Сценарий или версия не найдены")
		return 0, false
	}
	return id, true
}

func (s *Server) designScenarioRequest(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if _, ok := s.requireUser(w, r); !ok {
		return 0, false
	}
	return designScenarioPathID(w, r, "id")
}

func (s *Server) designScenarioBody(w http.ResponseWriter, r *http.Request, body any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBody)
	return decodeBody(w, r, body)
}

func (s *Server) designScenarioDetailResponse(
	w http.ResponseWriter,
	status int,
	detail *designscenario.Detail,
	err error,
) {
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, status, detail)
}

func (s *Server) handleListDesignScenarios(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	scenarios, err := s.designScenariosRepo.List(r.Context())
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scenarios": scenarios})
}

func (s *Server) handleCreateDesignScenario(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input designscenario.CreateInput
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	input.Source = designActor(r)
	input.OwnerID = &user.ID
	detail, err := s.designScenariosRepo.Create(r.Context(), input)
	s.designScenarioDetailResponse(w, http.StatusCreated, detail, err)
}

func (s *Server) handleGetDesignScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	detail, err := s.designScenariosRepo.Detail(r.Context(), id)
	s.designScenarioDetailResponse(w, http.StatusOK, detail, err)
}

func (s *Server) handleSaveDesignScenarioDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	user, _ := UserFrom(r.Context())
	var input designscenario.SaveInput
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	input.Source = designActor(r)
	input.OwnerID = &user.ID
	detail, err := s.designScenariosRepo.Save(r.Context(), id, input)
	s.designScenarioDetailResponse(w, http.StatusOK, detail, err)
}

func (s *Server) handleApplyDesignScenarioCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	user, _ := UserFrom(r.Context())
	var input designscenario.CommandsInput
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	input.Source = designActor(r)
	input.OwnerID = &user.ID
	detail, err := s.designScenariosRepo.Apply(r.Context(), id, input)
	s.designScenarioDetailResponse(w, http.StatusOK, detail, err)
}

func (s *Server) handleGetDesignScenarioRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	revisionID, ok := designScenarioPathID(w, r, "rid")
	if !ok {
		return
	}
	revision, err := s.designScenariosRepo.Revision(r.Context(), id, revisionID)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, revision)
}

func (s *Server) handleGetDesignScenarioDiff(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	fromID, ok := designScenarioQueryRevision(w, r, "from")
	if !ok {
		return
	}
	toID, ok := designScenarioQueryRevision(w, r, "to")
	if !ok {
		return
	}
	diff, err := s.designScenariosRepo.Diff(r.Context(), id, fromID, toID)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, diff)
}

func designScenarioQueryRevision(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	values, exists := r.URL.Query()[key]
	if !exists {
		return 0, true
	}
	if len(values) != 1 {
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "Версия сценария не найдена")
		return 0, false
	}
	id, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || id <= 0 {
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "Версия сценария не найдена")
		return 0, false
	}
	return id, true
}

func (s *Server) handleRestoreDesignScenarioRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	var input designscenario.RestoreInput
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	input.Source = designActor(r)
	detail, err := s.designScenariosRepo.Restore(r.Context(), id, input)
	s.designScenarioDetailResponse(w, http.StatusOK, detail, err)
}

func (s *Server) handleValidateDesignScenario(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return
	}
	if _, err := s.designScenariosRepo.Detail(r.Context(), id); err != nil {
		s.designScenarioError(w, err)
		return
	}
	var body struct {
		Document designscenario.Document `json:"document"`
	}
	if !s.designScenarioBody(w, r, &body) {
		return
	}
	diagnostics, err := s.designScenariosRepo.Validate(r.Context(), body.Document)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"diagnostics": diagnostics})
}
