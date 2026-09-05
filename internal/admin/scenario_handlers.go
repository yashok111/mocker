// Scenario handlers implement §C's admin routes for DESIGN §4's Scenario
// layer (P2b), plus P2d's clone and rename additions: the two GETs (list
// and detail — deliberately DIFFERENT shapes, see
// scenarioListView/scenarioDetailView below), create (from the workspace's
// current state, or — with `from` present, P2d — as a clone of another
// scenario already saved in the same workspace), rename (P2d), delete, and
// the activate/deactivate pair that stands in for the PATCH DESIGN §14
// could not use to express "no scenario" (A6 — *int64 collapses null and
// absent, the same trap CLAUDE.md already records for specId).
//
// Every handler here is a thin HTTP adapter over [scenarios.Repo]. Nothing
// in this file reaches into overrides.Repo or workspaces.Repo to do a
// piece of the work itself — A8's ownership check and A7's activation
// idempotence live inside [scenarios.Repo.SetActive], called from BOTH
// this plane's activate/deactivate routes and the mock plane's own
// {prefix}/state switch (§B seam 2: the SAME repo method, not two
// versions of the same check that could drift).
//
// deactivate is this slice's own addition, not one of DESIGN §14 line
// 840's [/:sid] block (which the other verbs — including rename, P2d's own
// addition — mirror one-to-one): DESIGN was written before PATCH was known
// to be unable to express "no scenario", the same reason GET/POST
// .../auth-preset was this codebase's own addition on top of what DESIGN
// names verbatim (server.go:217-222).
package admin

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/store"
)

// scenarioSummaryView is one entry of [scenarioListView] — [scenarios.Summary]
// on the wire, and NOTHING from the snapshot itself. §C: a list endpoint
// that answered every scenario's full BLOB would be a page-load cost that
// grows with the workspace's history, for a screen that only ever needs a
// name, a date and which one is active.
type scenarioSummaryView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	IsActive  bool   `json:"isActive"`
	// EditVersion is A3's per-row compare-and-swap token (D5).
	EditVersion int64 `json:"editVersion"`
}

func newScenarioSummaryView(sc scenarios.Summary) scenarioSummaryView {
	return scenarioSummaryView{
		ID: sc.ID, Name: sc.Name, CreatedAt: sc.CreatedAt.Unix(), IsActive: sc.IsActive,
		EditVersion: sc.EditVersion,
	}
}

// scenarioListView is GET .../scenarios' wire shape.
type scenarioListView struct {
	Scenarios []scenarioSummaryView `json:"scenarios"`
}

// scenarioSpecView is Bundle.Spec on the wire: provenance only (A15) —
// nothing that reads this back ever consults Hash to decide whether the
// snapshot may be applied (see [bundle.SpecRef]'s own doc comment). Inline
// is always JSON null in THIS slice's bundles (P4's portable-export use of
// the same field is not this slice's business), carried through anyway so
// the wire shape never silently drops a field the format itself has.
type scenarioSpecView struct {
	Hash   string           `json:"hash"`
	Name   string           `json:"name"`
	Inline jsonx.RawMessage `json:"inline"`
}

// scenarioDetailView is GET .../scenarios/{sid}'s wire shape: the FULL
// decoded snapshot, deliberately different from scenarioListView (§C) —
// this is what screen 5's A18 banner reads to learn which settings/
// overrides keys a running scenario currently masks, so it has to carry
// the actual composed-FROM state, not just a name and a date.
//
// Overrides reuses [bundle.OverrideEntry] directly rather than a second
// hand-declared wire type: the bundle package's own json tags ARE this
// route's wire shape already (A13 — the bundle encodes rows, and rows are
// exactly what this screen needs to show "which operations does this
// scenario touch, and how").
type scenarioDetailView struct {
	ID        int64                  `json:"id"`
	Name      string                 `json:"name"`
	CreatedAt int64                  `json:"createdAt"`
	IsActive  bool                   `json:"isActive"`
	Settings  domain.Settings        `json:"settings"`
	BasePath  string                 `json:"basePath"`
	Spec      scenarioSpecView       `json:"spec"`
	Overrides []bundle.OverrideEntry `json:"overrides"`
	// EditVersion is A3's per-row compare-and-swap token (D5): this view is
	// both GET .../scenarios/{sid}'s read and PUT (rename)'s write
	// response, so growing it here carries it into both at once — D5's "a
	// caller can write twice without re-reading" promise for the rename
	// route.
	EditVersion int64 `json:"editVersion"`
}

// newScenarioDetailView builds the wire view of sc. isActive is computed by
// the caller from the WORKSPACE's own scenario_id (this type has no notion
// of "am I active" on its own — see [scenarios.Summary]'s identical
// comment): Get/CreateFromCurrentState return a bare snapshot, and asking
// "is this active" is a fact about the workspace pointing at it, not about
// the scenario row itself.
func newScenarioDetailView(sc *scenarios.Scenario, isActive bool) scenarioDetailView {
	// bundle.New/Decode always produce a non-nil (possibly empty) slice —
	// see Bundle.Overrides' own field comment — but this guard costs
	// nothing and keeps the invariant from being this handler's problem if
	// it ever stops holding.
	entries := sc.Bundle.Overrides
	if entries == nil {
		entries = []bundle.OverrideEntry{}
	}
	return scenarioDetailView{
		ID:        sc.ID,
		Name:      sc.Name,
		CreatedAt: sc.CreatedAt.Unix(),
		IsActive:  isActive,
		Settings:  sc.Bundle.Workspace.Settings,
		BasePath:  sc.Bundle.BasePath,
		Spec: scenarioSpecView{
			Hash:   sc.Bundle.Spec.Hash,
			Name:   sc.Bundle.Spec.Name,
			Inline: sc.Bundle.Spec.Inline,
		},
		Overrides:   entries,
		EditVersion: sc.EditVersion,
	}
}

// scenarioActiveView is the wire shape both activate and deactivate answer
// with. ScenarioID is a pointer so a deactivate renders "scenarioId":null
// rather than 0, which would read as a real id — mirrors the mock plane's
// own scenarioSwitchResponse.Scenario (internal/mockplane/livestate.go).
type scenarioActiveView struct {
	ScenarioID *int64 `json:"scenarioId"`
	Revision   int64  `json:"revision"`
}

// createScenarioRequest is POST .../scenarios' request body. Name is always
// required. From is P2d's addition and optional: absent, this is "create
// from current state" — everything else in the snapshot comes from the
// workspace's own current state, which is the whole point of that path (§0:
// there is no scenario EDITOR, by design); present, this is instead a CLONE
// of the named scenario's own stored snapshot into a new row under Name —
// see handleCreateScenario and [scenarios.Repo.CloneFrom]'s doc comment.
type createScenarioRequest struct {
	Name string `json:"name"`
	From *int64 `json:"from"`
}

// renameScenarioRequest is PUT .../scenarios/{sid}'s request body: the name
// alone, nothing else — a rename changes one column (see
// [scenarios.Repo.Rename]'s own doc comment on why it bumps no revision).
type renameScenarioRequest struct {
	Name string `json:"name"`
	// EditVersion is A3's REQUIRED compare-and-swap expectation (D10): a
	// nil pointer means the caller omitted the field and is rejected BY
	// NAME below, never treated as "no expectation" (D7). &0 is refused
	// with a conflict rather than accepted — a scenario row addressed by
	// {sid} always already exists (D7's "0 is meaningful only for
	// op_overrides"), and scenarios.RenameExpecting's own doc comment
	// gives the exact mechanism.
	EditVersion *int64 `json:"editVersion"`
}

// scenarioConflictDetails is PUT .../scenarios/{sid}'s conflict payload
// (D6): every field [renameScenarioRequest] accepts — just Name, since a
// rename changes one column — plus the version the server actually holds.
type scenarioConflictDetails struct {
	Name        string `json:"name"`
	EditVersion int64  `json:"editVersion"`
}

// answerScenarioEditConflict writes PUT .../scenarios/{sid}'s 409 for a
// lost compare-and-swap. conflict.Current is boxed by
// [scenarios.Repo.RenameExpecting] as a *scenarios.Scenario (a pointer, the
// one payload type among the four single-object routes that is — see
// renameZeroRowsErr's own re-read), so the type assertion below differs from
// the other three routes' value assertions.
func (s *Server) answerScenarioEditConflict(w http.ResponseWriter, conflict *store.EditConflictError) {
	if conflict.Gone {
		httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
			"scenario was deleted by another write", editConflictGone{Gone: true})
		return
	}
	sc, ok := conflict.Current.(*scenarios.Scenario)
	if !ok || sc == nil {
		s.log.Error("scenario edit conflict: unexpected payload type", "type", fmt.Sprintf("%T", conflict.Current))
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to build conflict details")
		return
	}
	httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
		"scenario was changed by another write",
		scenarioConflictDetails{Name: sc.Name, EditVersion: sc.EditVersion})
}

// handleListScenarios answers GET /api/workspaces/{id}/scenarios: every
// scenario saved for the workspace, oldest first, as [scenarioListView] —
// never a snapshot (§C).
func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
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

	list, err := s.scenariosRepo.List(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list scenarios", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list scenarios")
		return
	}
	out := make([]scenarioSummaryView, len(list))
	for i, sc := range list {
		out[i] = newScenarioSummaryView(sc)
	}
	httpx.JSON(w, http.StatusOK, scenarioListView{Scenarios: out})
}

// handleCreateScenario answers POST /api/workspaces/{id}/scenarios. With no
// `from` field it snapshots the workspace's CURRENT settings and
// op_overrides rows into a new named scenario (A1: overrides only, never
// custom endpoints — §0) — [scenarios.Repo.CreateFromCurrentState] does the
// actual coherent read (A11) and the A10 active-scenario refusal. With
// `from` present (P2d) it instead CLONES that scenario's own stored
// snapshot under the new name via [scenarios.Repo.CloneFrom], a path that
// never reads the workspace's own layer at all — A10's refusal and A11's
// retry both do not apply to it (see CloneFrom's own doc comment), so a
// clone SUCCEEDS while a scenario is active, unlike the current-state path
// right above it. Either way this handler only decodes the body and maps
// the error set.
func (s *Server) handleCreateScenario(w http.ResponseWriter, r *http.Request) {
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

	var body createScenarioRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	var sc *scenarios.Scenario
	var err error
	if body.From != nil {
		sc, err = s.scenariosRepo.CloneFrom(r.Context(), ws.ID, *body.From, body.Name)
	} else {
		sc, err = s.scenariosRepo.CreateFromCurrentState(r.Context(), ws.ID, body.Name)
	}
	if err != nil {
		s.answerScenarioError(w, err)
		return
	}
	// isActive is always false here, on EITHER path — not because a
	// refusal makes it so (P2d's clone succeeds precisely while a scenario
	// IS active, so A10 alone no longer proves this), but because neither
	// CreateFromCurrentState nor CloneFrom ever writes
	// workspaces.scenario_id: only SetActive does, on the activate route
	// below, and nothing here calls it. A row this handler just inserted
	// therefore cannot be the one ws.ScenarioID already names, so there is
	// still nothing worth an extra read of ws.ScenarioID to "confirm".
	httpx.JSON(w, http.StatusCreated, newScenarioDetailView(sc, false))
}

// handleGetScenario answers GET /api/workspaces/{id}/scenarios/{sid}: the
// FULL snapshot (§C) — what screen 5's A18 banner reads.
func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
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
	sid, ok := parsePathInt64(w, r, "sid")
	if !ok {
		return
	}

	sc, err := s.scenariosRepo.Get(r.Context(), ws.ID, sid)
	if err != nil {
		s.answerScenarioError(w, err)
		return
	}
	isActive := ws.ScenarioID != nil && *ws.ScenarioID == sc.ID
	httpx.JSON(w, http.StatusOK, newScenarioDetailView(sc, isActive))
}

// handleRenameScenario answers PUT /api/workspaces/{id}/scenarios/{sid}
// (P2d) — rename only, the one verb of DESIGN §14 line 840's [/:sid] block
// this codebase did not ship in P2b. [scenarios.Repo.Rename] does the
// actual work (scoped by workspace, A8, and re-read through its own write
// transaction — see its doc comment); this handler only decodes the name
// and maps the error set. isActive is recomputed from ws.ScenarioID exactly
// like handleGetScenario does, NOT hardcoded false the way create's is:
// unlike a freshly inserted row, a rename can target the scenario that IS
// currently active, and answering isActive:false unconditionally here would
// lie about it to screen 9's own row for that scenario.
func (s *Server) handleRenameScenario(w http.ResponseWriter, r *http.Request) {
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
	sid, ok := parsePathInt64(w, r, "sid")
	if !ok {
		return
	}

	var body renameScenarioRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if body.EditVersion == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "editVersion is required")
		return
	}

	sc, err := s.scenariosRepo.RenameExpecting(r.Context(), ws.ID, sid, body.Name, body.EditVersion)
	if err != nil {
		s.answerScenarioError(w, err)
		return
	}
	isActive := ws.ScenarioID != nil && *ws.ScenarioID == sc.ID
	httpx.JSON(w, http.StatusOK, newScenarioDetailView(sc, isActive))
}

// handleDeleteScenario answers DELETE /api/workspaces/{id}/scenarios/{sid}.
// A9: deleting the ACTIVE scenario bumps the workspace's revision in the
// same write ([scenarios.Repo.Delete] does this, not this handler) —
// ON DELETE SET NULL alone would change what the workspace serves without
// invalidating the runtime cache, which keys on revision.
func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
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
	sid, ok := parsePathInt64(w, r, "sid")
	if !ok {
		return
	}

	if err := s.scenariosRepo.Delete(r.Context(), ws.ID, sid); err != nil {
		s.answerScenarioError(w, err)
		return
	}
	httpx.NoContent(w)
}

// handleActivateScenario answers
// POST /api/workspaces/{id}/scenarios/{sid}/activate. Takes no request
// body — {sid} in the path is the entire instruction, exactly like
// POST .../traffic/{tid}/to-override takes none for the same reason.
func (s *Server) handleActivateScenario(w http.ResponseWriter, r *http.Request) {
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
	sid, ok := parsePathInt64(w, r, "sid")
	if !ok {
		return
	}

	revision, err := s.scenariosRepo.SetActive(r.Context(), ws.ID, &sid)
	if err != nil {
		s.answerScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, scenarioActiveView{ScenarioID: &sid, Revision: revision})
}

// handleDeactivateScenario answers
// POST /api/workspaces/{id}/scenarios/deactivate — this slice's stand-in
// for the PATCH DESIGN §14 cannot express (see this file's own header
// comment and A6). Takes no request body: there is nothing to name, unlike
// activate, which needs {sid}.
func (s *Server) handleDeactivateScenario(w http.ResponseWriter, r *http.Request) {
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

	revision, err := s.scenariosRepo.SetActive(r.Context(), ws.ID, nil)
	if err != nil {
		s.answerScenarioError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, scenarioActiveView{ScenarioID: nil, Revision: revision})
}

// answerScenarioError maps [scenarios.Repo]'s error set to wire codes, once,
// for all six mutating/reading scenario routes that can produce one of them
// — create, get, rename (P2d), delete, activate, deactivate (list never
// calls this — it has no per-scenario lookup to fail).
// Shared rather than duplicated per handler for the same reason
// answerCreateEndpointError is shared by two callers: a caller must not see
// one 404/409 shape from the create route and a different one from
// activate.
//
// err.Error() is used directly as the message for the sentinels that
// already name the offending thing (ErrNotFound, ErrInvalidName,
// ErrDuplicateName, ErrScenarioActive, ErrConcurrentEdit all wrap the
// workspace id, scenario id or name into their own text — see each
// sentinel's doc comment in internal/scenarios/repo.go) — §I requires a
// 409/404 name what it refuses, and duplicating that text here would be a
// second copy to drift from the sentinel's own wrapping.
// codeSnapshotUnreadable is the envelope code for a stored scenario or
// checkpoint snapshot this build cannot decode — see the bundle.ErrInvalid
// case in answerScenarioError; answerCheckpointError uses the same code for
// the same reason.
const codeSnapshotUnreadable = "snapshot_unreadable"

func (s *Server) answerScenarioError(w http.ResponseWriter, err error) {
	var conflict *store.EditConflictError
	switch {
	case errors.As(err, &conflict):
		// A3, D7/D8: only PUT .../scenarios/{sid} (rename) can ever produce
		// this — RenameExpecting is the one scenario write this handler
		// passes a non-nil expectation to — so this case is unreachable
		// from every other caller of this shared mapper (create, get,
		// delete, activate, deactivate).
		s.answerScenarioEditConflict(w, conflict)
	case errors.Is(err, scenarios.ErrNotFound):
		// A8: this is also what a scenario id belonging to a DIFFERENT
		// workspace answers — the repo's own WHERE clause makes "exists
		// elsewhere" and "does not exist" indistinguishable by construction,
		// so there is nothing more specific for this handler to say.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, err.Error())
	case errors.Is(err, scenarios.ErrWorkspaceNotFound):
		// A race with a concurrent workspace delete, not a client mistake —
		// loadWorkspace already confirmed existence moments earlier, exactly
		// like answerCreateEndpointError's identical case.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, scenarios.ErrInvalidName):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	case errors.Is(err, scenarios.ErrDuplicateName):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, scenarios.ErrScenarioActive):
		// A10.
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, scenarios.ErrConcurrentEdit):
		// A11's bounded fence exhausted its retries — a 409 telling the
		// operator to try the save again, not a 500: nothing is broken,
		// the workspace just kept changing under the coherent read.
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, bundle.ErrInvalid):
		// A stored snapshot this build cannot read — since A18 a
		// mockerBundle:4 scenario, which bundle refuses BY NAME (CARVE-OUTS
		// D5) — on GET, activate or any other read of it. It is the row's
		// state and not the request, so 409 and not 400, and not the 500
		// it used to be: the message carries the codec's own words, which
		// name the version and the range this build reads.
		httpx.Err(w, http.StatusConflict, codeSnapshotUnreadable, err.Error())
	default:
		s.log.Error("scenario operation failed", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
	}
}
