package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleQueryBackendFlow(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var raw jsontext.Value
	if !s.backendBodyLimit(w, r, &raw, s.cfg.MaxBody) {
		return
	}
	var in backendmodel.FlowQueryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok {
			s.backendError(w, fault)
		} else {
			s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Request must match the flow query schema"})
		}
		return
	}
	out, err := s.backendRepo.QueryFlow(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
