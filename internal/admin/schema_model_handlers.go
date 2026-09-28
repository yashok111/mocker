package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/schemamodel"
)

func (s *Server) handleGetSchemaModel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	result, err := s.designsRepo.SchemaModel(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (s *Server) handlePreviewSchemaModel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var in apidesign.SchemaModelProposal
	if !s.designBody(w, r, &in) {
		return
	}
	result, err := s.designsRepo.PreviewSchemaModel(r.Context(), id, in)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (s *Server) handleApplySchemaModelCommands(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var in struct {
		ExpectedVersion int64                 `json:"expectedVersion"`
		Commands        []schemamodel.Command `json:"commands"`
	}
	if !s.designBody(w, r, &in) {
		return
	}
	if in.Commands == nil {
		httpx.Err(w, 400, "design_invalid", "Укажите массив commands")
		return
	}
	result, err := s.designsRepo.ApplySchemaModelCommands(r.Context(), id, in.ExpectedVersion, designActor(r), in.Commands)
	s.designDetailResponse(w, r, 200, result, err)
}
