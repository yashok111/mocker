// tools_probe.go registers A4's first tool (decisions.md
// mocker-a4-mcp-reach D1.1, D5): probe_workspace, an adapter over the
// EXISTING POST /api/workspaces/{id}/probe — mocker dialling a workspace's
// own externally reachable health route
// (internal/admin/probe_handlers.go's handleProbeWorkspace, DESIGN §14
// screen 4's server-side «Проверить»). The route already exists and was
// already reachable from the admin UI; this slice's only change is that
// admin.mcpAllowedRoutes now allows CallAsMCP to dispatch to it (D9), and
// this file is the tool that reaches it.
//
// No new threat: internal/probe is the one outgoing HTTP client in the
// tree, and the target is assembled from the workspace record — a scheme
// off httpx.ForwardedProto's whitelist, a port httpx.RequestPort requires
// to be a bare decimal number — never from anything this tool's caller
// supplies. An agent calling this tool can reach exactly the hosts an
// operator clicking «Проверить» can reach, and no others.
//
// No confirmSlug (D5): that field guards verbs that destroy
// workspace-created data (decide_resource's declining branch,
// reset_resource_data, the six D6-era destructive tools of the mocker-a-mcp
// gate, and rollback_workspace's restoreData path). A probe destroys
// nothing — adding the field here would be decoration, and decoration on
// this one field would weaken what it means everywhere else it appears.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func addProbeTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "probe_workspace",
		Description: "Asks mocker itself to dial a workspace's own externally reachable URL " +
			"(scheme, host and port as an outside caller would reach it) at its reserved health " +
			"route, and reports what happened: \"ok\" (the health route answered 2xx with this " +
			"workspace's own slug and current revision), \"wrong-workspace\" (a 2xx health body " +
			"named a DIFFERENT workspace slug — routing points somewhere unexpected), \"http-error\" " +
			"(a non-2xx status, or a 2xx body that was not the health shape at all), \"timeout\", or " +
			"\"network-error\" (connection refused or unreachable). This is the same server-side check " +
			"DESIGN §14 screen 4's «Проверить» button performs — it reaches exactly the hosts an " +
			"operator clicking that button can reach, and no others: the target is assembled from the " +
			"workspace record and cfg.ReservedPrefix, never from anything this call supplies. " +
			"Use it to confirm a workspace is reachable from mocker's own network position before " +
			"telling a caller to point traffic at it, or to diagnose why a probe from elsewhere fails.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleProbeWorkspace(lb))
}

// ---- probe_workspace ----

// ProbeWorkspaceInput is probe_workspace's input.
type ProbeWorkspaceInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// ProbeWorkspaceOutput is probe_workspace's declared output schema,
// projected field-for-field from serverProbeView
// (internal/admin/probe_handlers.go) — the same five "kind" values that
// view documents, so a caller comparing this against a browser-side probe
// result reads one vocabulary rather than two. Only the fields that make
// sense for a given Kind are populated; the rest come back as Go zero
// values because serverProbeView itself omits them from the wire on that
// Kind (omitempty on the admin side), and jsonx decoding an absent JSON key
// into these fields leaves them at their zero value, exactly as intended.
type ProbeWorkspaceOutput struct {
	Kind      string `json:"kind"`
	Status    int    `json:"status,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Message   string `json:"message,omitempty"`
}

// probeWire decodes serverProbeView (internal/admin/probe_handlers.go) —
// POST .../probe's whole 200 body. This route always answers 200 once the
// workspace is found (handleProbeWorkspace's own doc comment): the
// TARGET's own failure is reported inside the body via Kind, never as this
// route's own HTTP status, so lb.call sees no non-2xx to turn into a tool
// error on that path — only a genuinely unknown workspace id (404) does.
type probeWire struct {
	Kind      string `json:"kind"`
	Status    int    `json:"status,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Message   string `json:"message,omitempty"`
}

func handleProbeWorkspace(lb *loopback) sdk.ToolHandlerFor[ProbeWorkspaceInput, ProbeWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ProbeWorkspaceInput) (*sdk.CallToolResult, ProbeWorkspaceOutput, error) {
		var wire probeWire
		method, path := toolPath("probe_workspace", "POST /api/workspaces/{id}/probe", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ProbeWorkspaceOutput{}, err
		}
		// probeWire and ProbeWorkspaceOutput share an identical field set —
		// a plain type conversion, the same move handleGetWorkspaceDrift
		// (tools_drift.go) and handleOverrideFromTraffic (tools_traffic.go)
		// already make for the identical reason (staticcheck S1016): the
		// compiler refuses to compile this line the day the two diverge.
		return nil, ProbeWorkspaceOutput(wire), nil
	}
}
