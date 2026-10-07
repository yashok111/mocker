package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/recordproxy"
)

type ProxyWorkspaceInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}
type ProxySaveInput struct {
	WorkspaceID int64              `json:"workspaceId"`
	Config      recordproxy.Config `json:"config"`
}
type ProxyDeleteInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	RecordingID int64 `json:"recordingId"`
	Version     int64 `json:"version"`
}
type ProxyClearInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Version     int64  `json:"version"`
	ConfirmSlug string `json:"confirmSlug"`
}
type ProxyViewOutput struct {
	Config         recordproxy.Config `json:"config"`
	AllowedOrigins []string           `json:"allowedOrigins"`
}
type ProxyRecordingsOutput struct {
	Items []map[string]any `json:"items"`
}
type ProxyMutationOutput struct {
	OK bool `json:"ok"`
}

func addProxyTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{Name: "get_workspace_proxy", Description: "Read installation-local upstream settings, mode, operation policies and config version. Empty allowedOrigins disables outbound HTTP. These controls are not exported or forked.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in ProxyWorkspaceInput) (*sdk.CallToolResult, ProxyViewOutput, error) {
		var out ProxyViewOutput
		if in.WorkspaceID <= 0 {
			return nil, out, fmt.Errorf("workspaceId must be positive")
		}
		method, path := toolPath("get_workspace_proxy", "GET /api/workspaces/{id}/proxy", in.WorkspaceID)
		err := lb.call(ctx, method, path, nil, &out)
		return nil, out, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "set_workspace_proxy", Description: "Replace live proxy configuration using version from get_workspace_proxy. Modes off/passthrough/record/replay; operations keyed METHOD /path/{parameter}. Replay never contacts upstream. Only allowlisted upstream origins are accepted. forwardAuth/forwardCookies are explicit opt-ins. captureEntities upserts redacted successful GET responses into confirmed families.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false}}, func(ctx context.Context, _ *sdk.CallToolRequest, in ProxySaveInput) (*sdk.CallToolResult, ProxyViewOutput, error) {
		var out ProxyViewOutput
		if in.WorkspaceID <= 0 {
			return nil, out, fmt.Errorf("workspaceId must be positive")
		}
		method, path := toolPath("set_workspace_proxy", "PUT /api/workspaces/{id}/proxy", in.WorkspaceID)
		body, err := jsonx.Marshal(in.Config)
		if err != nil {
			return nil, out, err
		}
		err = lb.call(ctx, method, path, body, &out)
		return nil, out, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "list_proxy_recordings", Description: "List up to 500 recorded JSON responses (32 MiB total), including redacted bodies. Request credentials, bodies and query values are never returned.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *sdk.CallToolRequest, in ProxyWorkspaceInput) (*sdk.CallToolResult, ProxyRecordingsOutput, error) {
		var out ProxyRecordingsOutput
		if in.WorkspaceID <= 0 {
			return nil, out, fmt.Errorf("workspaceId must be positive")
		}
		method, path := toolPath("list_proxy_recordings", "GET /api/workspaces/{id}/proxy/recordings", in.WorkspaceID)
		err := lb.call(ctx, method, path, nil, &out)
		return nil, out, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "delete_proxy_recording", Description: "Delete one recording and advance proxy config version. Reload get_workspace_proxy before another edit.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true)}}, func(ctx context.Context, _ *sdk.CallToolRequest, in ProxyDeleteInput) (*sdk.CallToolResult, ProxyMutationOutput, error) {
		var out ProxyMutationOutput
		if in.WorkspaceID <= 0 || in.RecordingID <= 0 {
			return nil, out, fmt.Errorf("workspaceId and recordingId must be positive")
		}
		method, path := toolPath("delete_proxy_recording", "DELETE /api/workspaces/{id}/proxy/recordings/{rid}", in.WorkspaceID, in.RecordingID)
		body, err := jsonx.Marshal(struct {
			Version int64 `json:"version"`
		}{in.Version})
		if err != nil {
			return nil, out, err
		}
		err = lb.call(ctx, method, path, body, &out)
		return nil, out, err
	})
	sdk.AddTool(s, &sdk.Tool{Name: "clear_proxy_recordings", Description: "Clear all recordings, confirming the workspace slug and current proxy config version. Does not remove captured entity rows. In-flight old recordings cannot reappear after clear.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true)}}, func(ctx context.Context, _ *sdk.CallToolRequest, in ProxyClearInput) (*sdk.CallToolResult, ProxyMutationOutput, error) {
		var out ProxyMutationOutput
		if in.WorkspaceID <= 0 || in.ConfirmSlug == "" {
			return nil, out, fmt.Errorf("workspaceId and confirmSlug required")
		}
		method, path := toolPath("clear_proxy_recordings", "POST /api/workspaces/{id}/proxy/recordings/clear", in.WorkspaceID)
		body, err := jsonx.Marshal(struct {
			Version     int64  `json:"version"`
			ConfirmSlug string `json:"confirmSlug"`
		}{in.Version, in.ConfirmSlug})
		if err != nil {
			return nil, out, err
		}
		err = lb.call(ctx, method, path, body, &out)
		return nil, out, err
	})
}
