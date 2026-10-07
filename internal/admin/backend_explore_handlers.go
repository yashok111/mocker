package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleQueryBackendExplore(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.diagramContext(w, r.Context(), r)
	if !ok {
		return
	}
	var in backendmodel.ExploreInput
	if !s.diagramBody(w, r, &in, 8192) {
		return
	}
	out, err := s.backendRepo.QueryExplore(ctx, r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListBackendImportSummaries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, err := backendListInput(r)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.ImportSummaries(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
