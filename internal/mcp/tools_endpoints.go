// addEndpointTools registers slice A2's custom-endpoint group (create_
// endpoint, update_endpoint, delete_endpoint, endpoint_from_traffic) and its
// scenario group (create_scenario, rename_scenario, delete_scenario,
// activate_scenario, deactivate_scenario) — the two groups D5 of the
// mocker-a-mcp gate document names "Custom endpoints and traffic
// conversions" and "Scenarios".
//
// Like tools_ops.go, tools_traffic.go, tools_read.go and tools_edit.go,
// every tool here is an ADAPTER over the admin plane's own routes: it
// decodes the JSON the handler in internal/admin actually writes, through a
// PRIVATE wire struct that mirrors only the fields this tool needs. Every
// wire type below cites the handler and line it was read from — except
// where a type already exists elsewhere in this package for the identical
// JSON shape, in which case it is reused rather than redeclared a third
// time:
//
//   - endpointWire, EndpointLine and responseShapes (tools_read.go) are the
//     exact wire shape and projection list_endpoints already established
//     for endpointView (internal/admin/endpoint_handlers.go:37-49) — create
//     and update answer with the SAME view, so their output reads
//     identically to a list_endpoints row for the same id.
//   - VariantInput and ListSizeView (tools_edit.go, tools_ops.go) are the
//     typed mirrors of overrides.Variant and overrides.ListSize
//     set_operation_variant already uses on its own PUT — customep.Row's
//     own Responses/ListSize fields are the identical Go types, so
//     update_endpoint's request body is built from the same pieces.
//   - scenarioSummaryWire and ScenarioSummaryLine (tools_read.go) decode
//     the subset of scenarioDetailView's fields (internal/admin/
//     scenario_handlers.go:82-91) that create_scenario and rename_scenario
//     project — jsonx ignores the rest of that response (settings, basePath,
//     spec, overrides), the same safe-subset move workspaceWire's own
//     comment (tools_ops.go) describes for a different route.
//
// delete_scenario is one of D6's six confirmation tools: it calls
// confirmWorkspaceSlug (confirm.go) FIRST, and issues nothing when that
// returns an error. The other five confirmation tools (delete_workspace,
// clear_traffic, delete_checkpoint, rollback_workspace, reset_overrides)
// are built by a different agent, in tools_history.go and elsewhere — this
// file calls the one shared helper rather than writing a second copy of it.
// Two more tools carry the identical confirmSlug argument without calling
// this helper at all — decide_resource's decline branch and
// reset_resource_data (D7 of mocker-p3b-resources, tools_resources.go) —
// so eight tools require confirmSlug in total, six of them (including this
// one) through confirmWorkspaceSlug.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ---- shared: A3 conflict shapes for this file's two write tools ----

// EndpointConflictDocument is D6's round-trippable conflict payload for
// PUT .../endpoints/{eid} — every field the route accepts, as the server
// currently holds it, compacted the same way EndpointLine already is
// (Responses as ResponseShape, never a pinned body or recipe definitions —
// D5's "compaction wins" at the MCP hop) plus the version the server
// actually holds. Mirrors endpointConflictDetails (endpoint_handlers.go)
// minus the fields that struct has no use for restating (a stored row has
// nothing to omit, so OverrideOn/RouteOff/ActiveStatus are plain, not
// pointers, exactly like the admin struct they mirror).
type EndpointConflictDocument struct {
	Method       string                   `json:"method"`
	Path         string                   `json:"path"`
	OverrideOn   bool                     `json:"overrideOn"`
	RouteOff     bool                     `json:"routeOff"`
	ActiveStatus int                      `json:"activeStatus"`
	Responses    map[string]ResponseShape `json:"responses,omitempty"`
	ListSize     *ListSizeView            `json:"listSize,omitempty"`
	DelayMs      *int                     `json:"delayMs,omitempty"`
	EditVersion  int64                    `json:"editVersion"`
}

// EndpointConflictDetail is update_endpoint's typed conflict field.
// Document is nil exactly when Gone is true (D6's tombstone) — the
// endpoint itself was deleted by another write between this call's read
// and its write.
type EndpointConflictDetail struct {
	Gone     bool                      `json:"gone"`
	Document *EndpointConflictDocument `json:"document,omitempty"`
}

// decodeEndpointConflict decodes a 409 edit_conflict's `details` for
// PUT .../endpoints/{eid}. endpointWire (tools_read.go) already decodes
// this exact field set (plus id/canonicalPath/createdAt/updatedAt, which
// the conflict payload simply omits and jsonx leaves at their zero value),
// so it is reused here rather than a third hand-copy of the field list.
func decodeEndpointConflict(details jsonx.RawMessage) (*EndpointConflictDetail, error) {
	if detailsAreGone(details) {
		return &EndpointConflictDetail{Gone: true}, nil
	}
	var wire endpointWire
	if err := jsonx.Unmarshal(details, &wire); err != nil {
		return nil, fmt.Errorf("mcp: decode edit_conflict details: %w", err)
	}
	return &EndpointConflictDetail{Document: &EndpointConflictDocument{
		Method: wire.Method, Path: wire.Path, OverrideOn: wire.OverrideOn, RouteOff: wire.RouteOff,
		ActiveStatus: wire.ActiveStatus, Responses: responseShapes(wire.Responses),
		ListSize: wire.ListSize, DelayMs: wire.DelayMs, EditVersion: wire.EditVersion,
	}}, nil
}

// ScenarioConflictDocument is D6's conflict payload for
// PUT .../scenarios/{sid} (rename) — just Name, since a rename changes one
// column, plus the version the server actually holds. Mirrors
// scenarioConflictDetails (scenario_handlers.go) field-for-field.
type ScenarioConflictDocument struct {
	Name        string `json:"name"`
	EditVersion int64  `json:"editVersion"`
}

// ScenarioConflictDetail is rename_scenario's typed conflict field.
// Document is nil exactly when Gone is true (D6's tombstone) — the
// scenario itself was deleted by another write between this call's read
// and its write.
type ScenarioConflictDetail struct {
	Gone     bool                      `json:"gone"`
	Document *ScenarioConflictDocument `json:"document,omitempty"`
}

// decodeScenarioConflict decodes a 409 edit_conflict's `details` for
// PUT .../scenarios/{sid}.
func decodeScenarioConflict(details jsonx.RawMessage) (*ScenarioConflictDetail, error) {
	if detailsAreGone(details) {
		return &ScenarioConflictDetail{Gone: true}, nil
	}
	var doc ScenarioConflictDocument
	if err := jsonx.Unmarshal(details, &doc); err != nil {
		return nil, fmt.Errorf("mcp: decode edit_conflict details: %w", err)
	}
	return &ScenarioConflictDetail{Document: &doc}, nil
}

func addEndpointTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "create_endpoint",
		Description: "Creates a custom endpoint that answers a (method, path) nothing else on this " +
			"workspace currently serves — an operator-authored route with no spec operation behind it. " +
			"status defaults to 200 when omitted; the single response is pinned exactly as given. Refuses " +
			"with 409 if an operation override already exists at the same (method, path) — a custom " +
			"endpoint there would silently outrank it and strand the override's own body unreachable. A " +
			"retry after a lost or timed-out response is refused by a unique key, not duplicated: " +
			"custom_endpoints carries a unique (method, path) index, so calling this again with the same " +
			"values fails cleanly rather than creating a second endpoint.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleCreateEndpoint(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "update_endpoint",
		Description: "The full editor for one custom endpoint's definition: method, path, on/off state, " +
			"the response pinned at every status, list size and delay — a FULL REPLACEMENT of the stored " +
			"row, not a merge. Call list_endpoints first to see the current definition and resend every " +
			"response you want kept, or it is silently discarded. overrideOn and routeOff may be omitted " +
			"— an omitted overrideOn defaults to true and an omitted routeOff defaults to false, the same " +
			"safe defaults create_endpoint applies to a brand-new row — but activeStatus has no spec to " +
			"fall back on the way an operation override does, so it is required on every call. Refuses " +
			"with 409 (a plain tool error, not a conflict field) if the new (method, path) collides with " +
			"an existing operation override. editVersion is REQUIRED and must be the exact value " +
			"list_endpoints last reported for this row — 0 is refused here, unlike an operation override, " +
			"because an endpoint addressed by its id always already exists. A change made in the admin UI " +
			"since you last read this endpoint answers a 409 edit_conflict, carried as a populated " +
			"conflict field holding the current definition (compacted the same way list_endpoints is) and " +
			"the real editVersion to retry with.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleUpdateEndpoint(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "delete_endpoint",
		Description: "Permanently deletes one custom endpoint by id. The row itself is not undone by " +
			"anything on this surface; a checkpoint's config snapshot DOES carry custom endpoints " +
			"(internal/checkpoints restores them beside overrides and settings), so rollback_workspace " +
			"to a checkpoint taken before the delete brings the definition back under the same id. " +
			"Errors if the id does not name an endpoint in this workspace: already deleted, or it " +
			"never existed.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleDeleteEndpoint(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "endpoint_from_traffic",
		Description: "Converts one observed traffic row into a new custom endpoint at the row's own " +
			"(method, path), so future requests there replay what was observed. A literal observed 404 " +
			"becomes a pinned 200 (an empty observed body becomes \"{}\"); any other observed status is " +
			"preserved exactly. Use this right after list_traffic shows a request nothing currently " +
			"answers. Refuses — reporting the specific reason — a row whose body was truncated or " +
			"redacted, one whose recorded path does not start with the workspace's current base path, one " +
			"whose path is ambiguous enough to shadow a spec operation it never actually matched, or one " +
			"that collides with an existing operation override at the same (method, path). A retry after " +
			"a lost or timed-out response is refused by a unique key, not duplicated: custom_endpoints " +
			"carries a unique (method, path) index.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleEndpointFromTraffic(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "create_scenario",
		Description: "Saves a new named scenario. Without from, this snapshots the workspace's CURRENT " +
			"settings and operation overrides (never custom endpoints — a scenario's snapshot has no room " +
			"for them) into a fresh row, and is refused with 409 while a different scenario is already " +
			"active. With from set to another scenario's id, this instead CLONES that scenario's own " +
			"stored snapshot under the new name in one copy that never reads the workspace's live state at " +
			"all — it succeeds even while a different scenario is active, since there is nothing live for " +
			"it to conflict with. A retry after a lost or timed-out response is refused by a unique key, " +
			"not duplicated: scenarios carries a unique (workspace, name) index, so calling this again " +
			"with the same name fails cleanly rather than creating a second scenario.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleCreateScenario(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "rename_scenario",
		Description: "Renames a saved scenario — only the name changes, nothing else about its snapshot " +
			"moves, and this does NOT bump the workspace's revision. The mock plane resolves a scenario by " +
			"name LIVE on every POST {prefix}/state {\"scenario\":\"<name>\"} that switches to one, so the " +
			"rename is visible immediately and silently breaks any external caller — a client, a test " +
			"suite — that has the OLD name hardcoded into a session directive; there is no way for this " +
			"tool to detect or warn about that beyond this sentence. A name COLLISION inside this " +
			"workspace is still a plain 409 tool error (nothing to retry a version against — it names a " +
			"DIFFERENT row). editVersion is REQUIRED and must be the exact value get_scenario last " +
			"reported — 0 is refused here, unlike an operation override, because a scenario addressed by " +
			"its id always already exists. A rename made in the admin UI (or by another agent) since you " +
			"last read this scenario answers a 409 edit_conflict, carried as a populated conflict field " +
			"holding the current name and the real editVersion to retry with.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleRenameScenario(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "delete_scenario",
		Description: "Permanently deletes one saved scenario. There is no undo: unlike rollback_workspace " +
			"or reset_overrides, which take a pre-destructive checkpoint of the Workspace layer " +
			"automatically, nothing ever captures a scenario's own snapshot — deleting one discards that " +
			"state for good. Deleting the currently ACTIVE scenario also bumps the workspace's revision, " +
			"since the mock plane stops composing it into what it serves. Requires confirmSlug naming the " +
			"exact workspace this call targets; a mismatch refuses the call and changes nothing.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleDeleteScenario(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "activate_scenario",
		Description: "Activates one saved scenario on the workspace: from the next request onward the " +
			"mock plane composes the scenario's snapshot on top of the Spec layer instead of the " +
			"workspace's own live Workspace-layer edits, and bumps the revision so the switch is visible " +
			"immediately. Three settings stay the workspace's own even while a scenario is active and are " +
			"never taken from the snapshot: basePath, the CORS policy and the custom 404 body. Activating " +
			"the scenario that is already active is a harmless no-op that reports the unchanged revision.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleActivateScenario(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "deactivate_scenario",
		Description: "Deactivates whichever scenario is currently active on the workspace, returning it " +
			"to serving its own Workspace-layer state directly. Takes no scenario id — there is nothing to " +
			"name, only whatever is active right now. A harmless no-op when nothing is active: it still " +
			"reports the current revision, unchanged.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleDeactivateScenario(lb))
}

// ---- shared: projecting endpointWire (tools_read.go) into EndpointLine ----

// toEndpointLine projects a decoded endpointWire down to EndpointLine — the
// same compaction list_endpoints already applies (never a pinned body or
// recipe definitions, only each response's shape), reused here rather than
// redeclared so create_endpoint's 201 and update_endpoint's 200 read
// identically to a list_endpoints row for the same id.
func toEndpointLine(w endpointWire) EndpointLine {
	return EndpointLine{
		ID:            w.ID,
		Method:        w.Method,
		Path:          w.Path,
		CanonicalPath: w.CanonicalPath,
		OverrideOn:    w.OverrideOn,
		RouteOff:      w.RouteOff,
		ActiveStatus:  w.ActiveStatus,
		Responses:     responseShapes(w.Responses),
		ListSize:      w.ListSize,
		DelayMs:       w.DelayMs,
		Kind:          w.Kind,
		Stream:        w.Stream,
		CreatedAt:     w.CreatedAt,
		UpdatedAt:     w.UpdatedAt,
		EditVersion:   w.EditVersion,
	}
}

// ---- create_endpoint ----

// CreateEndpointInput is create_endpoint's input, mirroring
// createEndpointRequest (internal/admin/endpoint_handlers.go:107-113)
// field-for-field. Body is `any`, not jsonx.RawMessage: this package's
// Input types are decoded FROM the MCP client's JSON by the SDK, which
// already leaves arbitrary content as native Go values (map[string]any,
// []any, string, float64, bool, nil) — jsonx.RawMessage would infer a
// nonsensical byte-array schema instead (jsonschema-go has no special case
// for it), the same rule tools_edit.go's own header comment on RecipeInput
// states in full.
type CreateEndpointInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	// Status defaults to 200 when omitted — defaultCreateEndpointStatus
	// (endpoint_handlers.go:116).
	Status    int    `json:"status,omitempty"`
	Body      any    `json:"body,omitempty" jsonschema:"the pinned response body: any JSON value (object, array, string, number, boolean or null); omit to serve a generated body."`
	MediaType string `json:"mediaType,omitempty"`
	// BodyRef is A6's (DESIGN §32.3): the endpoint's pinned response is the
	// named uploaded asset, served verbatim under its stored type.
	BodyRef string `json:"bodyRef,omitempty" jsonschema:"asset:<name> — the response body is this workspace's uploaded asset (upload_asset), served verbatim under its stored media type. Exclusive with body and mediaType."`
	// Kind and Stream are P6b's (decisions.md mocker-p6b-sse-mock D3, D6).
	// Omitted kind is "http". kind "sse" needs method GET, no
	// status/body/mediaType, and a stream document: {timeline: {frames:
	// [{delayMs, event, data}], loop}, tick: {intervalMs, event, schema},
	// closeWhenDone} — at least one of timeline/tick; intervalMs >= 100;
	// at most 500 frames; delayMs 0..30000; schema an inline JSON Schema
	// object with no $ref. preview_endpoint lays a draft out first.
	// kind "ws" (P6d, decisions.md mocker-p6d-websocket D2–D4) is a
	// WebSocket endpoint: the same document plus reactive: [{when:
	// Condition[], data?, close?: {code, reason?}}] (at most 100 rules,
	// first match wins; when[] is op_overrides' own language — in=body
	// reads the inbound frame's top-level keys, in=query/header the
	// HANDSHAKE's; data goes out as one text frame; close.code is 1000 or
	// 4000..4999) and echo: bool (an unmatched frame comes back as-is).
	// reactive/echo are refused by name on kind "sse". A ws row needs at
	// least one behaviour of the four.
	Kind   string `json:"kind,omitempty"`
	Stream any    `json:"stream,omitempty" jsonschema:"the stream document (kind sse or ws): {timeline: {frames: [{delayMs, event, data}], loop}, tick: {intervalMs, event, schema}, closeWhenDone}, plus reactive/echo for ws — see create_endpoint's stream field A18 adds two Lua hooks: tick.lua (the tick's producer, exclusive with tick.schema — the function body over one argument named ordinal, returning a table, a string, or nil to skip that firing) and onFrame (ws only, REPLACES reactive and echo — the function body over one argument named frame, returning nil, or the pair (reply, data), or (close, code, reason?))."`
	// Schema, ReqSchema and Operation are P7a's (DESIGN §34.3). Schema is
	// an inline JSON Schema the response is GENERATED from — with no body
	// beside it the endpoint answers a generated body under the workspace
	// seed, recipes and refs included, exactly like a spec operation; with
	// a body it is the exported shape only and the body serves. A `$ref`
	// into the bound spec's components is allowed and must resolve at
	// write time. Operation carries the OpenAPI fields the contract needs:
	// {summary, description, tags, operationId, deprecated, parameters:
	// [{name, in: query|path|header, required, description, schema}]}.
	Schema    any `json:"schema,omitempty" jsonschema:"an inline JSON Schema; the response is generated from it when no body is pinned. A $ref must point into the bound spec (#/components/...)."`
	ReqSchema any `json:"reqSchema,omitempty" jsonschema:"the request body's JSON Schema; exported as requestBody, never enforced on a request."`
	Operation any `json:"operation,omitempty" jsonschema:"the OpenAPI operation fields: summary, description, tags, operationId, deprecated, parameters."`
	// Function is A18's (D5, D8): the Lua that produces this endpoint's
	// response. It needs a field of its own on the CREATE input for the
	// same reason BodyRef does — create builds one variant from flat
	// fields, while update_endpoint carries it inside responses[].
	Function string `json:"function,omitempty" jsonschema:"Lua that PRODUCES this response instead of one being assembled for it. The string is the function BODY over one argument req (req.method, req.path, req.pathParams, req.query, req.headers, req.body) and returns status, body, headers — e.g. \"if req.body.password == 'x' then return 200, {token = mock.jwt({sub = 1})} end return 401, {error = 'bad credentials'}\". Helpers: mock.jwt(claims), mock.now(offsetSec), mock.entities(family, scopeArray). Exclusive with body, bodyEncoding, bodyRef, recipes, schemaPatch and mediaType; refused on a stream row; compiled at write time, so a syntax error is a 400 carrying the parser's words."`
}

// CreateEndpointOutput is create_endpoint's declared output schema.
// Endpoint reuses EndpointLine (tools_read.go) — see this file's own header
// comment on why the created row's own projection is shared with
// list_endpoints rather than redeclared.
type CreateEndpointOutput struct {
	Endpoint EndpointLine `json:"endpoint"`
}

// createEndpointBody is the request body POST .../endpoints accepts —
// createEndpointRequest's own shape (endpoint_handlers.go:107-113).
type createEndpointBody struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status,omitempty"`
	Body      any    `json:"body,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	BodyRef   string `json:"bodyRef,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Stream    any    `json:"stream,omitempty"`
	Schema    any    `json:"schema,omitempty"`
	ReqSchema any    `json:"reqSchema,omitempty"`
	Operation any    `json:"operation,omitempty"`
	// Function was declared on CreateEndpointInput and absent here, so the
	// PRIMARY surface silently dropped the Lua on create: the admin route saw
	// no body, bodyRef or schema and stored a pinned variant with an empty
	// body, 201. Review finding 4.
	Function string `json:"function,omitempty"`
}

func handleCreateEndpoint(lb *loopback) sdk.ToolHandlerFor[CreateEndpointInput, CreateEndpointOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in CreateEndpointInput) (*sdk.CallToolResult, CreateEndpointOutput, error) {
		body, err := jsonx.Marshal(createEndpointBody{
			Method: in.Method, Path: in.Path, Status: in.Status, Body: in.Body, MediaType: in.MediaType,
			BodyRef: in.BodyRef, Kind: in.Kind, Stream: in.Stream,
			Schema: in.Schema, ReqSchema: in.ReqSchema, Operation: in.Operation, Function: in.Function,
		})
		if err != nil {
			return nil, CreateEndpointOutput{}, fmt.Errorf("mcp: encode create_endpoint request: %w", err)
		}

		var wire endpointWire
		method, path := toolPath("create_endpoint", "POST /api/workspaces/{id}/endpoints", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, CreateEndpointOutput{}, err
		}
		return nil, CreateEndpointOutput{Endpoint: toEndpointLine(wire)}, nil
	}
}

// ---- update_endpoint ----

// UpdateEndpointInput is update_endpoint's input, mirroring
// updateEndpointRequest (endpoint_handlers.go:257-266) field-for-field —
// the A1 route this run starts from. Responses reuses VariantInput
// (tools_edit.go), the same typed mirror of overrides.Variant
// set_operation_variant already builds its own PUT from, since
// customep.Row.Responses is the identical map[string]overrides.Variant Go
// type op_overrides stores.
//
// OverrideOn/RouteOff stay *bool with omitempty, UNLIKE
// OverrideDocumentInput's plain, required bool pair (tools_edit.go):
// updateEndpointRequest's own defaulting is safe to lean on — an omitted
// overrideOn becomes true and an omitted routeOff becomes false
// (updateEndpointRequest's own doc comment) — not the op_overrides footgun
// OverrideDocumentInput's comment describes (there, an omitted overrideOn
// silently writes false and the whole override goes inert), so there is no
// reason to force this caller to spell out what the admin plane already
// defaults sensibly.
//
// ActiveStatus carries NO omitempty, unlike op_overrides' own *int
// ActiveStatus (which falls back to the spec's own default status when
// nil): a custom endpoint has no spec document to fall back on at all
// (customep.Row.ActiveStatus's own doc comment, customep.go:49-55), and
// handleUpdateEndpoint assigns it wholesale with no re-defaulting the way
// Create does (endpoint_handlers.go:345: "cur.ActiveStatus = body.
// ActiveStatus", no zero-check). An omitted field here would silently write
// the literal, invalid status 0 — the SDK's own required-field inference
// (any field without omitempty becomes a required property) is what stops
// that before this tool's handler ever runs.
type UpdateEndpointInput struct {
	WorkspaceID  int64                   `json:"workspaceId"`
	EndpointID   int64                   `json:"endpointId"`
	Method       string                  `json:"method"`
	Path         string                  `json:"path"`
	OverrideOn   *bool                   `json:"overrideOn,omitempty"`
	RouteOff     *bool                   `json:"routeOff,omitempty"`
	ActiveStatus int                     `json:"activeStatus"`
	Responses    map[string]VariantInput `json:"responses,omitempty"`
	ListSize     *ListSizeView           `json:"listSize,omitempty"`
	DelayMs      *int                    `json:"delayMs,omitempty"`
	// Kind and Stream (P6b D3, D6): a FULL REPLACEMENT carries the kind too
	// — an omitted kind is "http", so resend kind "sse" and the stream
	// document when editing a stream, or the write is refused for carrying
	// a stream with kind http (never silently downgraded).
	Kind   string `json:"kind,omitempty"`
	Stream any    `json:"stream,omitempty" jsonschema:"the stream document (kind sse or ws), resent whole on every edit — see create_endpoint's stream field A18 adds two Lua hooks: tick.lua (the tick's producer, exclusive with tick.schema — the function body over one argument named ordinal, returning a table, a string, or nil to skip that firing) and onFrame (ws only, REPLACES reactive and echo — the function body over one argument named frame, returning nil, or the pair (reply, data), or (close, code, reason?))."`
	// ReqSchema and Operation are P7a's (DESIGN §34.3); the response
	// schema rides inside responses[status].schema on VariantInput. A
	// full replacement carries them like every other field — omitted
	// means cleared.
	ReqSchema any `json:"reqSchema,omitempty" jsonschema:"the request body's JSON Schema; exported as requestBody, never enforced on a request. Omitted means cleared — resend the row's own."`
	Operation any `json:"operation,omitempty" jsonschema:"the OpenAPI operation fields: summary, description, tags, operationId, deprecated, parameters. Omitted means cleared — resend the row's own."`
	// EditVersion is A3's REQUIRED compare-and-swap expectation
	// (mocker-a3-cas D10/D11) — the exact value list_endpoints last
	// reported for this row. A plain, non-pointer int64: see
	// SetOperationResponseInput.EditVersion's identical comment on why a
	// pointer would be required AND nullable. 0 is refused on this table
	// (D7: an endpoint addressed by {eid} always already exists) — there
	// is no "no expectation" state reachable on this wire.
	EditVersion int64 `json:"editVersion"`
}

// UpdateEndpointOutput is update_endpoint's declared output schema — the
// same projection create_endpoint answers with (Endpoint now carries
// EditVersion too, see EndpointLine's own comment), plus Conflict, present
// only when the write lost the compare-and-swap.
type UpdateEndpointOutput struct {
	Endpoint EndpointLine            `json:"endpoint"`
	Conflict *EndpointConflictDetail `json:"conflict,omitempty"`
}

func handleUpdateEndpoint(lb *loopback) sdk.ToolHandlerFor[UpdateEndpointInput, UpdateEndpointOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in UpdateEndpointInput) (*sdk.CallToolResult, UpdateEndpointOutput, error) {
		body, err := jsonx.Marshal(struct {
			Method       string                  `json:"method"`
			Path         string                  `json:"path"`
			OverrideOn   *bool                   `json:"overrideOn,omitempty"`
			RouteOff     *bool                   `json:"routeOff,omitempty"`
			ActiveStatus int                     `json:"activeStatus"`
			Responses    map[string]VariantInput `json:"responses,omitempty"`
			ListSize     *ListSizeView           `json:"listSize,omitempty"`
			DelayMs      *int                    `json:"delayMs,omitempty"`
			Kind         string                  `json:"kind,omitempty"`
			Stream       any                     `json:"stream,omitempty"`
			ReqSchema    any                     `json:"reqSchema,omitempty"`
			Operation    any                     `json:"operation,omitempty"`
			// EditVersion is the caller's own expectation, explicit on the
			// wire (mocker-a3-cas D5/D7) — see UpdateEndpointInput's own
			// comment.
			EditVersion int64 `json:"editVersion"`
		}{
			Method: in.Method, Path: in.Path, OverrideOn: in.OverrideOn, RouteOff: in.RouteOff,
			ActiveStatus: in.ActiveStatus, Responses: in.Responses, ListSize: in.ListSize, DelayMs: in.DelayMs,
			Kind: in.Kind, Stream: in.Stream,
			ReqSchema: in.ReqSchema, Operation: in.Operation,
			EditVersion: in.EditVersion,
		})
		if err != nil {
			return nil, UpdateEndpointOutput{}, fmt.Errorf("mcp: encode update_endpoint request: %w", err)
		}

		var wire endpointWire
		method, path := toolPath("update_endpoint", "PUT /api/workspaces/{id}/endpoints/{eid}", in.WorkspaceID, in.EndpointID)
		details, err := writeEditGuarded(ctx, lb, method, path, body, &wire)
		if err != nil {
			return nil, UpdateEndpointOutput{}, err
		}
		if details != nil {
			conflict, cerr := decodeEndpointConflict(details)
			if cerr != nil {
				return nil, UpdateEndpointOutput{}, cerr
			}
			return nil, UpdateEndpointOutput{Conflict: conflict}, nil
		}
		return nil, UpdateEndpointOutput{Endpoint: toEndpointLine(wire)}, nil
	}
}

// ---- delete_endpoint ----

// DeleteEndpointInput is delete_endpoint's input.
type DeleteEndpointInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	EndpointID  int64 `json:"endpointId"`
}

// DeleteEndpointOutput is delete_endpoint's declared output schema. Deleted
// is always true: lb.call already turns every non-2xx (404 not found, 500)
// into a tool error before this line runs, so a successful return means
// exactly one thing happened.
type DeleteEndpointOutput struct {
	EndpointID int64 `json:"endpointId"`
	Deleted    bool  `json:"deleted"`
}

func handleDeleteEndpoint(lb *loopback) sdk.ToolHandlerFor[DeleteEndpointInput, DeleteEndpointOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeleteEndpointInput) (*sdk.CallToolResult, DeleteEndpointOutput, error) {
		method, path := toolPath("delete_endpoint", "DELETE /api/workspaces/{id}/endpoints/{eid}", in.WorkspaceID, in.EndpointID)
		// call, not do: handleDeleteEndpoint (endpoint_handlers.go:369-398)
		// answers 204 with no body on every success and 404/500 on every
		// failure — a plain 2xx/non-2xx split this tool has no reason to
		// inspect itself, unlike reset_operation's own 204-vs-200 split
		// (tools_edit.go) between "nothing to remove" and "removed".
		if err := lb.call(ctx, method, path, nil, nil); err != nil {
			return nil, DeleteEndpointOutput{}, err
		}
		return nil, DeleteEndpointOutput{EndpointID: in.EndpointID, Deleted: true}, nil
	}
}

// ---- endpoint_from_traffic ----

// EndpointFromTrafficInput is endpoint_from_traffic's input — the same
// shape OverrideFromTrafficInput (tools_traffic.go) uses for its own
// traffic-row conversion.
type EndpointFromTrafficInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	TrafficID   int64 `json:"trafficId"`
}

// EndpointFromTrafficOutput is endpoint_from_traffic's declared output
// schema, projected from toEndpointView (internal/admin/from_traffic.go:
// 224-230).
type EndpointFromTrafficOutput struct {
	ID       int64  `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Revision int64  `json:"revision"`
}

// toEndpointWire decodes toEndpointView (from_traffic.go:224-230) — the
// success shape of POST .../traffic/{tid}/to-endpoint.
type toEndpointWire struct {
	ID       int64  `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Revision int64  `json:"revision"`
}

func handleEndpointFromTraffic(lb *loopback) sdk.ToolHandlerFor[EndpointFromTrafficInput, EndpointFromTrafficOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in EndpointFromTrafficInput) (*sdk.CallToolResult, EndpointFromTrafficOutput, error) {
		method, path := toolPath("endpoint_from_traffic", "POST /api/workspaces/{id}/traffic/{tid}/to-endpoint", in.WorkspaceID, in.TrafficID)

		var wire toEndpointWire
		// lb.call, not lb.do: every non-2xx here IS the tool's own error to
		// report — 404 (no such traffic row), 409 (the row can't be
		// trusted, its path is ambiguous, or it collides with an existing
		// override — from_traffic.go:272,280,312,335), 500 — the same
		// reasoning override_from_traffic's own identical comment gives
		// (tools_traffic.go): toolErr already surfaces the admin plane's
		// own httpx.ErrorBody.Error.Message verbatim, which IS the refusal
		// reason this tool's Description promises to report.
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, EndpointFromTrafficOutput{}, err
		}
		// toEndpointWire and EndpointFromTrafficOutput share an identical
		// field set, so this is a plain type conversion (staticcheck
		// S1016) — the same move handleOverrideFromTraffic makes for its
		// own matching pair (tools_traffic.go).
		return nil, EndpointFromTrafficOutput(wire), nil
	}
}

// ---- create_scenario ----

// CreateScenarioInput is create_scenario's input, mirroring
// createScenarioRequest (scenario_handlers.go:140-143) field-for-field.
type CreateScenarioInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Name        string `json:"name"`
	// From, when set, CLONES that scenario's own stored snapshot instead of
	// snapshotting the workspace's current state — see this tool's own
	// Description for what that changes about which refusals apply.
	From *int64 `json:"from,omitempty"`
}

// CreateScenarioOutput is create_scenario's declared output schema.
// Scenario reuses ScenarioSummaryLine (tools_read.go) rather than decoding
// the full scenarioDetailView this route actually answers with: a caller
// that wants the created snapshot's own content already has get_scenario
// for that, and id/name/createdAt/isActive is everything this call's own
// response could report that the caller's own arguments did not already
// determine.
type CreateScenarioOutput struct {
	Scenario ScenarioSummaryLine `json:"scenario"`
}

func handleCreateScenario(lb *loopback) sdk.ToolHandlerFor[CreateScenarioInput, CreateScenarioOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in CreateScenarioInput) (*sdk.CallToolResult, CreateScenarioOutput, error) {
		body, err := jsonx.Marshal(struct {
			Name string `json:"name"`
			From *int64 `json:"from,omitempty"`
		}{Name: in.Name, From: in.From})
		if err != nil {
			return nil, CreateScenarioOutput{}, fmt.Errorf("mcp: encode create_scenario request: %w", err)
		}

		// scenarioSummaryWire (tools_read.go) decodes the subset of
		// scenarioDetailView's own fields (scenario_handlers.go:82-91) this
		// tool projects — jsonx ignores the rest (settings/basePath/spec/
		// overrides), the safe-subset move this file's own header comment
		// describes.
		var wire scenarioSummaryWire
		method, path := toolPath("create_scenario", "POST /api/workspaces/{id}/scenarios", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, CreateScenarioOutput{}, err
		}
		return nil, CreateScenarioOutput{Scenario: ScenarioSummaryLine(wire)}, nil
	}
}

// ---- rename_scenario ----

// RenameScenarioInput is rename_scenario's input, mirroring
// renameScenarioRequest (scenario_handlers.go:148-150).
type RenameScenarioInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	ScenarioID  int64  `json:"scenarioId"`
	Name        string `json:"name"`
	// EditVersion is A3's REQUIRED compare-and-swap expectation
	// (mocker-a3-cas D10/D11) — the exact value get_scenario last reported.
	// A plain, non-pointer int64: see SetOperationResponseInput.
	// EditVersion's identical comment on why a pointer would be required
	// AND nullable. 0 is refused on this table (D7: a scenario addressed
	// by {sid} always already exists).
	EditVersion int64 `json:"editVersion"`
}

// RenameScenarioOutput is rename_scenario's declared output schema.
// Scenario reuses ScenarioSummaryLine, the same projection create_scenario
// answers with; EditVersion and Conflict are the A3 pair (present only one
// at a time, mirroring every other write tool in this package).
type RenameScenarioOutput struct {
	Scenario    ScenarioSummaryLine     `json:"scenario"`
	EditVersion int64                   `json:"editVersion,omitempty"`
	Conflict    *ScenarioConflictDetail `json:"conflict,omitempty"`
}

func handleRenameScenario(lb *loopback) sdk.ToolHandlerFor[RenameScenarioInput, RenameScenarioOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in RenameScenarioInput) (*sdk.CallToolResult, RenameScenarioOutput, error) {
		body, err := jsonx.Marshal(struct {
			Name string `json:"name"`
			// EditVersion is the caller's own expectation, explicit on the
			// wire (mocker-a3-cas D5/D7) — see RenameScenarioInput's own
			// comment.
			EditVersion int64 `json:"editVersion"`
		}{Name: in.Name, EditVersion: in.EditVersion})
		if err != nil {
			return nil, RenameScenarioOutput{}, fmt.Errorf("mcp: encode rename_scenario request: %w", err)
		}

		// handlePutScenario's success response is the full scenarioDetailView
		// (Name, EditVersion and more), not the summary shape this tool
		// projects down to — scenarioDetailWire (tools_read.go) decodes it,
		// and its own extra fields are simply ignored by the conversion
		// below, the same safe-subset move this package uses throughout.
		var wire scenarioDetailWire
		method, path := toolPath("rename_scenario", "PUT /api/workspaces/{id}/scenarios/{sid}", in.WorkspaceID, in.ScenarioID)
		details, err := writeEditGuarded(ctx, lb, method, path, body, &wire)
		if err != nil {
			return nil, RenameScenarioOutput{}, err
		}
		if details != nil {
			conflict, cerr := decodeScenarioConflict(details)
			if cerr != nil {
				return nil, RenameScenarioOutput{}, cerr
			}
			return nil, RenameScenarioOutput{Conflict: conflict}, nil
		}
		return nil, RenameScenarioOutput{
			Scenario:    ScenarioSummaryLine{ID: wire.ID, Name: wire.Name, CreatedAt: wire.CreatedAt, IsActive: wire.IsActive},
			EditVersion: wire.EditVersion,
		}, nil
	}
}

// ---- delete_scenario ----

// DeleteScenarioInput is delete_scenario's input. ConfirmSlug is one of the
// eight tools' shared confirmSlug arguments — confirmWorkspaceSlug
// (confirm.go) reads it for this one and five others (D6). confirmSlugDoc's
// exact wording is copied into the jsonschema tag
// below verbatim rather than referenced, because a Go struct tag cannot
// reference a constant: confirmSlugField's own doc comment states this is
// deliberate, so the eight tools that carry this argument read as one rule
// rather than eight independent paraphrases of it.
type DeleteScenarioInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	ScenarioID  int64  `json:"scenarioId"`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// DeleteScenarioOutput is delete_scenario's declared output schema. Deleted
// is always true, for the same reason DeleteEndpointOutput's own comment
// gives: every non-2xx already became a tool error before this line runs.
type DeleteScenarioOutput struct {
	ScenarioID int64 `json:"scenarioId"`
	Deleted    bool  `json:"deleted"`
}

func handleDeleteScenario(lb *loopback) sdk.ToolHandlerFor[DeleteScenarioInput, DeleteScenarioOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeleteScenarioInput) (*sdk.CallToolResult, DeleteScenarioOutput, error) {
		// FIRST statement, per D6: confirmWorkspaceSlug reads the live slug
		// through GET /api/workspaces/{id} (toolRoutes' own row for this
		// tool carries that route alongside the DELETE) and refuses before
		// anything below ever runs on an empty argument or a mismatch. No
		// second read of the workspace happens anywhere in this handler.
		if err := confirmWorkspaceSlug(ctx, lb, "delete_scenario", in.WorkspaceID, in.ConfirmSlug); err != nil {
			return nil, DeleteScenarioOutput{}, err
		}

		method, path := toolPath("delete_scenario", "DELETE /api/workspaces/{id}/scenarios/{sid}", in.WorkspaceID, in.ScenarioID)
		if err := lb.call(ctx, method, path, nil, nil); err != nil {
			return nil, DeleteScenarioOutput{}, err
		}
		return nil, DeleteScenarioOutput{ScenarioID: in.ScenarioID, Deleted: true}, nil
	}
}

// ---- activate_scenario ----

// ActivateScenarioInput is activate_scenario's input. Takes no request body
// on the wire it adapts — {sid} in the path is the entire instruction,
// exactly like override_from_traffic's own route takes none for the same
// reason (tools_traffic.go).
type ActivateScenarioInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	ScenarioID  int64 `json:"scenarioId"`
}

// ActivateScenarioOutput is activate_scenario's declared output schema.
// ScenarioID echoes the CALL'S OWN argument rather than re-decoding
// scenarioActiveView's own ScenarioID (scenario_handlers.go:128-131): {sid}
// in the path is the instruction, so the admin plane's response can only
// ever confirm what this call already knows — the same reasoning
// SetOperationVariantOutput's own comment gives (tools_edit.go) for not
// re-decoding overrideOn/routeOff after a PUT that just sent them. Revision
// is the one genuinely new fact.
type ActivateScenarioOutput struct {
	ScenarioID int64 `json:"scenarioId"`
	Revision   int64 `json:"revision"`
}

func handleActivateScenario(lb *loopback) sdk.ToolHandlerFor[ActivateScenarioInput, ActivateScenarioOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ActivateScenarioInput) (*sdk.CallToolResult, ActivateScenarioOutput, error) {
		var wire struct {
			Revision int64 `json:"revision"`
		}
		method, path := toolPath("activate_scenario", "POST /api/workspaces/{id}/scenarios/{sid}/activate", in.WorkspaceID, in.ScenarioID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ActivateScenarioOutput{}, err
		}
		return nil, ActivateScenarioOutput{ScenarioID: in.ScenarioID, Revision: wire.Revision}, nil
	}
}

// ---- deactivate_scenario ----

// DeactivateScenarioInput is deactivate_scenario's input. Takes no scenario
// id — this route (unlike activate) has nothing to name, only whatever is
// currently active.
type DeactivateScenarioInput struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// DeactivateScenarioOutput is deactivate_scenario's declared output schema.
type DeactivateScenarioOutput struct {
	Revision int64 `json:"revision"`
}

func handleDeactivateScenario(lb *loopback) sdk.ToolHandlerFor[DeactivateScenarioInput, DeactivateScenarioOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeactivateScenarioInput) (*sdk.CallToolResult, DeactivateScenarioOutput, error) {
		var wire struct {
			Revision int64 `json:"revision"`
		}
		method, path := toolPath("deactivate_scenario", "POST /api/workspaces/{id}/scenarios/deactivate", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, DeactivateScenarioOutput{}, err
		}
		return nil, DeactivateScenarioOutput{Revision: wire.Revision}, nil
	}
}
