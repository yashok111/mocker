package backendmodel

import (
	"context"
	"time"
)

// Import catalogs intentionally omit manifests. A status refresh must not copy
// every historical source file list into an otherwise read-only workspace.
type ImportStatusSummary struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"projectId"`
	BaseRevisionID string    `json:"baseRevisionId"`
	RepositoryName string    `json:"repositoryName"`
	State          string    `json:"state"`
	Version        int64     `json:"version"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
type ImportStatusSummaryPage struct {
	Items      []ImportStatusSummary `json:"items"`
	NextCursor string                `json:"nextCursor"`
}

func (r *Repo) ImportSummaries(ctx context.Context, pid string, in ListInput) (*ImportStatusSummaryPage, error) {
	if _, err := r.Get(ctx, pid); err != nil {
		return nil, err
	}
	if in.Limit == 0 {
		in.Limit = DefaultPageSize
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "import-summaries", pid, "newest", true)
	if err != nil {
		return nil, err
	}
	if limit > 100 {
		return nil, invalid("limit", "Use at most 100 import summaries")
	}
	if after == "" {
		after = "~"
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT id,json_extract(document,'$.baseRevisionId'),json_extract(document,'$.manifest.repositoryName'),json_extract(document,'$.state'),json_extract(document,'$.version'),json_extract(document,'$.updatedAt') FROM backend_import_sessions WHERE project_id=? AND id<? ORDER BY id DESC LIMIT ?`, pid, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &ImportStatusSummaryPage{Items: []ImportStatusSummary{}}
	for rows.Next() {
		var item ImportStatusSummary
		var updated string
		if err = rows.Scan(&item.ID, &item.BaseRevisionID, &item.RepositoryName, &item.State, &item.Version, &updated); err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeGraphPage("import-summaries", pid, "newest", out.Items[len(out.Items)-1].ID)
			break
		}
		item.ProjectID = pid
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
