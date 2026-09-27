package scenarioexport

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/yashok111/mocker/internal/designscenario"
)

func (s *Service) Options(rev designscenario.Revision) ([]Option, error) {
	diagram := diagramDiagnostics(rev.Document)
	options := []Option{makeOption(PlantUML, "", diagram), makeOption(Mermaid, "", diagram)}
	if len(rev.Document.Contracts) == 0 {
		ds := []Diagnostic{{Code: "contract_missing", Severity: "error", Message: "Опишите HTTP API у сообщений сценария"}}
		options = append(options, makeOption(OpenAPIJSON, "", ds), makeOption(OpenAPIYAML, "", ds))
	}
	for _, contract := range rev.Document.Contracts {
		if err := s.CheckResponse(options); err != nil {
			return nil, err
		}
		ds, err := s.contractDiagnostics(rev, contract)
		if err != nil {
			return nil, err
		}
		options = append(options, makeOption(OpenAPIJSON, contract.ID, ds), makeOption(OpenAPIYAML, contract.ID, ds))
	}
	for _, format := range []Format{Postman, CURL} {
		_, ds, err := s.prepareHTTP(rev, format)
		if err != nil {
			return nil, err
		}
		options = append(options, makeOption(format, "", ds))
	}
	if err := s.CheckResponse(options); err != nil {
		return nil, err
	}
	return options, nil
}

func makeOption(format Format, id string, diagnostics []Diagnostic) Option {
	return Option{Format: format, ContractID: id, Ready: !slices.ContainsFunc(diagnostics, func(d Diagnostic) bool { return d.Severity == "error" }), Diagnostics: diagnostics}
}

func (s *Service) Export(rev designscenario.Revision, req Request) (Artifact, error) {
	artifact := Artifact{ScenarioID: rev.ScenarioID, RevisionID: rev.ID, SourceHash: rev.Hash, Format: req.Format}
	var diagnostics []Diagnostic
	var content []byte
	var err error
	ext := ""
	switch req.Format {
	case Postman, CURL:
		if req.ContractID != "" {
			return Artifact{}, ErrInvalidRequest
		}
		var prepared httpExport
		prepared, diagnostics, err = s.prepareHTTP(rev, req.Format)
		if err != nil {
			return Artifact{}, err
		}
		if !makeOption(req.Format, "", diagnostics).Ready {
			return Artifact{}, &BlockedError{Diagnostics: diagnostics}
		}
		if req.Format == Postman {
			content, err = s.renderPostman(rev.Document.Title, prepared)
			ext, artifact.MediaType = "postman_collection.json", "application/json;charset=utf-8"
		} else {
			content, err = s.renderCURL(prepared)
			ext, artifact.MediaType = "sh", "application/x-sh;charset=utf-8"
		}
	case PlantUML, Mermaid:
		if req.ContractID != "" {
			return Artifact{}, ErrInvalidRequest
		}
		diagnostics = diagramDiagnostics(rev.Document)
		if !makeOption(req.Format, "", diagnostics).Ready {
			return Artifact{}, &BlockedError{Diagnostics: diagnostics}
		}
		content, err = renderSequence(rev.Document, req.Format, s.maxBytes)
		ext = "puml"
		if req.Format == Mermaid {
			ext = "mmd"
		}
		artifact.MediaType = "text/plain;charset=utf-8"
	case OpenAPIJSON, OpenAPIYAML:
		index := slices.IndexFunc(rev.Document.Contracts, func(c designscenario.Contract) bool { return c.ID == req.ContractID })
		if index < 0 {
			return Artifact{}, ErrContractNotFound
		}
		contract := rev.Document.Contracts[index]
		diagnostics, err = s.contractDiagnostics(rev, contract)
		if err != nil {
			return Artifact{}, err
		}
		if !makeOption(req.Format, req.ContractID, diagnostics).Ready {
			return Artifact{}, &BlockedError{Diagnostics: diagnostics}
		}
		content, err = s.renderOpenAPI(contract, req.Format)
		ext = fmt.Sprintf("api-%d.json", index+1)
		artifact.MediaType = "application/json;charset=utf-8"
		if req.Format == OpenAPIYAML {
			ext = fmt.Sprintf("api-%d.yaml", index+1)
			artifact.MediaType = "application/yaml;charset=utf-8"
		}
	default:
		return Artifact{}, ErrUnsupportedFormat
	}
	if err != nil {
		return Artifact{}, err
	}
	artifact.Content = string(content)
	artifact.Diagnostics = diagnostics
	artifact.Filename = fmt.Sprintf("scenario-%d-r%d.%s", rev.ScenarioID, rev.ID, ext)
	if err = s.CheckResponse(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// CheckResponse enforces the wire-size limit, including JSON escaping and metadata.
func (s *Service) CheckResponse(value any) error {
	budget := jsonBudget{remaining: s.maxBytes}
	return budget.value(reflect.ValueOf(value))
}

type boundedBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 || int64(len(p)) > b.limit-int64(b.Len()) {
		return 0, ErrTooLarge
	}
	return b.Buffer.Write(p)
}

func unavailableValidator() error {
	return errors.New("scenario export contract validator is unavailable")
}
