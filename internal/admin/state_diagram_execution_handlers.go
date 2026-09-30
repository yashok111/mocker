package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (s *Server) handleGetStateDiagramExecution(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.StateDiagramExecution(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (s *Server) handleApplyStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.editStateDiagramExecution(w, r, true)
}

func (s *Server) handleUnapplyStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.editStateDiagramExecution(w, r, false)
}

func (s *Server) editStateDiagramExecution(w http.ResponseWriter, r *http.Request, apply bool) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	fields := []string{"expectedVersion"}
	body, ok := s.responseRuleBody(w, r, fields, fields)
	if !ok {
		return
	}
	var version int64
	if err := jsonx.Unmarshal(body["expectedVersion"], &version); err != nil || version <= 0 {
		s.responseRuleInvalid(w, "/expectedVersion", "Обязательна положительная целая версия API")
		return
	}
	d, err := s.designsRepo.EditStateDiagramExecution(r.Context(), id, version, designActor(r), r.PathValue("did"), apply)
	s.designDetailResponse(w, r, http.StatusOK, d, err)
}
