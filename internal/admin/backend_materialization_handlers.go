package admin

import (
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmaterialize"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/specs"
)

func (s *Server) materializationError(w http.ResponseWriter, err error) {
	if bad, ok := errors.AsType[*apidesign.InvalidError](err); ok {
		s.backendError(w, &backendmodel.FaultError{Status: 422, Code: "backend_materialization_owner_invalid", Message: err.Error(), Details: map[string]any{"diagnostics": bad.Diagnostics}})
		return
	}
	if bad, ok := errors.AsType[*designscenario.InvalidError](err); ok {
		s.backendError(w, &backendmodel.FaultError{Status: 422, Code: "backend_materialization_owner_invalid", Message: err.Error(), Details: map[string]any{"diagnostics": bad.Diagnostics}})
		return
	}
	if errors.Is(err, specs.ErrTooLarge) {
		s.designError(w, err)
		return
	}
	if errors.Is(err, apidesign.ErrInvalid) || errors.Is(err, apidesign.ErrConflict) || errors.Is(err, apidesign.ErrNotFound) {
		s.designError(w, err)
		return
	}
	if errors.Is(err, designscenario.ErrInvalid) || errors.Is(err, designscenario.ErrConflict) || errors.Is(err, designscenario.ErrNotFound) || errors.Is(err, designscenario.ErrTooLarge) {
		s.designScenarioError(w, err)
		return
	}
	s.diagramError(w, err)
}
func (s *Server) handlePreviewBackendMaterialization(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	var in backendmaterialize.PreviewInput
	if !s.diagramBody(w, r, &in, backendmaterialize.MaxBytes) {
		return
	}
	out, err := s.backendMaterialization.Preview(ctx, r.PathValue("id"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
func (s *Server) handleApplyBackendMaterialization(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	ctx = backendmaterialize.WithActor(ctx, backendmaterialize.Actor{Name: user.Name, Source: designActor(r), OwnerID: &user.ID})
	var in backendmaterialize.ApplyInput
	if !s.diagramBody(w, r, &in, backendmaterialize.MaxBytes+1024) {
		return
	}
	out, err := s.backendMaterialization.Apply(ctx, r.PathValue("id"), in)
	if err != nil {
		s.materializationError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
