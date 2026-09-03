// addResourceTools registers P3b's resource group (decisions.md
// mocker-p3b-resources, D7): list_resource_suggestions, list_resources,
// decide_resource and reset_resource_data — the four MCP-shaped adapters
// over P3a's three routes and P3b's own one that the MCP surface never got
// when P3a shipped them (CLAUDE.md's own "P3a: no MCP tool" carve-out,
// which this file discharges).
//
// Like every other file in this package, each tool here is an ADAPTER over
// the admin plane's own routes (internal/admin/resource_handlers.go): it
// decodes the JSON that package actually writes, through a PRIVATE wire
// struct that mirrors only the fields this tool needs, and reaches the
// domain only through loopback.call — never around it into
// internal/resources or internal/specs directly (§A6, the same rule every
// other tool in this package already holds).
//
// Three of the four are P3a backfills — list_resource_suggestions,
// list_resources and decide_resource wrap routes that existed before this
// slice — and the fourth, reset_resource_data, wraps the one route P3b
// itself adds. decide_resource's declining branch and reset_resource_data
// both carry a confirmSlug argument for the reason D6's six tools already
// do (confirm.go): on a flat tool surface it is easy to hit a neighbour,
// and a miss is a SUCCESSFUL call of the wrong tool, which no schema
// rejects. Both use confirmSlugDoc's exact wording, verbatim, the same way
// the six do — but NEITHER calls confirmWorkspaceSlug (confirm.go): the
// admin routes they wrap already compare the argument against the live
// workspace slug themselves (resources.Repo.Decline,
// resources.Repo.ResetData), inside the same transaction that would act on
// it, so a second client-side round trip here would only duplicate a check
// the route cannot be reached without. That is why neither tool's entry in
// toolRoutes below carries the extra "GET /api/workspaces/{id}" the six D6
// tools' entries do.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

func addResourceTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "list_resource_suggestions",
		Description: "Lists the route families a spec's import derived as candidate resources — each " +
			"one a route family whose GET (collection) and GET (detail) both exist, with the id field " +
			"and confidence import assigned it. A suggestion is not yet served from storage: use " +
			"decide_resource to confirm one before its family starts serving from entity rows instead " +
			"of the generator. Derivation runs once per spec and is cached; this tool never re-derives.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListResourceSuggestions(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_resources",
		Description: "Lists every route family a workspace's bound spec suggests, alongside its " +
			"decision state: undecided (decision is null), confirmed (with the resource id, id field, " +
			"write form and live entity count), or declined. A family the current spec no longer " +
			"suggests but that is still confirmed from an earlier spec is included too, as an orphan. " +
			"Use this before decide_resource or reset_resource_data to see what a workspace's resources " +
			"currently look like.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListResources(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "decide_resource",
		Description: "Confirms or declines one route family as a resource. Confirming populates it " +
			"deterministically from the workspace's current settings (seed, listSize) and switches its " +
			"GET routes — and, when its write form is bare, its POST and DELETE routes — to serve from " +
			"the stored rows instead of the generator. Declining a family that was never confirmed only " +
			"records the refusal; declining a CONFIRMED family deletes its resource row and every entity " +
			"row under it, and requires confirmSlug naming the workspace, for the same reason the six D6 " +
			"destructive tools do: a flat tool surface puts this beside near neighbours, and a successful " +
			"call aimed at the wrong workspace is the failure no input schema can reject. A confirmed " +
			"resource has no editor — a wrong one is declined and confirmed again, not patched.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleDecideResource(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "reset_resource_data",
		Description: "Changes a workspace's stored entity rows and NOTHING else — settings, operation " +
			"overrides and custom endpoints are untouched. mode \"reseed\" repopulates every confirmed, " +
			"reachable family from the workspace's CURRENT configuration, deleting what it holds now " +
			"first; mode \"clear\" deletes every entity row of the workspace and mints nothing back, " +
			"leaving every confirmed resource's collections empty. THIS IS IRREVERSIBLE THROUGH THIS CALL " +
			"ITSELF: unlike rollback_workspace and reset_overrides, reset_resource_data writes no " +
			"pre-destructive checkpoint of its own, so nothing automatically remembers the rows it " +
			"deletes or overwrites. rollback_workspace with restoreData:true can bring an affected " +
			"family's entity rows back, but only to how they stood at some EARLIER checkpoint that " +
			"happened to have one — never to a state this call itself preserved. A per-family failure " +
			"(a family that lost its route, a population that would exceed a storage cap, generation " +
			"itself failing, or a nested family skipped as a GROUP because a member of its whole " +
			"subtree — an ancestor or a sibling at any depth — failed for one of the first three " +
			"reasons: a family and every confirmed family nested under it, however deep, reseed " +
			"together or not at all, never a partial group, P3e D8.2/P3g) is reported in the response's skipped list rather " +
			"than refusing the whole call. Requires confirmSlug naming the workspace, checked live before " +
			"anything is deleted, and mode — both mandatory precisely because of what this call cannot " +
			"be undone.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleResetResourceData(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "rederive_suggestions",
		Description: "Re-runs family derivation over a spec's already-imported document and writes the " +
			"result as a new resource_suggestions generation, but only when it differs from the current " +
			"newest one — a no-op call changes nothing and reports changed:false. Spec-scoped, not " +
			"workspace-scoped: derivation is a function of the spec's own document, so one call changes " +
			"what EVERY workspace bound to it sees. Touches no workspace table at all — no resource, " +
			"decision, entity, checkpoint, scenario, override, custom endpoint or traffic row changes, no " +
			"workspace's revision bumps, and no checkpoint is taken — so it needs no confirmSlug. added " +
			"and removed are the diff of the two generations' route families, never a statement about what " +
			"any workspace has confirmed or declined: a family in removed may still be confirmed somewhere, " +
			"and the next reset_resource_data reseed will report it stranded.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleRederiveSuggestions(lb))
}

// ---- shared wire projection: one route family's decision state ----

// resourceFamilyWire decodes one row of resourceFamiliesView/
// resourceDecisionView (internal/admin/resource_handlers.go's
// resourceFamilyView) — GET /api/workspaces/{id}/resources' own row shape,
// and POST .../resource-decisions' single-row answer under "family".
type resourceFamilyWire struct {
	RouteFamily string  `json:"routeFamily"`
	Name        string  `json:"name"`
	Decision    *string `json:"decision"`
	ResourceID  *int64  `json:"resourceId"`
	IDField     *string `json:"idField"`
	WriteForm   *string `json:"writeForm"`
	EntityCount *int64  `json:"entityCount"`
	// ByBaseScope is D13's per-base-scope breakdown beside EntityCount
	// (admin's resourceFamilyView.ByBaseScope) — [ResourceBaseScopeCountLine]
	// itself, not a separate unexported wire element type: it is a plain
	// value type with nothing to hide behind an exported/unexported split,
	// and reusing it on both sides is what keeps the plain type conversion
	// below type-checking (a distinct element type on each side would break
	// it — see ResourceFamilyLine's own comment).
	ByBaseScope []ResourceBaseScopeCountLine `json:"byBaseScope"`
}

// ResourceBaseScopeCountLine is one element of ResourceFamilyLine's
// ByBaseScope (D13): baseScope is [resources.EncodeScope]'s own output for
// one declared base value, never re-decoded here (the one-owner rule, D3.1
// — EncodeScope is the only encoder and there is no inverse anywhere in this
// tree, this package included).
type ResourceBaseScopeCountLine struct {
	BaseScope   string `json:"baseScope"`
	EntityCount int64  `json:"entityCount"`
}

// ResourceFamilyLine is the wire projection both list_resources (per row)
// and decide_resource (the one row it changed) answer with. Field set is
// identical to resourceFamilyWire — a plain type conversion, the same move
// CheckpointLine's own comment (tools_history.go) already makes for the
// identical reason (staticcheck S1016).
type ResourceFamilyLine struct {
	RouteFamily string                       `json:"routeFamily"`
	Name        string                       `json:"name"`
	Decision    *string                      `json:"decision"`
	ResourceID  *int64                       `json:"resourceId"`
	IDField     *string                      `json:"idField"`
	WriteForm   *string                      `json:"writeForm"`
	EntityCount *int64                       `json:"entityCount"`
	ByBaseScope []ResourceBaseScopeCountLine `json:"byBaseScope"`
}

// ---- list_resource_suggestions ----

// resourceSuggestionWire decodes one row of resourceSuggestionsView
// (internal/admin/resource_handlers.go's resourceSuggestionView) — GET
// /api/specs/{id}/resource-suggestions' own row shape. SpecID is
// deliberately not carried on the wire (the admin view's own comment: the
// caller already knows which spec it asked about), so this tool does not
// echo it back either.
type resourceSuggestionWire struct {
	RouteFamily string  `json:"routeFamily"`
	Name        string  `json:"name"`
	IDField     string  `json:"idField"`
	Confidence  float64 `json:"confidence"`
}

// ResourceSuggestionLine is list_resource_suggestions' per-row output —
// a plain type conversion from resourceSuggestionWire.
type ResourceSuggestionLine struct {
	RouteFamily string  `json:"routeFamily"`
	Name        string  `json:"name"`
	IDField     string  `json:"idField"`
	Confidence  float64 `json:"confidence"`
}

// ListResourceSuggestionsInput is list_resource_suggestions' input.
type ListResourceSuggestionsInput struct {
	SpecID int64 `json:"specId"`
}

// ListResourceSuggestionsOutput is list_resource_suggestions' declared
// output schema, projected from resourceSuggestionsView.
type ListResourceSuggestionsOutput struct {
	Suggestions []ResourceSuggestionLine `json:"suggestions"`
}

func handleListResourceSuggestions(lb *loopback) sdk.ToolHandlerFor[ListResourceSuggestionsInput, ListResourceSuggestionsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListResourceSuggestionsInput) (*sdk.CallToolResult, ListResourceSuggestionsOutput, error) {
		var wire struct {
			Suggestions []resourceSuggestionWire `json:"suggestions"`
		}
		method, path := toolPath("list_resource_suggestions", "GET /api/specs/{id}/resource-suggestions", in.SpecID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListResourceSuggestionsOutput{}, err
		}
		out := make([]ResourceSuggestionLine, len(wire.Suggestions))
		for i, s := range wire.Suggestions {
			out[i] = ResourceSuggestionLine(s)
		}
		return nil, ListResourceSuggestionsOutput{Suggestions: out}, nil
	}
}

// ---- list_resources ----

// ListResourcesInput is list_resources' input.
type ListResourcesInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// ListResourcesOutput is list_resources' declared output schema, projected
// from resourceFamiliesView.
type ListResourcesOutput struct {
	Families []ResourceFamilyLine `json:"families"`
}

func handleListResources(lb *loopback) sdk.ToolHandlerFor[ListResourcesInput, ListResourcesOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListResourcesInput) (*sdk.CallToolResult, ListResourcesOutput, error) {
		var wire struct {
			Families []resourceFamilyWire `json:"families"`
		}
		method, path := toolPath("list_resources", "GET /api/workspaces/{id}/resources", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListResourcesOutput{}, err
		}
		out := make([]ResourceFamilyLine, len(wire.Families))
		for i, f := range wire.Families {
			out[i] = ResourceFamilyLine(f)
		}
		return nil, ListResourcesOutput{Families: out}, nil
	}
}

// ---- decide_resource ----

// decideResourceBody is POST .../resource-decisions' request body
// (resourceDecisionRequest, internal/admin/resource_handlers.go) — the
// three fields an operator (or this tool) supplies. ConfirmSlug is sent
// exactly as the caller passed it, empty string included: the admin route
// itself tells an absent confirm apart from a present-but-wrong one
// (ErrConfirmSlugRequired vs ErrConfirmSlugMismatch), and re-deriving that
// split here would only duplicate a check this tool does not own.
type decideResourceBody struct {
	RouteFamily string `json:"routeFamily"`
	State       string `json:"state"`
	ConfirmSlug string `json:"confirmSlug,omitempty"`
}

// DecideResourceInput is decide_resource's input. ConfirmSlug carries
// confirmSlugDoc's exact wording, verbatim, the same six other tools in
// this package already use it with (confirm.go's own doc comment on why
// that must be copied rather than referenced) — it is REQUIRED on the wire
// here too (no omitempty) even though the admin route only enforces it
// conditionally (declining a CONFIRMED family): the SDK's schema inference
// cannot express "required sometimes," and an operator declining an
// undecided family simply passes the empty string the route already
// accepts for that case.
type DecideResourceInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	RouteFamily string `json:"routeFamily"`
	State       string `json:"state" jsonschema:"Either \"confirmed\" or \"declined\"."`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// DecideResourceOutput is decide_resource's declared output schema,
// projected from resourceDecisionView's single "family" row.
type DecideResourceOutput struct {
	Family ResourceFamilyLine `json:"family"`
}

func handleDecideResource(lb *loopback) sdk.ToolHandlerFor[DecideResourceInput, DecideResourceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DecideResourceInput) (*sdk.CallToolResult, DecideResourceOutput, error) {
		body, err := jsonx.Marshal(decideResourceBody{
			RouteFamily: in.RouteFamily,
			State:       in.State,
			ConfirmSlug: in.ConfirmSlug,
		})
		if err != nil {
			return nil, DecideResourceOutput{}, fmt.Errorf("mcp: encode decide_resource request: %w", err)
		}

		var wire struct {
			Family resourceFamilyWire `json:"family"`
		}
		method, path := toolPath("decide_resource", "POST /api/workspaces/{id}/resource-decisions", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, DecideResourceOutput{}, err
		}
		return nil, DecideResourceOutput{Family: ResourceFamilyLine(wire.Family)}, nil
	}
}

// ---- reset_resource_data ----

// resetResourceDataBody is POST .../reset-data's request body
// (resetDataRequest, internal/admin/resource_handlers.go). Unlike the admin
// package's own struct, Mode is a plain string, not a pointer: this tool's
// input schema already makes Mode required (no omitempty — the same move
// every other required field in this package's input structs makes, per
// CreateWorkspaceInput's own comment in tools_ops.go), so there is no
// "absent vs empty" distinction left for a Go type to preserve by the time
// this body is built — that distinction is the admin HANDLER's own job
// (resetDataRequest's doc comment on why it stays a pointer there), for a
// caller this tool's schema does not let reach it in the first place.
type resetResourceDataBody struct {
	Mode        string `json:"mode"`
	ConfirmSlug string `json:"confirmSlug"`
}

// ResetResourceDataInput is reset_resource_data's input. Both Mode and
// ConfirmSlug are REQUIRED (D7) — mode because there is no sane default
// between "reseed" and "clear" for a call this irreversible, confirmSlug
// for the reason every other destructive tool in this package carries it.
type ResetResourceDataInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Mode        string `json:"mode" jsonschema:"Either \"reseed\" (repopulate every confirmed family from current settings) or \"clear\" (delete every entity row and mint nothing back). Both are irreversible."`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// ResetResourceDataSkippedLine is one entry of
// [ResetResourceDataOutput.Skipped] — a family a reseed left standing
// rather than repopulating, and why (D8's closed four-value enum:
// "stranded", "over_caps", "population_failed", "group_skipped" — the
// fourth is P3e D8.2's own, widened by P3g from a parent-and-its-direct-
// children pair to a whole SUBTREE: every confirmed family at any depth
// under one root reseeds as one group, so a failure of one member reports
// its own reason while every OTHER member of the group — ancestor or
// descendant, not just the immediate parent — reports this one, rather
// than repopulating an ancestor with new keys beside a descendant still
// scoped to the old ones).
type ResetResourceDataSkippedLine struct {
	RouteFamily string `json:"routeFamily"`
	Reason      string `json:"reason"`
}

// ResetResourceDataOutput is reset_resource_data's declared output schema,
// projected from resetDataResponseView. Changed is TRUE when rows were
// deleted or inserted, never merely "the call did something" — a reseed in
// which every family was skipped, or a clear over a workspace with no
// entity row, reports Changed=false even though it ran to completion (D8).
type ResetResourceDataOutput struct {
	Changed bool                           `json:"changed"`
	Deleted int64                          `json:"deleted"`
	Skipped []ResetResourceDataSkippedLine `json:"skipped"`
}

func handleResetResourceData(lb *loopback) sdk.ToolHandlerFor[ResetResourceDataInput, ResetResourceDataOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ResetResourceDataInput) (*sdk.CallToolResult, ResetResourceDataOutput, error) {
		body, err := jsonx.Marshal(resetResourceDataBody{Mode: in.Mode, ConfirmSlug: in.ConfirmSlug})
		if err != nil {
			return nil, ResetResourceDataOutput{}, fmt.Errorf("mcp: encode reset_resource_data request: %w", err)
		}

		var wire struct {
			Changed bool  `json:"changed"`
			Deleted int64 `json:"deleted"`
			Skipped []struct {
				RouteFamily string `json:"routeFamily"`
				Reason      string `json:"reason"`
			} `json:"skipped"`
		}
		method, path := toolPath("reset_resource_data", "POST /api/workspaces/{id}/reset-data", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, ResetResourceDataOutput{}, err
		}
		skipped := make([]ResetResourceDataSkippedLine, len(wire.Skipped))
		for i, sk := range wire.Skipped {
			skipped[i] = ResetResourceDataSkippedLine{RouteFamily: sk.RouteFamily, Reason: sk.Reason}
		}
		return nil, ResetResourceDataOutput{Changed: wire.Changed, Deleted: wire.Deleted, Skipped: skipped}, nil
	}
}

// ---- rederive_suggestions ----

// RederiveSuggestionsInput is rederive_suggestions' input. No confirmSlug
// (unlike the two destructive tools above): the route it wraps resolves no
// workspace at all and destroys no workspace-created data (decisions.md
// D7.1, D8.3).
type RederiveSuggestionsInput struct {
	SpecID int64 `json:"specId"`
}

// RederiveSuggestionsOutput is rederive_suggestions' declared output schema,
// projected from rederiveResultView (internal/admin/resource_handlers.go).
// Generation is never absent — on Changed:false it is the generation that
// already was newest, the identical "always one shape" rule
// [RederiveResult] itself documents.
type RederiveSuggestionsOutput struct {
	Changed    bool     `json:"changed"`
	Generation int      `json:"generation"`
	Added      []string `json:"added"`
	Removed    []string `json:"removed"`
}

// rederiveResultWire decodes rederiveResultView — POST
// /api/specs/{id}/rederive's whole 200 body.
type rederiveResultWire struct {
	Changed    bool     `json:"changed"`
	Generation int      `json:"generation"`
	Added      []string `json:"added"`
	Removed    []string `json:"removed"`
}

func handleRederiveSuggestions(lb *loopback) sdk.ToolHandlerFor[RederiveSuggestionsInput, RederiveSuggestionsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in RederiveSuggestionsInput) (*sdk.CallToolResult, RederiveSuggestionsOutput, error) {
		var wire rederiveResultWire
		method, path := toolPath("rederive_suggestions", "POST /api/specs/{id}/rederive", in.SpecID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, RederiveSuggestionsOutput{}, err
		}
		return nil, RederiveSuggestionsOutput(wire), nil
	}
}
