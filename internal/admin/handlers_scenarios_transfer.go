package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleExportScenarioTransfer(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var input struct {
		ScenarioIDs    []int64 `json:"scenarioIds"`
		IncludeHistory bool    `json:"includeHistory"`
	}
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	bundle, err := s.designScenariosRepo.ExportTransfer(r.Context(), input.ScenarioIDs, input.IncludeHistory)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, bundle)
}
func (s *Server) handleImportScenarioTransfer(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var input struct {
		Bundle designscenario.TransferBundle `json:"bundle"`
		Relink bool                          `json:"relink"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, designscenario.MaxTransferBytes+1024)
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	result, err := s.designScenariosRepo.ImportTransfer(r.Context(), input.Bundle, input.Relink)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, result)
}
