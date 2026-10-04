package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendAnalysisTools(s *sdk.Server, lb *loopback) {
	const base = "/api/backend-projects/{id}/analyses"
	for _, spec := range []struct {
		name, route, contract string
		read                  bool
	}{
		{"start_backend_analysis", "POST " + base, "StartBackendAnalysisRequest", false},
		{"list_backend_analysis", "GET " + base, "", true},
		{"get_backend_analysis", "GET " + base + "/{aid}", "", true},
		{"cancel_backend_analysis", "POST " + base + "/{aid}/cancel", "CancelBackendAnalysisRequest", false},
		{"retry_backend_analysis", "POST " + base + "/{aid}/retry", "RetryBackendAnalysisRequest", false},
		{"get_backend_analysis_results", "GET " + base + "/{aid}/results", "", true},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var err error
			schema, err = api.BackendSchema(spec.contract)
			if err != nil {
				panic(err)
			}
			ids := []string{"projectId"}
			if spec.name != "start_backend_analysis" {
				ids = append(ids, "jobId")
			}
			backendToolPathSchema(schema, ids)
		} else {
			id := map[string]any{"type": "string", "format": "uuid"}
			fields := map[string]any{"projectId": id}
			required := []string{"projectId"}
			if spec.name != "list_backend_analysis" {
				fields["jobId"] = id
				required = append(required, "jobId")
			}
			if spec.name == "list_backend_analysis" || spec.name == "get_backend_analysis_results" {
				fields["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 500}
				fields["cursor"] = map[string]any{"type": "string", "maxLength": 1024}
			}
			if spec.name == "list_backend_analysis" {
				fields["kind"] = map[string]any{"type": "string", "enum": []string{"diff", "impact"}}
				fields["status"] = map[string]any{"type": "string", "enum": []string{"queued", "running", "completed", "failed", "cancelled", "interrupted"}}
			}
			if spec.name == "get_backend_analysis_results" {
				fields["resultVersion"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1, "maximum": int64(9223372036854775807)}
				fields["section"] = map[string]any{"type": "string", "enum": []string{"changes", "findings", "witnesses", "checks", "gaps"}}
				fields["service"], fields["kind"] = map[string]any{"type": "string"}, map[string]any{"type": "string"}
				fields["certainty"] = map[string]any{"type": "string", "enum": []string{"confirmed", "possible", "unknown"}}
				fields["direction"] = map[string]any{"type": "string", "enum": []string{"upstream", "downstream", "both"}}
				fields["depth"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 32}
				required = append(required, "resultVersion", "section")
			}
			schema = designScenarioSchemaObject(required, fields)
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: "Static, project-owned analysis of exact immutable targets. Start/retry return a durable job and 2000ms poll recommendation. Read saved input context and exact resultVersion; replay identical receipts after uncertain outcomes. Never executes source, SQL, jobs or brokers.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
