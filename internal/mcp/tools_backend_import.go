package mcp

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

// Schemas match the strict OpenAPI request contracts, with explicit MCP path IDs.
func addBackendImportTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, description, schema string
		readOnly, idempotent             bool
	}{
		{"begin_backend_import", "POST /api/backend-projects/{id}/imports", "Begins an initial or explicitly reconciled source-backed import. Pin capabilities and the import guide before writing. Returns durable collecting session and source IDs; leaves the active revision unchanged. Initial mode refuses sourced heads; reconcile requires repositoryId and whole-foundation graphScope. Retry the identical idempotency key and payload.", `{"type":"object","additionalProperties":false,"required":["projectId","expectedVersion","baseRevisionId","idempotencyKey","manifest","inventory"],"properties":{"expectedVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"baseRevisionId":{"type":"string","format":"uuid"},"idempotencyKey":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[!-~]+$"},"manifest":{"type":"object","additionalProperties":false,"required":["repositoryName","provider","snapshot"],"properties":{"repositoryName":{"type":"string"},"provider":{"type":"object","additionalProperties":false,"required":["name","version","namespace","method","profiles","limitations"],"properties":{"name":{"type":"string"},"version":{"type":"string"},"namespace":{"type":"string"},"method":{"type":"string","enum":["ast","sql","orm","contract","agent","manual","trace","test"]},"profiles":{"type":"array","items":{"type":"string"}},"limitations":{"type":"array","items":{"type":"string"}}}},"snapshot":{"type":"object","additionalProperties":false,"required":["dirty","consistency","capturedAt","files"],"properties":{"commit":{"type":"string"},"dirty":{"type":"boolean"},"consistency":{"type":"string","enum":["verified","unverified"]},"capturedAt":{"type":"string","format":"date-time"},"files":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["path","contentHash","fileType","analysisStatus"],"properties":{"path":{"type":"string"},"contentHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"fileType":{"type":"string"},"analysisStatus":{"type":"string","enum":["analyzed","excluded","unsupported"]},"reason":{"type":"string"}}},"maxItems":100000}}}}},"inventory":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["category","status","knownCount","denominator","discoverySource","gaps","reason"],"properties":{"category":{"type":"string","enum":["files","endpoints","datastores","migrations","producers","consumers","jobs","contracts","tests"]},"status":{"type":"string","enum":["complete","partial","unsupported","excluded"]},"knownCount":{"type":"integer","format":"int64","minimum":0,"maximum":9223372036854775807},"denominator":{"type":["integer","null"],"format":"int64","minimum":0,"maximum":9223372036854775807},"discoverySource":{"type":"string"},"gaps":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"}}},"minItems":9,"maxItems":9},"mode":{"type":"string","enum":["initial","reconcile"]},"repositoryId":{"type":"string","format":"uuid"},"graphScope":{"type":"object","additionalProperties":false,"required":["profile","status","gaps"],"properties":{"profile":{"type":"string","enum":["foundation-graph-v1"]},"status":{"type":"string","enum":["complete","partial"]},"gaps":{"type":"array","items":{"type":"string"}}}},"projectId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["mode"],"properties":{"mode":{"const":"reconcile"}}},"then":{"required":["repositoryId","graphScope"]},"else":{"not":{"anyOf":[{"required":["repositoryId"]},{"required":["graphScope"]}]}}}]}`, false, true},
		{"compare_backend_revisions", "POST /api/backend-projects/{id}/revisions/compare", "Compares two exact immutable revisions with pinned paging and summary. Read-only; pins remain in the request body.", `{"type":"object","additionalProperties":false,"required":["projectId","fromRevisionId","toRevisionId"],"properties":{"fromRevisionId":{"type":"string","format":"uuid"},"toRevisionId":{"type":"string","format":"uuid"},"recordType":{"type":"string","enum":["node","edge","evidence","source","identity"]},"changeKind":{"type":"string","enum":["added","removed","modified","identity_mapped","freshness_changed"]},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"},"projectId":{"type":"string","format":"uuid"}}}`, true, true},
		{"get_backend_import_changes", "GET /api/backend-projects/{id}/imports/{iid}/changes", "Reads saved preview source changes and decisions at an exact previewVersion. Does not trigger preview.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","previewVersion","recordType"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"previewVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"recordType":{"type":"string","enum":["source","identity","deletion"]},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"}}}`, true, true},
		{"list_backend_imports", "GET /api/backend-projects/{id}/imports", "Lists durable import sessions with project-bound pagination.", `{"type":"object","additionalProperties":false,"required":["projectId"],"properties":{"projectId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string"}}}`, true, true},
		{"get_backend_import", "GET /api/backend-projects/{id}/imports/{iid}", "Reads a durable session and paginated accepted batch summaries. Replay a batch to recover identity mappings.", `{"type":"object","additionalProperties":false,"required":["projectId","importId"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500},"cursor":{"type":"string"}}}`, true, true},
		{"put_backend_import_batch", "PUT /api/backend-projects/{id}/imports/{iid}/batches/{bid}", "Stages commands with a deterministic payloadHash. Forward references may be repaired before preview. Exact batch replay returns the original receipt before CAS, including after commit or abort; changed bodies conflict.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","batchId","expectedImportVersion","payloadHash","commands"],"properties":{"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"payloadHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"commands":{"type":"array","items":{"oneOf":[{"type":"object","additionalProperties":false,"required":["op","node"],"properties":{"op":{"type":"string","const":"upsert_node"},"node":{"oneOf":[{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"system"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"service"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"module"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"external_system"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"datastore"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]},"technology":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"symbol"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]},"language":{"type":["string","null"]},"qualifiedName":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"http_operation"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":["method","path"],"properties":{"description":{"type":["string","null"]},"method":{"type":"string","pattern":"^[A-Z]+$","minLength":1},"path":{"type":"string","pattern":"^/","minLength":1}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"handler"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]},"language":{"type":["string","null"]},"qualifiedName":{"type":["string","null"]}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}},{"type":"object","additionalProperties":false,"required":["externalKey","kind","name","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","const":"unresolved_target"},"name":{"type":"string","minLength":1},"attributes":{"type":"object","additionalProperties":false,"required":["expectedKind","reason","searchScope"],"properties":{"description":{"type":["string","null"]},"expectedKind":{"type":"string","minLength":1},"reason":{"type":"string","minLength":1},"searchScope":{"type":"string","minLength":1}}},"parentKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}}]}}},{"type":"object","additionalProperties":false,"required":["op","edge"],"properties":{"op":{"type":"string","const":"upsert_edge"},"edge":{"type":"object","additionalProperties":false,"required":["externalKey","kind","fromKey","toKey","attributes","evidenceKeys"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"kind":{"type":"string","enum":["contains","handles","calls","derived_from"]},"fromKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"toKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"attributes":{"type":"object","additionalProperties":false,"required":[],"properties":{"description":{"type":["string","null"]}}},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}}}},{"type":"object","additionalProperties":false,"required":["op","evidence"],"properties":{"op":{"type":"string","const":"upsert_evidence"},"evidence":{"type":"object","additionalProperties":false,"required":["externalKey","method","status","source","explanation","subjectType","subjectKey"],"properties":{"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"propertyPath":{"type":"string"},"method":{"type":"string","enum":["ast","sql","orm","contract","agent","manual","trace","test"]},"status":{"type":"string","enum":["explicit","inferred","unresolved"]},"source":{"type":"object","additionalProperties":false,"required":["repositoryId","snapshotId","file","contentHash"],"properties":{"repositoryId":{"type":"string","format":"uuid"},"snapshotId":{"type":"string","format":"uuid"},"file":{"type":"string"},"contentHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"symbol":{"type":"string"},"startLine":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"endLine":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807}}},"explanation":{"type":"string"},"snippet":{"type":"string","description":"At most 4096 UTF-8 bytes; collector must remove secrets.","maxLength":4096},"subjectType":{"type":"string","enum":["node","edge"]},"subjectKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}}},{"type":"object","additionalProperties":false,"required":["op","remove"],"properties":{"op":{"type":"string","const":"remove"},"remove":{"type":"object","additionalProperties":false,"required":["recordType","externalKey"],"properties":{"recordType":{"type":"string","enum":["node","edge","evidence"]},"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}}},{"type":"object","additionalProperties":false,"required":["op","identity"],"properties":{"op":{"type":"string","const":"map_identity"},"identity":{"type":"object","additionalProperties":false,"required":["recordType","fromExternalKey","toExternalKey","expectedId","reason","evidenceKeys"],"properties":{"recordType":{"type":"string","enum":["node","edge"]},"fromExternalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"toExternalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"expectedId":{"type":"string","format":"uuid"},"reason":{"type":"string","minLength":1},"evidenceKeys":{"type":"array","items":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"}}}}}},{"type":"object","additionalProperties":false,"required":["op","deletion"],"properties":{"op":{"type":"string","const":"delete_assertion"},"deletion":{"type":"object","additionalProperties":false,"required":["recordType","externalKey","expectedId","reason"],"properties":{"recordType":{"type":"string","enum":["node","edge","evidence"]},"externalKey":{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\p{Cc}]+$"},"expectedId":{"type":"string","format":"uuid"},"reason":{"type":"string","minLength":1}}}}}]},"minItems":1,"maxItems":500},"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"batchId":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[!-~]+$"}}}`, false, true},
		{"preview_backend_import", "POST /api/backend-projects/{id}/imports/{iid}/preview", "Validates durable staging and increments its version. Returns ready plus a candidateHash or needs_resolution diagnostics. Explicit baseRevisionId pins the selected base; data changes invalidate preview.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","expectedImportVersion","baseRevisionId"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"baseRevisionId":{"type":"string","format":"uuid"}}}`, false, false},
		{"commit_backend_import", "POST /api/backend-projects/{id}/imports/{iid}/commit", "Atomically commits one immutable source revision using project/import CAS and the exact preview candidateHash. Replay the identical idempotency payload after an uncertain response; reconcile conflicts before retrying.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","expectedVersion","expectedImportVersion","candidateHash","idempotencyKey"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"expectedVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"candidateHash":{"type":"string","pattern":"^[a-f0-9]{64}$"},"idempotencyKey":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[!-~]+$"}}}`, false, true},
		{"abort_backend_import", "POST /api/backend-projects/{id}/imports/{iid}/abort", "Aborts durable staging with CAS and idempotency; active graph remains unchanged. A committed session cannot abort.", `{"type":"object","additionalProperties":false,"required":["projectId","importId","expectedImportVersion","idempotencyKey"],"properties":{"projectId":{"type":"string","format":"uuid"},"importId":{"type":"string","format":"uuid"},"expectedImportVersion":{"type":"integer","format":"int64","minimum":1,"maximum":9223372036854775807},"idempotencyKey":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[!-~]+$"}}}`, false, true},
		{"query_backend_graph", "POST /api/backend-projects/{id}/graph/query", "Queries a pinned immutable revision with UUID keyset pagination. Select nodes or edges; search/parentId apply only to nodes, from/to only to edges. No arbitrary query language or traversal.", `{"oneOf":[{"type":"object","additionalProperties":false,"required":["projectId","revisionId","recordType"],"properties":{"revisionId":{"type":"string","format":"uuid"},"recordType":{"const":"nodes","type":"string"},"kind":{"type":"string"},"search":{"type":"string"},"parentId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"},"id":{"type":"string","format":"uuid"},"projectId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["id"]},"then":{"not":{"anyOf":[{"required":["kind"]},{"required":["search"]},{"required":["parentId"]},{"required":["from"]},{"required":["to"]},{"required":["cursor"]}]}}}]},{"type":"object","additionalProperties":false,"required":["projectId","revisionId","recordType"],"properties":{"revisionId":{"type":"string","format":"uuid"},"recordType":{"const":"edges","type":"string"},"kind":{"type":"string"},"from":{"type":"string","format":"uuid"},"to":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500,"default":100},"cursor":{"type":"string"},"id":{"type":"string","format":"uuid"},"projectId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["id"]},"then":{"not":{"anyOf":[{"required":["kind"]},{"required":["search"]},{"required":["parentId"]},{"required":["from"]},{"required":["to"]},{"required":["cursor"]}]}}}]}],"type":"object"}`, true, true},
		{"get_backend_node", "GET /api/backend-projects/{id}/revisions/{rid}/nodes/{nid}", "Reads a node from the explicit revision. Query relationships separately and paginate evidence IDs.", `{"type":"object","additionalProperties":false,"required":["projectId","revisionId","nodeId"],"properties":{"projectId":{"type":"string","format":"uuid"},"revisionId":{"type":"string","format":"uuid"},"nodeId":{"type":"string","format":"uuid"}}}`, true, true},
		{"get_backend_evidence", "GET /api/backend-projects/{id}/revisions/{rid}/evidence", "Lists source evidence at a pinned revision, optionally by subjectId. Source locations are provider assertions; the server never reads local files.", `{"type":"object","additionalProperties":false,"required":["projectId","revisionId"],"properties":{"projectId":{"type":"string","format":"uuid"},"revisionId":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":500},"cursor":{"type":"string"},"subjectId":{"type":"string","format":"uuid"},"evidenceId":{"type":"string","format":"uuid"}},"allOf":[{"if":{"required":["evidenceId"]},"then":{"not":{"anyOf":[{"required":["subjectId"]},{"required":["cursor"]}]}}}]}`, true, true},
		{"get_backend_coverage", "GET /api/backend-projects/{id}/revisions/{rid}/coverage", "Reads inventory gaps, coverage and source snapshots at a pinned immutable revision. Unknown denominator and partial coverage do not claim execution or test coverage.", `{"type":"object","additionalProperties":false,"required":["projectId","revisionId"],"properties":{"projectId":{"type":"string","format":"uuid"},"revisionId":{"type":"string","format":"uuid"}}}`, true, true},
	} {
		var inputSchema map[string]any
		decoder := jsonx.NewDecoder(bytes.NewReader([]byte(spec.schema)))
		decoder.UseNumber()
		if err := decoder.Decode(&inputSchema); err != nil {
			panic(err)
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
		var params []any
		for _, key := range []string{"projectId", "importId", "revisionId", "nodeId", "batchId"} {
			raw, ok := in[key]
			if !ok {
				continue
			}
			// revisionId is a body pin for graph/query rather than a path parameter.
			if key == "revisionId" && tool.Name == "query_backend_graph" {
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
			delete(in, key)
		}
		method, path := toolPath(tool.Name, route, params...)
		var body []byte
		var err error
		if method == "GET" {
			q := url.Values{}
			for _, key := range []string{"limit", "cursor", "subjectId", "evidenceId", "previewVersion", "recordType"} {
				if raw, ok := in[key]; ok {
					if key == "limit" || key == "previewVersion" {
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
