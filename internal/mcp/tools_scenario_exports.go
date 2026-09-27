package mcp

import (
	"errors"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type scenarioExportInput struct {
	ScenarioID int64  `json:"scenarioId"`
	RevisionID int64  `json:"revisionId"`
	Format     string `json:"format"`
	ContractID string `json:"contractId,omitempty"`
}

func addScenarioExportTools(s *sdk.Server, lb *loopback) {
	addDesignScenarioTool(s, lb, "get_design_scenario_export_options", "GET /api/design-scenarios/{id}/revisions/{rid}/export-options",
		"Checks which exports are ready for one immutable scenario revision. Returns per-format diagnostics with links to messages or contracts. Diagrams do not require API details. Does not modify the scenario or create mocks.", true,
		func(in designScenarioRevisionInput) (designScenarioCall, error) {
			return designScenarioRead(in.ScenarioID, in.RevisionID)
		})
	addDesignScenarioTool(s, lb, "export_design_scenario", "GET /api/design-scenarios/{id}/revisions/{rid}/exports/{format}",
		"Exports plantuml, mermaid, openapi-json, openapi-yaml, postman or curl from an immutable saved scenario revision. Only OpenAPI requires contractId. Postman v2.1 and cURL export enabled HTTP requests using saved execution settings; review diagnostics and base URL variables before running. content is a string: save it unchanged. Returns filename, mediaType and diagnostics. SVG/PNG are available in the browser. Does not create API projects, execute requests or publish.", true,
		func(in scenarioExportInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID, in.RevisionID)
			if err != nil {
				return call, err
			}
			switch in.Format {
			case "plantuml", "mermaid", "postman", "curl":
				if in.ContractID != "" {
					return call, errors.New("contractId is only used for OpenAPI")
				}
			case "openapi-json", "openapi-yaml":
				if in.ContractID == "" {
					return call, errors.New("contractId is required for OpenAPI")
				}
			default:
				return call, errors.New("format must be plantuml, mermaid, openapi-json, openapi-yaml, postman or curl")
			}
			call.params = append(call.params, in.Format)
			if in.ContractID != "" {
				call.query = url.Values{"contractId": {in.ContractID}}.Encode()
			}
			return call, nil
		})
}
