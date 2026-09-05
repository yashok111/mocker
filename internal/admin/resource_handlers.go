// Resource handlers implement P3a's three routes (decisions.md
// mocker-p3a-resources, D10): the derived route-family suggestions of a
// spec, a workspace's per-family decision state, and the one route that
// changes it. Every handler here is thin — [Server.resourcesRepo.Confirm]
// and [Server.resourcesRepo.Decline] (internal/resources) own the whole
// decision lifecycle, each as one transaction; nothing in this file writes
// resources, entities or resource_decisions itself.
//
// GET /api/specs/{id}/resource-suggestions and GET
// /api/workspaces/{id}/resources both call [specs.Repo.EnsureSuggestions],
// not only the spec-scoped route: R8's lazy backfill has to fire from
// EITHER entry point, because a spec imported before this slice existed
// carries no resource_suggestions row until something derives it, and a
// caller of the workspace route alone (the tab an operator actually opens)
// must not find it permanently empty.
//
// This file also composes ResourceFamiliesView/ResourceFamilyView — the
// join of a spec's current suggestions against a workspace's confirmed
// resources and declined decisions — because nothing under
// internal/resources or internal/specs exposes that view on its own
// ([resources.Repo] owns Confirm/Decline and the four-method entity store,
// [specs.Repo] owns EnsureSuggestions; neither owns "how a screen shows
// them side by side"). The one raw read this file makes directly against
// resource_decisions ([Server.declinedRouteFamilies]) is exactly that kind
// of presentation composition, not a second path around either package's
// writes — see that method's own comment for why no repo method covers it.
package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// The NINE wire codes the contract now declares for POST
// .../resource-decisions (P3e D5.1/D7.1 add the last two on top of P3a's
// seven): one 404 (codeResourceUnknownFamily) and eight distinct 409
// meanings told apart by error.code, all mapped 1:1 from internal/resources'
// own sentinel errors below ([Server.answerResourceDecisionError]).
const (
	codeResourceUnknownFamily       = "unknown_family"
	codeResourceAlreadyConfirmed    = "already_confirmed"
	codeResourceConfirmSlugRequired = "confirm_slug_required"
	codeResourceConfirmSlugMismatch = "confirm_slug_mismatch"
	codeResourceStaleConfig         = "stale_config"
	codeResourceEntityLimit         = "entity_limit"
	codeResourcePopulationFailed    = "population_failed"
	// codeResourceParentNotConfirmed is D5.1: confirming a nested family
	// whose parent is not itself confirmed, read inside the same
	// transaction the confirm runs in.
	codeResourceParentNotConfirmed = "parent_not_confirmed"
	// codeResourceChildConfirmed is D7.1: declining a family that is the
	// parent of a still-confirmed child — refused rather than cascaded,
	// because the cascade a wide DELETE would take is invisible behind a
	// confirmSlug that only names the workspace (D7.2).
	codeResourceChildConfirmed = "child_confirmed"
	// codeResourceBaseScopeUndeclared is P3h D6.4: confirming a family in a
	// workspace whose basePath carries a {param} while basePathValues
	// declares nothing to populate it with. Checked INSIDE the confirm
	// transaction beside the parent check, for that check's own reason — one
	// read before the transaction is one a concurrent settings edit walks
	// past — so it reaches this dispatcher like every other 409 here.
	codeResourceBaseScopeUndeclared = "base_scope_undeclared"
	// codeRederiveStaleGeneration is D4.4/P11: POST /api/specs/{id}/rederive's
	// own 409 — another writer minted a newer resource_suggestions generation
	// for the same spec between this call's pre-read and its own write
	// transaction, mapped from [specs.ErrStaleGeneration].
	codeRederiveStaleGeneration = "stale_generation"
)

// resourceSuggestionView is one [specs.ResourceSuggestion], re-tagged for
// the wire (D10): SpecID is deliberately NOT carried — the caller already
// knows which spec it asked about, and Wrapper stays derivation metadata
// the screen never reads (D10's own text on why).
type resourceSuggestionView struct {
	RouteFamily string  `json:"routeFamily"`
	Name        string  `json:"name"`
	IDField     string  `json:"idField"`
	Confidence  float64 `json:"confidence"`
}

// resourceSuggestionsView is GET /api/specs/{id}/resource-suggestions'
// whole body.
type resourceSuggestionsView struct {
	Suggestions []resourceSuggestionView `json:"suggestions"`
}

// resourceFamilyView is one route family's decision state — the shape both
// GET /api/workspaces/{id}/resources and POST .../resource-decisions answer
// with (D10). The five confirmed-only fields are explicitly-null (pointer or
// nil-slice), never omitted, on a row whose Decision is nil or "declined":
// the contract requires all eight keys present on every row.
type resourceFamilyView struct {
	RouteFamily string  `json:"routeFamily"`
	Name        string  `json:"name"`
	Decision    *string `json:"decision"`
	ResourceID  *int64  `json:"resourceId"`
	IDField     *string `json:"idField"`
	WriteForm   *string `json:"writeForm"`
	EntityCount *int64  `json:"entityCount"`
	// ByBaseScope is the per-base-scope breakdown beside EntityCount (D13),
	// an ARRAY of {baseScope, entityCount} rather than an object keyed by the
	// encoded tuple: an array keeps the workspace's own DECLARED order
	// (resources.DeclaredBaseScopes), and a map key would fold an encoding
	// (url.PathEscape'd tuple) into JSON where none belongs. nil (never an
	// empty slice) on an undecided or declined row, exactly mirroring
	// EntityCount's own nil-when-not-confirmed rule — both marshal to
	// `null`, so the two fields stay visibly in step.
	ByBaseScope []resourceBaseScopeCountView `json:"byBaseScope"`
}

// resourceBaseScopeCountView is one element of ResourceFamilyView.byBaseScope
// (D13): baseScope is the encoded base tuple ([resources.EncodeScope]'s own
// output, "" for a workspace whose basePath carries no parameter — D3.3's
// implicit singleton), never re-decoded here or anywhere else (the one-owner
// rule, D3.1: EncodeScope is the only encoder and there is no inverse).
type resourceBaseScopeCountView struct {
	BaseScope   string `json:"baseScope"`
	EntityCount int64  `json:"entityCount"`
}

// resourceFamiliesView is GET /api/workspaces/{id}/resources' whole body.
type resourceFamiliesView struct {
	Families []resourceFamilyView `json:"families"`
}

// resourceDecisionRequest is POST .../resource-decisions' request body
// (D4/D10). ConfirmSlug is optional on the wire and required by the server
// exactly when a resources row already exists for RouteFamily — enforced
// entirely inside [resources.Repo.Decline], never re-checked here.
type resourceDecisionRequest struct {
	RouteFamily string `json:"routeFamily"`
	State       string `json:"state"`
	ConfirmSlug string `json:"confirmSlug,omitempty"`
}

// resourceDecisionView is POST .../resource-decisions' 200 body: the one
// row the call just changed, under the singular key "family" so the screen
// cannot mistake it for a list (D10).
type resourceDecisionView struct {
	Family resourceFamilyView `json:"family"`
}

// resourceConfirmedStateLabel/resourceDeclinedStateLabel are the two
// [resourceFamilyView.Decision] string values the contract's enum declares
// — named constants rather than string literals scattered across every
// call site that builds one, the same shape overrides' Mode constants use.
const (
	resourceConfirmedStateLabel = "confirmed"
	resourceDeclinedStateLabel  = "declined"
)

// handleListResourceSuggestions answers GET
// /api/specs/{id}/resource-suggestions: specID's derived route-family
// suggestions, deriving once via [specs.Repo.EnsureSuggestions] and reading
// back every later call (R8).
func (s *Server) handleListResourceSuggestions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseSpecID(w, r)
	if !ok {
		return
	}
	// A missing spec must answer 404, not a derived-from-nothing empty
	// list: EnsureSuggestions' own WHERE spec_id = ? cannot distinguish
	// "spec absent" from "spec present, zero suggestions" any more than
	// handleSpecOperations' identical guard can (spec_handlers.go).
	if _, err := s.specsRepo.ByID(r.Context(), id); err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
			return
		}
		s.log.Error("resource suggestions: load spec", "spec", id, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec")
		return
	}

	suggestions, err := s.specsRepo.EnsureSuggestions(r.Context(), id)
	if err != nil {
		s.log.Error("resource suggestions", "spec", id, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to derive resource suggestions")
		return
	}
	out := make([]resourceSuggestionView, len(suggestions))
	for i, sugg := range suggestions {
		out[i] = resourceSuggestionView{
			RouteFamily: sugg.RouteFamily,
			Name:        sugg.Name,
			IDField:     sugg.IDField,
			Confidence:  sugg.Confidence,
		}
	}
	httpx.JSON(w, http.StatusOK, resourceSuggestionsView{Suggestions: out})
}

// rederiveResultView is POST /api/specs/{id}/rederive's whole body
// (decisions.md D4.2): [specs.RederiveResult], re-tagged for the wire.
// Added/Removed are never nil on the wire — an empty slice, not an omitted
// key, so a client always reads two arrays (possibly empty) rather than
// having to branch on null.
type rederiveResultView struct {
	Changed    bool     `json:"changed"`
	Generation int      `json:"generation"`
	Added      []string `json:"added"`
	Removed    []string `json:"removed"`
}

// newRederiveResultView adapts [specs.RederiveResult] to the wire, forcing
// Added/Removed to non-nil empty slices rather than JSON `null` (D4.2, D8.1:
// this is the one place a generation number is visible on the wire at all).
func newRederiveResultView(result specs.RederiveResult) rederiveResultView {
	added := result.Added
	if added == nil {
		added = []string{}
	}
	removed := result.Removed
	if removed == nil {
		removed = []string{}
	}
	return rederiveResultView{
		Changed:    result.Changed,
		Generation: result.Generation,
		Added:      added,
		Removed:    removed,
	}
}

// handleRederiveSuggestions answers POST /api/specs/{id}/rederive
// (decisions.md D4): re-runs family derivation over specID's already
// imported document and writes the result as a new resource_suggestions
// generation, exactly when it differs from the newest one
// ([specs.Repo.Rederive] owns the whole comparison and the write). This
// handler touches no workspace table, bumps no revision and takes no
// checkpoint by construction — it never resolves a workspace at all, and
// [specs.Repo.Rederive] itself writes only resource_suggestions (D7.1).
func (s *Server) handleRederiveSuggestions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseSpecID(w, r)
	if !ok {
		return
	}
	// Same reasoning as the sibling GET: a missing spec must answer 404,
	// not derive from thin air.
	if _, err := s.specsRepo.ByID(r.Context(), id); err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
			return
		}
		s.log.Error("rederive: load spec", "spec", id, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec")
		return
	}

	result, err := s.specsRepo.Rederive(r.Context(), id)
	if err != nil {
		if errors.Is(err, specs.ErrStaleGeneration) {
			httpx.Err(w, http.StatusConflict, codeRederiveStaleGeneration, err.Error())
			return
		}
		s.log.Error("rederive suggestions", "spec", id, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to rederive resource suggestions")
		return
	}
	httpx.JSON(w, http.StatusOK, newRederiveResultView(result))
}

// handleListWorkspaceResources answers GET /api/workspaces/{id}/resources:
// [Server.buildFamiliesView]'s composed, sorted set.
func (s *Server) handleListWorkspaceResources(w http.ResponseWriter, r *http.Request) {
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

	families, err := s.buildFamiliesView(r.Context(), ws)
	if err != nil {
		s.log.Error("list workspace resources", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list resources")
		return
	}
	httpx.JSON(w, http.StatusOK, resourceFamiliesView{Families: families})
}

// buildFamiliesView composes D10's whole GET .../resources answer: one row
// per suggestion of ws's bound spec (empty when ws has none), plus one row
// per confirmed resource the current spec no longer suggests — an orphan
// left behind by a re-bind, decided by [resources.Repo.OrphanedFamilies]
// (mocker-p4a-triage decisions.md D5/R1), never by a second membership
// check against the suggestion list fetched above — the two sets merged and
// then sorted once by RouteFamily, so a suggestion's own already-sorted
// order ([specs.Repo.EnsureSuggestions]) and an orphan's arbitrary map order
// both end up in the one final order D10 asks for. Reading this CAN derive —
// it calls the same EnsureSuggestions backfill entry point GET
// /api/specs/{id}/resource-suggestions' own handler does, so either route
// may be first to pay the derivation cost on a spec that has never had its
// suggestions computed (R8, see the package doc comment above).
func (s *Server) buildFamiliesView(ctx context.Context, ws *workspaces.Workspace) ([]resourceFamilyView, error) {
	var suggestions []*specs.ResourceSuggestion
	if ws.SpecID != nil {
		var err error
		suggestions, err = s.specsRepo.EnsureSuggestions(ctx, *ws.SpecID)
		if err != nil {
			return nil, fmt.Errorf("load suggestions for workspace %d: %w", ws.ID, err)
		}
	}

	confirmedRes, err := s.resourcesRepo.ForWorkspace(ctx, ws.ID)
	if err != nil {
		return nil, fmt.Errorf("load resources for workspace %d: %w", ws.ID, err)
	}
	confirmedByFamily := make(map[string]*resources.Resource, len(confirmedRes))
	for _, res := range confirmedRes {
		confirmedByFamily[res.RouteFamily] = res
	}

	declined, err := s.declinedRouteFamilies(ctx, ws.ID)
	if err != nil {
		return nil, err
	}

	out := make([]resourceFamilyView, 0, len(suggestions)+len(confirmedRes))
	for _, sugg := range suggestions {
		view, verr := s.resourceFamilyViewFor(ctx, sugg.RouteFamily, sugg.Name, confirmedByFamily[sugg.RouteFamily], declined[sugg.RouteFamily], ws.Settings)
		if verr != nil {
			return nil, verr
		}
		out = append(out, view)
	}

	// The leftover pass — a confirmed family the loop above never visited,
	// because it is not among the bound spec's newest suggestions — is
	// "a confirmed family the bound spec's newest suggestion generation
	// does not name" (mocker-p4a-triage decisions.md D5/R1), and that
	// predicate has exactly one implementation: [resources.OrphanedIn], the
	// pure half of the same predicate GET .../drift and reset-data's own
	// reseed reach through [resources.Repo.OrphanedFamilies], rather than a
	// second membership check reimplemented here — so the WHOLE confirmed
	// roster goes in, unfiltered, and only that function's returned map
	// decides which rows are orphaned.
	//
	// It is the PURE door rather than the fetching one because this function
	// already holds the generation's suggestions: its primary loop above
	// emits one row per suggestion and cannot be written without them.
	// Calling the wrapper here would read resource_suggestions a SECOND time,
	// at a later instant than the list above was taken at, and a rederive
	// committing between the two reads would make one family read as
	// suggested by the loop above and orphaned by the loop below inside one
	// response. The first launch of this slice shipped that; D5 says why the
	// predicate has two doors.
	confirmedFamilies := make([]string, len(confirmedRes))
	for i, res := range confirmedRes {
		confirmedFamilies[i] = res.RouteFamily
	}
	orphaned := resources.OrphanedIn(suggestions, confirmedFamilies)
	for _, res := range confirmedRes {
		if !orphaned[res.RouteFamily] {
			continue
		}
		view, verr := s.resourceFamilyViewFor(ctx, res.RouteFamily, res.Name, res, false, ws.Settings)
		if verr != nil {
			return nil, verr
		}
		out = append(out, view)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].RouteFamily < out[j].RouteFamily })
	return out, nil
}

// resourceFamilyViewFor builds one row: res == nil means the family has no
// resources row at all (undecided, or declined without ever being
// confirmed) — declined then decides whether Decision reads "declined" or
// stays nil (never decided). res != nil means CONFIRMED (a resources row
// exists precisely when [resources.Repo.Confirm] wrote one); EntityCount
// comes from [resources.Repo.CountEntities], not res.SeedCount — a
// resource's entity count can move after confirmation through ordinary
// POST/DELETE traffic, and a stale SeedCount would silently under- or
// over-report it (D10: "writeForm and entityCount are facts of the stored
// resource itself and can change afterward"). CountEntities counts across
// EVERY scope (P3e D6.2/D12.1) — [resources.Repo.List] became scope-scoped
// under nesting, and this view's count is deliberately not: a nested
// family's entityCount is its total across every parent row, not one scope's
// slice of it. settings is the workspace's OWN settings (never a
// scenario-composed effective one — D4.5's rule, the same
// [resources.DeclaredBaseScopes]'s own doc comment states): the breakdown
// this view carries is a fact about what the WORKSPACE declares, exactly
// like basePath and basePathValues themselves.
func (s *Server) resourceFamilyViewFor(ctx context.Context, routeFamily, name string, res *resources.Resource, declined bool, settings domain.Settings) (resourceFamilyView, error) {
	if res == nil {
		view := resourceFamilyView{RouteFamily: routeFamily, Name: name}
		if declined {
			state := resourceDeclinedStateLabel
			view.Decision = &state
		}
		return view, nil
	}

	count, err := s.resourcesRepo.CountEntities(ctx, res.ID)
	if err != nil {
		return resourceFamilyView{}, fmt.Errorf("count entities for resource %d: %w", res.ID, err)
	}
	byBase, err := s.resourceBaseScopeCounts(ctx, res.ID, settings)
	if err != nil {
		return resourceFamilyView{}, err
	}
	state := resourceConfirmedStateLabel
	resourceID := res.ID
	idField := res.IDField
	return resourceFamilyView{
		RouteFamily: routeFamily,
		Name:        res.Name,
		Decision:    &state,
		ResourceID:  &resourceID,
		IDField:     &idField,
		WriteForm:   res.WriteForm,
		EntityCount: &count,
		ByBaseScope: byBase,
	}, nil
}

// resourceBaseScopeCounts is the per-base-scope breakdown D13 puts beside
// entityCount: one {baseScope, entityCount} pair per DECLARED base value
// (settings.BasePath/BasePathValues, via [resources.DeclaredBaseScopes] —
// the same function [resources.Repo]'s own confirm and reseed paths read the
// declared set through, so a declared value this handler shows and a
// declared value a confirm actually populates can never disagree about what
// is declared), in declared order, defaulting to 0 for a declared value that
// holds no row yet. Grouped with one query rather than one per declared
// value: [resources.Repo] exposes no per-base-scope count method (the Store
// seam owns that package; this file already reads resource_decisions
// directly for the same reason, see [Server.declinedRouteFamilies]'s own
// comment on why a presentation-only composition read lives here instead of
// there).
func (s *Server) resourceBaseScopeCounts(ctx context.Context, resourceID int64, settings domain.Settings) ([]resourceBaseScopeCountView, error) {
	bases := resources.DeclaredBaseScopes(settings.BasePath, settings.BasePathValues)

	rows, err := s.db.R.QueryContext(ctx,
		"SELECT base_scope_key, COUNT(*) FROM entities WHERE resource_id = ? GROUP BY base_scope_key", resourceID)
	if err != nil {
		return nil, fmt.Errorf("count entities by base scope for resource %d: %w", resourceID, err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[resources.ScopeKey]int64, len(bases))
	for rows.Next() {
		var key string
		var n int64
		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("scan base-scope count for resource %d: %w", resourceID, err)
		}
		counts[resources.ScopeKey(key)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate base-scope counts for resource %d: %w", resourceID, err)
	}

	out := make([]resourceBaseScopeCountView, len(bases))
	for i, base := range bases {
		out[i] = resourceBaseScopeCountView{BaseScope: string(base), EntityCount: counts[base]}
	}
	return out, nil
}

// declinedRouteFamilies reads the DECLINED half of workspaceID's
// resource_decisions rows, directly against the table. The CONFIRMED half
// needs no equivalent read: a confirmed decision always has a matching
// resources row ([resources.Repo.Confirm] writes both inside one
// transaction), and [resources.Repo.ForWorkspace] already gives that row —
// the fact worth reporting — so a second confirmed-state read would only
// duplicate it. No method on [resources.Repo] surfaces resource_decisions
// on its own: Confirm and Decline write it, and nothing before this slice
// ever needed to read it back outside a transaction. This is a
// presentation-only composition read for a view neither internal/resources
// nor internal/specs owns (this file's own package doc comment) — not a
// second path around either package's writes, which stay exactly where D4
// puts them.
func (s *Server) declinedRouteFamilies(ctx context.Context, workspaceID int64) (map[string]bool, error) {
	rows, err := s.db.R.QueryContext(ctx,
		"SELECT route_family FROM resource_decisions WHERE workspace_id = ? AND state = 'declined'", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load declined resource decisions for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]bool)
	for rows.Next() {
		var family string
		if err := rows.Scan(&family); err != nil {
			return nil, fmt.Errorf("scan declined resource decision for workspace %d: %w", workspaceID, err)
		}
		out[family] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate declined resource decisions for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// resolveFamilyName finds routeFamily's display name for a decline
// response, BEFORE [resources.Repo.Decline] runs: a decline of a confirmed
// resource deletes its row, so the name must be captured ahead of the call
// rather than re-read after it. Checks the confirmed resource first (its
// Name is the one Confirm actually stored), then the current suggestion set
// — empty string when neither exists, which only happens when Decline is
// about to answer ErrUnknownFamily anyway, so this result is never the
// authoritative answer for whether the family exists at all.
func (s *Server) resolveFamilyName(ctx context.Context, ws *workspaces.Workspace, routeFamily string) string {
	if confirmedRes, err := s.resourcesRepo.ForWorkspace(ctx, ws.ID); err == nil {
		for _, res := range confirmedRes {
			if res.RouteFamily == routeFamily {
				return res.Name
			}
		}
	}
	if ws.SpecID != nil {
		if suggestions, err := s.specsRepo.EnsureSuggestions(ctx, *ws.SpecID); err == nil {
			for _, sugg := range suggestions {
				if sugg.RouteFamily == routeFamily {
					return sugg.Name
				}
			}
		}
	}
	return ""
}

// handleDecideResource answers POST /api/workspaces/{id}/resource-decisions
// (D4/D10): the whole decision lifecycle for one family, dispatched
// straight into [resources.Repo.Confirm] or [resources.Repo.Decline] — this
// handler decodes the request, resolves the display name a decline's
// response needs (BEFORE the row it would read from is gone), and maps the
// error set through [Server.answerResourceDecisionError]. It writes nothing
// of its own.
func (s *Server) handleDecideResource(w http.ResponseWriter, r *http.Request) {
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

	var req resourceDecisionRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if req.RouteFamily == "" {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "routeFamily is required")
		return
	}

	switch req.State {
	case resourceConfirmedStateLabel:
		res, err := s.resourcesRepo.Confirm(r.Context(), ws.ID, req.RouteFamily)
		if err != nil {
			s.answerResourceDecisionError(w, ws, err)
			return
		}
		state := resourceConfirmedStateLabel
		resourceID := res.ID
		idField := res.IDField
		// EntityCount here is res.Seq — the TOTAL just inserted, across
		// every scope (P3e D5.5 point 4) — not res.SeedCount, which for a
		// nested family holds only the PER-SCOPE count. Confirm just
		// populated the resource inside the transaction that returned res,
		// so nothing could have written or deleted an entity between that
		// commit and this response, and D12.1 fixes entityCount's meaning
		// as the family's total: unlike [Server.resourceFamilyViewFor]'s
		// own CountEntities-based read, this one is read off the struct
		// Confirm just built rather than queried back from the table.
		count := res.Seq
		// ws.Settings, not effectiveSettings: the breakdown reports what the
		// WORKSPACE declares (D4.5), the same rule resourceFamilyViewFor's
		// own doc comment states — Confirm itself already fenced the
		// population against these exact fields inside its own transaction
		// (fenceConfirmTx), so re-reading ws here cannot disagree with what
		// was just populated.
		byBase, err := s.resourceBaseScopeCounts(r.Context(), res.ID, ws.Settings)
		if err != nil {
			s.log.Error("resource base-scope counts", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load resource")
			return
		}
		httpx.JSON(w, http.StatusOK, resourceDecisionView{Family: resourceFamilyView{
			RouteFamily: req.RouteFamily, Name: res.Name, Decision: &state,
			ResourceID: &resourceID, IDField: &idField, WriteForm: res.WriteForm, EntityCount: &count,
			ByBaseScope: byBase,
		}})
	case resourceDeclinedStateLabel:
		name := s.resolveFamilyName(r.Context(), ws, req.RouteFamily)
		if err := s.resourcesRepo.Decline(r.Context(), ws.ID, req.RouteFamily, req.ConfirmSlug); err != nil {
			s.answerResourceDecisionError(w, ws, err)
			return
		}
		state := resourceDeclinedStateLabel
		httpx.JSON(w, http.StatusOK, resourceDecisionView{Family: resourceFamilyView{
			RouteFamily: req.RouteFamily, Name: name, Decision: &state,
		}})
	default:
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, `state must be "confirmed" or "declined"`)
	}
}

// The two wire codes P3b adds, on top of P3a's seven above — one for
// reset-data's own stale-inputs case (reusing codeResourceStaleConfig
// verbatim: the same wire meaning, "generation inputs or roster moved",
// whichever route names it) and one that names reset-data's request-body
// vocabulary error, so a caller reading error.code alone knows this is a
// SHAPE refusal, not a state one.
const codeResetDataBadMode = "bad_request"

// resetDataRequest is POST .../reset-data's request body (D3, D14.1).
// Mode is a POINTER, not [resources.ResetMode]: the handler is the only
// place that can tell an ABSENT `mode` field apart from an EMPTY string —
// both collapse into the identical zero value the instant a typed
// ResetMode is constructed from them (clause 6's own text) — so decoding
// stops one step short of that type and the switch in
// [Server.handleResetData] makes the distinction explicit.
type resetDataRequest struct {
	Mode        *string `json:"mode"`
	ConfirmSlug string  `json:"confirmSlug"`
}

// resetDataSkippedView is one entry of [resetDataResponseView.Skipped].
type resetDataSkippedView struct {
	RouteFamily string `json:"routeFamily"`
	Reason      string `json:"reason"`
}

// resetDataResponseView is POST .../reset-data's 200 body (D8/D14.1).
type resetDataResponseView struct {
	Changed bool                   `json:"changed"`
	Deleted int64                  `json:"deleted"`
	Skipped []resetDataSkippedView `json:"skipped"`
}

// handleResetData answers POST /api/workspaces/{id}/reset-data (D3): the
// whole verb lives in [resources.Repo.ResetData], entirely inside
// internal/resources — this handler only decodes the two-field request,
// tells an absent `mode` apart from an empty one (clause 6, only possible
// here, before either collapses into [resources.ResetMode]), dispatches,
// and maps the five-row refusal matrix D14.2 declares. It writes nothing
// of its own, opens no transaction and touches neither op_overrides nor
// custom_endpoints nor checkpoints — reset-data changes ONLY entities, a
// layer config_snap does not carry (D3 R5, R12).
func (s *Server) handleResetData(w http.ResponseWriter, r *http.Request) {
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

	var req resetDataRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	var mode resources.ResetMode
	switch {
	case req.Mode == nil:
		httpx.Err(w, http.StatusBadRequest, codeResetDataBadMode, `mode is required: must be "reseed" or "clear"`)
		return
	case *req.Mode == string(resources.ResetModeReseed):
		mode = resources.ResetModeReseed
	case *req.Mode == string(resources.ResetModeClear):
		mode = resources.ResetModeClear
	default:
		httpx.Err(w, http.StatusBadRequest, codeResetDataBadMode, `mode must be "reseed" or "clear"`)
		return
	}

	outcome, err := s.resourcesRepo.ResetData(r.Context(), ws.ID, mode, req.ConfirmSlug)
	if err != nil {
		s.answerResetDataError(w, ws, err)
		return
	}

	skipped := make([]resetDataSkippedView, len(outcome.Skipped))
	for i, sk := range outcome.Skipped {
		skipped[i] = resetDataSkippedView{RouteFamily: sk.RouteFamily, Reason: sk.Reason}
	}
	httpx.JSON(w, http.StatusOK, resetDataResponseView{
		Changed: outcome.Changed,
		Deleted: outcome.Deleted,
		Skipped: skipped,
	})
}

// answerResetDataError maps [resources.Repo.ResetData]'s error set onto
// D14.2's five-row refusal matrix — three of [resources.Repo]'s own
// sentinels, already published (and mapped) by [Server.answerResourceDecisionError]
// above, reused here verbatim rather than duplicated: the wire meaning is
// identical whichever route names it.
func (s *Server) answerResetDataError(w http.ResponseWriter, ws *workspaces.Workspace, err error) {
	switch {
	case errors.Is(err, resources.ErrConfirmSlugRequired):
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugRequired, err.Error())
	case errors.Is(err, resources.ErrConfirmSlugMismatch):
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugMismatch, err.Error())
	case errors.Is(err, resources.ErrStaleConfig):
		httpx.Err(w, http.StatusConflict, codeResourceStaleConfig, err.Error())
	case errors.Is(err, resources.ErrWorkspaceNotFound):
		// [Server.loadWorkspace] already proved the workspace existed a
		// moment ago; a race where it vanishes between that read and this
		// write answers the same 404 an ordinary lookup miss would.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	default:
		s.log.Error("reset data", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to reset resource data")
	}
}

// answerResourceDecisionError maps [resources.Repo.Confirm]/[Decline]'s
// error set onto D10/D5.1/D7.1/P3h D6.4's ten wire codes — one 404, nine 409s
// told apart by error.code, every one of them a case this file's own const
// block above names. A sentinel with no case here does not answer its own
// code: it falls to the default and ships a 500, which is how
// base_scope_undeclared reached this dispatcher unmapped.
func (s *Server) answerResourceDecisionError(w http.ResponseWriter, ws *workspaces.Workspace, err error) {
	switch {
	case errors.Is(err, resources.ErrUnknownFamily):
		httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, err.Error())
	case errors.Is(err, resources.ErrAlreadyConfirmed):
		httpx.Err(w, http.StatusConflict, codeResourceAlreadyConfirmed, err.Error())
	case errors.Is(err, resources.ErrConfirmSlugRequired):
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugRequired, err.Error())
	case errors.Is(err, resources.ErrConfirmSlugMismatch):
		httpx.Err(w, http.StatusConflict, codeResourceConfirmSlugMismatch, err.Error())
	case errors.Is(err, resources.ErrStaleConfig):
		httpx.Err(w, http.StatusConflict, codeResourceStaleConfig, err.Error())
	case errors.Is(err, resources.ErrEntityLimit):
		httpx.Err(w, http.StatusConflict, codeResourceEntityLimit, err.Error())
	case errors.Is(err, resources.ErrPopulationFailed):
		httpx.Err(w, http.StatusConflict, codeResourcePopulationFailed, err.Error())
	case errors.Is(err, resources.ErrParentNotConfirmed):
		httpx.Err(w, http.StatusConflict, codeResourceParentNotConfirmed, err.Error())
	case errors.Is(err, resources.ErrChildConfirmed):
		httpx.Err(w, http.StatusConflict, codeResourceChildConfirmed, err.Error())
	case errors.Is(err, resources.ErrBaseScopeUndeclared):
		httpx.Err(w, http.StatusConflict, codeResourceBaseScopeUndeclared, err.Error())

	case errors.Is(err, resources.ErrWorkspaceNotFound):
		// [Server.loadWorkspace] already proved the workspace existed a
		// moment ago; a race where it vanishes between that read and this
		// write answers the same 404 an ordinary lookup miss would.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	default:
		s.log.Error("resource decision", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to record resource decision")
	}
}

// resourceEntitiesDefaultLimit and resourceEntitiesMaxLimit bound the
// "limit" query parameter on GET .../resources/{family}/entities (D4's own
// Shape) — copied from trafficDefaultLimit/trafficMaxLimit's own numbers and
// clamp shape (traffic_handlers.go), not reused directly: a caller reaching
// the wrong ceiling by an import accident would be a defect these two names
// exist to prevent. A limit above the ceiling is clamped silently, never a
// 400 — the same reason parseTrafficLimit gives.
const (
	resourceEntitiesDefaultLimit = 100
	resourceEntitiesMaxLimit     = 500
)

// resourceEntityView is one row of GET .../resources/{family}/entities
// (D4): a [resources.Entity], re-tagged for the wire. Data is embedded raw —
// the stored JSON object, already carrying the forced id — never re-decoded
// on the read path.
type resourceEntityView struct {
	ID           int64            `json:"id"`
	EntityKey    string           `json:"entityKey"`
	ScopeKey     string           `json:"scopeKey"`
	BaseScopeKey string           `json:"baseScopeKey"`
	Data         jsonx.RawMessage `json:"data"`
	CreatedAt    time.Time        `json:"createdAt"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

// resourceEntitiesView is GET .../resources/{family}/entities' whole body
// (D4): rows ordered by [resources.Repo.ListFiltered]'s own id ASC, plus
// lastId — the same field name and mechanic D6 gives list_traffic one
// section over (trafficPollView), because both key on a row id.
type resourceEntitiesView struct {
	Rows   []resourceEntityView `json:"rows"`
	LastID int64                `json:"lastId"`
}

// handleListResourceEntities answers GET
// /api/workspaces/{id}/resources/{family}/entities (D4): a confirmed
// family's entity rows, structured, paginated and scope-filtered — the read
// [resources.Repo.List] cannot serve on its own, which is why
// [resources.Repo.ListFiltered] (D12) exists. Agent-only by policy from A4
// to A20 (2026-09-05), when the owner lifted the rule for it: «Записи» on
// the resources screen (ResourceEntities.tsx) pages it now, and the EXEMPT
// entry is withdrawn; GET .../drift keeps the policy.
func (s *Server) handleListResourceEntities(w http.ResponseWriter, r *http.Request) {
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

	// The {family} segment carries url.PathEscape(routeFamily) on the
	// CLIENT side. Go's ServeMux splits the raw path on literal "/" and
	// unescapes each segment on its own, so PathValue hands back an
	// already-decoded segment — no further unescape step exists or is
	// needed (D4's own Addressing note). Do NOT copy
	// opKeyFromPath (override_handlers.go): that function RE-escapes an
	// already-decoded value for a different caller, and copying it here
	// would 404 on every real family.
	family := r.PathValue("family")

	res, err := s.confirmedResourceByFamily(r.Context(), ws.ID, family)
	if err != nil {
		s.log.Error("list resource entities: resolve family", "workspace", ws.Slug, "family", family, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to resolve resource family")
		return
	}
	if res == nil {
		// D4's own Error taxonomy: never suggested, declined, or the
		// workspace has no spec bound all collapse to the identical
		// unknown_family 404 — distinguishing them costs a second query
		// for a caller whose repair (POST .../resource-decisions) does
		// not depend on which cause it was.
		httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, "unknown route family")
		return
	}

	limit := parseResourceEntitiesLimit(r)
	after := parseResourceEntitiesAfter(r)
	base := parseResourceScopeFilter(r, "baseScopeKey")
	scope := parseResourceScopeFilter(r, "scopeKey")

	rows, err := s.resourcesRepo.ListFiltered(r.Context(), res.ID, base, scope, after, limit)
	if err != nil {
		if errors.Is(err, resources.ErrResourceGone) {
			// The family resolved a moment ago and the resources row is
			// gone by the time ListFiltered ran (a concurrent decline) —
			// the same unknown_family 404 an unresolved family answers,
			// one result for every cause, exactly as above.
			httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, "unknown route family")
			return
		}
		s.log.Error("list resource entities", "workspace", ws.Slug, "family", family, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list resource entities")
		return
	}

	out := make([]resourceEntityView, len(rows))
	// lastID starts at the cursor the caller sent and only moves forward —
	// an empty page (the last one) must echo "after" back unchanged, the
	// same rule parseTrafficSince's own poll handler follows, so a chained
	// reader does not replay the whole family on a quiet page.
	lastID := after
	for i, e := range rows {
		out[i] = resourceEntityView{
			ID: e.ID, EntityKey: e.EntityKey, ScopeKey: e.ScopeKey, BaseScopeKey: e.BaseScopeKey,
			Data: e.Data, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		}
		lastID = e.ID
	}
	httpx.JSON(w, http.StatusOK, resourceEntitiesView{Rows: out, LastID: lastID})
}

// confirmedResourceByFamily resolves routeFamily to workspaceID's confirmed
// [resources.Resource], or nil when it is not confirmed for any reason —
// [resources.Repo] exposes no by-family lookup of its own ([resources.Repo]'s
// own resourceByFamily is unexported, kept for its two internal callers'
// pre-transaction/in-transaction distinction), so this reuses the same
// ForWorkspace scan [Server.resolveFamilyName] already performs, addressing
// the family by its natural key and never by resources.id — the same reason
// ref and restoreEntitiesTx already address it that way (an id does not
// survive decline-then-reconfirm).
func (s *Server) confirmedResourceByFamily(ctx context.Context, workspaceID int64, family string) (*resources.Resource, error) {
	confirmedRes, err := s.resourcesRepo.ForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load resources for workspace %d: %w", workspaceID, err)
	}
	for _, res := range confirmedRes {
		if res.RouteFamily == family {
			return res, nil
		}
	}
	return nil, nil
}

// parseResourceEntitiesLimit reads "limit" off r's query, defaulting to
// resourceEntitiesDefaultLimit and clamping to resourceEntitiesMaxLimit.
// Anything that is not a positive integer (missing, zero, negative,
// unparsable) falls back to the default rather than answering 400 — the
// same rule parseTrafficLimit follows, for the same reason (D4's own Shape:
// "clamped silently, never a 400").
func parseResourceEntitiesLimit(r *http.Request) int {
	limit := resourceEntitiesDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > resourceEntitiesMaxLimit {
		limit = resourceEntitiesMaxLimit
	}
	return limit
}

// parseResourceEntitiesAfter reads "after" off r's query: a missing,
// negative or unparsable value is 0 (the beginning of the family), never an
// error — the same rule parseTrafficSince follows for "since", for the same
// reason: a cursor a caller echoes back verbatim need not fail the request
// just because it arrived malformed.
func parseResourceEntitiesAfter(r *http.Request) int64 {
	v := r.URL.Query().Get("after")
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseResourceScopeFilter reads name off r's query as an optional
// [resources.ScopeKey] filter: ABSENT means "any value of this axis" (nil,
// left out of ListFiltered's WHERE entirely — D4's own Shape); PRESENT,
// including the empty string, pins the axis to that exact value, because the
// empty tuple is itself a real, addressable scope, not the absence of a
// filter. url.Values.Has is what makes the distinction possible — .Get alone
// cannot tell "absent" from "present and empty".
func parseResourceScopeFilter(r *http.Request, name string) *resources.ScopeKey {
	q := r.URL.Query()
	if !q.Has(name) {
		return nil
	}
	v := resources.ScopeKey(q.Get(name))
	return &v
}
