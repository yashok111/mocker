package admin

import (
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/scenarioexport"
	"github.com/yashok111/mocker/internal/specs"
)

func (s *Server) scenarioExporter() *scenarioexport.Service {
	return scenarioexport.New(func(document string) ([]designscenario.Diagnostic, error) {
		diagnostics, err := s.designsRepo.Validate(document)
		if errors.Is(err, specs.ErrTooLarge) {
			return nil, scenarioexport.ErrTooLarge
		}
		out := make([]designscenario.Diagnostic, 0, len(diagnostics))
		for _, d := range diagnostics {
			out = append(out, designscenario.Diagnostic{Pointer: d.Pointer, Message: d.Message, Severity: d.Severity})
		}
		return out, err
	}, s.cfg.MaxBody)
}

func (s *Server) scenarioExportRevision(w http.ResponseWriter, r *http.Request) (designscenario.Revision, bool) {
	id, ok := s.designScenarioRequest(w, r)
	if !ok {
		return designscenario.Revision{}, false
	}
	rid, ok := designScenarioPathID(w, r, "rid")
	if !ok {
		return designscenario.Revision{}, false
	}
	rev, err := s.designScenariosRepo.Revision(r.Context(), id, rid)
	if err != nil {
		s.designScenarioError(w, err)
		return designscenario.Revision{}, false
	}
	return rev, true
}

func (s *Server) scenarioExportError(w http.ResponseWriter, err error) {
	if blocked, ok := errors.AsType[*scenarioexport.BlockedError](err); ok {
		body := httpx.ErrorBody{Error: httpx.ErrorDetail{Code: "scenario_export_blocked", Message: blocked.Error(), Details: blocked}}
		if limitErr := s.scenarioExporter().CheckResponse(body); limitErr != nil {
			s.scenarioExportError(w, limitErr)
			return
		}
		httpx.JSON(w, http.StatusUnprocessableEntity, body)
		return
	}
	switch {
	case errors.Is(err, scenarioexport.ErrUnsupportedFormat), errors.Is(err, scenarioexport.ErrInvalidRequest):
		httpx.Err(w, http.StatusBadRequest, "scenario_export_invalid", "Проверьте формат и параметры экспорта")
	case errors.Is(err, scenarioexport.ErrContractNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "Контракт отсутствует в выбранной версии сценария")
	case errors.Is(err, scenarioexport.ErrTooManyPages):
		httpx.Err(w, http.StatusRequestEntityTooLarge, "scenario_export_too_large", "Схема требует более 200 печатных листов. Сократите сценарий или выберите Markdown.")
	case errors.Is(err, scenarioexport.ErrTooLarge):
		httpx.Err(w, http.StatusRequestEntityTooLarge, "scenario_export_too_large", "Результат превышает допустимый размер")
	default:
		s.designScenarioError(w, err)
	}
}

func (s *Server) handleDesignScenarioExportOptions(w http.ResponseWriter, r *http.Request) {
	rev, ok := s.scenarioExportRevision(w, r)
	if !ok {
		return
	}
	exporter := s.scenarioExporter()
	options, err := exporter.OptionsContext(r.Context(), rev)
	if err != nil {
		s.scenarioExportError(w, err)
		return
	}
	result := scenarioexport.OptionsResponse{ScenarioID: rev.ScenarioID, RevisionID: rev.ID, SourceHash: rev.Hash, Options: options}
	if err = exporter.CheckResponse(result); err != nil {
		s.scenarioExportError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (s *Server) handleExportDesignScenario(w http.ResponseWriter, r *http.Request) {
	rev, ok := s.scenarioExportRevision(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()["contractId"]) > 1 {
		s.scenarioExportError(w, scenarioexport.ErrInvalidRequest)
		return
	}
	artifact, err := s.scenarioExporter().ExportContext(r.Context(), rev, scenarioexport.Request{
		Format: scenarioexport.Format(r.PathValue("format")), ContractID: r.URL.Query().Get("contractId"),
	})
	if err != nil {
		s.scenarioExportError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, artifact)
}

func (s *Server) handleExportDesignScenarioArchive(w http.ResponseWriter, r *http.Request) {
	rev, ok := s.scenarioExportRevision(w, r)
	if !ok {
		return
	}
	var input scenarioexport.ArchiveRequest
	if !s.designScenarioBody(w, r, &input) {
		return
	}
	archive, err := s.scenarioExporter().ExportArchiveContext(r.Context(), rev, input.Items)
	if err != nil {
		s.scenarioExportError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, archive)
}
