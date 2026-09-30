package mockplane

import (
	"context"
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/statediagram"
	"github.com/yashok111/mocker/internal/workspaces"
)

// StateEntityStore selects a patch from the latest entity inside its writer
// transaction. Keeping this optional preserves the ordinary EntityStore seam.
type StateEntityStore interface {
	PatchComputed(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, string, func(map[string]any) (map[string]any, error)) (resources.Entity, bool, error)
}

var _ StateEntityStore = (*resources.Repo)(nil)

func (p *Plane) serveStateDiagram(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, route *router.Route, m *router.Match, base resources.ScopeKey, overrideActive bool, effect livestate.Effect, delay int) bool {
	program := rt.stateDiagrams[overrides.OpKey(route.Method, route.Path)]
	if program == nil || overrideActive || effect.Status != 0 {
		return false
	}
	admission := program.Admission()
	if !admission.NoBody && !acceptable(r.Header.Get("Accept"), admission.MediaType) {
		httpx.Err(w, http.StatusNotAcceptable, "not_acceptable", "state transition media type is excluded by Accept")
		return true
	}
	if !awaitDelay(r.Context(), delay) {
		return true
	}
	host := &responseRuleEntityHost{resolver: luaHost{p: p, rt: rt, base: base, outer: routeOuterValues(route, m)}}
	binding := program.Entity()
	res, scope, err := host.target(responserules.EntityTarget{Family: binding.Family})
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "unknown_family" {
			status = http.StatusNotFound
		}
		httpx.Err(w, status, err.Error(), "state transition entity target is unavailable")
		return true
	}
	if binding.StateField == res.IDField {
		httpx.Err(w, http.StatusInternalServerError, "state_diagram_failed", "state field cannot replace the entity identity")
		return true
	}
	key := m.Params[binding.KeyParam]
	if !resources.CanonicalEntityKey(key, res.Wrapper.IDType) {
		writeEntityRefusal(w, route, resources.ErrEntityKeyNotCanonical, http.StatusBadRequest, "entity key is not canonical")
		return true
	}
	if !p.admitStateAncestors(w, r, ws, rt, route, res, base, host.resolver.outer[:len(res.ScopeParams)]) {
		return true
	}
	store, ok := p.entities.(StateEntityStore)
	if !ok {
		httpx.Err(w, http.StatusInternalServerError, "state_diagram_failed", "atomic entity transitions are unavailable")
		return true
	}
	var selected statediagram.Selection
	row, found, err := store.PatchComputed(r.Context(), res.ID, base, scope, key, res.IDField, res.Wrapper.IDType, func(data map[string]any) (map[string]any, error) {
		var selectionErr error
		selected, selectionErr = program.Select(r.Context(), data)
		return selected.Patch, selectionErr
	})
	if err != nil {
		p.writeStateDiagramError(w, r, ws, route, err)
		return true
	}
	if !found {
		writeEntityNotFound(w, route, key)
		return true
	}
	p.writeAssembled(w, ws, admission.NoBody, domain.AssembledResponse{
		Status: selected.ResponseStatus, MediaType: admission.MediaType, Body: row.Data,
		Headers: map[string]string{"X-Content-Type-Options": "nosniff"},
	})
	return true
}

// A bound HTTP action has the same reachability rule as ordinary resource
// routes. Stored descendants of a deleted ancestor remain dormant.
func (p *Plane) admitStateAncestors(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, route *router.Route, res *resources.Resource, base resources.ScopeKey, outer []string) bool {
	for i, family := range ancestorFamilies(res.RouteFamily, len(outer)) {
		ancestor := rt.resources[family]
		if ancestor == nil {
			p.writeStateDiagramError(w, r, ws, route, resources.ErrResourceGone)
			return false
		}
		_, found, err := p.entities.Get(r.Context(), ancestor.ID, base, resources.EncodeScope(outer[:i]), outer[i])
		if err != nil {
			p.writeStateDiagramError(w, r, ws, route, err)
			return false
		}
		if !found {
			writeEntityNotFound(w, route, outer[i])
			return false
		}
	}
	return true
}

func (p *Plane) writeStateDiagramError(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, route *router.Route, err error) {
	if r.Context().Err() != nil {
		return
	}
	if conflict, ok := errors.AsType[*statediagram.TransitionError](err); ok {
		httpx.Err(w, http.StatusConflict, conflict.Code, conflict.Message)
		return
	}
	status := 0
	switch {
	case errors.Is(err, resources.ErrResourceGone):
		status = http.StatusNotFound
	case errors.Is(err, resources.ErrEntityLimit), errors.Is(err, resources.ErrEntityKeyConflict):
		status = http.StatusConflict
	case errors.Is(err, resources.ErrEntityKeyNotCanonical):
		status = http.StatusBadRequest
	case errors.Is(err, resources.ErrWriteBusy):
		status = http.StatusServiceUnavailable
	}
	if status != 0 {
		writeEntityRefusal(w, route, err, status, "state transition could not be stored")
		return
	}
	p.log.Error("execute state diagram", "workspace", ws.Slug, "method", route.Method, "path", route.Path)
	httpx.Err(w, http.StatusInternalServerError, "state_diagram_failed", "state transition failed")
}
