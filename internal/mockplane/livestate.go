// livestate.go wires internal/livestate's RAM session layer into the mock
// plane: the [LiveStateSource] contract [Plane.serveGenerated] (respond.go)
// consumes on every matched request, and POST/GET/DELETE {prefix}/state —
// DESIGN §12/§14's "переключение из тестов" — the one HTTP surface that lets
// a test (or a human) actually write to it.
//
// UNAUTHENTICATED BY DESIGN, same as the rest of this plane (DESIGN §15:
// "мок открыт"): the bound in [livestate.MaxDirectivesPerWorkspace] is what
// keeps an open endpoint that grows a map forever from being a memory leak
// with a URL, not a session cookie this plane has never had.
package mockplane

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/workspaces"
)

// LiveStateSource is *livestate.Store as the plane needs it. serveGenerated
// calls only Apply, on every matched request; serveLiveState below calls all
// four, exactly like the admin session API a later stage of this phase wires
// against the SAME concrete *livestate.Store.
type LiveStateSource interface {
	Apply(workspaceID int64, method, path string) livestate.Effect
	Set(workspaceID int64, d livestate.Directive) error
	List(workspaceID int64) []livestate.Directive
	Clear(workspaceID int64) int
	// Delete narrows Clear to one target (A13); see livestate.Store.Delete.
	Delete(workspaceID int64, target livestate.Target, action livestate.Action) (int, error)
}

// SetLiveState wires src as the Plane's [LiveStateSource]. Call it once,
// during startup, right after [New] and before the Plane serves its first
// request — cmd/mocker's own wiring is the only production caller, exactly
// like [Plane.SetOverrides] (see that method's doc comment for the full
// calling contract and why there is no lock here: all of these setters are
// meant to be written once, before the first request, and only ever read
// afterward).
//
// A Plane whose SetLiveState is never called behaves exactly as one built
// before this phase: respond.go sees a nil LiveStateSource and skips
// [livestate.Store.Apply] entirely (HARD RULE 6), and {prefix}/state answers
// 503 "service_unavailable" rather than a nil-pointer panic — every
// pre-P1c2 test, and every test in this package that never calls this
// method, keeps passing unmodified.
func (p *Plane) SetLiveState(src LiveStateSource) {
	p.livestate = src
}

// serveLiveState answers GET/POST/DELETE {prefix}/state — plane.go's
// serveReserved already filtered the method and the single "state" segment
// before calling this, the same pattern serveHealth's own caller uses.
func (p *Plane) serveLiveState(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace) {
	if p.livestate == nil {
		// Legal, exactly like a nil OverrideSource — but this endpoint has
		// nothing else to answer with: there is no "no live-state support"
		// degenerate reading of GET/POST/DELETE the way "no spec" has one
		// for route matching. 503, not 404: the endpoint exists, the
		// feature it backs is simply not wired in this deployment.
		httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable",
			"live-state is not available: no LiveStateSource was wired")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p.writeLiveStateList(w, ws)
	case http.MethodPost:
		p.serveLiveStatePost(w, r, ws)
	case http.MethodDelete:
		p.serveLiveStateDelete(w, r, ws)
	}
}

// liveStateClearBody is DELETE {prefix}/state's OPTIONAL body (A13): a
// target, and optionally one action on it. No body — the pre-A13 shape —
// clears every directive of the workspace.
type liveStateClearBody struct {
	Target *livestate.Target `json:"target"`
	Action livestate.Action  `json:"action,omitempty"`
}

// serveLiveStateDelete answers DELETE {prefix}/state: everything when the
// body is empty, one target (all actions, or the one named) otherwise.
// A body that is present but names no target is refused, never read as
// "everything" — a test that meant one target and sent a typo must not
// clear its sibling tests' directives.
func (p *Plane) serveLiveStateDelete(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace) {
	r.Body = http.MaxBytesReader(w, r.Body, livestate.MaxDirectiveBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf("read body: %v", err))
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		cleared := p.livestate.Clear(ws.ID)
		httpx.JSON(w, http.StatusOK, liveStateClearedResponse{Workspace: ws.Slug, Cleared: cleared})
		return
	}
	var body liveStateClearBody
	if err := jsonx.Unmarshal(raw, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf("decode clear request: %v", err))
		return
	}
	if body.Target == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
			`a DELETE body must name "target" ("*" or {method,path}); send no body to clear everything`)
		return
	}
	cleared, err := p.livestate.Delete(ws.ID, *body.Target, body.Action)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, liveStateClearedResponse{Workspace: ws.Slug, Cleared: cleared})
}

// liveStateListResponse is the wire shape GET and a successful POST both
// answer with — POST returns the FULL list after the write (the digest's own
// wording), not just the directive it was given, so a caller never has to
// issue a second GET just to see what else is already in force.
type liveStateListResponse struct {
	Workspace  string                `json:"workspace"`
	Directives []livestate.Directive `json:"directives"`
}

// liveStateClearedResponse is DELETE's wire shape.
type liveStateClearedResponse struct {
	Workspace string `json:"workspace"`
	Cleared   int    `json:"cleared"`
}

// writeLiveStateList answers the current directive list for ws. Directives
// is never rendered as JSON null: [livestate.Store.List] returns a nil slice
// for a workspace that has never had one set, and "directives":null is a
// needless surprise for a caller expecting an array it can range over
// unconditionally.
func (p *Plane) writeLiveStateList(w http.ResponseWriter, ws *workspaces.Workspace) {
	directives := p.livestate.List(ws.ID)
	if directives == nil {
		directives = []livestate.Directive{}
	}
	httpx.JSON(w, http.StatusOK, liveStateListResponse{Workspace: ws.Slug, Directives: directives})
}

// serveLiveStatePost decodes one directive and applies it, per the wire
// contract fixed in internal/livestate/livestate.go's own doc comment (both
// this handler and the admin session API decode straight into
// [livestate.Directive] so they cannot drift on a field name or the "*"
// union).
func (p *Plane) serveLiveStatePost(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace) {
	// Round-1 review finding 4: this endpoint is UNAUTHENTICATED by design
	// (this file's own header comment) and, before this cap, the only bound
	// on the body jsonx.Decode was about to read was httpx.MaxBody's general
	// MOCKER_MAX_BODY ceiling (10 MB by default) — 64 such requests per
	// workspace, times every workspace slug an attacker can name, is the RAM
	// bill this measured. livestate.MaxDirectiveBodyBytes is sized for what
	// a VALID directive can actually contain (see its own doc comment); a
	// caller sending more than that never reaches Set at all.
	r.Body = http.MaxBytesReader(w, r.Body, livestate.MaxDirectiveBodyBytes)
	var d livestate.Directive
	if err := jsonx.NewDecoder(r.Body).Decode(&d); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf("decode directive: %v", err))
		return
	}

	// The scenario switch is a TOP-LEVEL KEY on this endpoint, not a third
	// Action (livestate.Directive.Scenario's own doc comment), and it is
	// still checked BEFORE Set: it never reaches the Store, which has no
	// notion of it and never gains one — the session layer is RAM and a
	// scenario is a row in SQLite. A body carrying BOTH a scenario and a
	// directive is answered as a switch and the directive half is not
	// applied; that precedence is unchanged from when this branch answered
	// 501, only the answer moved.
	if d.HasScenario() {
		p.serveScenarioSwitch(w, r, ws, d.Scenario)
		return
	}

	if err := p.livestate.Set(ws.ID, d); err != nil {
		p.answerLiveStateSetError(w, ws, err)
		return
	}

	peer := httpx.ResolvePeer(r, p.cfg.TrustProxy)
	p.log.Info("live-state directive set",
		"workspace", ws.Slug, "peer", peer.String(),
		"target", targetLabel(d.Target), "action", d.Action, "status", d.Status)

	p.writeLiveStateList(w, ws)
}

// scenarioSwitchResponse is what a successful scenario switch answers with.
// It is deliberately NOT the directive list every other POST to this
// endpoint returns: a scenario is not a directive, it does not live in the
// live-state store, and rendering it inside "directives" would invite a
// client to believe DELETE {prefix}/state clears it (it does not — that
// clears RAM; a scenario is cleared by switching to "").
//
// Revision is here because it is the one observable that PROVES the write
// landed: the runtime cache keys on (workspace, revision), so a caller that
// sees the number move knows the next request rebuilds. Scenario is a
// pointer so a deactivation answers "scenario":null rather than "", which
// would read as a name.
type scenarioSwitchResponse struct {
	Workspace string  `json:"workspace"`
	Scenario  *string `json:"scenario"`
	Revision  int64   `json:"revision"`
}

// serveScenarioSwitch answers POST {prefix}/state {"scenario": ...} —
// DESIGN §12's «переключение сценария из тестов», and the first thing on
// this unauthenticated plane that writes to the workspaces table.
//
// raw is the directive's Scenario field, already known to carry real
// content (HasScenario screened out an absent key and an explicit null,
// which must keep meaning "this body is not a scenario switch at all"). It
// must decode to a JSON STRING:
//
//   - a non-empty name ACTIVATES the scenario of that name in this
//     workspace, 404 if there is none;
//   - the empty string DEACTIVATES. A scenario name is never empty (the
//     repo rejects a blank one on create), so "" is free to carry the one
//     meaning `null` cannot: null already means "no scenario key at all"
//     to HasScenario, and taking it for "deactivate" would make every
//     client that emits every field it knows about silently switch the
//     workspace off.
//
// Anything else — an object, a number, a bare `true` — is a 400 naming the
// expected shape. It is worth being explicit that this is not the shape
// the 501 era accepted: that handler took ANY content here and refused it
// all identically, so `{"scenario":{"name":"x"}}` used to be a 501 and is
// now a 400.
//
// A7's idempotence lives in SetActive, not here: this handler resolves a
// name and calls it, and re-activating the already-active scenario costs no
// write, no revision bump and therefore no rebuild. That matters on THIS
// plane specifically — it is unauthenticated by design and DESIGN §18
// refuses to rate-limit it, so an idempotent repeat has to be free or a
// caller in a loop drives a rebuild loop (each rebuild re-parses the spec)
// that nothing can throttle. Duplicating the check here would be a second
// version of it to drift.
func (p *Plane) serveScenarioSwitch(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, raw jsonx.RawMessage) {
	if p.scenarios == nil {
		// 503, exactly like the nil LiveStateSource above: the endpoint
		// exists, the feature it backs is simply not wired in this
		// deployment. Never the old 501 — "arrives in a later phase" became
		// false the moment scenarios shipped, and a client cannot tell a
		// permanent "never" from a transient "not here" if both say 501.
		httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable",
			"scenario switching is not available: no ScenarioSource was wired")
		return
	}

	var name string
	if err := jsonx.Unmarshal(raw, &name); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
			`"scenario" must be a scenario name as a JSON string; "" deactivates the active one`)
		return
	}

	target, ok := p.resolveScenarioTarget(w, r, ws, name)
	if !ok {
		return
	}

	revision, err := p.scenarios.SetActive(r.Context(), ws.ID, target)
	if err != nil {
		// ErrNotFound is reachable here even though resolveScenarioTarget
		// just found the row: the scenario can be deleted between the two
		// calls. It is the caller's 404 either way, not this handler's 500.
		if errors.Is(err, scenarios.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, fmt.Sprintf(
				"workspace %q has no scenario named %q", ws.Slug, name))
			return
		}
		p.log.Error("live-state: set active scenario", "workspace", ws.Slug, "scenario", name, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}

	// Logged at Info with the peer, like a directive is — more so, in fact:
	// this is an anonymous caller changing what a workspace SERVES, stored
	// in SQLite, and the log line is the only trace of who did it.
	peer := httpx.ResolvePeer(r, p.cfg.TrustProxy)
	p.log.Info("scenario switched",
		"workspace", ws.Slug, "peer", peer.String(),
		"scenario", name, "active", target != nil, "revision", revision)

	resp := scenarioSwitchResponse{Workspace: ws.Slug, Revision: revision}
	if target != nil {
		resp.Scenario = &name
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// resolveScenarioTarget turns the requested name into the argument
// [ScenarioSource.SetActive] takes: nil for the empty name (deactivate), or
// the id of this workspace's scenario of that name. ok is false when it has
// already written the response — an unknown name is a 404 that changes
// NOTHING, which is why the lookup happens before SetActive rather than
// SetActive being handed a name to fail on.
//
// The 404 carries httpx.CodeNotFound, deliberately not the
// "not_implemented_yet" code this plane answers for any unknown path under
// the reserved prefix (plane.go's serveReserved). A caller — and the smoke
// suite — must be able to tell "this workspace has no scenario by that
// name" from "this plane has never heard of what you asked for"; without
// distinct codes, a handler that had never looked a name up at all would be
// indistinguishable from this one.
func (p *Plane) resolveScenarioTarget(
	w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, name string,
) (*int64, bool) {
	if name == "" {
		return nil, true
	}
	sc, err := p.scenarios.ByName(r.Context(), ws.ID, name)
	if err != nil {
		if errors.Is(err, scenarios.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, fmt.Sprintf(
				"workspace %q has no scenario named %q", ws.Slug, name))
			return nil, false
		}
		p.log.Error("live-state: resolve scenario by name", "workspace", ws.Slug, "scenario", name, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return nil, false
	}
	return &sc.ID, true
}

// answerLiveStateSetError maps [livestate.Store.Set]'s two sentinel errors
// to the wire codes this file's own header comment pins down once for both
// planes: ErrTooManyDirectives is a 409 (never 429 — DESIGN §18 puts no rate
// limit on this plane; the bound is the directive count, not request
// frequency), ErrInvalidDirective is a 400. Anything else is this handler's
// own bug to log, never the caller's to see the internals of.
func (p *Plane) answerLiveStateSetError(w http.ResponseWriter, ws *workspaces.Workspace, err error) {
	switch {
	case errors.Is(err, livestate.ErrTooManyDirectives):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, livestate.ErrInvalidDirective):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	default:
		p.log.Error("live-state: set directive", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
	}
}

// targetLabel renders a directive's target for the one-line log entry above
// — display only, never a lookup key.
func targetLabel(t livestate.Target) string {
	if t.All {
		return "*"
	}
	return t.Method + " " + t.Path
}
