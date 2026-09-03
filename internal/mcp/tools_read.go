// addReadTools registers the nine read-only tools of slice A2: get_workspace,
// list_endpoints, list_scenarios, get_scenario, list_checkpoints,
// get_session_directive, get_auth_preset, get_spec and list_spec_operations.
//
// Like tools_ops.go and tools_traffic.go, every tool here is an ADAPTER over
// the admin plane's own routes (§A6): it decodes the JSON the handler in
// internal/admin actually writes, through a PRIVATE wire struct that mirrors
// only the fields this tool needs — never the admin package's own
// (unexported) response types, which this package cannot import anyway.
// Every wire type below cites the handler and line it was read from.
//
// Two tools reuse types tools_traffic.go already declares rather than
// hand-copying them a second time: get_session_directive answers exactly
// GET .../session's own directive list, the same shape
// set_session_directive already decodes on its own GET/POST/DELETE calls, so
// it reuses sessionListWire, SessionDirectiveLine and toDirectiveLine
// verbatim — a second copy of that union-decoding logic would only be a
// second place for the two to drift. Several tools here also reuse
// ResponseShape and ListSizeView from tools_ops.go, for the same reason
// get_operation and this file both need "what shape is this response
// variant, never its pinned body" — one type, not a per-file copy of it.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// specOperationsDefaultLimit/specOperationsMaxLimit mirror
// internal/admin/spec_handlers.go's own defaultOperationsLimit (100) and
// maxOperationsLimit (500) exactly (both unexported in package admin, and
// this package cannot import them — §A6). Computed here, the same way the
// admin route would, so list_spec_operations can report an honest `limit`
// alongside `hasMore` — matching listTrafficDefaultLimit/MaxLimit's own
// comment in tools_traffic.go for the identical reason.
const (
	specOperationsDefaultLimit = 100
	specOperationsMaxLimit     = 500
)

// getScenarioMaxOverrides bounds how many of a scenario's saved override
// rows get_scenario returns inline. A scenario snapshot's Overrides list has
// no admin-side cap (scenario_handlers.go:82-91 answers every row the
// snapshot holds) and can grow with the workspace's whole operation surface,
// so this tool caps it the same way find_operations caps its own match list
// (tools_ops.go) — and reports OverridesTruncated exactly the way
// FindOperationsOutput.Truncated does, for the same reason: a capped list
// that does not say so reads as "this scenario touches nothing else", which
// is the wrong conclusion for an operator relying on this before activating
// it.
const getScenarioMaxOverrides = 50

func addReadTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "get_workspace",
		Description: "Reads one workspace's identity, addressable URL and base path, attached spec " +
			"and scenario ids, revision, and the response-shaping settings that apply workspace-wide " +
			"(seed, listSize, nullRate, delayMs, validateRequests, envelope). Use this to confirm a " +
			"workspace exists and inspect its current settings before editing it, and to read the exact " +
			"slug the eight destructive tools' confirmSlug argument checks against.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetWorkspace(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_endpoints",
		Description: "Lists every custom endpoint an operator has defined from scratch in a workspace " +
			"— method, path, whether it is on and routed, and each response's SHAPE (mode, media type, " +
			"whether a body is pinned, how many recipes are bound) but never a pinned body itself. Use " +
			"this to see what custom routes already exist before creating, updating or deleting one.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListEndpoints(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_scenarios",
		Description: "Lists every scenario saved for a workspace — id, name, creation time and whether " +
			"it is the one currently active — never the snapshot contents. Use this to find a " +
			"scenario's id before reading it with get_scenario or switching to it with activate_scenario.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListScenarios(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_scenario",
		Description: "Reads one saved scenario's snapshot: its recorded base path and core settings, " +
			"the spec it was snapshotted against, and the operations it overrides — each as method, " +
			"path and the SHAPE of its stored response, never a pinned body. The override list is capped " +
			"and says so via overridesReturned/overridesTotal/overridesTruncated, so a capped list is " +
			"never mistaken for a scenario that touches nothing else. Use this to see exactly what " +
			"activating a scenario would change before switching to it.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetScenario(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_checkpoints",
		Description: "Lists every checkpoint saved for a workspace's history, newest first — id, kind " +
			"(auto or manual), label and who created it — never the snapshot contents. Use this to find " +
			"a checkpoint's id before rolling back to it, or to see what an automatic checkpoint " +
			"captured before a destructive action.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListCheckpoints(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_session_directive",
		Description: "Reads every temporary, RAM-only directive currently forced on a workspace's live " +
			"session — pinned statuses, fail-next conditions, delays and pauses, with their targets and " +
			"settings. Use this to see what is currently overriding normal serving before setting a new " +
			"directive with set_session_directive or deciding whether to clear one.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetSessionDirective(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_auth_preset",
		Description: "Derives, without writing anything, a proposed set of auth-related recipe bindings " +
			"from a workspace's current spec: which operations look like login/token paths, which " +
			"recipe each would get and why, plus a sample JWT the workspace's current identity/auth " +
			"settings would mint. Use this to preview what apply_auth_preset would write before calling " +
			"it — a workspace with no spec attached still answers 200 with an empty proposal and a note " +
			"explaining why, never an error.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetAuthPreset(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_spec",
		Description: "Reads one imported OpenAPI spec's metadata by id — name, version, format, source, " +
			"base path and import hash — never the raw or normalized document body. Use this to confirm " +
			"a specId before attaching it to a workspace, or to check what format and base path a spec " +
			"actually imported with.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetSpec(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_spec_operations",
		Description: "Pages through the operations a spec declared at import time — method, path, " +
			"operationId, summary, tag and (for a degraded one) its parse error — independent of any " +
			"workspace. Use this to browse a spec's raw surface before creating a workspace on it; for " +
			"an already-running workspace's MERGED view (spec plus override state), use find_operations " +
			"or get_operation instead. Always check returned/limit/hasMore rather than assuming one call " +
			"returned every operation — this repository pages the same way the admin UI does, 100 by " +
			"default and 500 at most per call.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListSpecOperations(lb))
}

// ---- get_workspace ----

// GetWorkspaceInput is get_workspace's input.
type GetWorkspaceInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// WorkspaceDetail is get_workspace's projection of workspaceView
// (internal/admin/workspace_handlers.go:42-63). OwnerID and ForkedFrom are
// simply absent — DESIGN §15's own note atop that file (also cited in
// workspaceWire's comment, tools_ops.go) is that ownership is a LABEL, not
// an access boundary, so neither tells an agent anything actionable — and
// Settings.Identity/Auth/CORS/NotFoundBody are absent too: those are
// multi-field sub-objects this slice has no dedicated tool for editing
// (update_workspace_settings, D5, round-trips the whole settings object it
// fetched through its OWN request/response shape, not this one), so
// carrying them here would be weight with no tool on this side of the
// surface that does anything with them.
type WorkspaceDetail struct {
	ID               int64   `json:"id"`
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	URL              string  `json:"url"`
	BasePath         string  `json:"basePath"`
	SpecID           *int64  `json:"specId,omitempty"`
	ScenarioID       *int64  `json:"scenarioId,omitempty"`
	Revision         int64   `json:"revision"`
	Seed             int64   `json:"seed"`
	ListSize         int     `json:"listSize"`
	NullRate         float64 `json:"nullRate"`
	Envelope         *string `json:"envelope,omitempty"`
	ValidateRequests bool    `json:"validateRequests"`
	DelayMs          int     `json:"delayMs"`
	CreatedAt        int64   `json:"createdAt"`
	UpdatedAt        int64   `json:"updatedAt"`
	// EditVersion is A3's per-row compare-and-swap token (mocker-a3-cas
	// D5): get_workspace is the read that actually precedes
	// update_workspace_settings' write, so it is THIS projection that
	// grows the field — not WorkspaceLine/workspaceWire (tools_ops.go),
	// which belong to list_workspaces and create_workspace and would leave
	// the real read-before-write tool tokenless.
	EditVersion int64 `json:"editVersion"`
}

// GetWorkspaceOutput is get_workspace's declared output schema.
type GetWorkspaceOutput struct {
	Workspace WorkspaceDetail `json:"workspace"`
}

// workspaceGetWire decodes the fields of workspaceView
// (workspace_handlers.go:42-63) this tool projects — the same safe-subset
// shape workspaceWire (tools_ops.go) uses for list_workspaces, with the
// settings sub-object widened to the knobs WorkspaceDetail's own comment
// names.
type workspaceGetWire struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	SpecID      *int64 `json:"specId"`
	ScenarioID  *int64 `json:"scenarioId"`
	Revision    int64  `json:"revision"`
	URL         string `json:"url"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	EditVersion int64  `json:"editVersion"`
	Settings    struct {
		Seed             int64   `json:"seed"`
		BasePath         string  `json:"basePath"`
		ListSize         int     `json:"listSize"`
		NullRate         float64 `json:"nullRate"`
		Envelope         *string `json:"envelope"`
		ValidateRequests bool    `json:"validateRequests"`
		DelayMs          int     `json:"delayMs"`
	} `json:"settings"`
}

func handleGetWorkspace(lb *loopback) sdk.ToolHandlerFor[GetWorkspaceInput, GetWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetWorkspaceInput) (*sdk.CallToolResult, GetWorkspaceOutput, error) {
		var wire workspaceGetWire
		method, path := toolPath("get_workspace", "GET /api/workspaces/{id}", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, GetWorkspaceOutput{}, err
		}
		return nil, GetWorkspaceOutput{Workspace: WorkspaceDetail{
			ID:               wire.ID,
			Slug:             wire.Slug,
			Name:             wire.Name,
			URL:              wire.URL,
			BasePath:         wire.Settings.BasePath,
			SpecID:           wire.SpecID,
			ScenarioID:       wire.ScenarioID,
			Revision:         wire.Revision,
			Seed:             wire.Settings.Seed,
			ListSize:         wire.Settings.ListSize,
			NullRate:         wire.Settings.NullRate,
			Envelope:         wire.Settings.Envelope,
			ValidateRequests: wire.Settings.ValidateRequests,
			DelayMs:          wire.Settings.DelayMs,
			CreatedAt:        wire.CreatedAt,
			UpdatedAt:        wire.UpdatedAt,
			EditVersion:      wire.EditVersion,
		}}, nil
	}
}

// ---- list_endpoints ----

// ListEndpointsInput is list_endpoints' input.
type ListEndpointsInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// EndpointLine is one custom endpoint, projected from endpointView
// (internal/admin/endpoint_handlers.go:37-49) down to its response SHAPES —
// never a pinned body or recipe definitions, exactly the compaction
// get_operation's OverrideDetail already applies to op_overrides (tools_
// ops.go): a list an agent scans to decide WHICH endpoint to inspect next
// has no use for bytes it would only discard for every row but the one it
// picks.
type EndpointLine struct {
	ID            int64                    `json:"id"`
	Method        string                   `json:"method"`
	Path          string                   `json:"path"`
	CanonicalPath string                   `json:"canonicalPath"`
	OverrideOn    bool                     `json:"overrideOn"`
	RouteOff      bool                     `json:"routeOff"`
	ActiveStatus  int                      `json:"activeStatus"`
	Responses     map[string]ResponseShape `json:"responses,omitempty"`
	ListSize      *ListSizeView            `json:"listSize,omitempty"`
	DelayMs       *int                     `json:"delayMs,omitempty"`
	// Kind and Stream are P6b's (decisions.md mocker-p6b-sse-mock D3):
	// "http" or "sse"; Stream is the stream document of an sse row —
	// {timeline: {frames: [{delayMs, event, data}], loop}, tick:
	// {intervalMs, event, schema}, closeWhenDone} — and absent otherwise.
	Kind      string `json:"kind"`
	Stream    any    `json:"stream,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// EditVersion is A3's per-row compare-and-swap token (mocker-a3-cas
	// D5): endpointView is GET's list entry, POST's created-row response
	// AND PUT's write response all at once on the admin side (that
	// struct's own comment), so growing this one MCP projection covers
	// list_endpoints (the read that precedes update_endpoint's write),
	// create_endpoint's response and update_endpoint's response together —
	// unlike workspace/override/scenario, there is no separate detail type
	// to prefer here.
	EditVersion int64 `json:"editVersion"`
}

// ListEndpointsOutput is list_endpoints' declared output schema.
type ListEndpointsOutput struct {
	Endpoints []EndpointLine `json:"endpoints"`
}

// endpointWire decodes endpointView's fields (endpoint_handlers.go:37-49).
// Responses reuses variantDetailWire (tools_ops.go) — the same private
// mirror of overrides.Variant get_operation already decodes — rather than
// declaring a third copy of that shape in this file.
type endpointWire struct {
	ID            int64                        `json:"id"`
	Method        string                       `json:"method"`
	Path          string                       `json:"path"`
	CanonicalPath string                       `json:"canonicalPath"`
	OverrideOn    bool                         `json:"overrideOn"`
	RouteOff      bool                         `json:"routeOff"`
	ActiveStatus  int                          `json:"activeStatus"`
	Responses     map[string]variantDetailWire `json:"responses"`
	ListSize      *ListSizeView                `json:"listSize,omitempty"`
	DelayMs       *int                         `json:"delayMs,omitempty"`
	Kind          string                       `json:"kind"`
	Stream        any                          `json:"stream,omitempty"`
	CreatedAt     int64                        `json:"createdAt"`
	UpdatedAt     int64                        `json:"updatedAt"`
	EditVersion   int64                        `json:"editVersion"`
}

// endpointListWire decodes endpointListView (endpoint_handlers.go:69-71).
type endpointListWire struct {
	Endpoints []endpointWire `json:"endpoints"`
}

// responseShapes projects a decoded responses map down to ResponseShape per
// status — the same rule newOverrideDetail (tools_ops.go) applies to
// overrideDetailWire.Responses, reimplemented here rather than shared
// because the two callers decode different wire STRUCTS (endpointWire and,
// below, scenarioOverrideWire) even though both happen to carry the same
// map[string]variantDetailWire shape for Responses.
func responseShapes(w map[string]variantDetailWire) map[string]ResponseShape {
	out := make(map[string]ResponseShape, len(w))
	for status, v := range w {
		mode := v.Mode
		if mode == "" {
			mode = "generated" // overrides.Variant.Mode's own documented default (overrides.go:68).
		}
		out[status] = ResponseShape{
			Mode:        mode,
			MediaType:   v.MediaType,
			HasBody:     len(v.Body) > 0,
			RecipeCount: len(v.Recipes),
		}
	}
	return out
}

func handleListEndpoints(lb *loopback) sdk.ToolHandlerFor[ListEndpointsInput, ListEndpointsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListEndpointsInput) (*sdk.CallToolResult, ListEndpointsOutput, error) {
		var wire endpointListWire
		method, path := toolPath("list_endpoints", "GET /api/workspaces/{id}/endpoints", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListEndpointsOutput{}, err
		}
		// ONE projection for every tool that answers an endpoint —
		// toEndpointLine (tools_endpoints.go) — rather than a second
		// field list here: P6b's smoke run found this loop had grown its
		// own copy and shipped list_endpoints without kind/stream while
		// create_endpoint and update_endpoint carried them.
		out := make([]EndpointLine, len(wire.Endpoints))
		for i, e := range wire.Endpoints {
			out[i] = toEndpointLine(e)
		}
		return nil, ListEndpointsOutput{Endpoints: out}, nil
	}
}

// ---- list_scenarios ----

// ListScenariosInput is list_scenarios' input.
type ListScenariosInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// ScenarioSummaryLine is one scenario, projected from scenarioSummaryView
// (internal/admin/scenario_handlers.go:43-48) — id, name, creation time and
// active flag, never a snapshot (that view is deliberately different from
// scenarioDetailView, per that file's own comment on scenarioListView).
type ScenarioSummaryLine struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	IsActive  bool   `json:"isActive"`
}

// ListScenariosOutput is list_scenarios' declared output schema.
type ListScenariosOutput struct {
	Scenarios []ScenarioSummaryLine `json:"scenarios"`
}

// scenarioSummaryWire decodes scenarioSummaryView (scenario_handlers.go:43-48).
type scenarioSummaryWire struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	IsActive  bool   `json:"isActive"`
}

// scenarioListWire decodes scenarioListView (scenario_handlers.go:60-62).
type scenarioListWire struct {
	Scenarios []scenarioSummaryWire `json:"scenarios"`
}

func handleListScenarios(lb *loopback) sdk.ToolHandlerFor[ListScenariosInput, ListScenariosOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListScenariosInput) (*sdk.CallToolResult, ListScenariosOutput, error) {
		var wire scenarioListWire
		method, path := toolPath("list_scenarios", "GET /api/workspaces/{id}/scenarios", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListScenariosOutput{}, err
		}
		out := make([]ScenarioSummaryLine, len(wire.Scenarios))
		for i, sc := range wire.Scenarios {
			// scenarioSummaryWire and ScenarioSummaryLine share an identical
			// field set, so this is a plain type conversion rather than a
			// field-by-field copy (staticcheck S1016) — the same move
			// handleGetOperation makes for mergedStatusWire -> MergedStatus
			// (tools_ops.go).
			out[i] = ScenarioSummaryLine(sc)
		}
		return nil, ListScenariosOutput{Scenarios: out}, nil
	}
}

// ---- get_scenario ----

// GetScenarioInput is get_scenario's input.
type GetScenarioInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	ScenarioID  int64 `json:"scenarioId"`
}

// ScenarioOverrideLine is one entry of a scenario snapshot's Overrides list,
// projected exactly the way EndpointLine projects a custom endpoint's own
// responses: method, path, on/off state and each response's SHAPE, never a
// pinned body, header set or recipe definitions.
type ScenarioOverrideLine struct {
	Method       string                   `json:"method"`
	Path         string                   `json:"path"`
	OverrideOn   bool                     `json:"overrideOn"`
	RouteOff     bool                     `json:"routeOff"`
	ActiveStatus *int                     `json:"activeStatus,omitempty"`
	Responses    map[string]ResponseShape `json:"responses,omitempty"`
	ListSize     *ListSizeView            `json:"listSize,omitempty"`
	DelayMs      *int                     `json:"delayMs,omitempty"`
	ValidateReq  *bool                    `json:"validateReq,omitempty"`
}

// ScenarioDetail is get_scenario's projection of scenarioDetailView
// (scenario_handlers.go:82-91). Spec.Inline is absent — it is the spec's own
// full document JSON, only ever non-null for P4's portable export (that
// file's own comment), and even a metadata-only Spec.Hash/Name pair already
// answers what this tool's Description promises ("the spec it was
// snapshotted against"). Settings is narrowed to the same knobs
// WorkspaceDetail carries, for the same reason that comment gives —
// Identity/Auth/CORS/NotFoundBody have no tool on this surface that acts on
// them yet.
type ScenarioDetail struct {
	ID                 int64                  `json:"id"`
	Name               string                 `json:"name"`
	CreatedAt          int64                  `json:"createdAt"`
	IsActive           bool                   `json:"isActive"`
	BasePath           string                 `json:"basePath"`
	Seed               int64                  `json:"seed"`
	ListSize           int                    `json:"listSize"`
	NullRate           float64                `json:"nullRate"`
	ValidateRequests   bool                   `json:"validateRequests"`
	DelayMs            int                    `json:"delayMs"`
	SpecName           string                 `json:"specName"`
	SpecHash           string                 `json:"specHash"`
	Overrides          []ScenarioOverrideLine `json:"overrides"`
	OverridesReturned  int                    `json:"overridesReturned"`
	OverridesTotal     int                    `json:"overridesTotal"`
	OverridesTruncated bool                   `json:"overridesTruncated"`
	// EditVersion is A3's per-row compare-and-swap token (mocker-a3-cas
	// D5): scenarioDetailView is BOTH GET .../scenarios/{sid}'s read and
	// PUT (rename)'s write response on the admin side (that view's own
	// comment), so get_scenario is the read that actually precedes
	// rename_scenario's write — not list_scenarios' ScenarioSummaryLine,
	// which this tool's own sibling comment leaves untouched for the same
	// reason WorkspaceLine stays untouched.
	EditVersion int64 `json:"editVersion"`
}

// GetScenarioOutput is get_scenario's declared output schema.
type GetScenarioOutput struct {
	Scenario ScenarioDetail `json:"scenario"`
}

// scenarioSpecWire decodes the two scenarioSpecView fields
// (scenario_handlers.go:65-72) this tool projects — Inline is absent, see
// ScenarioDetail's own comment.
type scenarioSpecWire struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
}

// scenarioOverrideWire decodes one entry of bundle.OverrideEntry
// (internal/bundle/bundle.go:184-221) as scenarioDetailView.Overrides
// carries it. FailDirective is absent — it is PRESERVED-ONLY on the admin
// side (that field's own doc comment: "this slice's evaluator … does not
// interpret it"), so surfacing it here would show an agent a raw directive
// document nothing in this build reads. Responses reuses variantDetailWire
// (tools_ops.go), the same mirror endpointWire uses above, since
// bundle.OverrideEntry.Responses is the identical map[string]overrides.
// Variant shape.
type scenarioOverrideWire struct {
	Method       string                       `json:"method"`
	Path         string                       `json:"path"`
	OverrideOn   bool                         `json:"overrideOn"`
	RouteOff     bool                         `json:"routeOff"`
	ActiveStatus *int                         `json:"activeStatus,omitempty"`
	Responses    map[string]variantDetailWire `json:"responses"`
	ListSize     *ListSizeView                `json:"listSize,omitempty"`
	DelayMs      *int                         `json:"delayMs,omitempty"`
	ValidateReq  *bool                        `json:"validateReq,omitempty"`
}

// scenarioDetailWire decodes scenarioDetailView (scenario_handlers.go:82-91)
// down to the fields ScenarioDetail projects. Settings is narrowed to the
// same knobs workspaceGetWire decodes, for the same reason.
type scenarioDetailWire struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	IsActive  bool   `json:"isActive"`
	BasePath  string `json:"basePath"`
	Settings  struct {
		Seed             int64   `json:"seed"`
		ListSize         int     `json:"listSize"`
		NullRate         float64 `json:"nullRate"`
		ValidateRequests bool    `json:"validateRequests"`
		DelayMs          int     `json:"delayMs"`
	} `json:"settings"`
	Spec        scenarioSpecWire       `json:"spec"`
	Overrides   []scenarioOverrideWire `json:"overrides"`
	EditVersion int64                  `json:"editVersion"`
}

func handleGetScenario(lb *loopback) sdk.ToolHandlerFor[GetScenarioInput, GetScenarioOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetScenarioInput) (*sdk.CallToolResult, GetScenarioOutput, error) {
		var wire scenarioDetailWire
		method, path := toolPath("get_scenario", "GET /api/workspaces/{id}/scenarios/{sid}", in.WorkspaceID, in.ScenarioID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, GetScenarioOutput{}, err
		}

		total := len(wire.Overrides)
		returned := total
		if returned > getScenarioMaxOverrides {
			returned = getScenarioMaxOverrides
		}
		lines := make([]ScenarioOverrideLine, returned)
		for i := range returned {
			ov := wire.Overrides[i]
			lines[i] = ScenarioOverrideLine{
				Method:       ov.Method,
				Path:         ov.Path,
				OverrideOn:   ov.OverrideOn,
				RouteOff:     ov.RouteOff,
				ActiveStatus: ov.ActiveStatus,
				Responses:    responseShapes(ov.Responses),
				ListSize:     ov.ListSize,
				DelayMs:      ov.DelayMs,
				ValidateReq:  ov.ValidateReq,
			}
		}

		return nil, GetScenarioOutput{Scenario: ScenarioDetail{
			ID:                 wire.ID,
			Name:               wire.Name,
			CreatedAt:          wire.CreatedAt,
			IsActive:           wire.IsActive,
			BasePath:           wire.BasePath,
			Seed:               wire.Settings.Seed,
			ListSize:           wire.Settings.ListSize,
			NullRate:           wire.Settings.NullRate,
			ValidateRequests:   wire.Settings.ValidateRequests,
			DelayMs:            wire.Settings.DelayMs,
			SpecName:           wire.Spec.Name,
			SpecHash:           wire.Spec.Hash,
			Overrides:          lines,
			OverridesReturned:  returned,
			OverridesTotal:     total,
			OverridesTruncated: returned < total,
			EditVersion:        wire.EditVersion,
		}}, nil
	}
}

// ---- list_checkpoints ----

// ListCheckpointsInput is list_checkpoints' input.
type ListCheckpointsInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// CheckpointLine is one checkpoint, projected from checkpointSummaryView
// (internal/admin/checkpoint_handlers.go:32-42) — never the snapshot itself
// (that file's own comment on checkpointSummaryView: a list endpoint
// answering every checkpoint's full blob would be a page-load cost that
// grows with history, the identical reasoning scenarioSummaryView's comment
// gives).
type CheckpointLine struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy *int64 `json:"createdBy,omitempty"`
}

// ListCheckpointsOutput is list_checkpoints' declared output schema.
type ListCheckpointsOutput struct {
	Checkpoints []CheckpointLine `json:"checkpoints"`
}

// checkpointSummaryWire decodes checkpointSummaryView (checkpoint_handlers.go:32-42).
type checkpointSummaryWire struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy *int64 `json:"createdBy"`
}

// checkpointListWire decodes checkpointListView (checkpoint_handlers.go:58-60).
type checkpointListWire struct {
	Checkpoints []checkpointSummaryWire `json:"checkpoints"`
}

func handleListCheckpoints(lb *loopback) sdk.ToolHandlerFor[ListCheckpointsInput, ListCheckpointsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListCheckpointsInput) (*sdk.CallToolResult, ListCheckpointsOutput, error) {
		var wire checkpointListWire
		method, path := toolPath("list_checkpoints", "GET /api/workspaces/{id}/checkpoints", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListCheckpointsOutput{}, err
		}
		out := make([]CheckpointLine, len(wire.Checkpoints))
		for i, c := range wire.Checkpoints {
			// Plain type conversion — see ScenarioSummaryLine's identical
			// comment above on why (staticcheck S1016).
			out[i] = CheckpointLine(c)
		}
		return nil, ListCheckpointsOutput{Checkpoints: out}, nil
	}
}

// ---- get_session_directive ----

// GetSessionDirectiveInput is get_session_directive's input.
type GetSessionDirectiveInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// GetSessionDirectiveOutput is get_session_directive's declared output
// schema — the same Directives shape SetSessionDirectiveOutput carries
// (tools_traffic.go), reused rather than redeclared: GET .../session and a
// successful POST both answer sessionListView (livestate_handlers.go:80-82),
// so decoding it a second way here would only be a second thing that could
// silently stop matching the first.
type GetSessionDirectiveOutput struct {
	Directives []SessionDirectiveLine `json:"directives"`
}

func handleGetSessionDirective(lb *loopback) sdk.ToolHandlerFor[GetSessionDirectiveInput, GetSessionDirectiveOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetSessionDirectiveInput) (*sdk.CallToolResult, GetSessionDirectiveOutput, error) {
		var wire sessionListWire
		method, path := toolPath("get_session_directive", "GET /api/workspaces/{id}/session", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, GetSessionDirectiveOutput{}, err
		}
		lines := make([]SessionDirectiveLine, len(wire.Directives))
		for i, d := range wire.Directives {
			line, err := toDirectiveLine(d)
			if err != nil {
				return nil, GetSessionDirectiveOutput{}, err
			}
			lines[i] = line
		}
		return nil, GetSessionDirectiveOutput{Directives: lines}, nil
	}
}

// ---- get_auth_preset ----

// GetAuthPresetInput is get_auth_preset's input.
type GetAuthPresetInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// AuthPresetBinding is one proposed binding, projected from
// authpreset.Binding (internal/authpreset/authpreset.go:46-54). Recipe is
// kept as raw JSON rather than expanded field-by-field through a private
// mirror of recipes.Recipe — the same round-trip-whole rationale
// overridePutBody's own comment gives (tools_ops.go): apply_auth_preset (D5)
// is the tool that would resend a caller-edited binding list, and decoding
// Recipe through a lossy intermediate type here would risk this tool
// silently dropping a field that round trip depends on.
type AuthPresetBinding struct {
	Method   string           `json:"method"`
	Path     string           `json:"path"`
	Status   int              `json:"status"`
	DataPath string           `json:"dataPath"`
	Recipe   jsonx.RawMessage `json:"recipe"`
	Reason   string           `json:"reason"`
	Source   string           `json:"source"`
}

// GetAuthPresetOutput is get_auth_preset's declared output schema — the same
// four fields authpreset.Proposal (authpreset.go:58-64) carries besides
// Bindings, passed straight through: this IS the preview, so trimming
// Schemes/AuthPaths/Notes would hide the "what was skipped and why" text §10
// requires an operator be able to see before approving.
type GetAuthPresetOutput struct {
	Bindings  []AuthPresetBinding `json:"bindings"`
	Schemes   []string            `json:"schemes,omitempty"`
	AuthPaths []string            `json:"authPaths,omitempty"`
	Notes     []string            `json:"notes,omitempty"`
	SampleJWT string              `json:"sampleJwt"`
	// EditVersions is A3's per-row compare-and-swap expectation for
	// apply_auth_preset (mocker-a3-cas D5/D12): one entry per opKey the
	// bindings above resolve to, 0 for an operation with no override row
	// yet. Forward this map (unedited, or narrowed to whatever subset of
	// Bindings you actually submit) as apply_auth_preset's own required
	// editVersions argument.
	EditVersions map[string]int64 `json:"editVersions"`
}

// authPresetBindingWire decodes authpreset.Binding (authpreset.go:46-54).
type authPresetBindingWire struct {
	Method   string           `json:"method"`
	Path     string           `json:"path"`
	Status   int              `json:"status"`
	DataPath string           `json:"dataPath"`
	Recipe   jsonx.RawMessage `json:"recipe"`
	Reason   string           `json:"reason"`
	Source   string           `json:"source"`
}

// authPresetWire decodes authpreset.Proposal (authpreset.go:58-64) — GET
// .../auth-preset's whole response body (preset_handlers.go:37-79).
type authPresetWire struct {
	Bindings     []authPresetBindingWire `json:"bindings"`
	Schemes      []string                `json:"schemes"`
	AuthPaths    []string                `json:"authPaths"`
	Notes        []string                `json:"notes"`
	SampleJWT    string                  `json:"sampleJwt"`
	EditVersions map[string]int64        `json:"editVersions"`
}

func handleGetAuthPreset(lb *loopback) sdk.ToolHandlerFor[GetAuthPresetInput, GetAuthPresetOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetAuthPresetInput) (*sdk.CallToolResult, GetAuthPresetOutput, error) {
		var wire authPresetWire
		method, path := toolPath("get_auth_preset", "GET /api/workspaces/{id}/auth-preset", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, GetAuthPresetOutput{}, err
		}
		// authPresetBindingWire and AuthPresetBinding share an identical
		// field set, but Go's tag-ignoring conversion rule for two named
		// struct types does not extend to a bulk []T1([]T2) slice
		// conversion — only a per-element conversion compiles, unlike the
		// single-value conversions elsewhere in this package (e.g.
		// ScenarioSummaryLine(sc) above).
		bindings := make([]AuthPresetBinding, len(wire.Bindings))
		for i, b := range wire.Bindings {
			bindings[i] = AuthPresetBinding(b)
		}
		return nil, GetAuthPresetOutput{
			Bindings:     bindings,
			Schemes:      wire.Schemes,
			AuthPaths:    wire.AuthPaths,
			Notes:        wire.Notes,
			SampleJWT:    wire.SampleJWT,
			EditVersions: wire.EditVersions,
		}, nil
	}
}

// ---- get_spec ----

// GetSpecInput is get_spec's input.
type GetSpecInput struct {
	SpecID int64 `json:"specId"`
}

// SpecDetail is get_spec's projection of specView
// (internal/admin/spec_handlers.go:38-49) — every field that view carries;
// unlike SpecLine (tools_ops.go, list_specs), this is a single-item detail
// call, so there is no per-row compaction cost to trimming Source/SourceRef/
// Hash/CreatedAt/CreatedBy the way a list would need.
type SpecDetail struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Format    string `json:"format"`
	Source    string `json:"source"`
	SourceRef string `json:"sourceRef,omitempty"`
	BasePath  string `json:"basePath"`
	Hash      string `json:"hash"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy *int64 `json:"createdBy,omitempty"`
}

// GetSpecOutput is get_spec's declared output schema.
type GetSpecOutput struct {
	Spec SpecDetail `json:"spec"`
}

// specDetailWire decodes specView (spec_handlers.go:38-49).
type specDetailWire struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Format    string `json:"format"`
	Source    string `json:"source"`
	SourceRef string `json:"sourceRef"`
	BasePath  string `json:"basePath"`
	Hash      string `json:"hash"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy *int64 `json:"createdBy"`
}

func handleGetSpec(lb *loopback) sdk.ToolHandlerFor[GetSpecInput, GetSpecOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetSpecInput) (*sdk.CallToolResult, GetSpecOutput, error) {
		var wire specDetailWire
		method, path := toolPath("get_spec", "GET /api/specs/{id}", in.SpecID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, GetSpecOutput{}, err
		}
		// Plain type conversion — see ScenarioSummaryLine's identical
		// comment above on why (staticcheck S1016).
		return nil, GetSpecOutput{Spec: SpecDetail(wire)}, nil
	}
}

// ---- list_spec_operations ----

// ListSpecOperationsInput is list_spec_operations' input. Limit/Offset mirror
// GET .../operations' own query parameters (spec_handlers.go:308-360)
// verbatim, so a caller pages this route exactly the way the admin UI does.
type ListSpecOperationsInput struct {
	SpecID int64 `json:"specId"`
	Limit  int   `json:"limit,omitempty"`
	Offset int   `json:"offset,omitempty"`
}

// SpecOperationLine is one spec operation, projected from operationView
// (spec_handlers.go:121-133). SpecID is absent — every line came from the
// one specId the caller already supplied, so echoing it back on every row
// would be pure repetition — and SourceOrder is absent too: it is the row's
// position in the spec document, bookkeeping [specs.Repo] itself uses to
// order results, not a fact this tool's caller has a use for.
type SpecOperationLine struct {
	ID            int64   `json:"id"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	CanonicalPath string  `json:"canonicalPath"`
	OperationID   *string `json:"operationId,omitempty"`
	Summary       *string `json:"summary,omitempty"`
	Tag           *string `json:"tag,omitempty"`
	Pointer       string  `json:"pointer"`
	ParseError    *string `json:"parseError,omitempty"`
}

// ListSpecOperationsOutput is list_spec_operations' declared output schema.
// Unlike find_operations' FindOperationsOutput (tools_ops.go), Total is not
// obtainable here — GET .../operations pages in SQL and reports no count of
// what it did not send (matching ListTrafficOutput's own comment for the
// identical admin-side reason) — so this tool reports what IS knowable: the
// page it asked for and got, via Returned/Limit/Offset/HasMore.
type ListSpecOperationsOutput struct {
	Operations []SpecOperationLine `json:"operations"`
	Returned   int                 `json:"returned"`
	Limit      int                 `json:"limit"`
	Offset     int                 `json:"offset"`
	HasMore    bool                `json:"hasMore"`
}

// specOperationWire decodes operationView (spec_handlers.go:121-133) — GET
// .../operations' bare-array response body (spec_handlers.go:308-360; unlike
// every other list route in this package, it answers `[...]` directly, never
// wrapped in an object).
type specOperationWire struct {
	ID            int64   `json:"id"`
	SpecID        int64   `json:"specId"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	CanonicalPath string  `json:"canonicalPath"`
	OperationID   *string `json:"operationId"`
	Summary       *string `json:"summary"`
	Tag           *string `json:"tag"`
	SourceOrder   int64   `json:"sourceOrder"`
	Pointer       string  `json:"pointer"`
	ParseError    *string `json:"parseError"`
}

func handleListSpecOperations(lb *loopback) sdk.ToolHandlerFor[ListSpecOperationsInput, ListSpecOperationsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListSpecOperationsInput) (*sdk.CallToolResult, ListSpecOperationsOutput, error) {
		limit := specOperationsDefaultLimit
		if in.Limit > 0 {
			limit = in.Limit
		}
		if limit > specOperationsMaxLimit {
			limit = specOperationsMaxLimit
		}
		offset := in.Offset
		if offset < 0 {
			offset = 0
		}

		var wire []specOperationWire
		method, base := toolPath("list_spec_operations", "GET /api/specs/{id}/operations", in.SpecID)
		path := fmt.Sprintf("%s?limit=%d&offset=%d", base, limit, offset)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListSpecOperationsOutput{}, err
		}

		lines := make([]SpecOperationLine, len(wire))
		for i, op := range wire {
			lines[i] = SpecOperationLine{
				ID:            op.ID,
				Method:        op.Method,
				Path:          op.Path,
				CanonicalPath: op.CanonicalPath,
				OperationID:   op.OperationID,
				Summary:       op.Summary,
				Tag:           op.Tag,
				Pointer:       op.Pointer,
				ParseError:    op.ParseError,
			}
		}
		return nil, ListSpecOperationsOutput{
			Operations: lines,
			Returned:   len(lines),
			Limit:      limit,
			Offset:     offset,
			HasMore:    len(lines) == limit,
		}, nil
	}
}
