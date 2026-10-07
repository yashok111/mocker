package scenarioexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/yashok111/mocker/internal/designscenario"
)

func (s *Service) Options(rev designscenario.Revision) ([]Option, error) {
	return s.OptionsContext(context.Background(), rev)
}

func (s *Service) OptionsContext(ctx context.Context, rev designscenario.Revision) ([]Option, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	diagram := diagramDiagnostics(rev.Document)
	options := []Option{makeOption(PlantUML, "", diagram), makeOption(Mermaid, "", diagram)}
	if len(rev.Document.Contracts) == 0 {
		ds := []Diagnostic{{Code: "contract_missing", Severity: "error", Message: "Опишите HTTP API у сообщений сценария"}}
		options = append(options, makeOption(OpenAPIJSON, "", ds), makeOption(OpenAPIYAML, "", ds))
	}
	for _, contract := range rev.Document.Contracts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	eventCache := newEventValidationCache(ctx, rev)
	documentation, err := s.documentationDiagnosticsWithCache(rev, eventCache)
	if err != nil {
		return nil, err
	}
	options = append(options, makeOption(Markdown, "", documentation), makeOption(HTML, "", documentation))
	if rev.Document.EventModel == nil || len(rev.Document.EventModel.Contracts) == 0 {
		ds := []Diagnostic{{Code: "event_contract_empty", Severity: "error", Message: "Создайте событийный контракт"}}
		options = append(options, makeOption(AsyncAPIJSON, "", ds), makeOption(AsyncAPIYAML, "", ds))
	} else {
		for _, contract := range rev.Document.EventModel.Contracts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ds, err := s.eventContractDiagnostics(eventCache, contract)
			if err != nil {
				return nil, err
			}
			options = append(options, makeOption(AsyncAPIJSON, contract.ID, ds), makeOption(AsyncAPIYAML, contract.ID, ds))
		}
	}
	if err := s.CheckResponse(options); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return options, nil
}

func makeOption(format Format, id string, diagnostics []Diagnostic) Option {
	return Option{Format: format, ContractID: id, Ready: !slices.ContainsFunc(diagnostics, func(d Diagnostic) bool { return d.Severity == "error" }), Diagnostics: diagnostics}
}

func (s *Service) Export(rev designscenario.Revision, req Request) (Artifact, error) {
	return s.ExportContext(context.Background(), rev, req)
}

func (s *Service) ExportContext(ctx context.Context, rev designscenario.Revision, req Request) (Artifact, error) {
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	body, err := s.exportBody(ctx, rev, req)
	if err != nil {
		return Artifact{}, err
	}
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	artifact := Artifact{ScenarioID: rev.ScenarioID, RevisionID: rev.ID, SourceHash: rev.Hash, Format: req.Format, MediaType: body.mediaType}
	artifact.Content = string(body.content)
	artifact.Diagnostics = body.diagnostics
	artifact.Filename = fmt.Sprintf("scenario-%d-r%d.%s", rev.ScenarioID, rev.ID, body.ext)
	if err = s.CheckResponse(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// exportBody is one format's rendered artifact before the shared envelope.
type exportBody struct {
	diagnostics    []Diagnostic
	content        []byte
	ext, mediaType string
}

func (s *Service) exportBody(ctx context.Context, rev designscenario.Revision, req Request) (exportBody, error) {
	switch req.Format {
	case AsyncAPIJSON, AsyncAPIYAML:
		return s.exportAsyncAPI(ctx, rev, req)
	case Markdown, HTML:
		return s.exportDocumentation(ctx, rev, req)
	case Postman, CURL:
		return s.exportHTTP(rev, req)
	case PlantUML, Mermaid:
		return s.exportSequence(rev, req)
	case OpenAPIJSON, OpenAPIYAML:
		return s.exportOpenAPI(rev, req)
	default:
		return exportBody{}, ErrUnsupportedFormat
	}
}

func (s *Service) exportAsyncAPI(ctx context.Context, rev designscenario.Revision, req Request) (exportBody, error) {
	if req.ContractID == "" {
		return exportBody{}, ErrInvalidRequest
	}
	if slices.ContainsFunc(rev.Document.Contracts, func(c designscenario.Contract) bool { return c.ID == req.ContractID }) {
		return exportBody{}, ErrInvalidRequest
	}
	if rev.Document.EventModel == nil {
		return exportBody{}, ErrContractNotFound
	}
	index := slices.IndexFunc(rev.Document.EventModel.Contracts, func(c designscenario.EventContract) bool { return c.ID == req.ContractID })
	if index < 0 {
		return exportBody{}, ErrContractNotFound
	}
	diagnostics, content, err := s.prepareAsyncAPI(newEventValidationCache(ctx, rev), rev.Document.EventModel.Contracts[index])
	if err != nil {
		return exportBody{}, err
	}
	if !makeOption(req.Format, req.ContractID, diagnostics).Ready {
		return exportBody{}, &BlockedError{Diagnostics: diagnostics}
	}
	body := exportBody{diagnostics: diagnostics, content: content, ext: fmt.Sprintf("asyncapi-%d.json", index+1), mediaType: "application/json;charset=utf-8"}
	if req.Format == AsyncAPIYAML {
		if body.content, err = convertAsyncAPIYAML(content, s.maxBytes); err != nil {
			return exportBody{}, err
		}
		body.ext, body.mediaType = fmt.Sprintf("asyncapi-%d.yaml", index+1), "application/yaml;charset=utf-8"
	}
	return body, nil
}

func (s *Service) exportDocumentation(ctx context.Context, rev designscenario.Revision, req Request) (exportBody, error) {
	if req.ContractID != "" {
		return exportBody{}, ErrInvalidRequest
	}
	diagnostics, err := s.documentationDiagnosticsWithCache(rev, newEventValidationCache(ctx, rev))
	if err != nil {
		return exportBody{}, err
	}
	if !makeOption(req.Format, "", diagnostics).Ready {
		return exportBody{}, &BlockedError{Diagnostics: diagnostics}
	}
	content, err := s.renderDocumentation(rev, req.Format, diagnostics)
	if err != nil {
		return exportBody{}, err
	}
	body := exportBody{diagnostics: diagnostics, content: content, ext: "md", mediaType: "text/markdown;charset=utf-8"}
	if req.Format == HTML {
		body.ext, body.mediaType = "html", "text/html;charset=utf-8"
	}
	return body, nil
}

func (s *Service) exportHTTP(rev designscenario.Revision, req Request) (exportBody, error) {
	if req.ContractID != "" {
		return exportBody{}, ErrInvalidRequest
	}
	prepared, diagnostics, err := s.prepareHTTP(rev, req.Format)
	if err != nil {
		return exportBody{}, err
	}
	if !makeOption(req.Format, "", diagnostics).Ready {
		return exportBody{}, &BlockedError{Diagnostics: diagnostics}
	}
	body := exportBody{diagnostics: diagnostics, ext: "sh", mediaType: "application/x-sh;charset=utf-8"}
	if req.Format == Postman {
		body.content, err = s.renderPostman(rev.Document.Title, prepared)
		body.ext, body.mediaType = "postman_collection.json", "application/json;charset=utf-8"
	} else {
		body.content, err = s.renderCURL(prepared)
	}
	if err != nil {
		return exportBody{}, err
	}
	return body, nil
}

func (s *Service) exportSequence(rev designscenario.Revision, req Request) (exportBody, error) {
	if req.ContractID != "" {
		return exportBody{}, ErrInvalidRequest
	}
	diagnostics := diagramDiagnostics(rev.Document)
	if !makeOption(req.Format, "", diagnostics).Ready {
		return exportBody{}, &BlockedError{Diagnostics: diagnostics}
	}
	content, err := renderSequence(rev.Document, req.Format, s.maxBytes)
	if err != nil {
		return exportBody{}, err
	}
	body := exportBody{diagnostics: diagnostics, content: content, ext: "puml", mediaType: "text/plain;charset=utf-8"}
	if req.Format == Mermaid {
		body.ext = "mmd"
	}
	return body, nil
}

func (s *Service) exportOpenAPI(rev designscenario.Revision, req Request) (exportBody, error) {
	if rev.Document.EventModel != nil && slices.ContainsFunc(rev.Document.EventModel.Contracts, func(c designscenario.EventContract) bool { return c.ID == req.ContractID }) {
		return exportBody{}, ErrInvalidRequest
	}
	index := slices.IndexFunc(rev.Document.Contracts, func(c designscenario.Contract) bool { return c.ID == req.ContractID })
	if index < 0 {
		return exportBody{}, ErrContractNotFound
	}
	contract := rev.Document.Contracts[index]
	diagnostics, err := s.contractDiagnostics(rev, contract)
	if err != nil {
		return exportBody{}, err
	}
	if !makeOption(req.Format, req.ContractID, diagnostics).Ready {
		return exportBody{}, &BlockedError{Diagnostics: diagnostics}
	}
	content, err := s.renderOpenAPI(contract, req.Format)
	if err != nil {
		return exportBody{}, err
	}
	body := exportBody{diagnostics: diagnostics, content: content, ext: fmt.Sprintf("api-%d.json", index+1), mediaType: "application/json;charset=utf-8"}
	if req.Format == OpenAPIYAML {
		body.ext, body.mediaType = fmt.Sprintf("api-%d.yaml", index+1), "application/yaml;charset=utf-8"
	}
	return body, nil
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
