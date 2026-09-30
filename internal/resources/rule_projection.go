package resources

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

// RuleFamilyError identifies an applied family that cannot use the managed
// workspace's existing dataset without changing its identity.
type RuleFamilyError struct {
	Family, Message string
}

func (e *RuleFamilyError) Error() string {
	return fmt.Sprintf("Семейство ресурсов %s: %s", e.Family, e.Message)
}

// EnsureRuleFamiliesTx projects applied entity families as empty confirmed
// resources. Existing rows and sequence counters survive every configuration
// update; the caller decides which projected families are active in runtime.
func EnsureRuleFamiliesTx(ctx context.Context, tx *sql.Tx, source *specs.Repo, workspaceID, specID int64, document []byte, families []string) error {
	if len(families) == 0 {
		return nil
	}
	suggestions, err := source.SuggestionsTx(ctx, tx, specID)
	if err != nil {
		return err
	}
	routes, err := source.RoutesTx(ctx, tx, specID)
	if err != nil {
		return err
	}
	variants, err := source.VariantsTx(ctx, tx, specID)
	if err != nil {
		return err
	}
	doc, _, err := openapi.Load(document)
	if err != nil {
		return err
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	byFamily := make(map[string]*specs.ResourceSuggestion, len(suggestions))
	for _, suggestion := range suggestions {
		byFamily[suggestion.RouteFamily] = suggestion
	}
	families = slices.Clone(families)
	slices.SortFunc(families, func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.Count(a, "{}"), strings.Count(b, "{}")), strings.Compare(a, b))
	})
	families = slices.Compact(families)
	for _, family := range families {
		suggestion := byFamily[family]
		if suggestion == nil {
			return &RuleFamilyError{Family: family, Message: "в текущем API не найдено подходящее семейство сущностей"}
		}
		prep, err := prepareRuleFamily(suggestion, resolver, routes, variants)
		if err != nil {
			return err
		}
		existing, err := scanResource(tx.QueryRowContext(ctx, selectResource+" WHERE workspace_id=? AND route_family=?", workspaceID, family))
		if errors.Is(err, sql.ErrNoRows) {
			if parent := router.ParentFamily(family); parent != "" {
				var parentID int64
				if err := tx.QueryRowContext(ctx, "SELECT id FROM resources WHERE workspace_id=? AND route_family=?", workspaceID, parent).Scan(&parentID); err != nil {
					return fmt.Errorf("project parent %q: %w", parent, err)
				}
			}
			if _, err := insertConfirmedResourceTx(ctx, tx, workspaceID, family, prep); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if existing.IDField != suggestion.IDField || existing.Wrapper.IDType != suggestion.Wrapper.IDType || len(existing.ScopeParams) != len(prep.scopeParams) {
			return &RuleFamilyError{Family: family, Message: "поле ID, тип ID или глубина области несовместимы с сохранёнными данными"}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE resources SET name=?,scope_params=?,entity_schema=?,wrapper=?,write_form=? WHERE id=?`,
			suggestion.Name, prep.scopeParamsJSON, suggestion.EntitySchema, prep.wrapperJSON, prep.writeForm, existing.ID); err != nil {
			return err
		}
	}
	return nil
}

func prepareRuleFamily(suggestion *specs.ResourceSuggestion, resolver *openapi.Resolver, routes []router.Route, variants map[int64][]gen.ResponseVariant) (*confirmPrep, error) {
	detail, _, post, err := locateFamilyOperations(routes, suggestion.RouteFamily)
	if err != nil {
		return nil, err
	}
	scope := outerParamNames(detail.Path)
	scopeJSON, err := jsonx.Marshal(scope)
	if err != nil {
		return nil, err
	}
	wrapperJSON, err := jsonx.Marshal(suggestion.Wrapper)
	if err != nil {
		return nil, err
	}
	prep := &confirmPrep{sugg: suggestion, scopeParams: scope, scopeParamsJSON: string(scopeJSON), wrapperJSON: string(wrapperJSON)}
	if post != nil {
		prep.writeForm = computeWriteForm(resolver, variants[post.OpRowID], suggestion.EntitySchema)
	}
	return prep, nil
}
