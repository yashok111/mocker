// Ownership note (DESIGN §15): owner_id on a workspace is a LABEL, not a
// trusted identity — mocker's shared-password phase gives every logged-in
// user the same standing, so nothing below authorizes an action by comparing
// the caller to a workspace's owner. Every handler here stops at
// [Server.requireUser] ("is anybody logged in") and never asks "is this
// THEIR workspace". GET /api/workspaces defaults its listing to the caller's
// own workspaces purely as a convenience filter, overridable with ?all=1 —
// not an access boundary. Do not turn this into one; DESIGN §15 explains why
// that would be misleading (there is no per-user isolation to enforce).
package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// codeInvalidBasePath and codeInvalidBasePathValues are D4.3's two wire
// codes for a settings write whose basePath shape (including half two's
// spec-parameter collision, which is a fact about basePath, not about the
// declared list) or basePathValues shape is refused. Both writers reach
// them: the admin PATCH below and MCP's update_workspace_settings, which
// dispatches through this same handler via CallAsMCP (D4.3's own text).
const (
	codeInvalidBasePath       = "invalid_base_path"
	codeInvalidBasePathValues = "invalid_base_path_values"
)

// The path-mode prefix is httpx.WorkspacePathPrefix since A6 — one const
// for internal/server, this package and the mock plane, instead of the two
// copies that used to have to be updated together.

// errEmptyName is returned from inside an [workspaces.Repo.Update] mutate
// closure when a PATCH supplies a name field that trims to empty. It travels
// back through Update's own %w wrap, so errors.Is still finds it.
var errEmptyName = errors.New("name must not be empty")

// workspaceView is the wire shape of a [workspaces.Workspace]. Slug is always
// included: DESIGN §14 requires the actually-assigned slug be shown back to
// the caller, since a create with a taken name silently becomes "alex-2" and
// hiding that would point someone's frontend at a colleague's workspace.
type workspaceView struct {
	ID         int64           `json:"id"`
	Slug       string          `json:"slug"`
	Name       string          `json:"name"`
	SpecID     *int64          `json:"specId"`
	OwnerID    *int64          `json:"ownerId"`
	ForkedFrom *int64          `json:"forkedFrom"`
	ScenarioID *int64          `json:"scenarioId"`
	Revision   int64           `json:"revision"`
	Settings   domain.Settings `json:"settings"`
	// URL is the workspace's own externally reachable base URL: the origin a
	// browser would hit to actually talk to this workspace's mock plane
	// (DESIGN §14 screen 4's "Подключить"). It carries NO base path and NO
	// trailing slash — a UI concatenating url+"/api/v1/widgets" must not
	// produce a double slash — because /api/me returns only the user and the
	// CSRF token, and MOCKER_ADMIN_HOST is forbidden from sitting under
	// MOCKER_BASE_DOMAIN, so nothing else lets a browser derive this from
	// window.location. See [Server.workspaceURL] for how it is built.
	URL       string `json:"url"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// EditVersion is A3's per-row compare-and-swap token (D5): this view is
	// shared by every route that answers a workspace (list, create, get,
	// patch), so growing it here carries it into all four reads and the
	// PATCH write response at once — distinct from Revision above, which
	// is cache-invalidation bookkeeping (routeCache keys on it) and is
	// never bumped by fields PATCH cannot write.
	EditVersion int64 `json:"editVersion"`
}

// newWorkspaceView builds ws's wire view for r: a method (not the free
// function this used to be) because computing URL needs both cfg (for
// Routing/BaseDomain/AdminHost/TrustProxy) and the request (for its scheme
// and host) — see [Server.workspaceURL].
func (s *Server) newWorkspaceView(r *http.Request, ws *workspaces.Workspace) workspaceView {
	return workspaceView{
		ID:          ws.ID,
		Slug:        ws.Slug,
		Name:        ws.Name,
		SpecID:      ws.SpecID,
		OwnerID:     ws.OwnerID,
		ForkedFrom:  ws.ForkedFrom,
		ScenarioID:  ws.ScenarioID,
		Revision:    ws.Revision,
		Settings:    ws.Settings,
		URL:         s.workspaceURL(r, ws),
		CreatedAt:   ws.CreatedAt.Unix(),
		UpdatedAt:   ws.UpdatedAt.Unix(),
		EditVersion: ws.EditVersion,
	}
}

// workspaceURL computes ws's externally reachable base URL for r's scheme
// and host:
//
//	host mode (cfg.Routing == config.RoutingHost): <scheme>://<slug>.<cfg.BaseDomain>
//	path mode (cfg.Routing == config.RoutingPath): <scheme>://<cfg.AdminHost>/w/<slug>
//
// TWO PIECES OF THIS STRING COME FROM THE REQUEST, and [handleProbeWorkspace]
// DIALS the result — so each is whitelisted rather than merely read, and
// neither guard may be relaxed without re-reading this paragraph.
//
// The scheme is read per request via [httpx.ForwardedProto], not from cfg:
// internal/config has no scheme field, and a dev deployment terminating TLS at
// a reverse proxy needs the proxy's own X-Forwarded-Proto, which only a live
// request carries. ForwardedProto folds anything that is not exactly "https"
// to "http", because a scheme spliced into a URL string can smuggle an
// authority of its own.
//
// The port likewise comes from r.Host, never cfg — cfg.BaseDomain cannot hold
// one (IsWorkspaceHost strips it before matching a workspace host), so a
// config-only URL would be unreachable on any dev deployment behind a
// non-default port. It is read through [httpx.RequestPort], which requires a
// bare decimal number. That is not defensive tidiness: net.SplitHostPort
// splits on the last colon and returns whatever followed, with a nil error, so
// `Host: mocker.local:9@evil.example` produced the port "9@evil.example" and
// the string built below parsed with evil.example as its HOST and the
// workspace host demoted to userinfo — a read-SSRF pivot through the probe
// route, pinned now by TestP1c2WorkspaceView_urlRefusesAnInjectedPort.
func (s *Server) workspaceURL(r *http.Request, ws *workspaces.Workspace) string {
	// A6: delegated to the ONE construction in internal/httpx, which the
	// mock plane's asset_url recipe and the preview route also call — the
	// two guards the paragraph above describes live there now.
	return httpx.WorkspaceURL(r, s.cfg, ws.Slug)
}

// handleListWorkspaces answers the caller's own workspaces, or everyone's
// when ?all=1 is set (see the ownership note at the top of this file).
func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}

	var ownerID *int64
	if r.URL.Query().Get("all") != "1" {
		ownerID = &user.ID
	}

	list, err := s.ws.List(r.Context(), ownerID)
	if err != nil {
		s.log.Error("list workspaces", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list workspaces")
		return
	}

	out := make([]workspaceView, len(list))
	for i, ws := range list {
		out[i] = s.newWorkspaceView(r, ws)
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleCreateWorkspace creates a workspace owned (as a label, see above) by
// the caller. Slug is optional: when omitted, [workspaces.Repo.Create]
// derives one from name and the response always carries whichever slug was
// actually assigned.
//
// specId is optional too. When given, the spec must already exist (404
// otherwise) and its base_path is copied into the new workspace's
// settings.BasePath — DESIGN §7 step 3's hint "где его можно править": a
// freshly created workspace always starts with an empty base path, so there
// is nothing here for the spec's hint to overwrite.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}

	var body struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		SpecID *int64 `json:"specId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "name is required")
		return
	}

	var settings *domain.Settings
	if body.SpecID != nil {
		sp, ok := s.loadSpecForAttach(w, r, *body.SpecID)
		if !ok {
			return
		}
		def := domain.DefaultSettings()
		def.BasePath = sp.BasePath
		settings = &def
	}

	ownerID := user.ID
	ws, err := s.ws.Create(r.Context(), workspaces.CreateInput{
		Name:     name,
		Slug:     body.Slug,
		OwnerID:  &ownerID,
		SpecID:   body.SpecID,
		Settings: settings,
	})
	if err != nil {
		switch {
		case errors.Is(err, workspaces.ErrSlugTaken):
			httpx.Err(w, http.StatusConflict, httpx.CodeConflict, "slug already taken")
		case errors.Is(err, workspaces.ErrSlugInvalid):
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid slug")
		default:
			s.log.Error("create workspace", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to create workspace")
		}
		return
	}
	httpx.JSON(w, http.StatusCreated, s.newWorkspaceView(r, ws))
}

// handleGetWorkspace answers one workspace by id. Any logged-in user may
// fetch any workspace (see the ownership note at the top of this file).
func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}

	ws, err := s.ws.ByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, workspaces.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
			return
		}
		s.log.Error("get workspace", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load workspace")
		return
	}
	httpx.JSON(w, http.StatusOK, s.newWorkspaceView(r, ws))
}

// workspaceConflictDetails is PATCH /api/workspaces/{id}'s conflict payload
// (D6): every field the PATCH body accepts, as the server currently holds
// it — Name and Settings as plain values (a stored row has nothing to omit),
// SpecID kept nullable since it is the one field this route can never fully
// round-trip (a *int64 collapses "no spec" and "field omitted" the same way
// PATCH's own request body already does — see this route's own doc
// comment) — plus the version the server actually holds.
type workspaceConflictDetails struct {
	Name        string          `json:"name"`
	Settings    domain.Settings `json:"settings"`
	SpecID      *int64          `json:"specId"`
	EditVersion int64           `json:"editVersion"`
}

// answerWorkspaceEditConflict writes PATCH /api/workspaces/{id}'s 409 for a
// lost compare-and-swap. conflict.Current is boxed by
// [workspaces.Repo.UpdateExpecting] as a plain workspaces.Workspace (never a
// pointer), so the type assertion below is the one place this handler
// translates the sentinel's untyped payload into the route's declared wire
// shape.
func (s *Server) answerWorkspaceEditConflict(w http.ResponseWriter, conflict *store.EditConflictError) {
	if conflict.Gone {
		httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
			"workspace was deleted by another write", editConflictGone{Gone: true})
		return
	}
	ws, ok := conflict.Current.(workspaces.Workspace)
	if !ok {
		s.log.Error("workspace edit conflict: unexpected payload type", "type", fmt.Sprintf("%T", conflict.Current))
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to build conflict details")
		return
	}
	httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
		"workspace was changed by another write",
		workspaceConflictDetails{Name: ws.Name, Settings: ws.Settings, SpecID: ws.SpecID, EditVersion: ws.EditVersion})
}

// handlePatchWorkspace updates name, settings and/or the attached spec. All
// three fields are optional; whichever is present replaces the corresponding
// value wholesale — settings is not deep-merged, mirroring
// [workspaces.CreateInput.Settings]: the admin UI always round-trips the full
// settings object it fetched, never a sparse diff. [workspaces.Repo.Update]
// bumps Revision by exactly 1 regardless of what actually changed (DESIGN
// §12: it is a cache-invalidation counter, not a change count).
//
// specId, when present, must name an existing spec (404 otherwise) — checked
// before the mutate closure runs, since [workspaces.Repo.Update] has no way
// to turn a mutate error into a distinct 404 versus 500. Attaching copies the
// spec's base_path into settings.BasePath only when the workspace has none
// yet (DESIGN §7 step 3): a settings object supplied in the SAME request is
// applied first, so it is that value's emptiness which decides, not whatever
// the workspace had before the call.
func (s *Server) handlePatchWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}

	var body patchWorkspaceRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if body.EditVersion == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "editVersion is required")
		return
	}

	var spec *specs.Spec
	if body.SpecID != nil {
		var ok bool
		spec, ok = s.loadSpecForAttach(w, r, *body.SpecID)
		if !ok {
			return
		}
	}

	if !s.validatePatchBody(w, r, id, &body) {
		return
	}

	// P7a (D6): a rebind must not leave a stored endpoint's `$ref`
	// dangling — every custom row of this workspace is checked against
	// the NEW document before the write, and the whole PATCH is refused
	// (409) rather than binding a spec some row no longer resolves into.
	if body.SpecID != nil && s.refuseStoredRowsAgainstSpec(w, r, id, body.SpecID) {
		return
	}

	ws, err := s.ws.UpdateExpecting(r.Context(), id, body.EditVersion, func(cur *workspaces.Workspace) error {
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if name == "" {
				return errEmptyName
			}
			cur.Name = name
		}
		if body.Settings != nil {
			cur.Settings = *body.Settings
		}
		if body.SpecID != nil {
			cur.SpecID = body.SpecID
			if strings.TrimSpace(cur.Settings.BasePath) == "" {
				cur.Settings.BasePath = spec.BasePath
			}
		}
		return nil
	})
	if err != nil {
		s.answerPatchWorkspaceError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s.newWorkspaceView(r, ws))
}

// answerPatchWorkspaceError maps UpdateExpecting's failures onto the PATCH
// route's statuses — one place, so a new sentinel is answered once.
func (s *Server) answerPatchWorkspaceError(w http.ResponseWriter, err error) {
	var conflict *store.EditConflictError
	switch {
	case errors.As(err, &conflict):
		s.answerWorkspaceEditConflict(w, conflict)
	case errors.Is(err, workspaces.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, errEmptyName):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "name must not be empty")
	case errors.Is(err, workspaces.ErrSettingsTooLarge):
		// Round-1 review finding 3: an oversized settings blob is rejected
		// here, before it is ever persisted — the mock plane re-parses
		// settings on every unauthenticated request, so letting one
		// through would turn every future request into an amplified cost.
		httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "settings object is too large")
	case errors.Is(err, workspaces.ErrSlugTaken):
		// Unreachable today (PATCH never touches slug) but kept: a
		// future field addition to this handler must not silently start
		// returning 500 on a constraint this package already knows how
		// to report correctly.
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, "slug already taken")
	default:
		s.log.Error("patch workspace", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to update workspace")
	}
}

// validatePatchSettings is D4.3's both halves, pulled out of
// handlePatchWorkspace itself so that function's own branch count stays
// under gocyclo's ceiling (the specification is one refusal reason per
// branch, the identical shape the ten existing //nolint gocyclo pinpoints
// elsewhere in this tree already carry — extracting the validation costs
// nothing semantically, since it runs BEFORE the CAS transaction opens
// either way): a settings write that carries basePath/basePathValues has
// its shape validated here, never inside the import path
// (specs.Repo.Import) — a spec whose servers[].url names an undefaulted
// {variable} legitimately produces a base path with a live {param} on
// import, and this slice does not make that fail (D4.3's own "not applied
// to import" rule). Writes the refusal itself and returns false on any of
// the three checks; true means settings passed all of them.
func (s *Server) validatePatchSettings(w http.ResponseWriter, r *http.Request, workspaceID int64, specID *int64, settings *domain.Settings) bool {
	if verr := domain.ValidateBasePath(settings.BasePath); verr != nil {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBasePath, verr.Error())
		return false
	}
	if verr := domain.ValidateBasePathValues(settings.BasePath, settings.BasePathValues); verr != nil {
		httpx.Err(w, http.StatusBadRequest, codeInvalidBasePathValues, verr.Error())
		return false
	}
	return s.validateBasePathAgainstBoundSpec(w, r, workspaceID, specID, settings.BasePath)
}

// validateBasePathAgainstBoundSpec is D4.3's half two: a base parameter name
// equal to a path-parameter name declared by ANY route of the workspace's
// bound spec is refused. The mechanism it guards against is
// matchSegments' own `params[ps.name] = segments[i]` (router.go:264-283):
// two segments of one compiled pattern sharing a name write the same map
// key, the route-path one always winning silently because the base path's
// segments come first — internal/mockplane/resource.go documents the
// ADJACENT hazard (two Match.Params entries) and not this one, which is why
// it is written down here instead of assumed obvious.
//
// specIDOverride is the request body's own SpecID when this PATCH is
// rebinding the spec in the SAME call; nil means "read the workspace's
// currently bound spec". That read is deliberately UNFENCED by the CAS
// transaction below — D4.4 says the validator is a guard, never the
// mechanism, so a stale read here costs nothing: the mechanism a
// mis-configured workspace actually depends on is the POSITIONAL read
// (D7.1), which stays correct regardless of what this guard saw.
//
// It reads parameter names through router.BaseParamIndexes for BOTH
// basePath and every operation's own Path — the same one-owner function
// [domain.ValidateBasePath] already reads (D4.3), applied to a route path
// exactly as it is to a base path: both are "/"-joined strings with
// whole-segment {name} parameters, and BaseParamIndexes's own rule does not
// care which kind of path it is asked about. A second, local reader here
// would be the defect the one-owner rule exists to prevent.
func (s *Server) validateBasePathAgainstBoundSpec(w http.ResponseWriter, r *http.Request, workspaceID int64, specIDOverride *int64, basePath string) bool {
	_, baseNames, valid := router.BaseParamIndexes(domain.NormalizeBasePath(basePath))
	if !valid || len(baseNames) == 0 {
		// An invalid shape was already refused by domain.ValidateBasePath
		// above (called by this handler before this function runs); a shape
		// with no parameter at all has nothing to collide with.
		return true
	}

	specID := specIDOverride
	if specID == nil {
		cur, err := s.ws.ByID(r.Context(), workspaceID)
		if err != nil {
			if errors.Is(err, workspaces.ErrNotFound) {
				httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
				return false
			}
			s.log.Error("patch workspace: load current spec for base-path validation", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to validate base path")
			return false
		}
		specID = cur.SpecID
	}
	if specID == nil {
		// No spec bound at all: nothing to collide against.
		return true
	}

	ops, err := s.specsRepo.Operations(r.Context(), *specID, 0, 0)
	if err != nil {
		s.log.Error("patch workspace: load operations for base-path validation", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to validate base path")
		return false
	}

	baseNameSet := make(map[string]bool, len(baseNames))
	for _, n := range baseNames {
		baseNameSet[n] = true
	}
	for _, op := range ops {
		_, opNames, opValid := router.BaseParamIndexes(op.Path)
		if !opValid {
			continue // a degraded operation's own path shape is not this check's concern
		}
		for _, n := range opNames {
			if baseNameSet[n] {
				httpx.Err(w, http.StatusBadRequest, codeInvalidBasePath,
					fmt.Sprintf("basePath: parameter %q collides with a path parameter of the bound spec", n))
				return false
			}
		}
	}
	return true
}

// handleDeleteWorkspace deletes a workspace. Everything hanging off it
// cascades in the schema (see [workspaces.Repo.Delete]).
func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}

	if err := s.ws.Delete(r.Context(), id); err != nil {
		if errors.Is(err, workspaces.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
			return
		}
		s.log.Error("delete workspace", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to delete workspace")
		return
	}
	httpx.NoContent(w)
}

// loadSpecForAttach resolves specID for attaching to a workspace, answering
// 404 and reporting failure if no such spec exists. Shared by
// handleCreateWorkspace and handlePatchWorkspace, the two routes that accept
// specId (see the digest: "the /api/specs admin surface, plus attaching a
// spec to a workspace").
func (s *Server) loadSpecForAttach(w http.ResponseWriter, r *http.Request, specID int64) (*specs.Spec, bool) {
	sp, err := s.specsRepo.ByID(r.Context(), specID)
	if err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
			return nil, false
		}
		s.log.Error("lookup spec for workspace", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec")
		return nil, false
	}
	return sp, true
}

// validateRebindKeepsBasePath is D4.3's half two for a PATCH that rebinds
// the spec WITHOUT a settings body: the workspace's current basePath must
// not collide with a route parameter of the NEW spec. The check used to
// run only under body.Settings, which left this path — the exact silent
// params overwrite it refuses — unguarded.
func (s *Server) validateRebindKeepsBasePath(w http.ResponseWriter, r *http.Request, id int64, specID *int64) bool {
	cur, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return false
	}
	return s.validateBasePathAgainstBoundSpec(w, r, id, specID, cur.Settings.BasePath)
}

// validatePatchBody runs the settings checks a PATCH needs: the full set
// over a settings body when one is sent, and the bound-spec collision
// check over the CURRENT settings when the body rebinds the spec alone.
func (s *Server) validatePatchBody(w http.ResponseWriter, r *http.Request, id int64, body *patchWorkspaceRequest) bool {
	if body.Settings != nil {
		return s.validatePatchSettings(w, r, id, body.SpecID, body.Settings)
	}
	if body.SpecID != nil {
		return s.validateRebindKeepsBasePath(w, r, id, body.SpecID)
	}
	return true
}

// patchWorkspaceRequest is PATCH /api/workspaces/{id}'s body.
type patchWorkspaceRequest struct {
	Name     *string          `json:"name"`
	Settings *domain.Settings `json:"settings"`
	SpecID   *int64           `json:"specId"`
	// EditVersion is A3's REQUIRED compare-and-swap expectation (D10):
	// a nil pointer means the caller omitted the field and is rejected
	// BY NAME in the handler, never treated as "no expectation" (D7).
	EditVersion *int64 `json:"editVersion"`
}
