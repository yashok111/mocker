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
	if fault := materializationOwnerFault(err); fault != nil {
		s.backendError(w, fault)
		return
	}
	s.diagramError(w, err)
}

// materializationOwnerFault wraps the owner packages' sentinels into backend
// faults. Review 2026-10-06, F175: they went to designError and
// designScenarioError, which write the legacy httpx envelope (no
// `retryable`, codes such as not_found or design_conflict, and a Russian
// product message) on routes whose contract is BackendError; a bare
// apidesign.ErrInvalid even fell through to designError's 500. The conflict
// keeps the owner's fence: the draft version as currentVersion, the rest in
// details.
func materializationOwnerFault(err error) *backendmodel.FaultError {
	switch {
	case errors.Is(err, specs.ErrTooLarge) || errors.Is(err, designscenario.ErrTooLarge):
		return &backendmodel.FaultError{Status: 413, Code: "backend_too_large", Message: "Owner document exceeds its size limit"}
	case errors.Is(err, apidesign.ErrNotFound) || errors.Is(err, designscenario.ErrNotFound):
		return &backendmodel.FaultError{Status: 404, Code: "backend_materialization_owner_not_found", Message: "Owner artifact or revision not found"}
	case errors.Is(err, apidesign.ErrConflict) || errors.Is(err, designscenario.ErrConflict):
		fault := &backendmodel.FaultError{Status: 409, Code: "backend_materialization_owner_conflict", Message: "Owner draft changed; read it before retrying"}
		if c, ok := errors.AsType[*apidesign.ConflictError](err); ok {
			fault.CurrentVersion, fault.Details = c.Version, map[string]any{"draftRevisionId": c.DraftRevisionID}
		} else if c, ok := errors.AsType[*designscenario.LinkedConflictError](err); ok {
			fault.CurrentVersion, fault.Details = c.Version, map[string]any{"draftRevisionId": c.DraftRevisionID, "contractId": c.ContractID, "designId": c.DesignID}
		} else if c, ok := errors.AsType[*designscenario.ConflictError](err); ok {
			fault.CurrentVersion, fault.Details = c.Version, map[string]any{"draftRevisionId": c.DraftRevisionID}
		}
		return fault
	case errors.Is(err, apidesign.ErrInvalid) || errors.Is(err, designscenario.ErrInvalid):
		return &backendmodel.FaultError{Status: 422, Code: "backend_materialization_owner_invalid", Message: "Owner document is invalid"}
	}
	return nil
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
