package mcp

import (
	"errors"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/scenarioexport"
)

type scenarioExportInput struct {
	ScenarioID int64  `json:"scenarioId"`
	RevisionID int64  `json:"revisionId"`
	Format     string `json:"format"`
	ContractID string `json:"contractId,omitempty"`
}

type scenarioArchiveInput struct {
	ScenarioID int64                    `json:"scenarioId"`
	RevisionID int64                    `json:"revisionId"`
	Items      []scenarioexport.Request `json:"items"`
}

func addScenarioExportTools(s *sdk.Server, lb *loopback) {
	addDesignScenarioTool(s, lb, "export_design_scenario_archive", "POST /api/design-scenarios/{id}/revisions/{rid}/archive",
		"Exports 1–32 unique selections from one immutable saved scenario revision as ZIP. Each item has format and optionally contractId (required for OpenAPI and AsyncAPI). Server formats: plantuml, mermaid, openapi-json, openapi-yaml, asyncapi-json, asyncapi-yaml, postman, curl, markdown, html. SVG/PNG/PDF are browser-only. Decode contentBase64 into ZIP bytes; manifest v1 records exact byte lengths, SHA-256 and diagnostics. ZIP contains artifacts, not a restorable project. Any failed item cancels the archive. Does not modify resources, execute requests or publish.", true,
		func(in scenarioArchiveInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID, in.RevisionID)
			if err != nil {
				return call, err
			}
			if err := scenarioexport.ValidateArchiveRequests(in.Items); err != nil {
				return call, err
			}
			call.body = scenarioexport.ArchiveRequest{Items: in.Items}
			return call, nil
		})

	addDesignScenarioTool(s, lb, "get_design_scenario_export_options", "GET /api/design-scenarios/{id}/revisions/{rid}/export-options",
		"Checks which exports are ready for one immutable scenario revision. Returns per-format diagnostics with links to messages or contracts. Diagrams do not require API details. Does not modify the scenario or create mocks.", true,
		func(in designScenarioRevisionInput) (designScenarioCall, error) {
			return designScenarioRead(in.ScenarioID, in.RevisionID)
		})
	addDesignScenarioTool(s, lb, "export_design_scenario", "GET /api/design-scenarios/{id}/revisions/{rid}/exports/{format}",
		"Exports plantuml, mermaid, openapi-json, openapi-yaml, asyncapi-json, asyncapi-yaml, postman, curl, markdown or html from an immutable saved scenario revision. OpenAPI and AsyncAPI require contractId. AsyncAPI 3.0.0 describes one Kafka application using bindings 0.5.0; it does not execute Kafka operations. Postman v2.1 and cURL export enabled HTTP requests using saved execution settings; review diagnostics and base URL variables before running. content is a string: save it unchanged. Returns filename, mediaType and diagnostics. Markdown/HTML document the saved diagram and contracts without execution settings; incomplete API forms produce warnings. PDF is browser printing of HTML. SVG/PNG are available in the browser. Does not create API projects, execute requests or publish.", true,
		func(in scenarioExportInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID, in.RevisionID)
			if err != nil {
				return call, err
			}
			switch in.Format {
			case "plantuml", "mermaid", "postman", "curl", "markdown", "html":
				if in.ContractID != "" {
					return call, errors.New("contractId is only used for OpenAPI and AsyncAPI")
				}
			case "openapi-json", "openapi-yaml", "asyncapi-json", "asyncapi-yaml":
				if in.ContractID == "" {
					return call, errors.New("contractId is required for OpenAPI and AsyncAPI")
				}
			default:
				return call, errors.New("format must be plantuml, mermaid, openapi-json, openapi-yaml, asyncapi-json, asyncapi-yaml, postman, curl, markdown or html")
			}
			call.params = append(call.params, in.Format)
			if in.ContractID != "" {
				call.query = url.Values{"contractId": {in.ContractID}}.Encode()
			}
			return call, nil
		})
}
