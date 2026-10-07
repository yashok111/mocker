package apidesign

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/statediagram"
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
	states, err := statediagram.DecodeExecution(root)
	if err != nil {
		return stateDiagramExecutionError(err)
	}
	err = resources.EnsureRuleFamiliesTx(ctx, tx, r.specs, workspaceID, specID, []byte(document), ruleEntityFamilies(execution, states))
	if field, ok := errors.AsType[*resources.RuleFamilyError](err); ok {
		return ruleFamilyFieldError(states, field)
	}
	if err != nil {
		return err
	}
	return checkStateFieldsTx(ctx, tx, workspaceID, states)
}

// ruleEntityFamilies lists every family an executable rule or state diagram
// addresses, ancestors included, in first-use order.
func ruleEntityFamilies(execution responserules.Envelope, states statediagram.Envelope) []string {
	families := []string{}
	addFamily := func(start string) {
		for family := start; family != ""; family = router.ParentFamily(family) {
			if !slices.Contains(families, family) {
				families = append(families, family)
			}
		}
	}
	for _, rule := range execution.Rules {
		for _, node := range rule.Nodes {
			if node.Entity != nil {
				addFamily(node.Entity.Family)
			}
		}
	}
	for _, diagram := range states.Diagrams {
		if diagram.Entity != nil { // The execution compiler reports a missing binding.
			addFamily(diagram.Entity.Family)
		}
	}
	return families
}

// ruleFamilyFieldError points a rejected family at the first state diagram
// that reaches it, else at the response-rule execution copy.
func ruleFamilyFieldError(states statediagram.Envelope, field *resources.RuleFamilyError) error {
	for i, diagram := range states.Diagrams {
		if diagram.Entity == nil {
			continue
		}
		for family := diagram.Entity.Family; family != ""; family = router.ParentFamily(family) {
			if field.Family == family {
				return invalidField(fmt.Sprintf("/%s/diagrams/%d/entity/family", statediagram.ExecutionExtension, i), field.Error())
			}
		}
	}
	return invalidField("/"+responserules.ExecutionExtension, field.Error())
}

func checkStateFieldsTx(ctx context.Context, tx *sql.Tx, workspaceID int64, states statediagram.Envelope) error {
	for i, diagram := range states.Diagrams {
		if diagram.Entity == nil {
			continue
		}
		err := resources.CheckStateFieldTx(ctx, tx, workspaceID, diagram.Entity.Family, diagram.Entity.StateField)
		if field, ok := errors.AsType[*resources.RuleFamilyError](err); ok {
			return invalidField(fmt.Sprintf("/%s/diagrams/%d/entity/stateField", statediagram.ExecutionExtension, i), field.Error())
		}
		if err != nil {
			return err
		}
	}
	return nil
}
