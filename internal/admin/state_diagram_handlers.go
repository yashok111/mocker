package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/statediagram"
)

func (s *Server) handleListStateDiagrams(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.StateDiagrams(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (s *Server) handleGetStateDiagram(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.StateDiagrams(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	for _, d := range result.Diagrams {
		if d.ID == r.PathValue("did") {
			httpx.JSON(w, 200, map[string]any{"designId": id, "version": result.Version, "revisionId": result.RevisionID, "diagram": d})
			return
		}
	}
	s.designError(w, apidesign.ErrNotFound)
}
func (s *Server) handleCreateStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.editStateDiagram(w, r, "create")
}
func (s *Server) handleSaveStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.editStateDiagram(w, r, "save")
}
func (s *Server) handleDeleteStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.editStateDiagram(w, r, "delete")
}
func (s *Server) handleStateDiagramCommands(w http.ResponseWriter, r *http.Request) {
	s.editStateDiagram(w, r, "commands")
}
func (s *Server) editStateDiagram(w http.ResponseWriter, r *http.Request, action string) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64                  `json:"expectedVersion"`
		Diagram         *statediagram.Diagram  `json:"diagram"`
		Commands        []statediagram.Command `json:"commands"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	diagramID := r.PathValue("did")
	if action == "create" && body.Diagram != nil {
		diagramID = body.Diagram.ID
	}
	d, err := s.designsRepo.EditStateDiagram(r.Context(), id, body.ExpectedVersion, designActor(r), diagramID, action, body.Diagram, body.Commands)
	status := 200
	if action == "create" {
		status = 201
	}
	s.designDetailResponse(w, r, status, d, err)
}
func (s *Server) handleValidateStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.evaluateStateDiagram(w, r, false)
}
func (s *Server) handleSimulateStateDiagram(w http.ResponseWriter, r *http.Request) {
	s.evaluateStateDiagram(w, r, true)
}
func (s *Server) evaluateStateDiagram(w http.ResponseWriter, r *http.Request, simulate bool) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var body apidesign.StateDiagramProposal
	if !s.designBody(w, r, &body) {
		return
	}
	diagram, root, err := s.designsRepo.ResolveStateDiagram(r.Context(), id, r.PathValue("did"), body)
	if err != nil {
		s.designError(w, err)
		return
	}
	if !simulate {
		diagnostics := statediagram.Validate(diagram, root)
		httpx.JSON(w, 200, map[string]any{"valid": !statediagram.HasErrors(diagnostics), "diagnostics": diagnostics})
		return
	}
	if body.DataJSON == "" {
		body.DataJSON = "{}"
	}
	result, err := statediagram.Simulate(r.Context(), diagram, root, body.DataJSON, body.TransitionIDs)
	if err != nil {
		httpx.Err(w, 400, "simulation_invalid", err.Error())
		return
	}
	httpx.JSON(w, 200, result)
}
