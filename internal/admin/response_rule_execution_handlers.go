package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (s *Server) handleGetResponseRuleExecution(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.ResponseRuleExecution(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (s *Server) handleApplyResponseRule(w http.ResponseWriter, r *http.Request) {
	s.editResponseRuleExecution(w, r, true)
}

func (s *Server) handleUnapplyResponseRule(w http.ResponseWriter, r *http.Request) {
	s.editResponseRuleExecution(w, r, false)
}

func (s *Server) editResponseRuleExecution(w http.ResponseWriter, r *http.Request, apply bool) {
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
	d, err := s.designsRepo.EditResponseRuleExecution(r.Context(), id, version, designActor(r), r.PathValue("rid"), apply)
	s.designDetailResponse(w, r, http.StatusOK, d, err)
}
