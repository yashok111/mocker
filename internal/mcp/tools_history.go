// addHistoryTools registers slice A2's history group (§C tools of the
// mocker-a-mcp gate document's D5): create_checkpoint, delete_checkpoint,
// rollback_workspace and reset_overrides, plus the two standalone
// destructive tools delete_workspace and clear_traffic that D6 groups with
// them for the same confirmation rule.
//
// Like tools_ops.go, tools_traffic.go, tools_read.go, tools_edit.go and
// tools_endpoints.go, every tool here is an ADAPTER over the admin plane's
// own routes: it decodes the JSON the handler in internal/admin actually
// writes, through a PRIVATE wire struct that mirrors only the fields this
// tool needs. Every wire type below cites the handler and line it was read
// from.
//
// FIVE of these six tools are D6's confirmation tools — every one but
// create_checkpoint — and each calls the SHARED confirmWorkspaceSlug
// (confirm.go) as its handler's first statement, issuing nothing on a
// non-nil error. delete_scenario, D6's sixth member, is built in
// tools_endpoints.go by a different agent from the same helper; this file
// does not write a second copy of it (confirm.go's own doc comment on why
// that would repeat internal/httpx.BrowserExecutableMediaType's history).
//
// D6 draws a line THROUGH these five, not around them, and the Descriptions
// below say so rather than reading as six paraphrases of "this is
// dangerous, confirm first". Two more tools built elsewhere in this package
// — decide_resource's decline branch and reset_resource_data
// (tools_resources.go, D7 of mocker-p3b-resources) — carry the identical
// confirmSlug argument and description for the identical reason, taking
// the total population of tools that require confirmSlug to eight; neither
// calls confirmWorkspaceSlug, because the admin route each of them wraps
// already performs the live-slug comparison itself (tools_resources.go's
// own package doc comment says why):
//
//   - delete_workspace, clear_traffic and delete_checkpoint take NO
//     checkpoint of what they destroy — delete_workspace is the extreme
//     case (every workspace_id column cascades on delete, including
//     checkpoints.workspace_id itself: internal/store/migrations/
//     0001_init.sql:218), clear_traffic was never covered by a checkpoint at
//     all, and delete_checkpoint removes a history row, which is not served
//     state a rollback could restore anyway
//     (internal/checkpoints/repo.go:527's own doc comment on Delete).
//   - rollback_workspace and reset_overrides ARE undoable: each writes a
//     pre-destructive checkpoint inside the same transaction that applies it
//     (internal/checkpoints/repo.go:402#rollbackTx, :501#resetTx). They
//     still require confirmSlug, for the OTHER reason D6 states: on a flat
//     surface they sit among near neighbours, and the failure there is a
//     SUCCESSFUL call to the wrong tool, which no input schema rejects.
//     Their Descriptions say where the way back went, because knowing a
//     pre-destructive checkpoint exists is what recovering from either
//     actually requires.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

func addHistoryTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "create_checkpoint",
		Description: "Saves a manual, labelled snapshot of a workspace's Workspace layer — settings, " +
			"operation overrides, custom endpoints, confirmed-resource configuration (resources, " +
			"resource_decisions) and confirmed-resource entity rows — that rollback_workspace can later " +
			"restore, the last of those only when its own restoreData is set to true. Use " +
			"this before a risky series of edits, as a named point to come back to. Do not blindly retry " +
			"this after a lost or timed-out response: checkpoints carries no unique key at all, so a " +
			"retry creates a SECOND checkpoint with the same label rather than being refused — call " +
			"list_checkpoints first to check whether the first attempt actually landed.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleCreateCheckpoint(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "delete_checkpoint",
		Description: "Permanently removes one row from a workspace's checkpoint history — any kind, " +
			"including the newest one or the last one left; an empty history is legal. This does not " +
			"undo anything the checkpoint itself once could have restored: deleting a checkpoint changes " +
			"nothing the mock plane serves, only what rollback_workspace can later reach. There is no " +
			"undo for the deletion itself — the row is gone, not archived. Requires confirmSlug naming " +
			"the workspace the checkpoint belongs to, checked live against the admin plane before " +
			"anything is deleted.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleDeleteCheckpoint(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "rollback_workspace",
		Description: "Restores a workspace's Workspace layer — settings, operation overrides and custom " +
			"endpoints — wholesale (never merged) to exactly the state one checkpoint captured, " +
			"discarding whatever the layer holds right now. The checkpoint's confirmed-resource " +
			"configuration (resources, resource_decisions) is restored UPSERT-ONLY: a row the snapshot " +
			"names is written back, but no row is ever deleted by this call, so a family confirmed after " +
			"the checkpoint survives a rollback to it and a family declined after it comes back " +
			"configured. Its entities are untouched UNLESS restoreData is set to true, in which case " +
			"they are restored too — from the checkpoint's saved data, when it has one; a checkpoint " +
			"that carries none refuses with 409 no_data_snapshot rather than silently skipping. Before " +
			"applying, this itself writes a pre-destructive checkpoint of the " +
			"CURRENT state (kind \"pre-destructive\" in list_checkpoints) — call rollback_workspace again " +
			"against that one to undo this call. Requires confirmSlug naming the workspace being rolled " +
			"back: for a plain rollback the argument exists less to stop an accident than to stop a " +
			"successful call aimed at the wrong workspace, since nothing about this tool's shape would " +
			"catch that on its own; with restoreData:true it is additionally mandatory, the same way " +
			"decide_resource's decline and reset_resource_data require it, because that call can destroy " +
			"entity rows the identical way those two can.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleRollbackWorkspace(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "reset_overrides",
		Description: "Deletes every operation override AND every custom endpoint in a workspace's " +
			"Workspace layer — settings are untouched, and so is any confirmed resource: this route " +
			"never touches a resources or entities row, only op_overrides and custom_endpoints " +
			"(reset-data on the resources surface is the resource layer's own, separate reset). Before " +
			"applying, this itself writes a pre-destructive checkpoint of the CURRENT " +
			"state (kind \"pre-destructive\" in list_checkpoints), UNLESS there was nothing to reset, in " +
			"which case it is a true no-op: no checkpoint is written and the response says so " +
			"(changed:false). Call rollback_workspace against the checkpoint this call wrote to undo it. " +
			"Requires confirmSlug naming the workspace being reset, for the same reason " +
			"rollback_workspace does: a flat tool surface puts this beside near neighbours, and a " +
			"successful call aimed at the wrong workspace is the failure no input schema can reject.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleResetOverrides(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "delete_workspace",
		Description: "Permanently deletes a workspace and everything under it — settings, operation " +
			"overrides, custom endpoints, scenarios and recorded traffic all cascade with " +
			"it in the schema. Nothing brings a deleted workspace back: there is no undo, no trash, and " +
			"no other tool in this surface that touches it afterward. Requires confirmSlug naming the " +
			"exact workspace being deleted, read live from the admin plane and compared before the " +
			"delete is issued — a mismatch refuses the call and nothing is touched.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleDeleteWorkspace(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "clear_traffic",
		Description: "Permanently deletes every recorded traffic row for a workspace. Traffic was never " +
			"covered by a checkpoint — rollback_workspace cannot bring cleared rows back — but the mock " +
			"plane keeps serving normally afterward and the table simply refills as new requests arrive, " +
			"which is the sense in which this is safe to run. Requires confirmSlug naming the workspace " +
			"being cleared, checked live against the admin plane before anything is deleted.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleClearTraffic(lb))
}

// ---- create_checkpoint ----
//
// create_checkpoint's 201 body is checkpointSummaryView
// (internal/admin/checkpoint_handlers.go:32-44) — the SAME shape
// list_checkpoints' rows share, so this tool decodes it through
// checkpointSummaryWire and projects it through CheckpointLine, both
// already declared in tools_read.go, rather than a second copy of either.

// CreateCheckpointInput is create_checkpoint's input. Label has no
// `omitempty` — the SDK's schema inference (CreateWorkspaceInput's own
// comment in tools_ops.go gives the full reasoning) makes it required, and
// the admin route refuses an empty-after-trim label with its own 400
// anyway (checkpoints.validateLabel, checkpoints.go:225-234) — this tool
// does not re-derive that check client-side, matching set_session_
// directive's own choice to let the admin route's message speak for a
// validation it does not own.
type CreateCheckpointInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Label       string `json:"label"`
}

// CreateCheckpointOutput is create_checkpoint's declared output schema.
// Checkpoint reuses CheckpointLine (tools_read.go) — list_checkpoints'
// own row projection — rather than a second type for the identical shape.
type CreateCheckpointOutput struct {
	Checkpoint CheckpointLine `json:"checkpoint"`
}

// createCheckpointBody is POST .../checkpoints' request body
// (createCheckpointRequest, checkpoint_handlers.go:69-73) — the one field an
// operator supplies.
type createCheckpointBody struct {
	Label string `json:"label"`
}

func handleCreateCheckpoint(lb *loopback) sdk.ToolHandlerFor[CreateCheckpointInput, CreateCheckpointOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in CreateCheckpointInput) (*sdk.CallToolResult, CreateCheckpointOutput, error) {
		body, err := jsonx.Marshal(createCheckpointBody{Label: in.Label})
		if err != nil {
			return nil, CreateCheckpointOutput{}, fmt.Errorf("mcp: encode create_checkpoint request: %w", err)
		}

		var wire checkpointSummaryWire
		method, path := toolPath("create_checkpoint", "POST /api/workspaces/{id}/checkpoints", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, CreateCheckpointOutput{}, err
		}
		// Plain type conversion — checkpointSummaryWire and CheckpointLine
		// share an identical field set, the same move handleListCheckpoints
		// (tools_read.go) already makes for the same wire type (staticcheck
		// S1016).
		return nil, CreateCheckpointOutput{Checkpoint: CheckpointLine(wire)}, nil
	}
}

// ---- delete_checkpoint ----

// DeleteCheckpointInput is delete_checkpoint's input. ConfirmSlug is one of
// the eight tools' shared confirmSlug arguments — confirmWorkspaceSlug
// (confirm.go) reads it for this one and five others (D6). confirmSlugDoc's
// exact wording is copied into the jsonschema
// tag below verbatim rather than referenced, because a Go struct tag cannot
// reference a constant: confirmSlugField's own doc comment states this is
// deliberate, so the eight tools that carry this argument read as one rule
// rather than eight paraphrases of it.
type DeleteCheckpointInput struct {
	WorkspaceID  int64  `json:"workspaceId"`
	CheckpointID int64  `json:"checkpointId"`
	ConfirmSlug  string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// DeleteCheckpointOutput is delete_checkpoint's declared output schema.
// Deleted is always true, for the same reason DeleteEndpointOutput's own
// comment (tools_endpoints.go) gives: every non-2xx already became a tool
// error before this line runs.
type DeleteCheckpointOutput struct {
	CheckpointID int64 `json:"checkpointId"`
	Deleted      bool  `json:"deleted"`
}

func handleDeleteCheckpoint(lb *loopback) sdk.ToolHandlerFor[DeleteCheckpointInput, DeleteCheckpointOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeleteCheckpointInput) (*sdk.CallToolResult, DeleteCheckpointOutput, error) {
		// FIRST statement, per D6: confirmWorkspaceSlug reads the live slug
		// and refuses before this handler issues its own request. This
		// handler adds no GET of its own — the helper already made one, and
		// toolRoutes lists it under this tool's own name for exactly that
		// reason.
		if err := confirmWorkspaceSlug(ctx, lb, "delete_checkpoint", in.WorkspaceID, in.ConfirmSlug); err != nil {
			return nil, DeleteCheckpointOutput{}, err
		}

		method, path := toolPath("delete_checkpoint", "DELETE /api/workspaces/{id}/checkpoints/{cid}", in.WorkspaceID, in.CheckpointID)
		if err := lb.call(ctx, method, path, nil, nil); err != nil {
			return nil, DeleteCheckpointOutput{}, err
		}
		return nil, DeleteCheckpointOutput{CheckpointID: in.CheckpointID, Deleted: true}, nil
	}
}

// ---- rollback_workspace ----

// RollbackWorkspaceInput is rollback_workspace's input. RestoreData is
// P3d's own addition (DESIGN §14:871 draws it on the wire): unset or false
// keeps the call to what it always was — the Workspace layer only, entity
// rows untouched — while true also restores a confirmed resource's entity
// rows from the checkpoint's data_snap, when it has one (D9). ConfirmSlug
// is unconditionally required on every call to this tool — it has been one
// of D6's six confirmSlug-required tools since A2, and this slice does not
// relax that for a plain rollback. What RestoreData:true changes is what a
// mismatch — or a successful call aimed at the wrong workspace — would cost:
// with it set, this call can destroy entity rows the same way
// decide_resource's decline branch and reset_resource_data can, and its own
// undo is a checkpoint retention will eventually prune.
type RollbackWorkspaceInput struct {
	WorkspaceID  int64  `json:"workspaceId"`
	CheckpointID int64  `json:"checkpointId"`
	ConfirmSlug  string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. Required on every call, not only when restoreData is true; checked against the live workspace before anything is destroyed, a mismatch refuses the call and changes nothing. With restoreData:true, a mismatch is what stands between a wrong-workspace call and destroyed entity rows, not only a wrong-workspace edit."`
	RestoreData  bool   `json:"restoreData,omitempty" jsonschema:"Also restore the workspace's confirmed-resource entity rows from this checkpoint's saved data, not only settings, operation overrides and custom endpoints. Requires confirmSlug. Refused with 409 no_data_snapshot if the checkpoint carries none."`
}

// RollbackWorkspaceOutput is rollback_workspace's declared output schema,
// projected from rollbackResponseView (checkpoint_handlers.go:90-104).
// DataRestored is P3d's no-op signal (D7), not an echo of the request's own
// RestoreData: true when the restore ran for at least one carried family,
// false both when restoreData was never set and when every family in the
// document was skipped.
type RollbackWorkspaceOutput struct {
	Revision       int64 `json:"revision"`
	ScenarioActive bool  `json:"scenarioActive"`
	DataRestored   bool  `json:"dataRestored"`
}

// rollbackResponseWire decodes rollbackResponseView
// (checkpoint_handlers.go:90-104) — POST .../rollback/{cid}'s 200 body.
type rollbackResponseWire struct {
	Revision       int64 `json:"revision"`
	ScenarioActive bool  `json:"scenarioActive"`
	DataRestored   bool  `json:"dataRestored"`
}

// rollbackBody is POST .../rollback/{cid}'s request body (rollbackRequest,
// checkpoint_handlers.go:95-97) — sent ONLY when RestoreData is set (below).
type rollbackBody struct {
	RestoreData bool   `json:"restoreData"`
	ConfirmSlug string `json:"confirmSlug"`
}

func handleRollbackWorkspace(lb *loopback) sdk.ToolHandlerFor[RollbackWorkspaceInput, RollbackWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in RollbackWorkspaceInput) (*sdk.CallToolResult, RollbackWorkspaceOutput, error) {
		if err := confirmWorkspaceSlug(ctx, lb, "rollback_workspace", in.WorkspaceID, in.ConfirmSlug); err != nil {
			return nil, RollbackWorkspaceOutput{}, err
		}

		// A zero-byte POST when RestoreData is unset: handleRollbackWorkspace
		// treats it as "restoreData not named" (its own comment on the
		// io.EOF swallow), which is exactly what this call means when the
		// argument was never given. Since P3d the screen no longer sends
		// that zero-byte body — it always names restoreData (D8) — so this
		// nil is now the sole reason the swallow still exists at all (D9,
		// D7's own "what still posts nothing" note). RestoreData:true
		// requires confirmSlug on the WIRE too (D7): confirmWorkspaceSlug
		// above already refused a mismatch before this request was even
		// built, but the route re-checks it live inside its own
		// transaction, and only that second check is what makes the
		// refusal true of the workspace at write time.
		var body []byte
		if in.RestoreData {
			b, err := jsonx.Marshal(rollbackBody{RestoreData: true, ConfirmSlug: in.ConfirmSlug})
			if err != nil {
				return nil, RollbackWorkspaceOutput{}, fmt.Errorf("mcp: encode rollback_workspace request: %w", err)
			}
			body = b
		}

		var wire rollbackResponseWire
		method, path := toolPath("rollback_workspace", "POST /api/workspaces/{id}/rollback/{cid}", in.WorkspaceID, in.CheckpointID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, RollbackWorkspaceOutput{}, err
		}
		return nil, RollbackWorkspaceOutput(wire), nil
	}
}

// ---- reset_overrides ----

// ResetOverridesInput is reset_overrides' input. No target beyond the
// workspace itself — the admin route takes no request body
// (handleResetOverrides' own comment: "there is nothing for a caller to
// name").
type ResetOverridesInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// ResetOverridesOutput is reset_overrides' declared output schema, projected
// from resetOverridesResponseView (checkpoint_handlers.go:101-105). Changed
// is C9's no-op signal, carried through verbatim: false means nothing was
// reset, no pre-destructive checkpoint was written, and this tool's own
// Description already told the caller not to look for one in that case.
type ResetOverridesOutput struct {
	Revision       int64 `json:"revision"`
	ScenarioActive bool  `json:"scenarioActive"`
	Changed        bool  `json:"changed"`
}

// resetOverridesResponseWire decodes resetOverridesResponseView
// (checkpoint_handlers.go:101-105) — POST .../reset-overrides' 200 body.
type resetOverridesResponseWire struct {
	Revision       int64 `json:"revision"`
	ScenarioActive bool  `json:"scenarioActive"`
	Changed        bool  `json:"changed"`
}

func handleResetOverrides(lb *loopback) sdk.ToolHandlerFor[ResetOverridesInput, ResetOverridesOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ResetOverridesInput) (*sdk.CallToolResult, ResetOverridesOutput, error) {
		if err := confirmWorkspaceSlug(ctx, lb, "reset_overrides", in.WorkspaceID, in.ConfirmSlug); err != nil {
			return nil, ResetOverridesOutput{}, err
		}

		var wire resetOverridesResponseWire
		method, path := toolPath("reset_overrides", "POST /api/workspaces/{id}/reset-overrides", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ResetOverridesOutput{}, err
		}
		return nil, ResetOverridesOutput(wire), nil
	}
}

// ---- delete_workspace ----

// DeleteWorkspaceInput is delete_workspace's input.
type DeleteWorkspaceInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// DeleteWorkspaceOutput is delete_workspace's declared output schema.
// Deleted is always true, for the same reason every other confirmation
// tool's own comment gives: a non-2xx already became a tool error before
// this line runs.
type DeleteWorkspaceOutput struct {
	WorkspaceID int64 `json:"workspaceId"`
	Deleted     bool  `json:"deleted"`
}

func handleDeleteWorkspace(lb *loopback) sdk.ToolHandlerFor[DeleteWorkspaceInput, DeleteWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in DeleteWorkspaceInput) (*sdk.CallToolResult, DeleteWorkspaceOutput, error) {
		if err := confirmWorkspaceSlug(ctx, lb, "delete_workspace", in.WorkspaceID, in.ConfirmSlug); err != nil {
			return nil, DeleteWorkspaceOutput{}, err
		}

		method, path := toolPath("delete_workspace", "DELETE /api/workspaces/{id}", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, nil); err != nil {
			return nil, DeleteWorkspaceOutput{}, err
		}
		return nil, DeleteWorkspaceOutput{WorkspaceID: in.WorkspaceID, Deleted: true}, nil
	}
}

// ---- clear_traffic ----

// ClearTrafficInput is clear_traffic's input.
type ClearTrafficInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	ConfirmSlug string `json:"confirmSlug" jsonschema:"The exact slug of the workspace this call is aimed at, as list_workspaces or get_workspace reports it. It is checked against the live workspace before anything is destroyed; a mismatch refuses the call and changes nothing."`
}

// ClearTrafficOutput is clear_traffic's declared output schema, projected
// from trafficClearedView (traffic_handlers.go:86-89).
type ClearTrafficOutput struct {
	Deleted int64 `json:"deleted"`
}

// trafficClearedWire decodes trafficClearedView (traffic_handlers.go:86-89)
// — DELETE .../traffic's 200 body.
type trafficClearedWire struct {
	Deleted int64 `json:"deleted"`
}

func handleClearTraffic(lb *loopback) sdk.ToolHandlerFor[ClearTrafficInput, ClearTrafficOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ClearTrafficInput) (*sdk.CallToolResult, ClearTrafficOutput, error) {
		if err := confirmWorkspaceSlug(ctx, lb, "clear_traffic", in.WorkspaceID, in.ConfirmSlug); err != nil {
			return nil, ClearTrafficOutput{}, err
		}

		var wire trafficClearedWire
		method, path := toolPath("clear_traffic", "DELETE /api/workspaces/{id}/traffic", in.WorkspaceID)
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ClearTrafficOutput{}, err
		}
		return nil, ClearTrafficOutput(wire), nil
	}
}
