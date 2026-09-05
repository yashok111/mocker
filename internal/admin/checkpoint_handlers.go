// Checkpoint handlers implement §C's four admin routes for P2c's
// [checkpoints.Repo] — history and undo for the workspace layer: list,
// create a manual checkpoint, roll back to one, and reset the workspace
// layer to the spec (DESIGN §12:759-776, screen 10's «сбросить всё к
// спеке»).
//
// Every handler here is a thin HTTP adapter, exactly the shape
// scenario_handlers.go already establishes for a sibling snapshot-shaped
// repo: decode the request, call the repo method, map its error set once
// through [answerCheckpointError], shape the response. No handler reaches
// into overrides.Repo, customep.Repo or workspaces.Repo directly — every
// write [checkpoints.Repo] makes is inside its own transaction (C4/C17),
// and duplicating any of that here would be a second path around it.
package admin

import (
	"errors"
	"io"
	"net/http"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/checkpoints"
	"github.com/yashok111/mocker/internal/httpx"
)

// checkpointSummaryView is one entry of [checkpointListView] and what
// [Server.handleCreateCheckpoint] answers on 201 — [checkpoints.Summary] on
// the wire, and NOTHING from the snapshot itself (§C): a list endpoint that
// answered every checkpoint's full BLOB would be a page-load cost that grows
// with the workspace's history, the same call P2b already made for
// scenarios (scenario_handlers.go's identical comment on
// scenarioSummaryView).
type checkpointSummaryView struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// CreatedAt is Unix seconds, matching every other admin timestamp field
	// on this plane (scenarioSummaryView.CreatedAt, workspace views).
	CreatedAt int64 `json:"createdAt"`
	// CreatedBy is nullable on the wire because [checkpoints.Summary]'s own
	// field is (the column REFERENCES users(id) with no NOT NULL) — every
	// row THIS build writes carries a user (C15: the handler always passes
	// the session's own id down), but the type does not promise that for a
	// row written by hand.
	CreatedBy *int64 `json:"createdBy"`
	// HasData is P3d's own field: whether this row's data_snap IS NOT
	// NULL, projected from [checkpoints.Summary.HasData] — derived by both
	// server sites that produce one (the list query and the 201 of
	// POST .../checkpoints), never by decompressing the blob here. The
	// history screen uses it to enable the "restore data" checkbox only on
	// a row that actually carries entity rows to restore.
	HasData bool `json:"hasData"`
}

func newCheckpointSummaryView(c checkpoints.Summary) checkpointSummaryView {
	return checkpointSummaryView{
		ID:        c.ID,
		Kind:      c.Kind,
		Label:     c.Label,
		CreatedAt: c.CreatedAt.Unix(),
		CreatedBy: c.CreatedBy,
		HasData:   c.HasData,
	}
}

// checkpointListView is GET .../checkpoints' wire shape: newest first,
// exactly the order [checkpoints.Repo.List] already returns (its own doc
// comment on why — it is both the natural history-screen order and the
// order the checkpoints_ws index stores).
type checkpointListView struct {
	Checkpoints []checkpointSummaryView `json:"checkpoints"`
}

// createCheckpointRequest is POST .../checkpoints' request body: the ONE
// thing an operator supplies for a manual checkpoint — everything else in
// the snapshot comes from the workspace's own current state, the same shape
// createScenarioRequest uses for the same reason.
type createCheckpointRequest struct {
	Label string `json:"label"`
}

// rollbackRequest is POST .../rollback/{cid}'s request body. DESIGN
// §14:871 draws it as `{ restoreData: bool }`; RestoreData is declared here
// rather than left undeclared (C11). [decodeJSON]'s DisallowUnknownFields
// already refuses an UNDECLARED field with an opaque "unknown field
// restoreData" — so declaring it explicitly means a `true` value reaches
// [checkpoints.Repo.Rollback] instead of being refused here (P3d — D7).
//
// ConfirmSlug is P3d's own addition: required exactly when RestoreData is
// true, exactly as [decideResourceRequest] and [resetDataRequest] already
// require one — a restoreData:true rollback destroys entity rows the same
// way a decline or a reset-data does, and its undo is a checkpoint
// retention will eventually prune. [decodeJSON]'s DisallowUnknownFields
// means a body naming this field before it was declared here would have
// 400ed as an unknown field, so this is not optional plumbing.
type rollbackRequest struct {
	RestoreData bool   `json:"restoreData"`
	ConfirmSlug string `json:"confirmSlug"`
}

// rollbackResponseView is POST .../rollback/{cid}'s 200 body (§C): the
// fields [checkpoints.Outcome] carries that a rollback's caller needs.
// ScenarioActive is C8's flag — allowed while a scenario is active, so the
// screen can warn that part of what was just restored is currently masked.
// DataRestored is P3d's own no-op signal, projected from
// [checkpoints.Outcome.DataRestored] unchanged: true when the restore RAN
// for at least one carried family, false when every one was skipped or the
// request never asked for data at all — not an echo of the request's own
// RestoreData flag, which would tell the screen only what it already sent.
type rollbackResponseView struct {
	Revision       int64 `json:"revision"`
	ScenarioActive bool  `json:"scenarioActive"`
	DataRestored   bool  `json:"dataRestored"`
}

// resetOverridesResponseView is POST .../reset-overrides' 200 body (§C):
// rollbackResponseView's two fields plus Changed, C9's no-op signal —
// false when the reset deleted nothing, because [overrides.ReplaceAllTx]
// and [customep.ReplaceAllTx] deliberately return no count (C4) and this
// is the only place that signal can travel (checkpoints.Outcome's own doc
// comment).
type resetOverridesResponseView struct {
	Revision       int64 `json:"revision"`
	ScenarioActive bool  `json:"scenarioActive"`
	Changed        bool  `json:"changed"`
}

// handleListCheckpoints answers GET /api/workspaces/{id}/checkpoints: every
// checkpoint saved for the workspace, newest first, as [checkpointListView]
// — never a snapshot (§C).
func (s *Server) handleListCheckpoints(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	list, err := s.checkpointsRepo.List(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list checkpoints", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list checkpoints")
		return
	}
	out := make([]checkpointSummaryView, len(list))
	for i, c := range list {
		out[i] = newCheckpointSummaryView(c)
	}
	httpx.JSON(w, http.StatusOK, checkpointListView{Checkpoints: out})
}

// handleCreateCheckpoint answers POST /api/workspaces/{id}/checkpoints: a
// MANUAL checkpoint — the operator's own button, DESIGN §12:770-772's third
// trigger. [checkpoints.Repo.Create] does the coherent read (C5), the label
// validation (C14) and the prune (C7); this handler only decodes the label,
// supplies the session user (C15) and maps the error set.
//
// It does NOT bump workspaces.revision (C12 — [checkpoints.Repo.Create]'s
// own doc comment carries the full reason), so there is nothing to read back
// from ws here beyond the id the repo already needed.
func (s *Server) handleCreateCheckpoint(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	var body createCheckpointRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	summary, err := s.checkpointsRepo.Create(r.Context(), ws.ID, body.Label, user.ID)
	if err != nil {
		s.answerCheckpointError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, newCheckpointSummaryView(*summary))
}

// handleRollbackWorkspace answers
// POST /api/workspaces/{id}/rollback/{cid}: restores the workspace layer —
// settings, op_overrides and custom_endpoints — to checkpoint {cid}'s state,
// protecting what it overwrites with a pre-destructive checkpoint of its
// own, and allocates a new revision (max+1, C12).
//
// restoreData:true now reaches [checkpoints.Repo.Rollback] (P3d — D7):
// there is no request-shape refusal left in this handler, RestoreData and
// ConfirmSlug are threaded through unchanged, and the repo's own error set
// answers every refusal in D7's table (no data snapshot, the pre-destructive
// snapshot degraded past the cap, a missing or wrong confirm slug, a
// corrupt stored document) through [Server.answerCheckpointError] below.
func (s *Server) handleRollbackWorkspace(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	cid, ok := parsePathInt64(w, r, "cid")
	if !ok {
		return
	}

	// rollbackWorkspace is the ONE route in the whole contract whose
	// requestBody is optional (api/openapi.json .../rollback/{cid}.post:
	// required:false) — C11's "false and absent take the normal path"
	// means a genuinely empty POST is not malformed input, it is the
	// caller declining to name restoreData at all. decodeJSON's Decode
	// call returns io.EOF for a zero-byte body (both when r.Body is nil
	// and when the stream has nothing to read), so that specific error is
	// swallowed here rather than answered as "invalid request body" — any
	// OTHER decode error (truncated JSON, wrong type, an unknown field)
	// still 400s below. This is not, since P3d, a hypothesis about the
	// screen: the screen now sends `{restoreData: false}` on every click
	// of «Откатить» with the box unchecked (D8), so the zero-byte body
	// this swallow exists for is the MCP tool's — `rollback_workspace`
	// with its argument unset (D9) still posts nothing at all, and this is
	// what keeps that call from 400ing.
	var body rollbackRequest
	if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	// P7a (D6): the checkpoint's endpoint rows are about to REPLACE the
	// workspace's, and every `$ref` they carry must resolve against the
	// spec the workspace holds — a rollback restores layers, never the spec
	// binding, so the document to check against is the current one. The
	// checkpoint is read once more inside Rollback for the write; the read
	// here is the reader pool's, before anything is locked.
	cp, err := s.checkpointsRepo.Get(r.Context(), ws.ID, cid)
	if err != nil {
		s.answerCheckpointError(w, err)
		return
	}
	endpointRows, err := checkpoints.EndpointRowsFromBundle(cp.Bundle)
	if err != nil {
		s.log.Error("decode checkpoint endpoints", "checkpoint", cid, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to decode the checkpoint")
		return
	}
	if s.refuseRowsAgainstSpec(w, r, ws.SpecID, endpointRows) {
		return
	}

	outcome, err := s.checkpointsRepo.Rollback(r.Context(), ws.ID, cid, user.ID, body.RestoreData, body.ConfirmSlug)
	if err != nil {
		s.answerCheckpointError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, rollbackResponseView{
		Revision:       outcome.Revision,
		ScenarioActive: outcome.ScenarioActive,
		DataRestored:   outcome.DataRestored,
	})
}

// handleResetOverrides answers
// POST /api/workspaces/{id}/reset-overrides: screen 10's «сбросить всё к
// спеке» — deletes every op_overrides row AND every custom_endpoints row
// (C9), after writing a pre-destructive checkpoint, and bumps the revision
// by exactly one. settings survive (C9's own reasoning). Takes no request
// body, like POST .../scenarios/deactivate does for the same reason: there
// is nothing for a caller to name.
//
// A reset that would delete nothing is C9's no-op: 200, Changed:false, no
// checkpoint written and no revision bump — [checkpoints.Repo.Reset]'s own
// doc comment on where that decision is made and why.
func (s *Server) handleResetOverrides(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	outcome, err := s.checkpointsRepo.Reset(r.Context(), ws.ID, user.ID)
	if err != nil {
		s.answerCheckpointError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, resetOverridesResponseView{
		Revision:       outcome.Revision,
		ScenarioActive: outcome.ScenarioActive,
		Changed:        outcome.Changed,
	})
}

// handleDeleteCheckpoint answers
// DELETE /api/workspaces/{id}/checkpoints/{cid} (P2d §4): removes one
// history row outright. Unlike every other mutating route in this file it
// bumps no revision and takes no pre-destructive checkpoint of its own
// ([checkpoints.Repo.Delete]'s own doc comment carries the full reason) —
// a history row is not served state, so deleting one changes nothing a
// client can observe through the mock plane. Any kind may be deleted,
// including the newest row and the last one: an empty history is the
// legal state every workspace starts in, so this handler has no "keep at
// least one" guard to enforce.
func (s *Server) handleDeleteCheckpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	cid, ok := parsePathInt64(w, r, "cid")
	if !ok {
		return
	}

	if err := s.checkpointsRepo.Delete(r.Context(), ws.ID, cid); err != nil {
		s.answerCheckpointError(w, err)
		return
	}
	httpx.NoContent(w)
}

// The two wire codes P3d mints for [Server.answerCheckpointError], beside
// the neighbours' own per-route blocks (codePreviewResourceServes in
// preview_handlers.go, codeResourceConfirmSlugRequired and its sibling in
// resource_handlers.go). Without these two, [checkpoints.ErrNoDataSnapshot]
// and [checkpoints.ErrDataSnapshotTooLarge] would fall through to the
// generic httpx.CodeConflict/httpx.CodeTooLarge codes this route already
// answers for a concurrent edit and a config-snapshot overflow — the exact
// collision D7 mints these two to forbid. The two confirm-slug refusals
// deliberately do NOT get a code of their own here: they reuse
// codeResourceConfirmSlugRequired/codeResourceConfirmSlugMismatch from
// resource_handlers.go, so a client keeps one vocabulary across the three
// verbs (decline, reset-data, rollback) that take a confirmSlug.
const (
	codeCheckpointNoDataSnapshot       = "no_data_snapshot"
	codeCheckpointDataSnapshotTooLarge = "data_snapshot_too_large"
)

// answerCheckpointError maps [checkpoints.Repo]'s error set to wire codes,
// once, for all three mutating routes that can produce one of them (list
// never calls this — like handleListScenarios, it has no per-checkpoint
// lookup to fail). Shared rather than duplicated per handler for the same
// reason answerScenarioError is: a caller must not see one 404/409/413
// shape from create and a different one from rollback or reset.
//
// err.Error() is used directly for the sentinels that already name the
// offending thing (every one of checkpoints' own sentinels wraps the
// workspace id, checkpoint id or the size/limit into its own text — see
// each var's doc comment in internal/checkpoints/checkpoints.go) — the
// same choice answerScenarioError already makes, for the same reason.
//
// [checkpoints.ErrCorruptSnapshot] and any wrapped error from
// [overrides.ReplaceAllTx]/[customep.ReplaceAllTx] (reachable only if a
// stored snapshot outlived those packages' own write-time validation — C4
// duty 4 validates per row on every restore, not just at capture) fall to
// the default case: neither is something the client did or can retry its
// way out of, so both answer exactly like an unrecognized error would.
func (s *Server) answerCheckpointError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, checkpoints.ErrNotFound):
		// Also what a checkpoint id belonging to a DIFFERENT workspace
		// answers — the repo's own WHERE clause makes "exists elsewhere"
		// and "does not exist" indistinguishable by construction, exactly
		// like scenarios.ErrNotFound's identical comment.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, err.Error())
	case errors.Is(err, checkpoints.ErrWorkspaceNotFound):
		// A race with a concurrent workspace delete, not a client mistake
		// — loadWorkspace already confirmed existence moments earlier,
		// exactly like answerScenarioError's identical case.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, checkpoints.ErrInvalidLabel):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	case errors.Is(err, bundle.ErrInvalid):
		// A stored checkpoint blob this build cannot decode (a mockerBundle:4
		// snapshot since A18) on GET or rollback — the same 409 by name
		// answerScenarioError gives a scenario's, for the same reason.
		httpx.Err(w, http.StatusConflict, codeSnapshotUnreadable, err.Error())
	case errors.Is(err, checkpoints.ErrConcurrentEdit):
		// C5 step 7's bounded fence exhausted — the same 409 and the same
		// reasoning internal/scenarios' own ErrConcurrentEdit gets: nothing
		// is broken, the workspace just kept changing under the read.
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, checkpoints.ErrSnapshotTooLarge):
		// C18's ceiling, at either end: a capture that would exceed it
		// (Create/Rollback/Reset all snapshot the CURRENT state first) or a
		// stored blob that decompresses past it (Rollback's read of the
		// TARGET checkpoint). Same status workspaces.ErrSettingsTooLarge
		// already gets (workspace_handlers.go's PATCH handler).
		httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, err.Error())
	case errors.Is(err, checkpoints.ErrNoDataSnapshot):
		// P3d — D7's first new refusal: restoreData:true against a
		// checkpoint whose data_snap IS NULL. A state conflict, not a
		// malformed body, which is why this is 409 and not the 400 the
		// pre-P3d refusal used.
		httpx.Err(w, http.StatusConflict, codeCheckpointNoDataSnapshot, err.Error())
	case errors.Is(err, checkpoints.ErrDataSnapshotTooLarge):
		// P3d — D7's second new refusal: the PRE-DESTRUCTIVE snapshot this
		// rollback would take has itself degraded past the entity-data cap.
		// A distinct sentinel from checkpoints.ErrSnapshotTooLarge (the
		// config-side overflow) on purpose — the two need opposite
		// reactions from the operator, and reusing one code would make
		// them read as one message.
		httpx.Err(w, http.StatusRequestEntityTooLarge, codeCheckpointDataSnapshotTooLarge, err.Error())
	case errors.Is(err, checkpoints.ErrConfirmSlugRequired):
		// P3d — D7: reuses the resource verbs' own code, so a client keeps
		// one vocabulary for confirmSlug across decline, reset-data and
		// rollback.
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugRequired, err.Error())
	case errors.Is(err, checkpoints.ErrConfirmSlugMismatch):
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugMismatch, err.Error())
	default:
		s.log.Error("checkpoint operation failed", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
	}
}
