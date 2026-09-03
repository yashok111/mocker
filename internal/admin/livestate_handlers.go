// Live-state session handlers implement the admin half of DESIGN §14's
// session surface (lines 852-861): GET/POST/DELETE
// /api/workspaces/{id}/session over the SAME *livestate.Store the mock plane
// consumes on every matched request.
//
// The wire shape is deliberately identical to the mock plane's own POST
// {prefix}/state (internal/mockplane/livestate.go): both handlers decode
// straight into livestate.Directive, which owns its own json tags, its "*"
// union and the Scenario field (see that type's own doc comment,
// SIG_LIVESTATE). This file does not declare a second directive struct —
// doing so is exactly the drift the shared type exists to prevent.
//
// As of P2b (A17), a "scenario" key on THIS route answers 400, not the 501
// it used to. DESIGN §14's session body (lines 857-861) never named a
// "scenario" key at all — it only ever reached this shared decoder because
// the wire shape is shared with the mock plane's own directive endpoint —
// and scenarios now have their own dedicated activate/deactivate routes
// (scenario_handlers.go). "not implemented yet" stopped being true the
// moment those routes existed; holding onto 501 past that point would tell
// an operator to wait for a feature that had already shipped, under the
// wrong route. See handlePostSession's own comment on the HasScenario
// branch for the exact wording.
//
// The admin response bodies differ from the mock plane's on one point only:
// they never carry a "workspace" field. The mock plane needs it because the
// workspace comes from the request's host/path; here {id} is already in the
// URL, so a caller already knows which workspace it asked about.
package admin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/workspaces"
)

// codeServiceUnavailable is the wire code SIG_LIVESTATE pins once for both
// planes, not in httpx's shared Code* set: "service_unavailable" for "no
// LiveStateSource was wired". Its former sibling, codeNotImplementedYet
// ("not_implemented_yet", for DESIGN §12's scenario switch), is gone as of
// P2b — see handlePostSession's own comment on the scenario check below for
// why answering plain 400 here now is correct rather than a downgrade.
const codeServiceUnavailable = "service_unavailable"

// LiveStateSource is *livestate.Store as this package needs it: only the
// three methods the session routes actually call. Apply is deliberately
// absent — that method belongs to the mock plane's own serving path
// (internal/mockplane's identically-shaped interface), which this package
// never touches.
type LiveStateSource interface {
	Set(workspaceID int64, d livestate.Directive) error
	List(workspaceID int64) []livestate.Directive
	Clear(workspaceID int64) int
	// Delete narrows Clear to one target (A13); see livestate.Store.Delete.
	Delete(workspaceID int64, target livestate.Target, action livestate.Action) (int, error)
}

// SetLiveState wires src as the Server's [LiveStateSource]. It exists as a
// setter, not a New parameter, because New's signature
// (cfg, sessions, ws, db, log) is shared with cmd/mocker/main.go's five call
// sites: adding a second session-shaped dependency next to the existing
// *auth.Manager "sessions" parameter is exactly the adjacent-same-name swap
// this project's gate has caught before. cmd/mocker calls this once, during
// startup, with the SAME *livestate.Store instance the mock plane's own
// SetLiveState receives — RAM state shared between the two planes, not two
// independent stores that could disagree about what is "currently" forced.
//
// A Server whose SetLiveState is never called is not a bug: every session
// route below answers 503 rather than panicking on a nil interface, which is
// exactly the shape a test harness that has no reason to wire live state
// needs.
func (s *Server) SetLiveState(src LiveStateSource) {
	s.liveState = src
}

// sessionListView is GET and a successful POST's shared wire shape: the
// FULL directive list after the write, so a caller never needs a second GET
// just to see what else is already in force.
type sessionListView struct {
	Directives []livestate.Directive `json:"directives"`
}

// sessionClearedView is DELETE's wire shape.
type sessionClearedView struct {
	Cleared int `json:"cleared"`
}

// handleGetSession answers GET /api/workspaces/{id}/session.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
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
	if s.liveState == nil {
		s.answerNoLiveState(w)
		return
	}
	s.writeSessionList(w, ws)
}

// handlePostSession answers POST /api/workspaces/{id}/session: the same
// directive body POST {prefix}/state accepts on the mock plane.
func (s *Server) handlePostSession(w http.ResponseWriter, r *http.Request) {
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
	if s.liveState == nil {
		s.answerNoLiveState(w)
		return
	}

	// Round-1 review finding 4: the same directive-sized cap
	// internal/mockplane/livestate.go's serveLiveStatePost applies to its
	// own POST body, applied here too rather than left to httpx.MaxBody's
	// general MOCKER_MAX_BODY ceiling alone — this handler decodes the
	// identical wire shape, so it deserves the identical bound.
	r.Body = http.MaxBytesReader(w, r.Body, livestate.MaxDirectiveBodyBytes)
	var d livestate.Directive
	if err := decodeJSON(r, &d); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf("decode directive: %v", err))
		return
	}

	// A17: a "scenario" key is rejected here — checked BEFORE Set, exactly
	// like internal/mockplane/livestate.go's own serveLiveStatePost, so it
	// never reaches the Store (which has no notion of it and never gains
	// one — a scenario is a row in SQLite, not RAM session state). This
	// used to be a 501 ("arrives in a later phase"); it is a 400 now
	// because scenarios shipped their OWN routes (POST
	// .../scenarios/{sid}/activate, .../scenarios/deactivate) rather than
	// growing a third meaning for this endpoint's directive union — the
	// session route was never going to be where scenario switching lived,
	// even once it existed, so "not implemented" was always going to
	// resolve to "wrong route", never to "now accepted here".
	if d.HasScenario() {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"the session route does not switch scenarios; use POST /api/workspaces/{id}/scenarios/{sid}/activate "+
				"(or .../scenarios/deactivate) instead")
		return
	}

	if err := s.liveState.Set(ws.ID, d); err != nil {
		s.answerSessionSetError(w, ws, err)
		return
	}
	s.writeSessionList(w, ws)
}

// handleDeleteSession answers DELETE /api/workspaces/{id}/session.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
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
	if s.liveState == nil {
		s.answerNoLiveState(w)
		return
	}
	// A13: an optional body narrows the clear to one target (and optionally
	// one action) — the same shape the mock plane's DELETE {prefix}/state
	// takes. No body clears everything, exactly as before.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, livestate.MaxDirectiveBodyBytes))
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		cleared := s.liveState.Clear(ws.ID)
		httpx.JSON(w, http.StatusOK, sessionClearedView{Cleared: cleared})
		return
	}
	var body sessionClearRequest
	if err := decodeStrict(bytes.NewReader(raw), &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if body.Target == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
			`a DELETE body must name "target" ("*" or {method,path}); send no body to clear everything`)
		return
	}
	cleared, err := s.liveState.Delete(ws.ID, *body.Target, body.Action)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, sessionClearedView{Cleared: cleared})
}

// sessionClearRequest is DELETE .../session's optional body (A13).
type sessionClearRequest struct {
	Target *livestate.Target `json:"target"`
	Action livestate.Action  `json:"action,omitempty"`
}

// writeSessionList answers ws's current directive list. Never rendered as
// JSON null: [livestate.Store.List] returns a nil slice for a workspace that
// has never had a directive set, and "directives":null would surprise a
// caller expecting an array it can range over unconditionally.
func (s *Server) writeSessionList(w http.ResponseWriter, ws *workspaces.Workspace) {
	directives := s.liveState.List(ws.ID)
	if directives == nil {
		directives = []livestate.Directive{}
	}
	httpx.JSON(w, http.StatusOK, sessionListView{Directives: directives})
}

// answerNoLiveState is the 503 every session route answers when no
// LiveStateSource was ever wired: the endpoint exists, the feature it backs
// is simply not available in this deployment (mirrors
// internal/mockplane/livestate.go's identical decision for {prefix}/state).
func (s *Server) answerNoLiveState(w http.ResponseWriter) {
	httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable,
		"live-state is not available: no LiveStateSource was wired")
}

// answerSessionSetError maps [livestate.Store.Set]'s two sentinel errors to
// the wire codes SIG_LIVESTATE pins for both planes: ErrTooManyDirectives is
// a 409 (never 429 — DESIGN §18 puts no rate limit on this plane; the bound
// is the directive count, not request frequency), ErrInvalidDirective is a
// 400. Anything else is this handler's own bug to log, never the caller's to
// see the internals of.
func (s *Server) answerSessionSetError(w http.ResponseWriter, ws *workspaces.Workspace, err error) {
	switch {
	case errors.Is(err, livestate.ErrTooManyDirectives):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, livestate.ErrInvalidDirective):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	default:
		s.log.Error("session: set directive", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
	}
}
