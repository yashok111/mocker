// addEditTools registers the five workspace and operation tools of slice A2:
// update_workspace_settings, apply_auth_preset, set_operation_variant,
// reset_operation and preview_operation.
//
// Like tools_ops.go, tools_traffic.go and tools_read.go, every tool here is
// an ADAPTER over the admin plane's own routes (§A6): it decodes the JSON
// the handler in internal/admin actually writes, through a PRIVATE wire
// struct that mirrors only the fields this tool needs — never the admin
// package's own (unexported) response types, which this package cannot
// import anyway, and never internal/domain either (tools_traffic.go's own
// header comment on why: one convention across every file in this package,
// not two). Every wire type below cites the handler and line it was read
// from.
//
// Three types here are shared by more than one tool in this file rather than
// copied per tool, the same discipline tools_read.go already applies to
// types from tools_ops.go and tools_traffic.go:
//
//   - RecipeInput mirrors recipes.Recipe (internal/recipes/recipes.go:104-124)
//     verbatim and is used both by set_operation_variant's Responses[status]
//     and by apply_auth_preset's per-binding Recipe — the same recipe shape,
//     bound in two different places in the domain.
//   - VariantInput mirrors overrides.Variant (internal/overrides/
//     overrides.go:72-81) and is set_operation_variant's Responses map value
//     — and preview_operation's Draft carries the SAME map, because D7 pins
//     preview's draft to "the same shape PUT .../operations/{opKey} would
//     save".
//   - OverrideDocumentInput mirrors overrideMutableFields
//     (internal/admin/override_handlers.go:213-222) — the full op_overrides
//     document PUT replaces wholesale. set_operation_variant embeds it
//     directly (so its own JSON is flat: workspaceId/opKey alongside
//     overrideOn/routeOff/…), and preview_operation carries one as its named
//     "draft" field — the two tools build the identical document, one to
//     SAVE it and one to only RENDER what saving it would produce, so one
//     type describes both request shapes.
package mcp

import (
	"context"
	"fmt"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// codeEditConflict mirrors internal/admin/override_handlers.go's own
// codeEditConflict constant (mocker-a3-cas D6): the wire spelling of A3's
// conflict code. This package cannot import that constant — it is
// unexported, in a different package — so the literal is duplicated here
// deliberately, as the one place this package agrees on the code's exact
// spelling.
const codeEditConflict = "edit_conflict"

// errorEnvelopeWire decodes httpx.ErrorBody far enough to read Code and the
// raw Details payload — toolErr (loopback_client.go) decodes the identical
// envelope but keeps only Message, which is exactly the gap D6/D13 property
// 7 exists to close for the six A3 write tools.
type errorEnvelopeWire struct {
	Error struct {
		Code    string           `json:"code"`
		Message string           `json:"message"`
		Details jsonx.RawMessage `json:"details"`
	} `json:"error"`
}

// isEditConflict reports whether status/body is A3's edit_conflict — the
// ONLY 409 shape any handler in this package inspects itself rather than
// handing to toolErr (mocker-a3-cas D6: "the carrier is lb.do on the six
// write handlers, and NOT a branch in toolErr" — toolErr remains the
// generic funnel for the other ~21 StatusConflict sites this surface can
// still answer, including update_endpoint's duplicate-path 409 and
// rename_scenario's duplicate-name 409, both asserted as TOOL ERRORS by
// name in tools_endpoints_test.go). ok is false, and the caller must fall
// through to toolErr, whenever status isn't 409, the body doesn't decode,
// or the code names anything else.
func isEditConflict(status int, body []byte) (details jsonx.RawMessage, ok bool) {
	if status != http.StatusConflict {
		return nil, false
	}
	var env errorEnvelopeWire
	if jsonx.Unmarshal(body, &env) != nil {
		return nil, false
	}
	if env.Error.Code != codeEditConflict {
		return nil, false
	}
	return env.Error.Details, true
}

// goneProbeWire decodes just enough of a conflict's `details` to tell D6's
// tombstone (editConflictGone{gone:true, editVersion:null},
// override_handlers.go) apart from a route's own document shape, none of
// which declare a `gone` field at all.
type goneProbeWire struct {
	Gone bool `json:"gone"`
}

func detailsAreGone(details jsonx.RawMessage) bool {
	var probe goneProbeWire
	return jsonx.Unmarshal(details, &probe) == nil && probe.Gone
}

// writeEditGuarded issues one of A3's six writes through lb.do and
// separates D6's edit_conflict from every other outcome, so toolErr itself
// never grows the branch (its own doc comment, and this file's
// codeEditConflict comment above, both explain why not). On 2xx it decodes
// respBody into out exactly like lb.call would (nil out skips decoding,
// matching lb.call's own contract) and returns a nil conflictDetails. On a
// 409 whose code is edit_conflict it returns the raw `details` payload for
// the caller to project into its OWN typed conflict field (D6's per-route
// compacted shape — decodeOperationConflict, decodeWorkspaceConflict,
// decodeEndpointConflict, decodeScenarioConflict, decodePresetConflict) —
// out is left untouched on this path. Anything else — including a 409 that
// is NOT this slice's, like a duplicate name or path — becomes toolErr,
// unchanged from what every other write in this package already answers.
func writeEditGuarded(ctx context.Context, lb *loopback, method, path string, body []byte, out any) (conflictDetails jsonx.RawMessage, err error) {
	status, respBody, err := lb.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if status >= 200 && status < 300 {
		if out != nil {
			if derr := jsonx.Unmarshal(respBody, out); derr != nil {
				return nil, fmt.Errorf("mcp: decode admin response (status %d): %w", status, derr)
			}
		}
		return nil, nil
	}
	if details, ok := isEditConflict(status, respBody); ok {
		return details, nil
	}
	return nil, toolErr(status, respBody)
}

func addEditTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "update_workspace_settings",
		Description: "Renames a workspace, attaches a spec to it, and/or replaces its response-shaping " +
			"settings — seed, base path, list size, null rate, envelope, identity, auth, CORS, delay and " +
			"the custom 404 body. settings, when given, REPLACES the whole settings object at once: there " +
			"is no partial merge, so every field — including auth.signingKey and identity — must be given " +
			"together or the ones left out are wiped to their zero value. Omit settings entirely to leave " +
			"it untouched, and read back what actually landed from this call's own response rather than " +
			"guessing. specId can only ATTACH a spec, never detach one — the wire has no way to tell " +
			"\"detach\" apart from \"leave alone\", so a workspace already attached to a spec stays attached " +
			"no matter what this sends. editVersion is REQUIRED: the exact value get_workspace's own " +
			"response just reported. A change made in the admin UI (or by another agent) since your last " +
			"read no longer vanishes unannounced: this call answers a 409 edit_conflict, carried as a " +
			"populated conflict field naming the current name/settings/specId and the real editVersion to " +
			"retry with — never a plain tool error for this specific failure.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleUpdateWorkspaceSettings(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "apply_auth_preset",
		Description: "Writes EXACTLY the recipe bindings given, merged into each operation's existing " +
			"override (an operator's own pinned body on an unrelated status survives) — never a fresh " +
			"derivation of its own. Call get_auth_preset first to see what would be proposed, then pass " +
			"that list back here unedited, filtered, or hand-written; passing nothing writes nothing and " +
			"just reports the current revision. editVersions is REQUIRED and must be get_auth_preset's own " +
			"editVersions map, covering every opKey your bindings resolve to — a missing key is refused by " +
			"name. The whole call is refused with a 409 edit_conflict if ANY one row moved since you read " +
			"it: this is all-or-nothing, not a partial apply, and the answer carries a conflict field " +
			"naming only the opKeys that disagreed (never the whole set), each with its current version to " +
			"retry with — or null when that row was deleted out from under you.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleApplyAuthPreset(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "set_operation_variant",
		Description: "The full editor for one operation's override: pinned body, media type, headers, " +
			"recipes, schemaPatch and when-conditions per status, plus the document-level delay and list " +
			"size — a FULL REPLACEMENT of the stored document, not a merge. Every status you want to keep " +
			"must be included on every call, including ones you are not changing — call get_operation " +
			"first to see the current document and resend whatever you want to survive, or it is silently " +
			"discarded. overrideOn and routeOff are required on every call for the same reason " +
			"set_operation_response forces them on: this project has already shipped the bug where an " +
			"omitted overrideOn writes false and the override goes inert — a 200 over a row that never " +
			"serves, which no bar catches — so here the caller must say both explicitly rather than the " +
			"tool guessing. Use set_operation_response instead for the common case of just forcing a " +
			"status while leaving everything else exactly as it was. editVersion is REQUIRED and must be " +
			"YOUR OWN expectation from a prior get_operation call; if that call 404'd (no override yet), " +
			"pass editVersion: 0 — that IS the correct expectation for a fresh row, not an error. A write " +
			"whose editVersion is stale answers with a 409 edit_conflict, carried as a populated conflict " +
			"field (never a generic tool error) holding the current document's compacted shape and its " +
			"real editVersion to retry with.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleSetOperationVariant(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "reset_operation",
		Description: "Deletes the stored override for one operation entirely — no partial undo, the " +
			"whole row is gone and the operation goes back to serving whatever the spec alone would " +
			"generate. Reports deleted:false, not an error, when there was nothing to remove: that " +
			"operation was already at the spec default, which is exactly what this call asked for.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleResetOperation(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "preview_operation",
		Description: "Renders the response one operation WOULD serve for a draft override, without " +
			"saving it, bumping the revision, or writing any traffic — the same validation PUT would run, " +
			"so a draft that previews clean is one PUT would actually accept. Two things this deliberately " +
			"does NOT do: it never applies a forced status, fail_next or pause set on the live session — " +
			"those belong to whoever set them, so a preview that ignores one is not a bug — and if a " +
			"custom endpoint outranks this opKey at match time, it refuses with custom_endpoint_wins " +
			"rather than rendering a body the mock plane would never actually serve. draft is the SAME " +
			"full-document shape set_operation_variant writes — call get_operation first to build it " +
			"accurately, since a field left out here previews as absent, not as \"whatever is currently " +
			"stored\".",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handlePreviewOperation(lb))
}

// ---- shared: recipes, variants, the override document ----

// RecipeInput mirrors recipes.Recipe's wire shape (internal/recipes/
// recipes.go:104-124) field-for-field. Value and Claims are `any` rather
// than jsonx.RawMessage: this package's Input types are decoded FROM the
// MCP client's JSON by the SDK, which already leaves them as native Go
// values (map[string]any, []any, string, float64, bool, nil) — exactly what
// jsonx.Marshal needs to re-encode them on the way to the admin plane, with
// no intermediate byte-slice round trip. jsonx.RawMessage would infer a
// nonsensical byte-array schema here (jsonschema-go has no special case for
// it, unlike time.Time): see this file's own header comment on why `any`,
// not jsonx.RawMessage, is this package's rule for arbitrary-shaped INPUT
// fields specifically.
type RecipeInput struct {
	Kind   string `json:"kind"`
	Value  any    `json:"value,omitempty"`
	Field  string `json:"field,omitempty"`
	Offset string `json:"offset,omitempty"`
	Format string `json:"format,omitempty"`
	Claims any    `json:"claims,omitempty"`
	TTLSec int    `json:"ttlSec,omitempty"`
}

// VariantCondition mirrors overrides.Condition's wire shape (internal/
// overrides/overrides.go:89-94) — one entry of a response variant's when[].
type VariantCondition struct {
	In    string `json:"in"`
	Name  string `json:"name"`
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// VariantInput mirrors overrides.Variant's wire shape (internal/overrides/
// overrides.go:72-81) field-for-field — see this file's header comment on
// why it is shared between set_operation_variant and preview_operation.
// Mode carries omitempty (unlike overrideOn/routeOff on
// OverrideDocumentInput below): "" is overrides.Variant.Mode's own
// documented default ("generated"), a legal value on the admin side
// (overrides.ValidateVariant's own switch accepts "", "generated" and
// "pinned" alike), not a footgun the way an omitted overrideOn is.
type VariantInput struct {
	Mode         string             `json:"mode,omitempty"`
	When         []VariantCondition `json:"when,omitempty"`
	Body         any                `json:"body,omitempty"`
	BodyEncoding string             `json:"bodyEncoding,omitempty"`
	// BodyRef is A6's "asset:<name>" (DESIGN §32.3): on a pinned variant,
	// the body IS that uploaded asset, served verbatim under the asset's
	// own type — exclusive with body, bodyEncoding and mediaType, refused
	// by the admin plane otherwise.
	BodyRef     string                 `json:"bodyRef,omitempty" jsonschema:"asset:<name> — the pinned body is this workspace's uploaded asset (upload_asset), served verbatim under its stored media type. Exclusive with body, bodyEncoding and mediaType."`
	MediaType   string                 `json:"mediaType,omitempty"`
	Headers     map[string]string      `json:"headers,omitempty"`
	SchemaPatch any                    `json:"schemaPatch,omitempty"`
	Recipes     map[string]RecipeInput `json:"recipes,omitempty"`
	// Schema is P7a's (DESIGN §34.3): a CUSTOM endpoint's inline response
	// schema, generated from when no body is pinned. Refused by name on a
	// spec operation's override (400 schema_on_override) — that operation
	// already has a schema, and schemaPatch is how it changes.
	Schema any `json:"schema,omitempty" jsonschema:"a custom endpoint's inline response JSON Schema; refused on a spec operation's override, where schemaPatch is the way to change the schema."`
}

// OverrideDocumentInput mirrors overrideMutableFields (internal/admin/
// override_handlers.go:213-222) — every field PUT .../operations/{opKey}
// writes, in the same full-replacement shape. OverrideOn and RouteOff carry
// NO omitempty tag, which is load-bearing, not an oversight: the SDK's
// schema inference (github.com/google/jsonschema-go — any field without
// omitempty/omitzero becomes a REQUIRED property, exactly like
// CreateWorkspaceInput.Name in tools_ops.go) turns their absence into a
// rejected call rather than a silently-false write. D5 of the mocker-a-mcp
// gate document states the reason directly: overrideMutableFields declares
// both as plain bools and handlePutOperation assigns them wholesale, so an
// omitted overrideOn writes false and the whole override goes inert — a 200
// over a row that never serves, which no bar catches. set_operation_response
// (tools_ops.go:91) dodges the same trap by forcing both itself, because
// that narrower tool never lets the caller choose them at all; this type is
// shared by two tools that must NOT dodge it the same way —
// set_operation_variant because the caller genuinely may want routeOff:true,
// and preview_operation because a preview that silently assumed
// overrideOn:true would misreport what a routeOff:true or overrideOn:false
// draft actually renders.
type OverrideDocumentInput struct {
	OverrideOn    bool                    `json:"overrideOn"`
	RouteOff      bool                    `json:"routeOff"`
	ActiveStatus  *int                    `json:"activeStatus,omitempty"`
	Responses     map[string]VariantInput `json:"responses,omitempty"`
	ListSize      *ListSizeView           `json:"listSize,omitempty"`
	DelayMs       *int                    `json:"delayMs,omitempty"`
	FailDirective any                     `json:"failDirective,omitempty"`
	ValidateReq   *bool                   `json:"validateReq,omitempty"`
}

// ---- update_workspace_settings ----

// OrgInput mirrors domain.Org's wire shape (internal/domain/settings.go:
// 61-65). ID is `any` — domain.Org.ID is itself untyped (real specs use
// both integers and UUIDs, per that field's own doc comment).
type OrgInput struct {
	ID   any    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// IdentityInput mirrors domain.Identity's wire shape (internal/domain/
// settings.go:48-56).
type IdentityInput struct {
	ID    any       `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Roles []string  `json:"roles"`
	Org   *OrgInput `json:"org,omitempty"`
}

// AuthSettingsInput mirrors domain.AuthSettings' wire shape (internal/
// domain/settings.go:68-80).
type AuthSettingsInput struct {
	JWTTTLSec     int    `json:"jwtTtlSec"`
	Alg           string `json:"alg"`
	SigningKey    string `json:"signingKey"`
	RequireHeader bool   `json:"requireHeader"`
}

// CORSSettingsInput mirrors domain.CORSSettings' wire shape (internal/
// domain/settings.go:83-89).
type CORSSettingsInput struct {
	Mode        string `json:"mode"`
	Credentials bool   `json:"credentials"`
}

// SettingsInput mirrors domain.Settings' wire shape (internal/domain/
// settings.go:17-44) field-for-field, used as BOTH the request shape a
// caller builds and the response shape this tool decodes back — see this
// tool's own Description on why: PATCH's settings field is a full
// replacement with no partial merge (handlePatchWorkspace,
// internal/admin/workspace_handlers.go:299-301: "cur.Settings = *body.Settings"
// wholesale), so no field here carries omitempty except NotFoundBody
// (domain.Settings.NotFoundBody itself carries omitempty — nil is its own
// legitimate "no custom body" state, unlike an empty signingKey which has
// no legitimate meaning at all). get_workspace (tools_read.go) deliberately
// leaves identity/auth/cors/notFoundBody out of ITS OWN projection for
// exactly this reason: this type, round-tripped through this tool's own
// request and response, is the one place in this package that carries them.
type SettingsInput struct {
	Seed     int64  `json:"seed"`
	BasePath string `json:"basePath"`
	// BasePathValues declares which values BasePath's {param} segments may
	// take (P3h, domain.Settings.BasePathValues's own doc comment) — no
	// omitempty, same as every other field here: this type round-trips the
	// full settings object, and an omitted field on the way back in would
	// silently clear a declared set the PATCH-equivalent handler this tool
	// dispatches into (workspace_handlers.go's own D4.3 validator) then
	// refuses as base_scope_undeclared the moment anything tries to serve.
	BasePathValues   []string          `json:"basePathValues"`
	ListSize         int               `json:"listSize"`
	NullRate         float64           `json:"nullRate"`
	Envelope         *string           `json:"envelope"`
	Identity         IdentityInput     `json:"identity"`
	Auth             AuthSettingsInput `json:"auth"`
	CORS             CORSSettingsInput `json:"cors"`
	ValidateRequests bool              `json:"validateRequests"`
	DelayMs          int               `json:"delayMs"`
	NotFoundBody     any               `json:"notFoundBody,omitempty"`
}

// UpdateWorkspaceSettingsInput is update_workspace_settings' input. Name and
// SpecID carry omitempty (either may be left out to leave that field
// untouched, mirroring handlePatchWorkspace's own *string/*int64 optionality
// — workspace_handlers.go:272-276); Settings does too, at the TOP level only
// — omit it entirely to leave settings alone, but once given every one of
// its own subfields is required (SettingsInput's own comment explains why).
type UpdateWorkspaceSettingsInput struct {
	WorkspaceID int64          `json:"workspaceId"`
	Name        string         `json:"name,omitempty"`
	SpecID      *int64         `json:"specId,omitempty"`
	Settings    *SettingsInput `json:"settings,omitempty"`
	// EditVersion is A3's REQUIRED compare-and-swap expectation
	// (mocker-a3-cas D10/D11) — the value get_workspace's own response
	// last reported. A plain, non-pointer int64 (never *int64): see
	// SetOperationResponseInput.EditVersion's identical comment on why a
	// pointer here would be required AND nullable. 0 is refused on this
	// table (D7: a workspace addressed by {id} always already exists) —
	// there is no "no expectation" state reachable on this wire.
	EditVersion int64 `json:"editVersion"`
}

// UpdateWorkspaceSettingsOutput is update_workspace_settings' declared
// output schema: the workspace's identity, its attached spec, its new
// revision, and its FULL settings as actually stored — the round-trip
// SettingsInput's own comment promises.
type UpdateWorkspaceSettingsOutput struct {
	ID       int64         `json:"id"`
	Slug     string        `json:"slug"`
	Name     string        `json:"name"`
	SpecID   *int64        `json:"specId,omitempty"`
	Settings SettingsInput `json:"settings"`
	Revision int64         `json:"revision"`
	// EditVersion and Conflict are A3's pair (mocker-a3-cas D5/D6):
	// EditVersion is present on every successful write, so a caller can
	// write again without re-reading; Conflict is present only when the
	// write lost the compare-and-swap, never both at once.
	EditVersion int64                    `json:"editVersion,omitempty"`
	Conflict    *WorkspaceConflictDetail `json:"conflict,omitempty"`
}

// WorkspaceConflictDocument is D6's round-trippable conflict payload for
// PATCH /api/workspaces/{id} — every field the route accepts (name,
// specId, settings), as the server currently holds it, plus the version
// the server actually holds — mirroring workspaceConflictDetails
// (workspace_handlers.go) field-for-field.
type WorkspaceConflictDocument struct {
	Name        string        `json:"name"`
	SpecID      *int64        `json:"specId,omitempty"`
	Settings    SettingsInput `json:"settings"`
	EditVersion int64         `json:"editVersion"`
}

// WorkspaceConflictDetail is update_workspace_settings' typed conflict
// field. Document is nil exactly when Gone is true (D6's tombstone) — the
// workspace itself was deleted by another write between this call's read
// and its write.
type WorkspaceConflictDetail struct {
	Gone     bool                       `json:"gone"`
	Document *WorkspaceConflictDocument `json:"document,omitempty"`
}

// decodeWorkspaceConflict decodes a 409 edit_conflict's `details` for
// PATCH /api/workspaces/{id} into WorkspaceConflictDetail.
func decodeWorkspaceConflict(details jsonx.RawMessage) (*WorkspaceConflictDetail, error) {
	if detailsAreGone(details) {
		return &WorkspaceConflictDetail{Gone: true}, nil
	}
	var doc WorkspaceConflictDocument
	if err := jsonx.Unmarshal(details, &doc); err != nil {
		return nil, fmt.Errorf("mcp: decode edit_conflict details: %w", err)
	}
	return &WorkspaceConflictDetail{Document: &doc}, nil
}

// workspacePatchWire decodes the fields of workspaceView
// (workspace_handlers.go:42-63) this tool projects. OwnerID, ForkedFrom,
// ScenarioID, URL, CreatedAt and UpdatedAt are simply absent — the same
// safe-subset shape workspaceWire (tools_ops.go) and workspaceGetWire
// (tools_read.go) already use for the fields THEY project, and none of the
// six says anything this tool's own caller — someone who just wrote a
// PATCH — needs back. Settings decodes directly into SettingsInput: its
// field tags already match workspaceView's own settings sub-object
// one-for-one, so a second, hand-duplicated decode shape would only be a
// second thing to keep in sync with domain.Settings.
type workspacePatchWire struct {
	ID          int64         `json:"id"`
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	SpecID      *int64        `json:"specId"`
	Revision    int64         `json:"revision"`
	Settings    SettingsInput `json:"settings"`
	EditVersion int64         `json:"editVersion"`
}

func handleUpdateWorkspaceSettings(lb *loopback) sdk.ToolHandlerFor[UpdateWorkspaceSettingsInput, UpdateWorkspaceSettingsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in UpdateWorkspaceSettingsInput) (*sdk.CallToolResult, UpdateWorkspaceSettingsOutput, error) {
		editVersion := in.EditVersion
		patch := struct {
			Name     *string        `json:"name,omitempty"`
			Settings *SettingsInput `json:"settings,omitempty"`
			SpecID   *int64         `json:"specId,omitempty"`
			// EditVersion is the caller's own expectation, explicit on the
			// wire (mocker-a3-cas D5/D7: the hand-off must be built into the
			// body this handler actually marshals, never left to the input
			// schema alone — a required argument the SDK accepts and this
			// function then drops would protect nothing).
			EditVersion *int64 `json:"editVersion"`
		}{Settings: in.Settings, SpecID: in.SpecID, EditVersion: &editVersion}
		if in.Name != "" {
			name := in.Name
			patch.Name = &name
		}

		body, err := jsonx.Marshal(patch)
		if err != nil {
			return nil, UpdateWorkspaceSettingsOutput{}, fmt.Errorf("mcp: encode workspace patch: %w", err)
		}

		var wire workspacePatchWire
		method, path := toolPath("update_workspace_settings", "PATCH /api/workspaces/{id}", in.WorkspaceID)
		details, err := writeEditGuarded(ctx, lb, method, path, body, &wire)
		if err != nil {
			return nil, UpdateWorkspaceSettingsOutput{}, err
		}
		if details != nil {
			conflict, cerr := decodeWorkspaceConflict(details)
			if cerr != nil {
				return nil, UpdateWorkspaceSettingsOutput{}, cerr
			}
			return nil, UpdateWorkspaceSettingsOutput{Conflict: conflict}, nil
		}
		return nil, UpdateWorkspaceSettingsOutput{
			ID: wire.ID, Slug: wire.Slug, Name: wire.Name, SpecID: wire.SpecID,
			Settings: wire.Settings, Revision: wire.Revision, EditVersion: wire.EditVersion,
		}, nil
	}
}

// ---- apply_auth_preset ----

// BindingInput mirrors authpreset.Binding's wire shape (internal/authpreset/
// authpreset.go:46-54) minus Reason and Source: both are Derive's own
// EXPLANATION of why get_auth_preset proposed a binding, read but never
// written back — handleApplyAuthPreset's own validation
// (preset_handlers.go:193-209) never looks at either, so carrying them here
// would be a caller obligation with nothing on the other end that reads it.
type BindingInput struct {
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Status   int         `json:"status"`
	DataPath string      `json:"dataPath"`
	Recipe   RecipeInput `json:"recipe"`
}

// ApplyAuthPresetInput is apply_auth_preset's input. Bindings carries no
// omitempty: handleApplyAuthPreset treats an EMPTY list as a legitimate
// no-op (preset_handlers.go:183-191, "nothing to write"), so this is a
// required-but-possibly-empty array, never an optional one — a caller that
// forgot the argument entirely gets the SDK's own rejection instead of a
// silent no-op it might mistake for success.
type ApplyAuthPresetInput struct {
	WorkspaceID int64          `json:"workspaceId"`
	Bindings    []BindingInput `json:"bindings"`
	// EditVersions is A3's REQUIRED compare-and-swap expectation for this
	// route (mocker-a3-cas D12) — a MAP, not a scalar, because the preset
	// writes many op_overrides rows in one call and has no single token
	// (D12 shape 1: keyed by opKey, one entry per row the submitted
	// bindings resolve to). This is get_auth_preset's own editVersions map,
	// forwarded unedited or narrowed to the subset still relevant after
	// filtering bindings — every opKey the submitted Bindings resolve to
	// must have an entry, or the call is refused by name. No omitempty: a
	// Go map already distinguishes an absent field (nil, rejected by the
	// SDK's required-field inference) from a sent-empty one (`{}`,
	// legal only alongside an empty Bindings — see the zero-bindings
	// short-circuit's own note in preset_handlers.go).
	EditVersions map[string]int64 `json:"editVersions"`
}

// ApplyAuthPresetOutput is apply_auth_preset's declared output schema,
// decoded from applyAuthPresetView (preset_handlers.go). EditVersions is
// the fresh per-row version of every op_overrides row this call touched —
// the same name and shape D5/D12 requires on the way in — or a caller could
// not write the SAME operation again without re-reading first (D13
// property 8). Conflict is present only when the write lost the
// compare-and-swap, never alongside a non-zero Applied.
type ApplyAuthPresetOutput struct {
	Applied      int                      `json:"applied"`
	Revision     int64                    `json:"revision"`
	EditVersions map[string]int64         `json:"editVersions"`
	Conflict     *ApplyAuthPresetConflict `json:"conflict,omitempty"`
}

// ApplyAuthPresetConflict is D12's set-valued conflict payload — NOT
// EditVersions (a different name, a different Go type: this one's values
// are pointers). Keyed by ONLY the opKeys that disagreed, never the whole
// set; a nil value means that row is GONE. The contrast that matters is
// nil versus NO ENTRY — an absent opKey did not disagree and needs nothing
// from the caller. Mirrors presetConflictDetails (preset_handlers.go)
// field-for-field.
type ApplyAuthPresetConflict struct {
	StaleVersions map[string]*int64 `json:"staleVersions"`
}

func decodePresetConflict(details jsonx.RawMessage) (*ApplyAuthPresetConflict, error) {
	var c ApplyAuthPresetConflict
	if err := jsonx.Unmarshal(details, &c); err != nil {
		return nil, fmt.Errorf("mcp: decode edit_conflict details: %w", err)
	}
	return &c, nil
}

func handleApplyAuthPreset(lb *loopback) sdk.ToolHandlerFor[ApplyAuthPresetInput, ApplyAuthPresetOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ApplyAuthPresetInput) (*sdk.CallToolResult, ApplyAuthPresetOutput, error) {
		// BindingInput's own tags already match applyAuthPresetBody's
		// element shape (authpreset.Binding) one-for-one, so this wraps the
		// caller's slice verbatim rather than rebuilding it field by field.
		// EditVersions rides beside Bindings explicitly — the caller's own
		// map, not anything this handler derives — so the hand-off is
		// visible in the body this function actually marshals (D5/D7).
		body, err := jsonx.Marshal(struct {
			Bindings     []BindingInput   `json:"bindings"`
			EditVersions map[string]int64 `json:"editVersions"`
		}{Bindings: in.Bindings, EditVersions: in.EditVersions})
		if err != nil {
			return nil, ApplyAuthPresetOutput{}, fmt.Errorf("mcp: encode auth preset bindings: %w", err)
		}

		var out ApplyAuthPresetOutput
		method, path := toolPath("apply_auth_preset", "POST /api/workspaces/{id}/auth-preset", in.WorkspaceID)
		details, err := writeEditGuarded(ctx, lb, method, path, body, &out)
		if err != nil {
			return nil, ApplyAuthPresetOutput{}, err
		}
		if details != nil {
			conflict, cerr := decodePresetConflict(details)
			if cerr != nil {
				return nil, ApplyAuthPresetOutput{}, cerr
			}
			// EditVersions has no omitempty (D5/D13 property 8: the zero-
			// bindings success response must carry `{}` byte-exactly, not
			// an absent key), so every ApplyAuthPresetOutput needs a
			// non-nil map or the SDK's own output-schema validation
			// rejects the JSON `null` a zero-value Go map would otherwise
			// produce here — this branch never touched `out`, which is
			// why it cannot just reuse out.EditVersions.
			return nil, ApplyAuthPresetOutput{Conflict: conflict, EditVersions: map[string]int64{}}, nil
		}
		return nil, out, nil
	}
}

// ---- set_operation_variant ----

// SetOperationVariantInput is set_operation_variant's input.
// OverrideDocumentInput is embedded, not nested under its own key: encoding/
// json (via jsonx) promotes an anonymous embedded struct's fields onto the
// wire flat, so this decodes/encodes as
// {"workspaceId":…,"opKey":…,"overrideOn":…,"routeOff":…,…} — matching this
// package's existing convention of flat Input fields over a nested object
// the caller has to construct (SetSessionDirectiveInput's own comment,
// tools_traffic.go, draws the identical line for its Target union).
type SetOperationVariantInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	OpKey       string `json:"opKey"`
	OverrideDocumentInput
	// EditVersion sits beside the embedded document, never inside it —
	// putOperationRequest's own sibling rule (override_handlers.go,
	// mocker-a3-cas D7): OverrideDocumentInput also mirrors preview_
	// operation's draft field, and a field added inside it would land on
	// that read-only tool's schema too. REQUIRED, a plain non-pointer
	// int64 for the same reason SetOperationResponseInput.EditVersion is
	// (see its comment): jsonschema-go follows pointers and allows JSON
	// null for them, so *int64 would be required AND nullable. 0 is legal
	// here and means "I expect no row yet" (get_operation 404'd) — this is
	// the one table in A3's population where that is meaningful.
	EditVersion int64 `json:"editVersion"`
}

// SetOperationVariantOutput is set_operation_variant's declared output
// schema. It echoes OpKey/OverrideOn/RouteOff/ResponseCount from the CALL
// itself rather than re-decoding the PUT response's own document
// (overridePutView, override_handlers.go:256-259) — the caller already has
// every one of those values, having just sent them; only Revision is new
// information this tool could not already know.
type SetOperationVariantOutput struct {
	OpKey         string                   `json:"opKey"`
	OverrideOn    bool                     `json:"overrideOn"`
	RouteOff      bool                     `json:"routeOff"`
	ResponseCount int                      `json:"responseCount"`
	Revision      int64                    `json:"revision"`
	EditVersion   int64                    `json:"editVersion,omitempty"`
	Conflict      *OperationConflictDetail `json:"conflict,omitempty"`
}

func handleSetOperationVariant(lb *loopback) sdk.ToolHandlerFor[SetOperationVariantInput, SetOperationVariantOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in SetOperationVariantInput) (*sdk.CallToolResult, SetOperationVariantOutput, error) {
		// The caller's expectation MUST ride along explicitly: marshalling
		// in.OverrideDocumentInput alone (as this handler used to) drops
		// EditVersion on the floor before the wire ever sees it — a
		// required argument the SDK accepts and this function then
		// discards, which is exactly the "reads as protection" failure
		// mode this slice exists to close (mocker-a3-cas D7/D13 property
		// 8). The wrapper below is putOperationRequest's own shape: the
		// embedded document plus EditVersion sitting beside it.
		reqBody := struct {
			OverrideDocumentInput
			EditVersion int64 `json:"editVersion"`
		}{OverrideDocumentInput: in.OverrideDocumentInput, EditVersion: in.EditVersion}
		body, err := jsonx.Marshal(reqBody)
		if err != nil {
			return nil, SetOperationVariantOutput{}, fmt.Errorf("mcp: encode override document: %w", err)
		}

		// overridePutView carries far more than Revision/EditVersion
		// (Method, Path, every response, ListSize, DelayMs, FailDirective,
		// ValidateReq, UpdatedAt) — none of the rest is new information to
		// a caller decoding its OWN just-sent document, so only the two
		// genuinely new fields are decoded here.
		var wire struct {
			Revision    int64 `json:"revision"`
			EditVersion int64 `json:"editVersion"`
		}
		method, path := toolPath("set_operation_variant", "PUT /api/workspaces/{id}/operations/{opKey}", in.WorkspaceID, in.OpKey)
		details, err := writeEditGuarded(ctx, lb, method, path, body, &wire)
		if err != nil {
			return nil, SetOperationVariantOutput{}, err
		}
		if details != nil {
			conflict, cerr := decodeOperationConflict(details)
			if cerr != nil {
				return nil, SetOperationVariantOutput{}, cerr
			}
			return nil, SetOperationVariantOutput{OpKey: in.OpKey, Conflict: conflict}, nil
		}
		return nil, SetOperationVariantOutput{
			OpKey: in.OpKey, OverrideOn: in.OverrideOn, RouteOff: in.RouteOff,
			ResponseCount: len(in.Responses), Revision: wire.Revision, EditVersion: wire.EditVersion,
		}, nil
	}
}

// ---- reset_operation ----

// ResetOperationInput is reset_operation's input.
type ResetOperationInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	OpKey       string `json:"opKey"`
}

// ResetOperationOutput is reset_operation's declared output schema. Revision
// is present only when Deleted — handleDeleteOperation
// (override_handlers.go:524-551) answers 204 with no body at all when
// nothing was removed, so there is no new revision to report in that case
// (the workspace's own revision did not move).
type ResetOperationOutput struct {
	OpKey    string `json:"opKey"`
	Deleted  bool   `json:"deleted"`
	Revision *int64 `json:"revision,omitempty"`
}

func handleResetOperation(lb *loopback) sdk.ToolHandlerFor[ResetOperationInput, ResetOperationOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ResetOperationInput) (*sdk.CallToolResult, ResetOperationOutput, error) {
		method, path := toolPath("reset_operation", "DELETE /api/workspaces/{id}/operations/{opKey}", in.WorkspaceID, in.OpKey)
		// do, not call: handleDeleteOperation answers 204 (nothing removed)
		// or 200 with a revision (a row WAS removed) as two different
		// SUCCESSES, not a status this tool may fold into one uniform
		// decode — a 204's empty body would fail jsonx.Unmarshal outright.
		status, respBody, err := lb.do(ctx, method, path, nil)
		if err != nil {
			return nil, ResetOperationOutput{}, err
		}
		switch status {
		case http.StatusNoContent:
			return nil, ResetOperationOutput{OpKey: in.OpKey, Deleted: false}, nil
		case http.StatusOK:
			var wire struct {
				Revision int64 `json:"revision"`
			}
			if err := jsonx.Unmarshal(respBody, &wire); err != nil {
				return nil, ResetOperationOutput{}, fmt.Errorf("mcp: decode delete response: %w", err)
			}
			revision := wire.Revision
			return nil, ResetOperationOutput{OpKey: in.OpKey, Deleted: true, Revision: &revision}, nil
		default:
			return nil, ResetOperationOutput{}, toolErr(status, respBody)
		}
	}
}

// ---- preview_operation ----

// PreviewOperationInput is preview_operation's input, mirroring
// previewRequestWire (preview_handlers.go:83-91) field-for-field. Status is
// a string, not an int, for the identical reason previewRequestWire.Status
// is a *string: the wire expects a quoted 3-digit status
// ("status must be a 3-digit status code", parsePreviewStatus,
// preview_handlers.go:253-262), matching the string keys Draft.Responses is
// keyed by — not the bare integer a RESULT document's own "status" field is.
type PreviewOperationInput struct {
	WorkspaceID int64                 `json:"workspaceId"`
	OpKey       string                `json:"opKey"`
	Draft       OverrideDocumentInput `json:"draft"`
	Status      string                `json:"status,omitempty"`
	Query       string                `json:"query,omitempty"`
	Headers     map[string]string     `json:"headers,omitempty"`
	Body        any                   `json:"body,omitempty"`
	PathParams  map[string]string     `json:"pathParams,omitempty"`
}

// PreviewOperationOutput is preview_operation's declared output schema,
// projected from previewResultView (preview_handlers.go:111-125) with
// Refused flattened to two plain strings rather than a nested object —
// SessionTarget's own flattening (tools_traffic.go) sets the precedent this
// package already follows for a small, fixed-shape nested value. Every
// OTHER field of previewResultView is carried through unchanged: unlike
// this file's write tools, a preview result IS the thing an agent called
// this tool to see, so nothing here is compacted away.
type PreviewOperationOutput struct {
	Status             int               `json:"status"`
	StatusSource       string            `json:"statusSource"`
	MediaType          string            `json:"mediaType,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	Encoding           string            `json:"encoding,omitempty"`
	Body               *string           `json:"body,omitempty"`
	NoBody             bool              `json:"noBody"`
	RouteOff           bool              `json:"routeOff"`
	RefusedReason      string            `json:"refusedReason,omitempty"`
	RefusedDetail      string            `json:"refusedDetail,omitempty"`
	SchemaPatchApplied bool              `json:"schemaPatchApplied"`
	RecipesBound       int               `json:"recipesBound"`
	DelayMs            int               `json:"delayMs"`
	ShadowedBy         string            `json:"shadowedBy,omitempty"`
}

// previewResultWire decodes previewResultView (preview_handlers.go:111-125).
type previewResultWire struct {
	Status             int               `json:"status"`
	StatusSource       string            `json:"statusSource"`
	MediaType          string            `json:"mediaType,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	Encoding           string            `json:"encoding,omitempty"`
	Body               *string           `json:"body,omitempty"`
	NoBody             bool              `json:"noBody"`
	RouteOff           bool              `json:"routeOff"`
	Refused            *refusedWire      `json:"refused"`
	SchemaPatchApplied bool              `json:"schemaPatchApplied"`
	RecipesBound       int               `json:"recipesBound"`
	DelayMs            int               `json:"delayMs"`
	ShadowedBy         *string           `json:"shadowedBy"`
}

// refusedWire decodes refusedView (preview_handlers.go:98-101).
type refusedWire struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

func handlePreviewOperation(lb *loopback) sdk.ToolHandlerFor[PreviewOperationInput, PreviewOperationOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in PreviewOperationInput) (*sdk.CallToolResult, PreviewOperationOutput, error) {
		reqBody := struct {
			OpKey      string                `json:"opKey"`
			Draft      OverrideDocumentInput `json:"draft"`
			Status     *string               `json:"status,omitempty"`
			Query      string                `json:"query,omitempty"`
			Headers    map[string]string     `json:"headers,omitempty"`
			Body       any                   `json:"body,omitempty"`
			PathParams map[string]string     `json:"pathParams,omitempty"`
		}{OpKey: in.OpKey, Draft: in.Draft, Query: in.Query, Headers: in.Headers, Body: in.Body, PathParams: in.PathParams}
		if in.Status != "" {
			status := in.Status
			reqBody.Status = &status
		}

		body, err := jsonx.Marshal(reqBody)
		if err != nil {
			return nil, PreviewOperationOutput{}, fmt.Errorf("mcp: encode preview request: %w", err)
		}

		// lb.call, not lb.do: every non-2xx D6/D12 of the preview taxonomy
		// answers (invalid_draft, custom_endpoint_wins, no_spec,
		// operation_not_found, missing_path_param, resource_serves —
		// preview_handlers.go's own wire-code const block) is genuinely this
		// tool's own error to report, exactly the same shape
		// override_from_traffic's own 409 refusal already takes
		// (tools_traffic.go) — toolErr surfaces the admin plane's own
		// message verbatim, which for custom_endpoint_wins already names
		// the outranking endpoint, and for resource_serves names the
		// confirmed resource that took the route over (P3a, D12).
		var wire previewResultWire
		method, path := toolPath("preview_operation", "POST /api/workspaces/{id}/preview", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, PreviewOperationOutput{}, err
		}

		out := PreviewOperationOutput{
			Status: wire.Status, StatusSource: wire.StatusSource, MediaType: wire.MediaType,
			Headers: wire.Headers, Encoding: wire.Encoding, Body: wire.Body,
			NoBody: wire.NoBody, RouteOff: wire.RouteOff,
			SchemaPatchApplied: wire.SchemaPatchApplied, RecipesBound: wire.RecipesBound, DelayMs: wire.DelayMs,
		}
		if wire.Refused != nil {
			out.RefusedReason = wire.Refused.Reason
			out.RefusedDetail = wire.Refused.Detail
		}
		if wire.ShadowedBy != nil {
			out.ShadowedBy = *wire.ShadowedBy
		}
		return nil, out, nil
	}
}
