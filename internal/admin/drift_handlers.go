// Drift handlers implement P4a's one route (decisions.md
// mocker-p4a-triage): GET /api/workspaces/{id}/drift, which names the three
// things that silently stop working when a workspace is re-bound to a new
// spec version or when a rederive drops a family (D3):
//
//   - an op_overrides row whose (method, path) no operation of the bound
//     spec produces (D3.2) — compared LITERALLY, the same key
//     [overrides.Repo.ForWorkspace] already returns the map under, never
//     through router.CanonicalPath;
//   - a confirmed resources row whose route_family no suggestion of the
//     bound spec's newest generation names (D3.3) — the ONE predicate
//     [resources.Repo.OrphanedFamilies] implements, the same call
//     reset-data's own reseed loop and [Server.buildFamiliesView] make,
//     never a second copy of the check here;
//   - a custom_endpoints row whose (method, canonical_path) a spec
//     operation ALSO declares (D3.4), carrying precededSpec — whether the
//     endpoint's own created_at is STRICTLY before the bound spec's own —
//     as a hint an operator reads beside every reported collision, never a
//     filter over which ones are reported.
//
// This route is a GET, so it joins none of the auto-checkpoint groups
// below: it writes nothing but the resource_suggestions row
// [specs.Repo.EnsureSuggestions]'s own lazy backfill may write on a spec
// that has never had its suggestions computed (D4.4) — the same backfill
// [Server.buildFamiliesView] and GET /api/specs/{id}/resource-suggestions
// already trigger. A workspace with no spec bound answers 200 with
// hasDrift: false and three empty arrays (D4.3): there is no spec to
// diverge FROM, so nothing here is drift.
//
// The report carries KEYS, never a remedy: every repair this slice's three
// signals name already has its own verb —
// DELETE /api/workspaces/{id}/operations/{opKey},
// DELETE /api/workspaces/{id}/endpoints/{eid} and
// POST /api/workspaces/{id}/resource-decisions with state: "declined" — and
// a route string in the response body would be a second place the route
// table is written, one this file's own contract test never checks (D4.1).
package admin

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/workspaces"
)

// driftOrphanedOverrideView is one row of [driftReportView.OrphanedOverrides]
// — an op_overrides row the bound spec no longer produces an operation for.
// OpKey is [overrides.OpKey]'s own encoding, the exact percent-escaped
// segment DELETE /api/workspaces/{id}/operations/{opKey} accepts verbatim
// (D4.1) — never re-derived by the caller from Method/Path, which would
// risk a client's own encoding disagreeing with the server's.
type driftOrphanedOverrideView struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	OpKey  string `json:"opKey"`
}

// driftOrphanedResourceView is one row of
// [driftReportView.OrphanedResources] — a confirmed resource family the
// bound spec's newest suggestion generation no longer names. EntityCount is
// [resources.Repo.CountEntities]'s own count across every scope, the same
// field [resourceFamilyView] already reports for a confirmed family, so an
// operator deciding whether to decline it sees exactly what a decline would
// destroy.
type driftOrphanedResourceView struct {
	RouteFamily string `json:"routeFamily"`
	Name        string `json:"name"`
	ResourceID  int64  `json:"resourceId"`
	EntityCount int64  `json:"entityCount"`
}

// driftShadowedEndpointView is one row of [driftReportView.ShadowedEndpoints]
// — a custom endpoint whose (method, canonicalPath) a spec operation also
// declares (D3.4). PrecededSpec is custom_endpoints.created_at <
// specs.created_at for the BOUND spec, STRICTLY (R4): an endpoint created
// before the bound spec was ever imported cannot have been aimed at an
// operation that spec declares, so it reads as a genuine collision rather
// than the deliberate override rule 3 of router.compareRoutes documents.
// The flag is a hint carried BESIDE the row, never a filter — every
// canonical collision is reported either way (D3.4's own stated limit: a
// workspace re-bound to an OLDER spec reports precededSpec: false for a
// genuine accident, costing a hint and never a row).
type driftShadowedEndpointView struct {
	EndpointID    int64  `json:"endpointId"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	CanonicalPath string `json:"canonicalPath"`
	PrecededSpec  bool   `json:"precededSpec"`
}

// driftReportView is GET /api/workspaces/{id}/drift's whole body (D4.2).
// Three TYPED arrays, never `null` — an empty result serializes as `[]`,
// the rule [rederiveResultView]'s own Added/Removed already follow — and no
// row of any of them carries a remedy field (D4.1, D8 P24). HasDrift is
// DERIVED from the three arrays on the way out, the disjunction of their
// emptiness, and is NEVER computed from a separate query (D4.2/R3): the one
// assignment below is that expression and nothing else, so it cannot
// disagree with the arrays it summarizes.
type driftReportView struct {
	HasDrift          bool                        `json:"hasDrift"`
	OrphanedOverrides []driftOrphanedOverrideView `json:"orphanedOverrides"`
	OrphanedResources []driftOrphanedResourceView `json:"orphanedResources"`
	ShadowedEndpoints []driftShadowedEndpointView `json:"shadowedEndpoints"`
}

// handleGetWorkspaceDrift answers GET /api/workspaces/{id}/drift: the whole
// report [Server.buildDriftReport] composes, over the workspace's
// CURRENTLY bound spec and nothing else (D3.1) — there is no baseline
// column recording what the workspace was configured against, so a re-bind
// to a different spec and a rederive minting a narrower generation over the
// SAME spec are deliberately not distinguished; both leave a stored row the
// current spec no longer answers for, which is the whole of what this
// route reports.
func (s *Server) handleGetWorkspaceDrift(w http.ResponseWriter, r *http.Request) {
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

	report, err := s.buildDriftReport(r.Context(), ws)
	if err != nil {
		s.log.Error("workspace drift", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to compute workspace drift")
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}

// buildDriftReport reads the three reads the handler needs (D12.2:
// s.overridesRepo.ForWorkspace, s.specsRepo.Operations,
// s.resourcesRepo.ForWorkspace, s.customepRepo.ForWorkspace, and
// s.specsRepo.EnsureSuggestions through [resources.Repo.OrphanedFamilies])
// and nothing else — no new package, no new field on [Server]. ws.SpecID ==
// nil short-circuits every comparison to "nothing lives in the bound spec",
// which [resources.Repo.OrphanedFamilies]'s own nil-specID case already
// answers without a query (P27), and which the override/endpoint halves
// reach the identical way: an empty operations set names nothing as live,
// so every override reads orphaned... except D4.3 promises a workspace
// with no spec bound reports NOTHING as drift, not "every row", so the
// override and endpoint halves below are gated on ws.SpecID != nil
// explicitly, the same way the resource half's own nil-specID answer
// already is.
func (s *Server) buildDriftReport(ctx context.Context, ws *workspaces.Workspace) (driftReportView, error) {
	orphanedOverrides, shadowedEndpoints, err := s.driftOverridesAndEndpoints(ctx, ws)
	if err != nil {
		return driftReportView{}, err
	}
	orphanedResources, err := s.driftOrphanedResources(ctx, ws)
	if err != nil {
		return driftReportView{}, err
	}

	report := driftReportView{
		OrphanedOverrides: orphanedOverrides,
		OrphanedResources: orphanedResources,
		ShadowedEndpoints: shadowedEndpoints,
	}
	report.HasDrift = len(report.OrphanedOverrides) > 0 || len(report.OrphanedResources) > 0 || len(report.ShadowedEndpoints) > 0
	return report, nil
}

// driftOverridesAndEndpoints answers signals one (D3.2) and three (D3.4) —
// both gated on ws.SpecID != nil, since with no spec bound there is no live
// operation set to compare either kind of row against, and D4.3 promises a
// workspace with no spec bound reports NOTHING as drift, not "every row".
func (s *Server) driftOverridesAndEndpoints(ctx context.Context, ws *workspaces.Workspace) ([]driftOrphanedOverrideView, []driftShadowedEndpointView, error) {
	overrideRows, err := s.overridesRepo.ForWorkspace(ctx, ws.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("load overrides for workspace %d: %w", ws.ID, err)
	}

	orphanedOverrides := make([]driftOrphanedOverrideView, 0, len(overrideRows))
	shadowedEndpoints := make([]driftShadowedEndpointView, 0)

	if ws.SpecID != nil {
		ops, operr := s.specsRepo.Operations(ctx, *ws.SpecID, 0, 0)
		if operr != nil {
			return nil, nil, fmt.Errorf("load operations for spec %d: %w", *ws.SpecID, operr)
		}

		// Signal one (D3.2): liveKeys is the SAME key
		// [overrides.Repo.ForWorkspace] already returns its map under —
		// method + the operation's RELATIVE path, never CanonicalPath — the
		// identical literal comparison [lookupOverride] makes on the serve
		// path, so a row this report calls orphaned is a row that path also
		// treats as inert.
		liveKeys := make(map[string]bool, len(ops))
		// canonicalOps is signal three's own live set (D3.4): method plus
		// the operation's OWN canonical_path column, never recomputed here
		// — both sides of the comparison are already stored, each written
		// by router.CanonicalPath's one owner.
		canonicalOps := make(map[string]bool, len(ops))
		for _, op := range ops {
			liveKeys[overrides.OpKey(op.Method, op.Path)] = true
			canonicalOps[op.Method+" "+op.CanonicalPath] = true
		}

		for key, row := range overrideRows {
			if liveKeys[key] {
				continue
			}
			orphanedOverrides = append(orphanedOverrides, driftOrphanedOverrideView{
				Method: row.Method,
				Path:   row.Path,
				OpKey:  key,
			})
		}

		found, serr := s.driftShadowedEndpoints(ctx, ws, canonicalOps)
		if serr != nil {
			return nil, nil, serr
		}
		shadowedEndpoints = append(shadowedEndpoints, found...)
	}
	sort.Slice(orphanedOverrides, func(i, j int) bool { return orphanedOverrides[i].OpKey < orphanedOverrides[j].OpKey })
	sort.Slice(shadowedEndpoints, func(i, j int) bool { return shadowedEndpoints[i].EndpointID < shadowedEndpoints[j].EndpointID })
	return orphanedOverrides, shadowedEndpoints, nil
}

// driftShadowedEndpoints answers signal three (D3.4) alone: ws.SpecID is
// guaranteed non-nil by the one caller above, so the bound spec's own
// created_at is read (and precededSpec computed) only when at least one
// candidate actually collides — never for a workspace whose custom
// endpoints collide with nothing.
func (s *Server) driftShadowedEndpoints(ctx context.Context, ws *workspaces.Workspace, canonicalOps map[string]bool) ([]driftShadowedEndpointView, error) {
	endpoints, err := s.customepRepo.ForWorkspace(ctx, ws.ID)
	if err != nil {
		return nil, fmt.Errorf("load custom endpoints for workspace %d: %w", ws.ID, err)
	}
	var shadowed []*customep.Row
	for _, ep := range endpoints {
		if canonicalOps[ep.Method+" "+ep.CanonicalPath] {
			shadowed = append(shadowed, ep)
		}
	}
	if len(shadowed) == 0 {
		return nil, nil
	}

	spec, err := s.specsRepo.ByID(ctx, *ws.SpecID)
	if err != nil {
		return nil, fmt.Errorf("load bound spec %d: %w", *ws.SpecID, err)
	}
	out := make([]driftShadowedEndpointView, 0, len(shadowed))
	for _, ep := range shadowed {
		out = append(out, driftShadowedEndpointView{
			EndpointID:    ep.ID,
			Method:        ep.Method,
			Path:          ep.Path,
			CanonicalPath: ep.CanonicalPath,
			PrecededSpec:  ep.CreatedAt.Before(spec.CreatedAt),
		})
	}
	return out, nil
}

// driftOrphanedResources answers signal two (D3.3): the ONE predicate
// implementation, [resources.Repo.OrphanedFamilies], called over every
// confirmed family of the workspace in one round trip — never a local
// membership check reimplemented against a suggestion list fetched here.
func (s *Server) driftOrphanedResources(ctx context.Context, ws *workspaces.Workspace) ([]driftOrphanedResourceView, error) {
	confirmedRes, err := s.resourcesRepo.ForWorkspace(ctx, ws.ID)
	if err != nil {
		return nil, fmt.Errorf("load resources for workspace %d: %w", ws.ID, err)
	}
	families := make([]string, len(confirmedRes))
	for i, res := range confirmedRes {
		families[i] = res.RouteFamily
	}
	orphanedFamilies, err := s.resourcesRepo.OrphanedFamilies(ctx, ws.SpecID, families)
	if err != nil {
		return nil, fmt.Errorf("load orphaned families for workspace %d: %w", ws.ID, err)
	}
	orphanedResources := make([]driftOrphanedResourceView, 0, len(confirmedRes))
	for _, res := range confirmedRes {
		if !orphanedFamilies[res.RouteFamily] {
			continue
		}
		count, cerr := s.resourcesRepo.CountEntities(ctx, res.ID)
		if cerr != nil {
			return nil, fmt.Errorf("count entities for resource %d: %w", res.ID, cerr)
		}
		orphanedResources = append(orphanedResources, driftOrphanedResourceView{
			RouteFamily: res.RouteFamily,
			Name:        res.Name,
			ResourceID:  res.ID,
			EntityCount: count,
		})
	}
	sort.Slice(orphanedResources, func(i, j int) bool { return orphanedResources[i].RouteFamily < orphanedResources[j].RouteFamily })
	return orphanedResources, nil
}
