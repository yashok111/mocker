package apidesign

import (
	"context"
	"database/sql"
	"reflect"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

type ResponseRuleExecutionState struct {
	RuleID  string                `json:"ruleId"`
	Name    string                `json:"name"`
	Binding responserules.Binding `json:"binding"`
	State   string                `json:"state"`
}

type ResponseRuleExecution struct {
	DesignID   int64                        `json:"designId"`
	Version    int64                        `json:"version"`
	RevisionID int64                        `json:"revisionId"`
	Rules      []ResponseRuleExecutionState `json:"rules"`
}

// ResponseRuleExecution reads authoring and applied copies from one immutable
// revision. Equality includes labels and layout, so the status describes the
// exact copied rule rather than only its possible response behavior.
func (r *Repo) ResponseRuleExecution(ctx context.Context, id int64) (ResponseRuleExecution, error) {
	d, draft, err := r.responseRuleSnapshot(ctx, id)
	if err != nil {
		return ResponseRuleExecution{}, err
	}
	root, authoring, err := r.decodeResponseRules(ctx, draft.Document)
	if err != nil {
		return ResponseRuleExecution{}, err
	}
	execution, err := responserules.DecodeExecution(root)
	if err != nil {
		return ResponseRuleExecution{}, responseRuleError(err)
	}
	result := ResponseRuleExecution{DesignID: id, Version: d.Version, RevisionID: draft.ID, Rules: []ResponseRuleExecutionState{}}
	for _, copy := range execution.Rules {
		if copy.Binding == nil {
			return ResponseRuleExecution{}, invalidField("/"+responserules.ExecutionExtension, "У применённого правила отсутствует операция")
		}
		state := "missing"
		if i := slices.IndexFunc(authoring.Rules, func(rule responserules.Rule) bool { return rule.ID == copy.ID }); i >= 0 {
			state = "outdated"
			if reflect.DeepEqual(authoring.Rules[i], copy) {
				state = "current"
			}
		}
		result.Rules = append(result.Rules, ResponseRuleExecutionState{RuleID: copy.ID, Name: copy.Name, Binding: *copy.Binding, State: state})
	}
	return result, nil
}

// EditResponseRuleExecution keeps source selection, version fencing and draft
// projection in one writer transaction. SaveTx supplies the same API history and
// publication isolation as any source edit; no workspace guard is bypassed.
func (r *Repo) EditResponseRuleExecution(ctx context.Context, id, version int64, actor, ruleID string, apply bool) (*Detail, error) {
	if version <= 0 {
		return nil, invalidField("/expectedVersion", "Обязательна положительная версия API")
	}
	if !responserules.ValidID(ruleID) {
		return nil, invalidField("/ruleId", "Некорректный ID правила")
	}
	if err := checkSource(actor); err != nil {
		return nil, err
	}
	var result *Detail
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		d, err := getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkVersion(d, version); err != nil {
			return err
		}
		draft, err := getRevision(ctx, tx, id, d.DraftRevisionID)
		if err != nil {
			return err
		}
		root, authoring, err := r.decodeResponseRules(ctx, draft.Document)
		if err != nil {
			return err
		}
		execution, err := responserules.DecodeExecution(root)
		if err != nil {
			return responseRuleError(err)
		}
		i := slices.IndexFunc(execution.Rules, func(rule responserules.Rule) bool { return rule.ID == ruleID })
		summary := "Remove response rule: " + ruleID
		if apply {
			source := slices.IndexFunc(authoring.Rules, func(rule responserules.Rule) bool { return rule.ID == ruleID })
			if source < 0 {
				return ErrNotFound
			}
			validation, err := responserules.Validate(ctx, authoring, ruleID, root)
			if err != nil {
				return responseRuleError(err)
			}
			if !validation.Valid {
				diagnostics := make([]Diagnostic, 0, len(validation.Diagnostics))
				for _, diagnostic := range validation.Diagnostics {
					diagnostics = append(diagnostics, Diagnostic{Pointer: diagnostic.Pointer, Message: diagnostic.Message, Severity: diagnostic.Severity})
				}
				return &InvalidError{Diagnostics: diagnostics}
			}
			if i >= 0 && reflect.DeepEqual(execution.Rules[i], authoring.Rules[source]) {
				result, err = detailTx(ctx, tx, id)
				return err
			}
			if i < 0 {
				execution.Rules = append(execution.Rules, authoring.Rules[source])
			} else {
				execution.Rules[i] = authoring.Rules[source]
			}
			summary = "Apply response rule: " + ruleID
		} else {
			if i < 0 {
				result, err = detailTx(ctx, tx, id)
				return err
			}
			execution.Rules = slices.Delete(execution.Rules, i, i+1)
		}
		root[responserules.ExecutionExtension] = execution
		raw, err := jsonx.Marshal(root)
		if err != nil {
			return err
		}
		// Re-decoding in SaveTx isolates both slices and validates the complete
		// execution envelope before any revision or runtime is committed.
		result, err = r.SaveTx(ctx, tx, id, SaveInput{ExpectedVersion: version, Document: string(raw), Source: actor, Summary: summary})
		return err
	})
	return result, err
}
