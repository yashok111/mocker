package designscenario

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

const designUsageBytesLimit int64 = 64 * 1024 * 1024

type designUsageDraft struct {
	ScenarioID   int64
	ScenarioName string
	RevisionID   int64
	Document     Document
}

type designUsageReadResult struct {
	Scanned          int
	TruncatedReasons []string
}

type designUsageDraftMeta struct {
	ScenarioID   int64
	ScenarioName string
	RevisionID   int64
	Bytes        int64
}

// readDesignUsageDrafts holds only bounded metadata and one decoded document.
// Size checks and document reads share a transaction, so a concurrent draft
// update cannot bypass the byte budget or mix a revision ID with another body.
func (r *Repo) readDesignUsageDrafts(
	ctx context.Context,
	visit func(designUsageDraft) (bool, error),
) (designUsageReadResult, error) {
	result := designUsageReadResult{TruncatedReasons: []string{}}
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		metadata, err := readDesignUsageMetadata(ctx, tx)
		if err != nil {
			return err
		}
		var bytesRead int64
		for index, meta := range metadata {
			if err := ctx.Err(); err != nil {
				return err
			}
			if index >= resourceMapScenarioScanLimit {
				result.TruncatedReasons = append(result.TruncatedReasons, "scenario_scan")
				break
			}
			if meta.Bytes > designUsageBytesLimit-bytesRead {
				result.TruncatedReasons = append(result.TruncatedReasons, "scenario_bytes")
				break
			}
			var raw string
			if err := tx.QueryRowContext(
				ctx,
				`SELECT document FROM design_scenario_revisions WHERE id=? AND scenario_id=?`,
				meta.RevisionID,
				meta.ScenarioID,
			).Scan(&raw); err != nil {
				return err
			}
			bytesRead += meta.Bytes
			draft := designUsageDraft{
				ScenarioID: meta.ScenarioID, ScenarioName: meta.ScenarioName, RevisionID: meta.RevisionID,
			}
			if err := jsonx.Unmarshal([]byte(raw), &draft.Document); err != nil {
				return fmt.Errorf(
					"decode scenario %d revision %d: %w",
					meta.ScenarioID,
					meta.RevisionID,
					err,
				)
			}
			result.Scanned++
			stop, err := visit(draft)
			if err != nil {
				return err
			}
			if stop {
				break
			}
		}
		return ctx.Err()
	})
	return result, err
}

func readDesignUsageMetadata(ctx context.Context, tx *sql.Tx) ([]designUsageDraftMeta, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.name,s.draft_revision_id,length(CAST(v.document AS BLOB))
		FROM design_scenarios s JOIN design_scenario_revisions v
			ON v.scenario_id=s.id AND v.id=s.draft_revision_id
		ORDER BY s.id LIMIT ?`, resourceMapScenarioScanLimit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	metadata := []designUsageDraftMeta{}
	for rows.Next() {
		var meta designUsageDraftMeta
		if err := rows.Scan(&meta.ScenarioID, &meta.ScenarioName, &meta.RevisionID, &meta.Bytes); err != nil {
			return nil, err
		}
		metadata = append(metadata, meta)
	}
	return metadata, rows.Err()
}
