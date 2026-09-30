package apidesign

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
)

func (r *Repo) projectRuleEntitiesTx(ctx context.Context, tx *sql.Tx, workspaceID, specID int64, document string) error {
	root, err := decodeDocument(document)
	if err != nil {
		return err
	}
	execution, err := responserules.DecodeExecution(root)
	if err != nil {
		return responseRuleError(err)
	}
	families := []string{}
	for _, rule := range execution.Rules {
		for _, node := range rule.Nodes {
			if node.Entity == nil {
				continue
			}
			for family := node.Entity.Family; family != ""; family = router.ParentFamily(family) {
				if !slices.Contains(families, family) {
					families = append(families, family)
				}
			}
		}
	}
	err = resources.EnsureRuleFamiliesTx(ctx, tx, r.specs, workspaceID, specID, []byte(document), families)
	if field, ok := errors.AsType[*resources.RuleFamilyError](err); ok {
		return invalidField("/"+responserules.ExecutionExtension, field.Error())
	}
	return err
}
