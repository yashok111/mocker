package backendmodel

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
)

type pageCursor struct {
	Kind      string `json:"kind"`
	ProjectID string `json:"projectId"`
	After     string `json:"after"`
}

func decodePage(in ListInput, kind, projectID string) (int, string, error) {
	if in.Limit < 0 || in.Limit > MaxPageSize {
		return 0, "", invalid("limit", "limit must be between 1 and 100")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultPageSize
	}
	if in.Cursor == "" {
		return limit, "", nil
	}
	if len(in.Cursor) > 512 {
		return 0, "", invalid("cursor", "Invalid cursor")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(in.Cursor)
	if err != nil {
		return 0, "", invalid("cursor", "Invalid cursor")
	}
	var c pageCursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil || c.Kind != kind || c.ProjectID != projectID || !ValidID(c.After) {
		return 0, "", invalid("cursor", "Cursor does not match this resource")
	}
	return limit, c.After, nil
}

func encodePage(kind, projectID, after string) string {
	data, _ := json.Marshal(pageCursor{Kind: kind, ProjectID: projectID, After: after})
	return base64.RawURLEncoding.EncodeToString(data)
}

func (r *Repo) List(ctx context.Context, in ListInput) (*ProjectPage, error) {
	limit, after, err := decodePage(in, "projects", "")
	if err != nil {
		return nil, err
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id>? ORDER BY id LIMIT ?`, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &ProjectPage{Items: []Project{}}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			page.NextCursor = encodePage("projects", "", page.Items[limit-1].ID)
			break
		}
		page.Items = append(page.Items, *p)
	}
	return page, rows.Err()
}

func (r *Repo) Revisions(ctx context.Context, projectID string, in ListInput) (*RevisionPage, error) {
	if _, err := r.Get(ctx, projectID); err != nil {
		return nil, err
	}
	limit, after, err := decodePage(in, "revisions", projectID)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id>? ORDER BY id LIMIT ?`, projectID, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &RevisionPage{Items: []Revision{}}
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			page.NextCursor = encodePage("revisions", projectID, page.Items[limit-1].ID)
			break
		}
		var rev Revision
		if err := json.Unmarshal([]byte(doc), &rev); err != nil {
			return nil, err
		}
		page.Items = append(page.Items, rev)
	}
	return page, rows.Err()
}
