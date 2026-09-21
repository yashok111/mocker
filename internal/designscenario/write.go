package designscenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (r *Repo) Create(ctx context.Context, input CreateInput) (*Detail, error) {
	if err := checkSource(input.Source); err != nil {
		return nil, err
	}
	document, err := cloneDocument(input.Document)
	if err != nil {
		return nil, err
	}
	var result *Detail
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if err := r.pinLinkedContracts(ctx, tx, &document); err != nil {
			return err
		}
		prepared, _, err := r.prepare(document, input.FormDrafts)
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		row, err := tx.ExecContext(ctx, `INSERT INTO design_scenarios(name,created_at,updated_at) VALUES (?,?,?)`, document.Title, now, now)
		if err != nil {
			return err
		}
		scenarioID, err := row.LastInsertId()
		if err != nil {
			return err
		}
		summary := revisionDescription(nil, document, input.FormDrafts, input.Summary)
		revisionID, err := insertRevision(ctx, tx, scenarioID, 1, nil, prepared, input.Source, summary, now)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE design_scenarios SET draft_revision_id=? WHERE id=?", revisionID, scenarioID); err != nil {
			return err
		}
		result, err = r.detailTx(ctx, tx, scenarioID)
		return err
	})
	return result, err
}

func insertRevision(ctx context.Context, tx *sql.Tx, scenarioID, version int64, parentID *int64, prepared preparedDocument, source, summary string, now int64) (int64, error) {
	row, err := tx.ExecContext(ctx, `INSERT INTO design_scenario_revisions(scenario_id,version,parent_id,hash,document,form_drafts,source,summary,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, scenarioID, version, parentID, prepared.hash, prepared.document, prepared.formDrafts, source, summary, now)
	if err != nil {
		return 0, err
	}
	return row.LastInsertId()
}

func (r *Repo) Save(ctx context.Context, id int64, input SaveInput) (*Detail, error) {
	if err := checkSource(input.Source); err != nil {
		return nil, err
	}
	document, err := cloneDocument(input.Document)
	if err != nil {
		return nil, err
	}
	var result *Detail
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = r.saveTx(ctx, tx, id, input.ExpectedVersion, document, input.FormDrafts, input.Summary, input.Source)
		return err
	})
	return result, err
}

func (r *Repo) saveTx(ctx context.Context, tx *sql.Tx, id, expectedVersion int64, document Document, formDrafts map[string]string, summary, source string) (*Detail, error) {
	scenario, err := getScenario(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = checkVersion(scenario, expectedVersion); err != nil {
		return nil, err
	}
	current, err := getRevision(ctx, tx, id, scenario.DraftRevisionID)
	if err != nil {
		return nil, err
	}
	if err = r.saveLinkedContracts(ctx, tx, &document, source); err != nil {
		return nil, err
	}
	prepared, _, err := r.prepare(document, formDrafts)
	if err != nil {
		return nil, err
	}
	if current.Hash == prepared.hash {
		return r.detailTx(ctx, tx, id)
	}
	now := time.Now().Unix()
	summary = revisionDescription(&current, document, formDrafts, summary)
	revisionID, err := insertRevision(ctx, tx, id, scenario.Version+1, &scenario.DraftRevisionID, prepared, source, summary, now)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE design_scenarios SET name=?,version=version+1,draft_revision_id=?,updated_at=? WHERE id=? AND version=?`, document.Title, revisionID, now, id, expectedVersion)
	if err != nil {
		return nil, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		latest, readErr := getScenario(ctx, tx, id)
		if readErr != nil {
			return nil, readErr
		}
		return nil, &ConflictError{Version: latest.Version, DraftRevisionID: latest.DraftRevisionID}
	}
	return r.detailTx(ctx, tx, id)
}

func (r *Repo) Restore(ctx context.Context, id int64, input RestoreInput) (*Detail, error) {
	if err := checkSource(input.Source); err != nil {
		return nil, err
	}
	var result *Detail
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		scenario, err := getScenario(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkVersion(scenario, input.ExpectedVersion); err != nil {
			return err
		}
		revision, err := getRevision(ctx, tx, id, input.RevisionID)
		if err != nil {
			return err
		}
		prepared, _, err := r.prepare(revision.Document, revision.FormDrafts)
		if err != nil {
			return err
		}
		now := time.Now().Unix()
		summary := input.Summary
		if strings.TrimSpace(summary) == "" {
			summary = fmt.Sprintf("Восстановлена версия %d", revision.Version)
		}
		newRevisionID, err := insertRevision(ctx, tx, id, scenario.Version+1, &scenario.DraftRevisionID, prepared, input.Source, summary, now)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE design_scenarios SET name=?,version=version+1,draft_revision_id=?,updated_at=? WHERE id=?`, revision.Document.Title, newRevisionID, now, id); err != nil {
			return err
		}
		result, err = r.detailTx(ctx, tx, id)
		return err
	})
	return result, err
}

func (r *Repo) pinLinkedContracts(ctx context.Context, tx *sql.Tx, document *Document) error {
	for index := range document.Contracts {
		contract := &document.Contracts[index]
		if contract.Mode != "linked" {
			continue
		}
		if contract.Source == nil {
			return invalidAt(fmt.Sprintf("/contracts/%d/source", index), "linked contract requires a source")
		}
		revision, err := r.designs.RevisionTx(ctx, tx, contract.Source.DesignID, contract.Source.RevisionID)
		if errors.Is(err, apidesign.ErrNotFound) {
			return invalidAt(fmt.Sprintf("/contracts/%d/source/revisionId", index), "API revision does not exist")
		}
		if err != nil {
			return err
		}
		if revision.Version != contract.Source.Version {
			return invalidAt(fmt.Sprintf("/contracts/%d/source/version", index), "version does not match the pinned API revision")
		}
		equal, err := equalJSON(contract.Document, []byte(revision.Document))
		if err != nil {
			return invalidAt(fmt.Sprintf("/contracts/%d/document", index), "API document must be valid JSON")
		}
		if !equal {
			return invalidAt(fmt.Sprintf("/contracts/%d/document", index), "linked snapshot must match its pinned API revision")
		}
		contract.Document = jsonx.RawMessage(revision.Document)
	}
	return nil
}

func (r *Repo) saveLinkedContracts(ctx context.Context, tx *sql.Tx, document *Document, source string) error {
	for index := range document.Contracts {
		contract := &document.Contracts[index]
		if contract.Mode != "linked" {
			continue
		}
		if contract.Source == nil {
			return invalidAt(fmt.Sprintf("/contracts/%d/source", index), "linked contract requires a source")
		}
		pinned, err := r.designs.RevisionTx(ctx, tx, contract.Source.DesignID, contract.Source.RevisionID)
		if errors.Is(err, apidesign.ErrNotFound) {
			return invalidAt(fmt.Sprintf("/contracts/%d/source/revisionId", index), "API revision does not exist")
		}
		if err != nil {
			return err
		}
		if pinned.Version != contract.Source.Version {
			return invalidAt(fmt.Sprintf("/contracts/%d/source/version", index), "version does not match the pinned API revision")
		}
		equal, err := equalJSON(contract.Document, []byte(pinned.Document))
		if err != nil {
			return invalidAt(fmt.Sprintf("/contracts/%d/document", index), "API document must be valid JSON")
		}
		if equal {
			contract.Document = jsonx.RawMessage(pinned.Document)
			continue
		}
		detail, err := r.designs.SaveTx(ctx, tx, contract.Source.DesignID, apidesign.SaveInput{
			ExpectedVersion: contract.Source.Version,
			Document:        string(contract.Document),
			Summary:         "Изменение из сценария " + document.Title,
			Source:          source,
		})
		if err != nil {
			if conflict, ok := errors.AsType[*apidesign.ConflictError](err); ok {
				return &LinkedConflictError{ContractID: contract.ID, DesignID: contract.Source.DesignID, Version: conflict.Version, DraftRevisionID: conflict.DraftRevisionID}
			}
			if invalid, ok := errors.AsType[*apidesign.InvalidError](err); ok {
				diagnostics := make([]Diagnostic, 0, len(invalid.Diagnostics))
				for _, diagnostic := range invalid.Diagnostics {
					diagnostics = append(diagnostics, Diagnostic{Pointer: fmt.Sprintf("/contracts/%d/document", index) + diagnostic.Pointer, Message: diagnostic.Message, Severity: diagnostic.Severity})
				}
				return &InvalidError{Diagnostics: diagnostics}
			}
			return err
		}
		contract.Source = &ContractSource{DesignID: detail.Design.ID, RevisionID: detail.Draft.ID, Version: detail.Draft.Version}
		contract.Document = jsonx.RawMessage(detail.Draft.Document)
	}
	return nil
}

func equalJSON(left, right []byte) (bool, error) {
	leftValue, err := decodeJSONValue(left)
	if err != nil {
		return false, err
	}
	rightValue, err := decodeJSONValue(right)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}

func invalidAt(pointer, message string) error {
	return &InvalidError{Diagnostics: []Diagnostic{{Pointer: pointer, Message: message, Severity: "error"}}}
}
