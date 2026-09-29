package apidesign

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

type ResponseRules struct {
	DesignID   int64                `json:"designId"`
	Version    int64                `json:"version"`
	RevisionID int64                `json:"revisionId"`
	Rules      []responserules.Rule `json:"rules"`
}
type ResponseRuleProposal struct {
	Document *string `json:"document,omitempty"`
}
type ResponseRuleSource struct {
	DesignID     int64  `json:"designId"`
	RuleID       string `json:"ruleId"`
	Kind         string `json:"kind"`
	DocumentHash string `json:"documentHash"`
	Version      *int64 `json:"version,omitempty"`
	RevisionID   *int64 `json:"revisionId,omitempty"`
}
type ResolvedResponseRule struct {
	Envelope responserules.Envelope
	Root     map[string]any
	Source   ResponseRuleSource
}

// responseRuleSnapshot loads the exact stored document and matching version in
// one snapshot. It intentionally avoids read-time operation-key enrichment.
func (r *Repo) responseRuleSnapshot(ctx context.Context, id int64) (Design, Revision, error) {
	var design Design
	var revision Revision
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		design, err = getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		revision, err = scanRevision(tx.QueryRowContext(ctx, "SELECT "+revisionColumns+" FROM api_design_revisions WHERE design_id=? AND id=?", id, design.DraftRevisionID))
		return err
	})
	return design, revision, err
}

func (r *Repo) decodeResponseRules(ctx context.Context, document string) (map[string]any, responserules.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, responserules.Envelope{}, err
	}
	if int64(len(document)) > r.cfg.MaxBody {
		return nil, responserules.Envelope{}, invalidField("/document", "Документ слишком большой")
	}
	root, err := decodeDocument(document)
	if err != nil {
		return nil, responserules.Envelope{}, invalidField("/document", err.Error())
	}
	if err = ctx.Err(); err != nil {
		return nil, responserules.Envelope{}, err
	}
	env, err := responserules.Decode(root)
	if err != nil {
		return nil, responserules.Envelope{}, responseRuleError(err)
	}
	if err = ctx.Err(); err != nil {
		return nil, responserules.Envelope{}, err
	}
	return root, env, nil
}

func (r *Repo) ResponseRules(ctx context.Context, id int64) (ResponseRules, error) {
	design, draft, err := r.responseRuleSnapshot(ctx, id)
	if err != nil {
		return ResponseRules{}, err
	}
	_, env, err := r.decodeResponseRules(ctx, draft.Document)
	if err != nil {
		return ResponseRules{}, err
	}
	return ResponseRules{DesignID: id, Version: design.Version, RevisionID: draft.ID, Rules: env.Rules}, nil
}

// EditResponseRule changes only the extension. Save fences the original whole
// API version again, so a concurrent unrelated edit cannot be overwritten.
func (r *Repo) EditResponseRule(ctx context.Context, id, version int64, actor, ruleID, action string, rule *responserules.Rule, commands []responserules.Command) (*Detail, error) {
	if version <= 0 {
		return nil, invalidField("/expectedVersion", "Обязательна положительная версия API")
	}
	design, draft, err := r.responseRuleSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = checkVersion(design, version); err != nil {
		return nil, err
	}
	root, env, err := r.decodeResponseRules(ctx, draft.Document)
	if err != nil {
		return nil, err
	}
	if !responserules.ValidID(ruleID) {
		return nil, invalidField("/rule/id", "Некорректный ID правила")
	}
	if (action == "create" || action == "save") && (rule == nil || rule.ID != ruleID) {
		return nil, invalidField("/rule/id", "Нужно правило с указанным id")
	}
	index := slices.IndexFunc(env.Rules, func(v responserules.Rule) bool { return v.ID == ruleID })
	if action != "create" && index < 0 {
		return nil, ErrNotFound
	}
	switch action {
	case "create":
		if index >= 0 {
			return nil, invalidField("/rule/id", "Правило с таким id уже существует")
		}
		env.Rules = append(env.Rules, *rule)
	case "save":
		env.Rules[index] = *rule
	case "delete":
		env.Rules = slices.Delete(env.Rules, index, index+1)
	case "commands":
		env.Rules[index], err = responserules.ApplyCommands(env.Rules[index], commands)
		if err != nil {
			return nil, responseRuleError(err)
		}
	default:
		return nil, invalidField("/action", "Неизвестное действие")
	}
	root[responserules.Extension] = env
	if _, err = responserules.Decode(root); err != nil {
		return nil, responseRuleError(err)
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		return nil, err
	}
	return r.Save(ctx, id, SaveInput{ExpectedVersion: version, Document: string(raw), Source: actor, Summary: "Response rule: " + action + " " + ruleID})
}

func (r *Repo) ResolveResponseRule(ctx context.Context, id int64, ruleID string, proposal ResponseRuleProposal) (ResolvedResponseRule, error) {
	design, draft, err := r.responseRuleSnapshot(ctx, id)
	if err != nil {
		return ResolvedResponseRule{}, err
	}
	raw := draft.Document
	source := ResponseRuleSource{DesignID: id, RuleID: ruleID, Kind: "saved", Version: &design.Version, RevisionID: &draft.ID}
	if proposal.Document != nil {
		raw = *proposal.Document
		source.Kind = "proposal"
		source.Version = nil
		source.RevisionID = nil
	}
	root, env, err := r.decodeResponseRules(ctx, raw)
	if err != nil {
		return ResolvedResponseRule{}, err
	}
	if !slices.ContainsFunc(env.Rules, func(rule responserules.Rule) bool { return rule.ID == ruleID }) {
		return ResolvedResponseRule{}, ErrNotFound
	}
	source.DocumentHash = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(raw)))
	return ResolvedResponseRule{Envelope: env, Root: root, Source: source}, nil
}

// responseRuleError keeps the domain's exact JSON pointer for API diagnostics.
func responseRuleError(err error) error {
	if field, ok := errors.AsType[*responserules.FieldError](err); ok {
		return invalidField(field.Pointer, field.Message)
	}
	return invalidField("/"+responserules.Extension, err.Error())
}
