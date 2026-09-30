package mockplane

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
)

// The Lua host already owns the request roster and scope policy for explicit
// entity operations. Reusing its resolver keeps visual rules on those same
// two axes while avoiding Lua's float conversion of returned JSON values.
type responseRuleEntityHost struct{ resolver luaHost }

func (h *responseRuleEntityHost) target(target responserules.EntityTarget) (*resources.Resource, resources.ScopeKey, error) {
	res, scope, err := h.resolver.resolveFamily(target.Family, target.Scope)
	if err != nil {
		return nil, "", err
	}
	declared := resources.DeclaredBaseScopes(h.resolver.rt.settings.BasePath, h.resolver.rt.settings.BasePathValues)
	if !slices.Contains(declared, h.resolver.base) {
		return nil, "", errors.New("bad_base_scope")
	}
	return res, scope, nil
}

func responseRuleEntityValue(row resources.Entity) (any, error) {
	decoder := jsonx.NewDecoder(strings.NewReader(string(row.Data)))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("row_undecodable")
	}
	return value, nil
}

func (h *responseRuleEntityHost) Read(ctx context.Context, target responserules.EntityTarget, key *string) (responserules.EntityResult, error) {
	res, scope, err := h.target(target)
	if err != nil {
		return responserules.EntityResult{}, err
	}
	if key != nil {
		if !resources.CanonicalEntityKey(*key, res.Wrapper.IDType) {
			return responserules.EntityResult{}, errors.New("bad_key")
		}
		row, found, err := h.resolver.p.entities.Get(ctx, res.ID, h.resolver.base, scope, *key)
		if err != nil {
			return responserules.EntityResult{}, storeErr(err)
		}
		if !found {
			return responserules.EntityResult{Found: false}, nil
		}
		value, err := responseRuleEntityValue(row)
		return responserules.EntityResult{Found: true, Value: value}, err
	}
	rows, err := h.resolver.p.entities.List(ctx, res.ID, h.resolver.base, scope)
	if err != nil {
		return responserules.EntityResult{}, storeErr(err)
	}
	values := make([]any, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return responserules.EntityResult{}, err
		}
		value, err := responseRuleEntityValue(row)
		if err != nil {
			return responserules.EntityResult{}, err
		}
		values = append(values, value)
	}
	return responserules.EntityResult{Found: true, Value: values}, nil
}

func (h *responseRuleEntityHost) Create(ctx context.Context, target responserules.EntityTarget, data map[string]any) (responserules.EntityResult, error) {
	res, scope, err := h.target(target)
	if err != nil {
		return responserules.EntityResult{}, err
	}
	row, err := h.resolver.p.entities.Create(ctx, res.ID, h.resolver.base, scope, res.IDField, res.Wrapper.IDType, data)
	if err != nil {
		return responserules.EntityResult{}, storeErr(err)
	}
	value, err := responseRuleEntityValue(row)
	return responserules.EntityResult{Found: true, Value: value}, err
}

func (h *responseRuleEntityHost) Update(ctx context.Context, target responserules.EntityTarget, key string, data map[string]any) (responserules.EntityResult, error) {
	res, scope, err := h.target(target)
	if err != nil {
		return responserules.EntityResult{}, err
	}
	if !resources.CanonicalEntityKey(key, res.Wrapper.IDType) {
		return responserules.EntityResult{}, errors.New("bad_key")
	}
	row, found, err := h.resolver.p.entities.Patch(ctx, res.ID, h.resolver.base, scope, key, res.IDField, res.Wrapper.IDType, data)
	if err != nil {
		return responserules.EntityResult{}, storeErr(err)
	}
	if !found {
		return responserules.EntityResult{Found: false}, nil
	}
	value, err := responseRuleEntityValue(row)
	return responserules.EntityResult{Found: true, Value: value}, err
}
