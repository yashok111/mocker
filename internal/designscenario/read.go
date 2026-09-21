package designscenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (r *Repo) List(ctx context.Context) ([]Scenario, error) {
	result := []Scenario{}
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+scenarioColumns+" FROM design_scenarios ORDER BY id DESC")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			scenario, err := scanScenario(rows)
			if err != nil {
				return err
			}
			result = append(result, scenario)
		}
		return rows.Err()
	})
	return result, err
}

// Detail reads the mutable pointer and revision history from one SQLite snapshot.
func (r *Repo) Detail(ctx context.Context, id int64) (*Detail, error) {
	var result *Detail
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = r.detailTx(ctx, tx, id)
		return err
	})
	return result, err
}

func (r *Repo) detailTx(ctx context.Context, tx *sql.Tx, id int64) (*Detail, error) {
	scenario, err := getScenario(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	draft, err := getRevision(ctx, tx, id, scenario.DraftRevisionID)
	if err != nil {
		return nil, err
	}
	result := &Detail{
		Scenario:        scenario,
		Draft:           draft,
		Revisions:       []RevisionSummary{},
		Diagnostics:     validateDocument(draft.Document),
		ContractUpdates: []ContractUpdate{},
	}
	result.Revisions, err = readRevisionHistory(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	for _, revision := range result.Revisions {
		if revision.ID == draft.ID {
			result.Draft.Summary = revision.Summary
		}
	}
	slices.Reverse(result.Revisions)
	for index, contract := range draft.Document.Contracts {
		if contract.Mode != "linked" || contract.Source == nil {
			continue
		}
		design, err := r.designs.DetailTx(ctx, tx, contract.Source.DesignID)
		if errors.Is(err, apidesign.ErrNotFound) {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Pointer:  fmt.Sprintf("/contracts/%d/source/designId", index),
				Message:  "linked API design no longer exists",
				Severity: "warning",
			})
			continue
		}
		if err != nil {
			return nil, err
		}
		if design.Design.DraftRevisionID != contract.Source.RevisionID {
			result.ContractUpdates = append(result.ContractUpdates, ContractUpdate{
				ContractID: contract.ID,
				DesignID:   design.Design.ID,
				RevisionID: design.Design.DraftRevisionID,
				Version:    design.Design.Version,
			})
		}
	}
	return result, nil
}

func readRevisionHistory(ctx context.Context, tx *sql.Tx, scenarioID int64) ([]RevisionSummary, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,scenario_id,version,hash,source,summary,created_at
		FROM design_scenario_revisions WHERE scenario_id=? ORDER BY version ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	history := []RevisionSummary{}
	if err := func() error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var revision RevisionSummary
			if err := rows.Scan(&revision.ID, &revision.ScenarioID, &revision.Version, &revision.Hash, &revision.Source, &revision.Summary, &revision.CreatedAt); err != nil {
				return err
			}
			history = append(history, revision)
		}
		return rows.Err()
	}(); err != nil {
		return nil, err
	}

	// Only legacy blank descriptions need documents. Include their immediate
	// predecessors, even when those predecessors already have stored summaries.
	needed := map[int64]int{}
	for index, revision := range history {
		if strings.TrimSpace(revision.Summary) != "" {
			continue
		}
		needed[revision.ID] = index
		if index > 0 {
			needed[history[index-1].ID] = index - 1
		}
	}
	if len(needed) == 0 {
		return history, nil
	}
	ids, err := jsonx.Marshal(slices.Sorted(maps.Keys(needed)))
	if err != nil {
		return nil, err
	}
	// One batch avoids both N+1 reads and SQLite's bound-parameter limit. Stream
	// the selected snapshots so long legacy histories retain only two documents.
	rows, err = tx.QueryContext(ctx, "SELECT "+revisionColumns+` FROM design_scenario_revisions
		WHERE scenario_id=? AND id IN (SELECT value FROM json_each(?)) ORDER BY version ASC`, scenarioID, string(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var previous *Revision
	for rows.Next() {
		revision, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		index := needed[revision.ID]
		history[index].Summary = revisionDescription(previous, revision.Document, revision.FormDrafts, revision.Summary)
		previous = &revision
	}
	return history, rows.Err()
}

func (r *Repo) Revision(ctx context.Context, scenarioID, revisionID int64) (Revision, error) {
	var result Revision
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = getRevision(ctx, tx, scenarioID, revisionID)
		if err != nil || strings.TrimSpace(result.Summary) != "" {
			return err
		}
		previous, err := scanRevision(tx.QueryRowContext(ctx,
			"SELECT "+revisionColumns+" FROM design_scenario_revisions WHERE scenario_id=? AND version<? ORDER BY version DESC LIMIT 1",
			scenarioID, result.Version,
		))
		if errors.Is(err, ErrNotFound) {
			result.Summary = revisionDescription(nil, result.Document, result.FormDrafts, "")
			return nil
		}
		if err != nil {
			return err
		}
		result.Summary = revisionDescription(&previous, result.Document, result.FormDrafts, "")
		return nil
	})
	return result, err
}

func (r *Repo) Diff(ctx context.Context, scenarioID, fromRevisionID, toRevisionID int64) (*Diff, error) {
	var result *Diff
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		if toRevisionID == 0 {
			scenario, err := getScenario(ctx, tx, scenarioID)
			if err != nil {
				return err
			}
			toRevisionID = scenario.DraftRevisionID
		}
		to, err := getRevision(ctx, tx, scenarioID, toRevisionID)
		if err != nil {
			return err
		}
		if fromRevisionID == 0 {
			err = tx.QueryRowContext(ctx,
				"SELECT id FROM design_scenario_revisions WHERE scenario_id=? AND version<? ORDER BY version DESC LIMIT 1",
				scenarioID, to.Version,
			).Scan(&fromRevisionID)
			if errors.Is(err, sql.ErrNoRows) {
				fromRevisionID = toRevisionID
			} else if err != nil {
				return err
			}
		}
		from, err := getRevision(ctx, tx, scenarioID, fromRevisionID)
		if err != nil {
			return err
		}
		fromValue, err := revisionValue(from)
		if err != nil {
			return err
		}
		toValue, err := revisionValue(to)
		if err != nil {
			return err
		}
		result = &Diff{FromRevisionID: from.ID, ToRevisionID: to.ID, Changes: []Change{}}
		diffValues("", fromValue, true, toValue, true, &result.Changes)
		return nil
	})
	return result, err
}

func revisionValue(revision Revision) (any, error) {
	raw, err := jsonx.Marshal(struct {
		Document   Document          `json:"document"`
		FormDrafts map[string]string `json:"formDrafts"`
	}{Document: revision.Document, FormDrafts: revision.FormDrafts})
	if err != nil {
		return nil, err
	}
	return decodeJSONValue(raw)
}

func diffValues(pointer string, before any, beforeOK bool, after any, afterOK bool, changes *[]Change) {
	if beforeOK && afterOK && reflect.DeepEqual(before, after) {
		return
	}
	beforeMap, beforeMapOK := before.(map[string]any)
	afterMap, afterMapOK := after.(map[string]any)
	if beforeOK && afterOK && beforeMapOK && afterMapOK {
		keys := map[string]struct{}{}
		for key := range beforeMap {
			keys[key] = struct{}{}
		}
		for key := range afterMap {
			keys[key] = struct{}{}
		}
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			beforeChild, beforeChildOK := beforeMap[key]
			afterChild, afterChildOK := afterMap[key]
			diffValues(pointer+"/"+escapePointer(key), beforeChild, beforeChildOK, afterChild, afterChildOK, changes)
		}
		return
	}
	change := Change{Pointer: pointer}
	if beforeOK {
		change.Before = rawValue(before)
	}
	if afterOK {
		change.After = rawValue(after)
	}
	*changes = append(*changes, change)
}

func rawValue(value any) *jsonx.RawMessage {
	raw, err := jsonx.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("designscenario: marshal already-decoded JSON value: %v", err))
	}
	message := jsonx.RawMessage(raw)
	return &message
}
