package designscenario

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
)

const MaxTransferBytes = 2_000_000
const MaxTransferScenarios = 20
const MaxTransferRevisions = 200

type TransferRevision struct {
	Document   Document          `json:"document"`
	FormDrafts map[string]string `json:"formDrafts"`
	Summary    string            `json:"summary"`
	Source     string            `json:"source"`
	CreatedAt  int64             `json:"createdAt"`
}
type TransferScenario struct {
	Revisions []TransferRevision `json:"revisions"`
}
type TransferBundle struct {
	Kind          string             `json:"kind"`
	FormatVersion int                `json:"formatVersion"`
	Scenarios     []TransferScenario `json:"scenarios"`
}
type TransferResult struct {
	Scenarios       []Scenario `json:"scenarios"`
	LinkedContracts int        `json:"linkedContracts"`
	CopiedContracts int        `json:"copiedContracts"`
}

func transferSize(bundle TransferBundle) error {
	raw, err := jsonx.Marshal(bundle)
	if err != nil {
		return err
	}
	if len(raw) > MaxTransferBytes {
		return ErrTooLarge
	}
	return nil
}

func portableTransferDocument(document Document) (Document, error) {
	cloned, err := cloneDocument(document)
	if err != nil {
		return Document{}, err
	}
	for i := range cloned.Contracts {
		cloned.Contracts[i].Mode = "copy"
		cloned.Contracts[i].Source = nil
	}
	return cloned, nil
}

// ExportTransfer captures all selected histories from one database snapshot.
func (r *Repo) ExportTransfer(ctx context.Context, ids []int64, history bool) (TransferBundle, error) {
	bundle := TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []TransferScenario{}}
	if len(ids) == 0 || len(ids) > MaxTransferScenarios {
		return bundle, ErrInvalid
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return bundle, ErrInvalid
		}
		seen[id] = true
	}
	var size int64
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			scenario, err := getScenario(ctx, tx, id)
			if err != nil {
				return err
			}
			query := "SELECT " + revisionColumns + " FROM design_scenario_revisions WHERE scenario_id=?"
			args := []any{id}
			if !history {
				query += " AND id=?"
				args = append(args, scenario.DraftRevisionID)
			}
			query += " ORDER BY version ASC LIMIT 201"
			rows, err := tx.QueryContext(ctx, query, args...)
			if err != nil {
				return err
			}
			item := TransferScenario{Revisions: []TransferRevision{}}
			err = func() error {
				defer func() { _ = rows.Close() }()
				for rows.Next() {
					rev, err := scanRevision(rows)
					if err != nil {
						return err
					}
					doc, err := portableTransferDocument(rev.Document)
					if err != nil {
						return err
					}
					item.Revisions = append(item.Revisions, TransferRevision{Document: doc, FormDrafts: rev.FormDrafts, Summary: rev.Summary, Source: rev.Source, CreatedAt: rev.CreatedAt})
					if len(item.Revisions) > MaxTransferRevisions {
						return ErrTooLarge
					}
					// Check incrementally, before reading the next potentially
					// large snapshot. The bundle's encoding contains every
					// revision's encoding, so a running sum of them is a lower
					// bound: past the cap here means past it in the bundle, and
					// the exact check runs once at the end. Review 2026-10-06,
					// F182: this used to copy and marshal the whole accumulated
					// bundle after every revision, quadratic in the revisions
					// (up to 4000 marshals of a growing ~2 MB bundle).
					raw, err := jsonx.Marshal(item.Revisions[len(item.Revisions)-1])
					if err != nil {
						return err
					}
					if size += int64(len(raw)); size > MaxTransferBytes {
						return ErrTooLarge
					}
				}
				return rows.Err()
			}()
			if err != nil {
				return err
			}
			bundle.Scenarios = append(bundle.Scenarios, item)
		}
		return transferSize(bundle)
	})
	return bundle, err
}

// API matching ignores foreign IDs and only accepts one distinct local design.
// Multiple equal revisions of the same design pin the newest matching revision.
//
// The scan covers revisions with id > after and extends index, so a caller can
// index the catalog in a read transaction and then, under the writer, scan
// only the revisions committed since. Review 2026-10-06, F181: the whole
// catalog (one RevisionTx hydration, decode and canonical marshal per row)
// was scanned inside the single db.Write of ImportTransfer. That split is
// exact because API revisions are immutable and never deleted, ids are
// AUTOINCREMENT assigned under the single writer (so a revision a read
// snapshot missed has a higher id than every one it saw), and within one
// design version order is id order, so the newest-revision rule holds.
func (r *Repo) transferAPIIndex(ctx context.Context, tx *sql.Tx, needed map[string]bool, after int64, index map[string]*ContractSource) (map[string]*ContractSource, int64, error) {
	if index == nil {
		index = map[string]*ContractSource{}
	}
	if len(needed) == 0 {
		return index, after, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT design_id,id FROM api_design_revisions WHERE id>? ORDER BY version ASC", after)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	last := after
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		var source ContractSource
		if err := rows.Scan(&source.DesignID, &source.RevisionID); err != nil {
			return nil, 0, err
		}
		last = max(last, source.RevisionID)
		// Public revisions hydrate operation keys missing in legacy storage.
		revision, err := r.designs.RevisionTx(ctx, tx, source.DesignID, source.RevisionID)
		if err != nil {
			return nil, 0, err
		}
		source.Version = revision.Version
		key, err := transferJSONKey([]byte(revision.Document))
		if err != nil {
			return nil, 0, err
		}
		if !needed[key] {
			continue
		}
		previous, found := index[key]
		if found && (previous == nil || previous.DesignID != source.DesignID) {
			index[key] = nil
			continue
		}
		index[key] = &source
	}
	return index, last, rows.Err()
}
func transferJSONKey(raw []byte) (string, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return "", err
	}
	encoded, err := jsonx.Marshal(canonicalTransferNumbers(value))
	return string(encoded), err
}

// Keep number values distinct from strings and preserve exact decimal precision.
func canonicalTransferNumbers(value any) any {
	switch v := value.(type) {
	case jsonx.Number:
		return jsonx.CanonicalNumber(v)
	case map[string]any:
		for key, child := range v {
			v[key] = canonicalTransferNumbers(child)
		}
	case []any:
		for i, child := range v {
			v[i] = canonicalTransferNumbers(child)
		}
	}
	return value
}

// ImportTransfer is all-or-nothing, including history and optional link restoration.
// It never writes APIs and never uses source-server row IDs.
func (r *Repo) ImportTransfer(ctx context.Context, bundle TransferBundle, relink bool) (TransferResult, error) {
	result := TransferResult{Scenarios: []Scenario{}}
	if bundle.Kind != "mocker.scenarios" || bundle.FormatVersion != 1 || len(bundle.Scenarios) == 0 || len(bundle.Scenarios) > MaxTransferScenarios {
		return result, ErrInvalid
	}
	if err := transferSize(bundle); err != nil {
		return result, err
	}
	var matches map[string]*ContractSource
	var needed map[string]bool
	var indexed int64
	if relink {
		// Retain only keys from the bounded package, not the entire API catalog.
		needed = map[string]bool{}
		for _, item := range bundle.Scenarios {
			for _, revision := range item.Revisions {
				for _, contract := range revision.Document.Contracts {
					key, err := transferJSONKey(contract.Document)
					if err != nil {
						return result, err
					}
					needed[key] = true
				}
			}
		}
		// The catalog scan runs on a reader; the writer below only catches
		// up on revisions committed since (review 2026-10-06, F181).
		err := r.db.Read(ctx, func(tx *sql.Tx) error {
			var err error
			matches, indexed, err = r.transferAPIIndex(ctx, tx, needed, 0, nil)
			return err
		})
		if err != nil {
			return result, err
		}
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		if relink {
			var err error
			matches, _, err = r.transferAPIIndex(ctx, tx, needed, indexed, maps.Clone(matches))
			if err != nil {
				return err
			}
		}
		for _, item := range bundle.Scenarios {
			if len(item.Revisions) == 0 || len(item.Revisions) > MaxTransferRevisions {
				return ErrInvalid
			}
			now := time.Now().Unix()
			row, err := tx.ExecContext(ctx, "INSERT INTO design_scenarios(name,created_at,updated_at) VALUES (?,?,?)", item.Revisions[len(item.Revisions)-1].Document.Title, now, now)
			if err != nil {
				return err
			}
			id, err := row.LastInsertId()
			if err != nil {
				return err
			}
			var parent *int64
			for i, rev := range item.Revisions {
				if err := checkSource(rev.Source); err != nil {
					return err
				}
				if rev.CreatedAt < 0 {
					return ErrInvalid
				}
				document, err := portableTransferDocument(rev.Document)
				if err != nil {
					return err
				}
				linkedDesigns := map[int64]bool{}
				for j := range document.Contracts {
					contract := &document.Contracts[j]
					key, err := transferJSONKey(contract.Document)
					if err != nil {
						return err
					}
					if source := matches[key]; source != nil && !linkedDesigns[source.DesignID] {
						// Match by exact numeric value, then pin the original target
						// snapshot so existing lexical pin checks remain unchanged.
						revision, err := r.designs.RevisionTx(ctx, tx, source.DesignID, source.RevisionID)
						if err != nil {
							return err
						}
						contract.Document = jsonx.RawMessage(revision.Document)
						linkedDesigns[source.DesignID] = true
						detached := *source
						contract.Mode = "linked"
						contract.Source = &detached
						result.LinkedContracts++
					} else {
						result.CopiedContracts++
					}
				}
				if err := r.pinLinkedContracts(ctx, tx, &document); err != nil {
					return err
				}
				prepared, _, err := r.prepare(document, rev.FormDrafts)
				if err != nil {
					return fmt.Errorf("import scenario %d revision %d: %w", len(result.Scenarios)+1, i+1, err)
				}
				createdAt := rev.CreatedAt
				if createdAt == 0 {
					createdAt = now
				}
				rid, err := insertRevision(ctx, tx, id, int64(i+1), parent, prepared, rev.Source, rev.Summary, createdAt)
				if err != nil {
					return err
				}
				parent = &rid
			}
			if _, err := tx.ExecContext(ctx, "UPDATE design_scenarios SET version=?,draft_revision_id=? WHERE id=?", len(item.Revisions), *parent, id); err != nil {
				return err
			}
			scenario, err := getScenario(ctx, tx, id)
			if err != nil {
				return err
			}
			result.Scenarios = append(result.Scenarios, scenario)
		}
		return nil
	})
	if err != nil {
		return TransferResult{}, err
	}
	return result, nil
}
