// tools_entities.go registers A4's third tool (decisions.md
// mocker-a4-mcp-reach D1.3, D4): list_resource_entities, an adapter over
// the NEW GET /api/workspaces/{id}/resources/{family}/entities
// (internal/admin/resource_handlers.go's handleListResourceEntities) — a
// confirmed resource family's entity ROWS, structured, paginated and
// scope-filtered, which today are readable only as their COUNT
// (list_resources' own EntityCount/ByBaseScope) or incidentally as bodies
// inside traffic log rows. Read-only, agent-only: no screen calls the route
// it wraps (coverage.test.ts's own EXEMPT entry names the policy), and this
// tool never writes — a confirmed resource has no editor at all (D4's own
// "no write verb over entity rows"), a rule this tool changes nothing
// about.
//
// Addressing (D4's own "Addressing" clause): the {family} path segment
// carries url.PathEscape(RouteFamily) on this tool's own side, built
// BEFORE the call to toolPath — renderParam (routes.go) substitutes a
// string path parameter RAW by design, the same convention opKey already
// relies on (opKey arrives pre-escaped from overrides.OpKey), and
// route_family is NOT pre-escaped the way opKey is: it crosses the wire
// today as a plain json:"routeFamily" string and carries a leading "/"
// plus, when nested, internal "/{}" segments (D4). Escaping it here, once,
// before it ever reaches renderParam, is what keeps a nested family's own
// slashes from being read as extra path segments by the admin mux.
package mcp

import (
	"context"
	"fmt"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// listResourceEntitiesDefaultLimit/listResourceEntitiesMaxLimit mirror
// internal/admin/resource_handlers.go's own resourceEntitiesDefaultLimit
// (100) and resourceEntitiesMaxLimit (500) exactly, rather than reusing
// them (both are unexported in package admin, and this package cannot
// import them anyway — §A6) — the same duplication listTrafficDefaultLimit/
// listTrafficMaxLimit (tools_traffic.go) already accepts, for the same
// reason: computing the effective limit here is what lets this tool report
// an honest limit alongside lastId.
const (
	listResourceEntitiesDefaultLimit = 100
	listResourceEntitiesMaxLimit     = 500
)

func addEntityTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "list_resource_entities",
		Description: "Lists a confirmed resource family's stored entity rows — the rows GET/POST/DELETE " +
			"on the family's own mock-plane routes actually serve, structured and paginated, rather than " +
			"only their count (list_resources) or an incidental sighting inside a traffic log body. Rows " +
			"come back ordered by id ascending. Pass after (a row id, from a previous call's lastId) to " +
			"page forward — the response always echoes lastId, unchanged on an empty page, so a repeated " +
			"call with after set to it never replays a row already seen. scopeKey and baseScopeKey are " +
			"independently optional filters: omit either to see every value of that axis, or pass one " +
			"(including the empty string, which is itself a real, addressable scope for a family with no " +
			"outer parameter) to pin it — list_resources' own byBaseScope breakdown is what tells a caller " +
			"which baseScopeKey values exist to ask for. limit defaults to 100 and is clamped to 500. " +
			"routeFamily addresses the family by its natural key (the same string list_resources and " +
			"decide_resource use), never by a numeric resource id — a wrong family answers unknown_family, " +
			"the identical 404 for a never-suggested, declined, or never-confirmed family, because the " +
			"repair (decide_resource) is the same in every case. Read-only: a confirmed resource has no " +
			"editor here or anywhere else — a wrong one is declined and confirmed again through " +
			"decide_resource, never patched.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListResourceEntities(lb))

	// A11: the read's two write siblings.
	sdk.AddTool(s, &sdk.Tool{
		Name: "set_resource_entity",
		Description: "Creates or replaces ONE row of a confirmed resource family by key — the row the " +
			"family's mock-plane routes serve at GET X/{key}. `data` is the whole row as a JSON " +
			"object; data[idField] is overwritten with the key coerced to the family's id type, so " +
			"the key IS the identity and the body cannot disagree with its own address. A decimal " +
			"key raises the family's counter to at least that value, so the mock plane's next POST " +
			"never collides with a row you placed. For a nested family pass scopeKey (the outer " +
			"parameter values, url.PathEscape'd and joined with /); for a parameterised basePath pass " +
			"baseScopeKey — both default to \"\", the top-level scope, and neither is verified against " +
			"live ancestor rows (a row under an unanchored scope is stored but unreachable until one " +
			"exists). Returns the stored row and created (true: inserted; false: replaced). 404 " +
			"unknown_family when the family is not confirmed; 409 entity_limit over the family's row " +
			"or byte cap. No revision bump, no auto checkpoint: undo is create_checkpoint before, " +
			"rollback_workspace {restoreData: true} after. The body is not validated against the " +
			"family's schema — the mock plane's own POST does not validate either.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleSetResourceEntity(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "delete_resource_entity",
		Description: "Deletes ONE row of a confirmed resource family by key, in the scope named by " +
			"scopeKey/baseScopeKey (both default to \"\"). 404 entity_not_found when no row has that " +
			"key in that scope, 404 unknown_family when the family is not confirmed. No confirmSlug: " +
			"this removes one row, exactly what the mock plane's anonymous DELETE X/{key} does; the " +
			"verbs that destroy a whole family (decide_resource declined, reset_resource_data) are " +
			"the ones that ask for it. Not undone by a config rollback — only by one with " +
			"restoreData: true to a checkpoint taken before.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleDeleteResourceEntity(lb))
}

// ---- list_resource_entities ----

// ListResourceEntitiesInput is list_resource_entities' input. ScopeKey and
// BaseScopeKey are *string, not string, so this tool can tell "omitted"
// (nil — any value of that axis) apart from "passed as the empty string"
// (a non-nil pointer to "" — the empty tuple, itself a real, addressable
// scope for a family with no outer parameter or no parameterised
// basePath) — the same distinction the admin route's own
// parseResourceScopeFilter draws with url.Values.Has rather than .Get
// alone (resource_handlers.go). A JSON omitempty on a pointer field is
// exactly this rule already: nil is omitted from the wire, a pointer to ""
// is not.
type ListResourceEntitiesInput struct {
	WorkspaceID  int64   `json:"workspaceId"`
	RouteFamily  string  `json:"routeFamily"`
	Limit        int     `json:"limit,omitempty"`
	After        int64   `json:"after,omitempty"`
	ScopeKey     *string `json:"scopeKey,omitempty" jsonschema:"Optional exact scopeKey filter for a nested family. Omit to see every scope; pass \"\" (including an explicit empty string) to see only the family's own top-level scope."`
	BaseScopeKey *string `json:"baseScopeKey,omitempty" jsonschema:"Optional exact baseScopeKey filter, for a workspace whose basePath carries a parameter. Omit to see every declared base value; pass \"\" to see rows with no base scope."`
}

// ResourceEntityLine is one row of list_resource_entities' output,
// projected from resourceEntityView (internal/admin/resource_handlers.go).
// Data is embedded raw — the stored JSON object, already carrying the
// forced id — never re-decoded here, exactly as the admin view itself
// never re-decodes it on the read path.
type ResourceEntityLine struct {
	ID           int64            `json:"id"`
	EntityKey    string           `json:"entityKey"`
	ScopeKey     string           `json:"scopeKey"`
	BaseScopeKey string           `json:"baseScopeKey"`
	Data         jsonx.RawMessage `json:"data"`
	CreatedAt    string           `json:"createdAt"`
	UpdatedAt    string           `json:"updatedAt"`
}

// ListResourceEntitiesOutput is list_resource_entities' declared output
// schema, projected from resourceEntitiesView (resource_handlers.go).
// LastID carries NO omitempty, matching ListTrafficOutput's own
// Returned/Limit/HasMore (tools_traffic.go): a lastId of 0 on an empty
// family is exactly as meaningful as a nonzero one, and omitempty would
// silently drop it from the wire on that page — indistinguishable, to a
// model reading raw JSON, from the field never having existed.
type ListResourceEntitiesOutput struct {
	Rows   []ResourceEntityLine `json:"rows"`
	LastID int64                `json:"lastId"`
}

// resourceEntityWire decodes one row of resourceEntitiesView.Rows
// (resource_handlers.go's resourceEntityView) — GET
// .../resources/{family}/entities' own row shape.
type resourceEntityWire struct {
	ID           int64            `json:"id"`
	EntityKey    string           `json:"entityKey"`
	ScopeKey     string           `json:"scopeKey"`
	BaseScopeKey string           `json:"baseScopeKey"`
	Data         jsonx.RawMessage `json:"data"`
	CreatedAt    string           `json:"createdAt"`
	UpdatedAt    string           `json:"updatedAt"`
}

// resourceEntitiesWire decodes resourceEntitiesView's whole body
// (resource_handlers.go) — rows plus lastId, the same field name and
// mechanic list_traffic's own D6 widening gives trafficPollView, because
// both key on a row id.
type resourceEntitiesWire struct {
	Rows   []resourceEntityWire `json:"rows"`
	LastID int64                `json:"lastId"`
}

func handleListResourceEntities(lb *loopback) sdk.ToolHandlerFor[ListResourceEntitiesInput, ListResourceEntitiesOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListResourceEntitiesInput) (*sdk.CallToolResult, ListResourceEntitiesOutput, error) {
		limit := listResourceEntitiesDefaultLimit
		if in.Limit > 0 {
			limit = in.Limit
		}
		if limit > listResourceEntitiesMaxLimit {
			limit = listResourceEntitiesMaxLimit
		}

		// D4's own Addressing clause: escape the family segment HERE,
		// before it ever reaches toolPath/renderParam, because
		// renderParam substitutes a string path param raw (routes.go's
		// own opKey trap) and route_family is not pre-escaped the way
		// opKey is — see this file's own header comment.
		escapedFamily := url.PathEscape(in.RouteFamily)
		method, base := toolPath("list_resource_entities",
			"GET /api/workspaces/{id}/resources/{family}/entities", in.WorkspaceID, escapedFamily)

		// url.Values so a scopeKey/baseScopeKey value carrying its own
		// reserved characters (a nested family's scope is itself a
		// "/"-joined, percent-escaped tuple — resources.EncodeScope) is
		// query-escaped correctly, and so ScopeKey/BaseScopeKey's
		// nil-vs-pointer-to-empty distinction becomes url.Values.Has's
		// own absent-vs-present-and-empty distinction on the wire,
		// exactly mirroring parseResourceScopeFilter's own read of it.
		q := url.Values{}
		q.Set("limit", fmt.Sprintf("%d", limit))
		if in.After > 0 {
			q.Set("after", fmt.Sprintf("%d", in.After))
		}
		if in.ScopeKey != nil {
			q.Set("scopeKey", *in.ScopeKey)
		}
		if in.BaseScopeKey != nil {
			q.Set("baseScopeKey", *in.BaseScopeKey)
		}
		path := base + "?" + q.Encode()

		var wire resourceEntitiesWire
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListResourceEntitiesOutput{}, err
		}

		rows := make([]ResourceEntityLine, len(wire.Rows))
		for i, e := range wire.Rows {
			rows[i] = ResourceEntityLine(e)
		}
		return nil, ListResourceEntitiesOutput{Rows: rows, LastID: wire.LastID}, nil
	}
}

// ---- set_resource_entity / delete_resource_entity (A11) ----

// SetResourceEntityInput is set_resource_entity's input.
type SetResourceEntityInput struct {
	WorkspaceID  int64          `json:"workspaceId"`
	RouteFamily  string         `json:"routeFamily"`
	EntityKey    string         `json:"entityKey" jsonschema:"the row's key, 1..128 characters of [A-Za-z0-9._~-]; a decimal integer for a family the mock plane populated"`
	Data         map[string]any `json:"data" jsonschema:"the whole row as a JSON object; data[idField] is overwritten with the key"`
	ScopeKey     string         `json:"scopeKey,omitempty" jsonschema:"a nested family's outer parameter tuple, EncodeScope'd; default the top-level scope"`
	BaseScopeKey string         `json:"baseScopeKey,omitempty" jsonschema:"the declared basePath-parameter value tuple; default none"`
}

// ResourceEntityRowOutput is the row as set_resource_entity declares it:
// Data is `any`, not jsonx.RawMessage — the SDK infers a JSON schema from
// the output type and reads a []byte as an array, so a RawMessage row body
// (always a JSON object) would fail its own output validation.
type ResourceEntityRowOutput struct {
	ID           int64  `json:"id"`
	EntityKey    string `json:"entityKey"`
	ScopeKey     string `json:"scopeKey"`
	BaseScopeKey string `json:"baseScopeKey"`
	Data         any    `json:"data"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// SetResourceEntityOutput is set_resource_entity's declared output.
type SetResourceEntityOutput struct {
	Row     ResourceEntityRowOutput `json:"row"`
	Created bool                    `json:"created"`
}

type setResourceEntityWire struct {
	Row     resourceEntityWire `json:"row"`
	Created bool               `json:"created"`
}

func handleSetResourceEntity(lb *loopback) sdk.ToolHandlerFor[SetResourceEntityInput, SetResourceEntityOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in SetResourceEntityInput) (*sdk.CallToolResult, SetResourceEntityOutput, error) {
		if in.Data == nil {
			return nil, SetResourceEntityOutput{}, fmt.Errorf("set_resource_entity: data is required — the whole row as a JSON object")
		}
		if in.EntityKey == "" {
			return nil, SetResourceEntityOutput{}, fmt.Errorf("set_resource_entity: entityKey is required")
		}
		body, err := jsonx.Marshal(map[string]any{"data": in.Data, "scopeKey": in.ScopeKey, "baseScopeKey": in.BaseScopeKey})
		if err != nil {
			return nil, SetResourceEntityOutput{}, fmt.Errorf("set_resource_entity: encode request: %w", err)
		}
		method, path := toolPath("set_resource_entity",
			"PUT /api/workspaces/{id}/resources/{family}/entities/{key}",
			in.WorkspaceID, url.PathEscape(in.RouteFamily), url.PathEscape(in.EntityKey))
		var wire setResourceEntityWire
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, SetResourceEntityOutput{}, err
		}
		var data any
		if len(wire.Row.Data) > 0 {
			if err := jsonx.Unmarshal(wire.Row.Data, &data); err != nil {
				return nil, SetResourceEntityOutput{}, fmt.Errorf("set_resource_entity: decode row data: %w", err)
			}
		}
		return nil, SetResourceEntityOutput{Row: ResourceEntityRowOutput{
			ID: wire.Row.ID, EntityKey: wire.Row.EntityKey, ScopeKey: wire.Row.ScopeKey, BaseScopeKey: wire.Row.BaseScopeKey,
			Data: data, CreatedAt: wire.Row.CreatedAt, UpdatedAt: wire.Row.UpdatedAt,
		}, Created: wire.Created}, nil
	}
}

// DeleteResourceEntityInput is delete_resource_entity's input.
type DeleteResourceEntityInput struct {
	WorkspaceID  int64  `json:"workspaceId"`
	RouteFamily  string `json:"routeFamily"`
	EntityKey    string `json:"entityKey"`
	ScopeKey     string `json:"scopeKey,omitempty"`
	BaseScopeKey string `json:"baseScopeKey,omitempty"`
}

// DeleteResourceEntityOutput is delete_resource_entity's declared output.
type DeleteResourceEntityOutput struct {
	EntityKey string `json:"entityKey"`
	Deleted   bool   `json:"deleted"`
}

func handleDeleteResourceEntity(lb *loopback) sdk.ToolHandlerFor[DeleteResourceEntityInput, DeleteResourceEntityOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeleteResourceEntityInput) (*sdk.CallToolResult, DeleteResourceEntityOutput, error) {
		if in.EntityKey == "" {
			return nil, DeleteResourceEntityOutput{}, fmt.Errorf("delete_resource_entity: entityKey is required")
		}
		body, err := jsonx.Marshal(map[string]any{"scopeKey": in.ScopeKey, "baseScopeKey": in.BaseScopeKey})
		if err != nil {
			return nil, DeleteResourceEntityOutput{}, fmt.Errorf("delete_resource_entity: encode request: %w", err)
		}
		method, path := toolPath("delete_resource_entity",
			"DELETE /api/workspaces/{id}/resources/{family}/entities/{key}",
			in.WorkspaceID, url.PathEscape(in.RouteFamily), url.PathEscape(in.EntityKey))
		if err := lb.call(ctx, method, path, body, nil); err != nil {
			return nil, DeleteResourceEntityOutput{}, err
		}
		return nil, DeleteResourceEntityOutput{EntityKey: in.EntityKey, Deleted: true}, nil
	}
}
