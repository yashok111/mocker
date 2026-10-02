package mockplane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/yashok111/mocker/internal/authpreset"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// ProxySource is the live-control seam. Immutable execution snapshots skip it.
type ProxySource interface {
	ServeProxy(http.ResponseWriter, *http.Request, *workspaces.Workspace, string, bool, func([]byte) (int, error)) bool
}
type noProxyKey struct{}

func (p *Plane) SetProxy(src ProxySource) { p.proxy = src }
func (p *Plane) tryProxy(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match, segments []string) bool {
	if p.proxy == nil || r.Context().Value(noProxyKey{}) != nil {
		return false
	}
	operation := r.Method + " " + authCheckPath(segments, ws.Settings.BasePath)
	if m != nil {
		operation = m.Route.Method + " " + m.Route.Path
		if m.Route.Custom {
			if row, ok := rt.lookupCustom(m.Route.CustomRowID); ok && row.RouteOff {
				return false
			}
		} else if row, ok := rt.lookupOverride(m.Route); ok && row.OverrideOn && row.RouteOff {
			return false
		}
	}
	auth := authpreset.IsAuthPath(authCheckPath(segments, ws.Settings.BasePath))
	return p.proxy.ServeProxy(w, r, ws, operation, auth, p.proxyEntityCapture(r.Context(), r, ws, rt, m, segments))
}

type proxyEntityWriter interface {
	Set(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, string, map[string]any) (resources.Entity, bool, error)
}

func (p *Plane) proxyEntityCapture(ctx context.Context, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match, segments []string) func([]byte) (int, error) {
	writer, ok := p.entities.(proxyEntityWriter)
	if !ok || rt == nil || m == nil || m.Route.Custom || r.Method != http.MethodGet {
		return nil
	}
	res, detail := lookupResource(rt, m.Route)
	if res == nil {
		return nil
	}
	return func(body []byte) (int, error) {
		base, scope, err := p.proxyCaptureScope(ctx, ws, rt, m, segments, res, detail)
		if err != nil {
			return 0, err
		}
		items, err := proxyResponseItems(body, rt.settings.Envelope, res, detail)
		if err != nil {
			return 0, err
		}
		objects, keys, err := proxyEntityRows(items, res, m, detail)
		if err != nil {
			return 0, err
		}
		for i, obj := range objects {
			if _, _, err := writer.Set(ctx, res.ID, base, scope, keys[i], res.IDField, res.Wrapper.IDType, obj); err != nil {
				return i, fmt.Errorf("record entity: %w", err)
			}
		}
		return len(objects), nil
	}
}
func (p *Plane) proxyCaptureScope(ctx context.Context, ws *workspaces.Workspace, rt *runtime, m *router.Match, segments []string, res *resources.Resource, detail bool) (resources.ScopeKey, resources.ScopeKey, error) {
	values, ok := router.BaseValues(ws.Settings.BasePath, segments)
	if !ok {
		return "", "", errors.New("invalid base scope")
	}
	base := resources.EncodeScope(values)
	if !slices.Contains(resources.DeclaredBaseScopes(ws.Settings.BasePath, ws.Settings.BasePathValues), base) {
		return "", "", errors.New("undeclared base scope")
	}
	scope, outer, ok := scopeOf(res, m.Route, m, detail)
	if !ok {
		return "", "", errors.New("resource scope changed")
	}
	for i, family := range ancestorFamilies(res.RouteFamily, len(outer)) {
		parent := rt.resources[family]
		if parent == nil {
			return "", "", errors.New("unconfirmed parent")
		}
		_, found, err := p.entities.Get(ctx, parent.ID, base, resources.EncodeScope(outer[:i]), outer[i])
		if err != nil {
			return "", "", err
		}
		if !found {
			return "", "", errors.New("missing ancestor entity")
		}
	}
	return base, scope, nil
}
func proxyResponseItems(body []byte, envelope *string, res *resources.Resource, detail bool) ([]any, error) {
	dec := jsonx.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if envelope != nil {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("response envelope is not an object")
		}
		value = obj[*envelope]
	}
	if detail {
		return []any{value}, nil
	}
	if res.Wrapper.ArrayKey != nil {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("collection wrapper is not an object")
		}
		value = obj[*res.Wrapper.ArrayKey]
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("collection is not an array")
	}
	return items, nil
}

// Validate every ID before the first upsert. Storage caps may still stop a
// later row; the caller reports how many rows were actually imported.
func proxyEntityRows(items []any, res *resources.Resource, m *router.Match, detail bool) ([]map[string]any, []string, error) {
	keys := make([]string, len(items))
	objects := make([]map[string]any, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, nil, errors.New("entity is not an object")
		}
		var key string
		switch id := obj[res.IDField].(type) {
		case string:
			key = id
		case jsonx.Number:
			key = string(id)
		default:
			return nil, nil, errors.New("entity has no string or numeric ID")
		}
		if !resources.CanonicalEntityKey(key, res.Wrapper.IDType) || seen[key] {
			return nil, nil, errors.New("invalid or duplicate entity ID")
		}
		if detail && key != m.Params[router.DetailIDParam(m.Route)] {
			return nil, nil, errors.New("response ID differs from requested ID")
		}
		seen[key] = true
		keys[i] = key
		objects[i] = obj
	}
	return objects, keys, nil
}
