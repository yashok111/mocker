package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendanalysis"
)

// backendAnalysisFamily is the text every analysis tool shares. It follows
// each tool's own summary sentence in the full description describe_tool
// returns; tools/list publishes only that first sentence (tool_catalog.go).
// When this text WAS the whole description, all six analysis tools listed the
// same summary and could not be told apart without six describe_tool calls.
const backendAnalysisFamily = "Static, project-owned analysis of exact immutable targets. Start/retry return a durable job and 2000ms poll recommendation. Read saved input context and exact resultVersion; replay identical receipts after uncertain outcomes. Never executes source, SQL, jobs or brokers."

func addBackendAnalysisTools(s *sdk.Server, lb *loopback) {
	const base = "/api/backend-projects/{id}/analyses"
	for _, spec := range []struct {
		name, route, contract, summary string
		read                           bool
	}{
		{"start_backend_analysis", "POST " + base, "StartBackendAnalysisRequest", "Starts a durable static analysis job (diff, impact, diagnostics, conformance and the other kinds) over an exact immutable target.", false},
		{"list_backend_analysis", "GET " + base, "", "Lists one page of the project's analysis jobs, optionally filtered by kind and status.", true},
		{"get_backend_analysis", "GET " + base + "/{aid}", "", "Reads one analysis job's current status together with its saved immutable input context and pins.", true},
		{"cancel_backend_analysis", "POST " + base + "/{aid}/cancel", "CancelBackendAnalysisRequest", "Cancels a queued or running analysis job with a persisted, idempotent cancellation.", false},
		{"retry_backend_analysis", "POST " + base + "/{aid}/retry", "RetryBackendAnalysisRequest", "Retries a terminal analysis job by starting a new job from its immutable input.", false},
		{"get_backend_analysis_results", "GET " + base + "/{aid}/results", "", "Reads one page of one section (changes, findings, witnesses, checks or gaps) of an exact analysis resultVersion.", true},
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
				fields["cursor"] = map[string]any{"type": "string", "maxLength": 1024, "description": "nextCursor of the previous page; it binds every selector and the limit, so keep the same limit"}
			}
			if spec.name == "list_backend_analysis" {
				fields["kind"] = map[string]any{"type": "string", "enum": backendanalysis.Kinds()}
				fields["order"] = map[string]any{"type": "string", "enum": []string{"asc", "desc"}}
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
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: spec.summary + " " + backendAnalysisFamily, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
}
