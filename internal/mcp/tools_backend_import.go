package mcp

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"slices"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

// Schemas match the strict OpenAPI request contracts, with explicit MCP path IDs.
func addBackendImportTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, description, schema string
		readOnly, idempotent             bool
	}{
		{"begin_backend_import", "POST /api/backend-projects/{id}/imports", "Begins legacy initial/reconcile import or explicit composed source6 sync. Pin capabilities and the matching guide first. Composed mode requires sourceScope and scopeStatus; whole-source-v1 and incremental-source-v1 are distinct. Incremental requires a current native6 provider partition and changeManifest, and cannot extend profiles. Source5 bootstrap with add_repository/add_provider retains existing partition profiles and adds a composed incoming partition. Whole-source reconcile upgrades only the selected eligible source5 partition, including one retained inside native6, with an explicit events-to-composed extension. Source IDs are durable; Begin leaves the active revision unchanged. Retry identical body/key after an unknown outcome.", "", false, true},
		{"compare_backend_revisions", "POST /api/backend-projects/{id}/revisions/compare", "Compares two exact immutable revisions with pinned paging and summary. Read-only; pins remain in the request body.", `{"type":"object","additionalProperties":false,"required":["projectId","fromRevisionId","toRevisionId"],"properties":{"fromRevisionId":{"type":"string","format":"uuid"},"toRevisionId":{"type":"string","format":"uuid"},"recordType":{"type":"string","enum":["node","edge","evidence","source","identity","artifact"]},"changeKind":{"type":"string","enum":["added","removed","modified","identity_mapped","freshness_changed"]},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"},"projectId":{"type":"string","format":"uuid"}}}`, true, true},
		{"get_backend_import_changes", "GET /api/backend-projects/{id}/imports/{iid}/changes", "Reads saved preview source changes and decisions at an exact previewVersion. Does not trigger preview.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","previewVersion","recordType"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"previewVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"recordType":{"type":"string","enum":["source","identity","deletion","assertion_conflict","claim_identity","migration"]},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"}}}`, true, true},
		{"list_backend_imports", "GET /api/backend-projects/{id}/imports", "Lists durable import sessions with project-bound pagination.", `{"type":"object","additionalProperties":false,"required":["projectId"],"properties":{"projectId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}}}`, true, true},
		{"get_backend_import", "GET /api/backend-projects/{id}/imports/{iid}", "Reads a durable session and paginated accepted batch summaries. Replay a batch to recover identity mappings.", `{"type":"object","additionalProperties":false,"required":["projectId","importId"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500},"cursor":{"type":"string"}}}`, true, true},
		{"put_backend_import_batch", "PUT /api/backend-projects/{id}/imports/{iid}/batches/{bid}", "Stages typed commands with a deterministic payloadHash. Composed batches include claim_identity and resolve_assertion with exact qualified source references; they do not run source code. Forward references may be repaired before preview. Exact batch replay returns its original receipt before CAS, including after commit/abort; changed bodies conflict.", "", false, true},
		{"preview_backend_import", "POST /api/backend-projects/{id}/imports/{iid}/preview", "Validates durable staging and increments its version. Returns ready plus a candidateHash or needs_resolution diagnostics. Explicit baseRevisionId pins the selected base; data changes invalidate preview.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","expectedImportVersion","baseRevisionId"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"baseRevisionId":{"type":"string","format":"uuid"}}}`, false, false},
		{"commit_backend_import", "POST /api/backend-projects/{id}/imports/{iid}/commit", "Atomically commits one immutable source revision using project/import CAS and the exact preview candidateHash. Replay the identical idempotency payload after an uncertain response; reconcile conflicts before retrying.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","expectedVersion","expectedImportVersion","candidateHash","idempotencyKey"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"expectedVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"candidateHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"idempotencyKey":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[!-~]+$"}}}`, false, true},
		{"abort_backend_import", "POST /api/backend-projects/{id}/imports/{iid}/abort", "Aborts durable staging with CAS and idempotency; active graph remains unchanged. A committed session cannot abort.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","expectedImportVersion","idempotencyKey"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"idempotencyKey":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[!-~]+$"}}}`, false, true},
		{"query_backend_graph", "POST /api/backend-projects/{id}/graph/query", "Queries exactly one revisionId, legacy proposal, full changeProposal or current READY importCandidate. UUID keyset pages preserve target/hash pins. Full graph edits are desired structure with baseline proof kept separate. Select nodes or edges; search/parentId apply to nodes, from/to to edges. No head fallback or arbitrary traversal.", `{"oneOf":[{"type":"object","additionalProperties":false,"required":["projectId","revisionId","recordType"],"properties":{"revisionId":{"type":"string","format":"uuid"},"recordType":{"const":"nodes","type":"string"},"kind":{"type":"string"},"search":{"type":"string"},"parentId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"},"id":{"type":"string","format":"uuid"},"projectId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["id"]},"then":{"not":{"anyOf":[{"required":["kind"]},{"required":["search"]},{"required":["parentId"]},{"required":["from"]},{"required":["to"]},{"required":["cursor"]}]}}}]},{"type":"object","additionalProperties":false,"required":["projectId","revisionId","recordType"],"properties":{"revisionId":{"type":"string","format":"uuid"},"recordType":{"const":"edges","type":"string"},"kind":{"type":"string"},"from":{"type":"string","format":"uuid"},"to":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"},"id":{"type":"string","format":"uuid"},"projectId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["id"]},"then":{"not":{"anyOf":[{"required":["kind"]},{"required":["search"]},{"required":["parentId"]},{"required":["from"]},{"required":["to"]},{"required":["cursor"]}]}}}]}],"type":"object"}`, true, true},
		{"query_backend_database", "POST /api/backend-projects/{id}/database/query", "Reads a selected datastore/facet at source2 through source6, a legacy database proposal, or full changeProposal with source5/6 baseline. importCandidate is unsupported. Desired cardinality is unverified at runtime; source evidence remains attributed to its actual baseline. Never introspects a live database.", "", true, true},
		{"get_backend_node", "GET /api/backend-projects/{id}/revisions/{rid}/nodes/{nid}", "Reads one exact node from revisionId, legacy proposal, full changeProposal or current READY importCandidate. Qualified source6 identities and full desired-field origins remain distinct. Preserve returned target/pins for related graph and evidence reads.", `{"type":"object","additionalProperties":false,"required":["projectId","revisionId","nodeId"],"properties":{"projectId":{"type":"string","format":"uuid"},"revisionId":{"type":"string","format":"uuid"},"nodeId":{"type":"string","format":"uuid"}}}`, true, true},
		{"get_backend_evidence", "GET /api/backend-projects/{id}/revisions/{rid}/evidence", "Reads evidence at one explicit revisionId, legacy proposal, full changeProposal or current READY importCandidate, optionally by subjectId. Full evidence describes baseline objects, not proof of changed desired values. Locations are provider assertions; the server never reads local files.", `{"type":"object","additionalProperties":false,"required":["projectId","revisionId"],"properties":{"projectId":{"type":"string","format":"uuid"},"revisionId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500},"cursor":{"type":"string"},"subjectId":{"type":"string","format":"uuid"},"evidenceId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["evidenceId"]},"then":{"not":{"anyOf":[{"required":["subjectId"]},{"required":["cursor"]}]}}}]}`, true, true},
		{"get_backend_coverage", "GET /api/backend-projects/{id}/revisions/{rid}/coverage", "Reads inventory, snapshots, coverage, stale counts and reconciliation gaps at one revisionId, legacy proposal, full changeProposal or current READY importCandidate. Preserve exact target/pins. Unknown denominator and partial coverage do not claim execution or test coverage.", `{"type":"object","additionalProperties":false,"required":["projectId","revisionId"],"properties":{"projectId":{"type":"string","format":"uuid"},"revisionId":{"type":"string","format":"uuid"}}}`, true, true},
	} {
		var inputSchema map[string]any
		contract := map[string]string{"begin_backend_import": "BeginBackendImportRequest", "put_backend_import_batch": "PutBackendImportBatchRequest", "query_backend_database": "QueryBackendDatabaseRequest", "query_backend_graph": "QueryBackendGraphRequest"}[spec.name]
		if contract != "" {
			var err error
			inputSchema, err = api.BackendSchema(contract)
			if err != nil {
				panic(err)
			}
			ids := []string{"projectId"}
			if spec.name == "put_backend_import_batch" {
				ids = append(ids, "importId", "batchId")
			}
			backendToolPathSchema(inputSchema, ids)
		} else {
			decoder := jsonx.NewDecoder(bytes.NewReader([]byte(spec.schema)))
			decoder.UseNumber()
			if err := decoder.Decode(&inputSchema); err != nil {
				panic(err)
			}
		}

		if spec.name == "get_backend_node" || spec.name == "get_backend_evidence" || spec.name == "get_backend_coverage" {
			backendReadToolTarget(inputSchema)
		}
		inputSchema["type"] = "object"
		tool := &sdk.Tool{Name: spec.name, Description: spec.description, InputSchema: inputSchema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.readOnly, IdempotentHint: spec.idempotent}}
		addBackendImportTool(s, lb, tool, spec.route)
	}
}

func addBackendImportTool(s *sdk.Server, lb *loopback, tool *sdk.Tool, route string) {
	schema, err := compileBackendImportToolSchema(tool)
	if err != nil {
		panic(fmt.Errorf("AddTool %q: %w", tool.Name, err))
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var in map[string]jsonx.RawMessage
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &in, schema); err != nil {
			return designScenarioToolErrorResult(fmt.Errorf("%s: invalid arguments: %w", tool.Name, err)), nil
		}
		// json-v2 additionally rejects duplicate members; retain original numeric bytes.
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		for _, key := range []string{"fromRevisionId", "toRevisionId", "id", "repositoryId"} {
			if raw, supplied := in[key]; supplied {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil || !backendmodel.ValidID(value) {
					return designScenarioToolErrorResult(fmt.Errorf("%s must be a canonical UUID", key)), nil
				}
			}
		}
		if tool.Name == "put_backend_import_batch" {
			var commands []backendmodel.ImportCommand
			if err := json.Unmarshal(in["commands"], &commands); err != nil {
				return designScenarioToolErrorResult(err), nil
			}
			for _, command := range commands {
				if command.Identity != nil && !backendmodel.ValidID(command.Identity.ExpectedID) {
					return designScenarioToolErrorResult(fmt.Errorf("identity expectedId must be a canonical UUID")), nil
				}
				if command.Deletion != nil && !backendmodel.ValidID(command.Deletion.ExpectedID) {
					return designScenarioToolErrorResult(fmt.Errorf("deletion expectedId must be a canonical UUID")), nil
				}
			}
		}
		if _, selected := in["evidenceId"]; selected && tool.Name == "get_backend_evidence" {
			if _, ok := in["subjectId"]; ok {
				return designScenarioToolErrorResult(fmt.Errorf("evidenceId cannot combine with subjectId")), nil
			}
			if _, ok := in["cursor"]; ok {
				return designScenarioToolErrorResult(fmt.Errorf("evidenceId cannot combine with cursor")), nil
			}
		}

		selectedRoute, selectedProposal, selectErr := backendPinnedToolRoute(tool.Name, route, in)
		if selectErr != nil {
			return backendAdmissionFault(selectErr), nil
		}
		var params []any
		for _, key := range []string{"projectId", "importId", "proposalId", "jobId", "viewId", "revisionId", "nodeId", "batchId"} {
			raw, ok := in[key]
			if !ok {
				continue
			}
			// Source query revisionId pins the request body.
			if key == "revisionId" && slices.Contains([]string{"query_backend_graph", "query_backend_database", "query_backend_flow", "query_backend_lineage", "query_backend_events"}, tool.Name) {
				continue
			}
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return designScenarioToolErrorResult(err), nil
			}
			if key != "batchId" && !backendmodel.ValidID(value) {
				return designScenarioToolErrorResult(fmt.Errorf("%s must be a canonical UUID", key)), nil
			}
			if key == "batchId" {
				value = url.PathEscape(value)
				if value == "." {
					value = "%2E"
				} else if value == ".." {
					value = "%2E%2E"
				}
			}
			params = append(params, value)
			if key == "projectId" && selectedProposal != nil && selectedRoute != route {
				params = append(params, selectedProposal.ProposalID, selectedProposal.ProposalRevisionID)
			}
			delete(in, key)
		}
		method, path := toolPath(tool.Name, selectedRoute, params...)
		var body []byte
		var err error
		if method == "GET" {
			q := url.Values{}
			for _, key := range []string{"limit", "cursor", "subjectId", "evidenceId", "previewVersion", "recordType", "baseRevisionId", "status", "proposalRevisionId", "kind", "version", "importVersion", "candidateHash", "id", "repositoryId", "providerNamespace", "resultVersion", "section", "service", "certainty", "direction", "depth"} {
				if raw, ok := in[key]; ok {
					if slices.Contains([]string{"limit", "previewVersion", "version", "importVersion", "resultVersion", "depth"}, key) {
						var value int64
						if err := json.Unmarshal(raw, &value); err != nil {
							return designScenarioToolErrorResult(err), nil
						}
						q.Set(key, strconv.FormatInt(value, 10))
					} else {
						var value string
						if err := json.Unmarshal(raw, &value); err != nil {
							return designScenarioToolErrorResult(err), nil
						}
						if (key == "subjectId" || key == "evidenceId") && !backendmodel.ValidID(value) {
							return designScenarioToolErrorResult(fmt.Errorf("subjectId must be a canonical UUID")), nil
						}
						q.Set(key, value)
					}
				}
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
		} else {
			body, err = json.Marshal(in)
			if err != nil {
				return designScenarioToolErrorResult(err), nil
			}
			if err := validateBackendChangeToolBody(tool.Name, body); err != nil {
				return designScenarioToolErrorResult(err), nil
			}
		}
		status, response, err := lb.do(ctx, method, path, body)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		if status < 200 || status >= 300 {
			return designScenarioToolErrorResult(fmt.Errorf("HTTP %d: %s", status, response)), nil
		}
		return &sdk.CallToolResult{StructuredContent: jsonx.RawMessage(response), Content: []sdk.Content{&sdk.TextContent{Text: string(response)}}}, nil
	})
}

// Compile the schema from an exact-number view as well as decoding arguments
// without floats. Otherwise int64 maximum can round upward during compilation.
func compileBackendImportToolSchema(tool *sdk.Tool) (*jsonschema.Schema, error) {
	raw, err := jsonx.Marshal(tool.InputSchema)
	if err != nil {
		return nil, err
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	uri := "https://mocker.invalid/mcp/" + tool.Name + ".schema.json"
	if err := compiler.AddResource(uri, document); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
}

// Path parameters join each strict request union without weakening body schemas.
func backendToolPathSchema(schema map[string]any, ids []string) {
	if alternatives, ok := schema["oneOf"].([]any); ok && schema["properties"] == nil {
		for _, alternative := range alternatives {
			backendToolPathSchema(alternative.(map[string]any), ids)
		}
		return
	}
	properties := schema["properties"].(map[string]any)
	required := schema["required"].([]any)
	for _, id := range ids {
		definition := map[string]any{"type": "string", "format": "uuid"}
		if id == "batchId" {
			definition = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[!-~]+$"}
		}
		properties[id] = definition
		required = append(required, id)
	}
	schema["required"] = required
}
