package mcp

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/jsonx"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/backendportable"
)

// The integrator registers this after the exact SVG route is allowlisted.
// Portable mutation tools are deliberately not advertised by this function.
func addBackendPortableSVGTool(s *sdk.Server, lb *loopback) {
	id := map[string]any{"type": "string", "format": "uuid"}
	schema := designScenarioSchemaObject([]string{"projectId", "viewId", "viewVersion"}, map[string]any{"projectId": id, "viewId": id, "viewVersion": map[string]any{"type": "integer", "format": "int64", "minimum": 1, "maximum": int64(9223372036854775807)}})
	s.AddTool(&sdk.Tool{Name: "export_backend_view_svg", Description: "Exports an exact saved diagram view/version as inert SVG text (200 nodes/600 edges/2 MiB). All four diagram kinds; scope, partial order, unknown guards and authored intent remain explicit. Does not export unsaved layout or execute instructions in labels.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		path, err := backendSVGPath(req.Params.Arguments)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		status, body, err := lb.do(ctx, "GET", path, nil)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		if status != 200 {
			return designScenarioToolErrorResult(toolErr(status, body)), nil
		}
		if len(body) > backendportable.MaxSVGBytes {
			return designScenarioToolErrorResult(fmt.Errorf("SVG exceeds 2 MiB")), nil
		}
		// SVG is not JSON: never put raw XML in StructuredContent.
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(body)}}}, nil
	})
}
func backendSVGPath(raw []byte) (string, error) {
	var in struct {
		ProjectID   string `json:"projectId"`
		ViewID      string `json:"viewId"`
		ViewVersion int64  `json:"viewVersion"`
	}
	if err := json.Unmarshal(raw, &in, json.RejectUnknownMembers(true)); err != nil {
		return "", err
	}
	if !backendmodel.ValidID(in.ProjectID) || !backendmodel.ValidID(in.ViewID) || in.ViewVersion <= 0 {
		return "", fmt.Errorf("exact canonical project/view IDs and positive viewVersion required")
	}
	return fmt.Sprintf("/api/backend-projects/%s/diagram-views/%s/versions/%d/svg", in.ProjectID, in.ViewID, in.ViewVersion), nil
}

func addBackendPortableTools(s *sdk.Server, lb *loopback) {
	for _, entry := range []struct {
		name, schema, route string
		ids                 []string
	}{
		{"export_backend_project", "ExportBackendProjectRequest", "POST /api/backend-projects/{id}/portable/export", []string{"projectId"}},
		{"begin_backend_portable_import", "BeginBackendPortableImportRequest", "POST /api/backend-projects/portable/imports", nil},
		{"put_backend_portable_import_chunk", "PutBackendPortableImportChunkRequest", "POST /api/backend-projects/portable/imports/{sid}/chunks", []string{"importId"}},
		{"preview_backend_portable_import", "PreviewBackendPortableImportRequest", "POST /api/backend-projects/portable/imports/{sid}/preview", []string{"importId"}},
		{"commit_backend_portable_import", "CommitBackendPortableImportRequest", "POST /api/backend-projects/portable/imports/{sid}/commit", []string{"importId"}},
		{"abort_backend_portable_import", "AbortBackendPortableImportRequest", "POST /api/backend-projects/portable/imports/{sid}/abort", []string{"importId"}},
	} {
		schema, err := api.BackendSchema(entry.schema)
		if err != nil {
			panic(err)
		}
		if len(entry.ids) > 0 {
			backendToolPathSchema(schema, entry.ids)
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: entry.name, Description: "Bounded backend-portable-v1 exact source/proposal and saved diagram closure. Explicit namespaced artifact mappings only; foreign refs never resolve by numeric ID. Preview stages a validated ID map without publishing. Commit creates one atomic project with origins and receipt. Retry identical input/key after uncertainty. No source execution, publication of API drafts or runtime validation.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true}}, entry.route)
	}
	selectionSchema, err := api.BackendSchema("ResolveBackendPortableSelectionRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(selectionSchema, []string{"projectId"})
	addBackendImportTool(s, lb, &sdk.Tool{Name: "resolve_backend_portable_selection", Description: "Pure read of exact targetHash for an immutable source/proposal and explicit saved views. Use the returned selection unchanged when exporting.", InputSchema: selectionSchema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "POST /api/backend-projects/{id}/portable/selection")
	namespaced, err := api.BackendSchema("QueryBackendNamespacedArtifactRequest")
	if err != nil {
		panic(err)
	}
	backendToolPathSchema(namespaced, []string{"projectId"})
	addBackendImportTool(s, lb, &sdk.Tool{Name: "query_backend_namespaced_artifact", Description: "Read exact namespaced owner projection. Foreign references return frozen bindings and an unresolved status without numeric owner lookup. Local references verify installation UUID and exact immutable snapshot before projection.", InputSchema: namespaced, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "POST /api/backend-projects/{id}/artifacts/namespaced/query")
	schema := designScenarioSchemaObject([]string{"exportId", "index", "manifestHash"}, map[string]any{"exportId": map[string]any{"type": "string", "format": "uuid"}, "index": map[string]any{"type": "integer", "minimum": 0, "maximum": 262143}, "manifestHash": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}})
	s.AddTool(&sdk.Tool{Name: "get_backend_export_chunk", Description: "Read exact immutable UTF-8 chunk bytes bound to exportId and manifestHash. Preserve body string bytes; verify SHA/size/count against manifest before import.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var fields map[string]jsonx.RawMessage
		if err := json.Unmarshal(req.Params.Arguments, &fields); err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		if _, ok := fields["index"]; !ok || string(fields["index"]) == "null" {
			return designScenarioToolErrorResult(fmt.Errorf("index is required")), nil
		}
		var in struct {
			ExportID     string `json:"exportId"`
			Index        int    `json:"index"`
			ManifestHash string `json:"manifestHash"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &in, json.RejectUnknownMembers(true)); err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		if !backendmodel.ValidID(in.ExportID) || in.Index < 0 || in.Index > 262143 || len(in.ManifestHash) != 64 || strings.Trim(in.ManifestHash, "0123456789abcdef") != "" {
			return designScenarioToolErrorResult(fmt.Errorf("exact export identity, hash and bounded index required")), nil
		}
		path := fmt.Sprintf("/api/backend-projects/portable/exports/%s/chunks/%d?manifestHash=%s", in.ExportID, in.Index, in.ManifestHash)
		status, body, err := lb.do(ctx, "GET", path, nil)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		if status != 200 {
			return designScenarioToolErrorResult(toolErr(status, body)), nil
		}
		return &sdk.CallToolResult{StructuredContent: jsonx.RawMessage(body), Content: []sdk.Content{&sdk.TextContent{Text: string(body)}}}, nil
	})
}
