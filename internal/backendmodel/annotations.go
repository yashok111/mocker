package backendmodel

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"time"
)

type annotationCursor struct {
	Kind           string `json:"kind"`
	ProjectID      string `json:"projectId"`
	ProjectVersion int64  `json:"projectVersion"`
	RevisionID     string `json:"revisionId"`
	FilterHash     string `json:"filterHash"`
	After          string `json:"after"`
}

func annotationFilters(in AnnotationListInput) (int, string, error) {
	for _, field := range []struct{ name, value string }{
		{name: "annotationId", value: in.AnnotationID},
		{name: "targetId", value: in.TargetID},
		{name: "revisionId", value: in.RevisionID},
	} {
		if field.value != "" && !ValidID(field.value) {
			return 0, "", invalid(field.name, "Use a canonical nonzero UUID")
		}
	}
	if in.RecordType != "" && in.RecordType != "node" && in.RecordType != "edge" {
		return 0, "", invalid("recordType", "Expected node or edge")
	}
	if in.TargetID != "" && in.RecordType == "" {
		return 0, "", invalid("recordType", "targetId requires recordType")
	}
	if in.Limit < 0 || in.Limit > MaxAnnotationPageSize {
		return 0, "", invalid("limit", "Use a limit between 1 and 500")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultAnnotationPageSize
	}
	filters := struct {
		AnnotationID string `json:"annotationId"`
		TargetID     string `json:"targetId"`
		RecordType   string `json:"recordType"`
		RevisionID   string `json:"revisionId"`
		Orphaned     *bool  `json:"orphaned"`
	}{AnnotationID: in.AnnotationID, TargetID: in.TargetID, RecordType: in.RecordType, RevisionID: in.RevisionID, Orphaned: in.Orphaned}
	raw, err := json.Marshal(filters)
	if err != nil {
		return 0, "", err
	}
	return limit, hashBytes(raw), nil
}
func decodeAnnotationCursor(raw string, p *Project, filterHash string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 {
		return "", invalid("cursor", "Invalid annotation cursor")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return "", invalid("cursor", "Invalid annotation cursor")
	}
	var c annotationCursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return "", invalid("cursor", "Invalid annotation cursor")
	}
	if c.Kind != "annotations" || c.ProjectID != p.ID || c.FilterHash != filterHash || !ValidID(c.After) || !ValidID(c.RevisionID) || c.ProjectVersion <= 0 {
		return "", invalid("cursor", "Cursor does not match this annotation query")
	}
	if c.ProjectVersion != p.Version || c.RevisionID != p.CurrentRevisionID {
		return "", &FaultError{Status: 409, Code: "backend_annotation_page_conflict", Message: "Project changed; restart annotation pagination", CurrentVersion: p.Version}
	}
	return c.After, nil
}
func encodeAnnotationCursor(p *Project, filterHash, after string) string {
	raw, _ := json.Marshal(annotationCursor{Kind: "annotations", ProjectID: p.ID, ProjectVersion: p.Version, RevisionID: p.CurrentRevisionID, FilterHash: filterHash, After: after})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ListAnnotations freezes metadata and source-head membership in one read snapshot.
func (r *Repo) ListAnnotations(ctx context.Context, projectID string, in AnnotationListInput) (*AnnotationPage, error) {
	if !ValidID(projectID) {
		return nil, notFound()
	}
	limit, filterHash, err := annotationFilters(in)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, projectID))
	if err != nil {
		return nil, err
	}
	after, err := decodeAnnotationCursor(in.Cursor, p, filterHash)
	if err != nil {
		return nil, err
	}
	orphaned := -1
	if in.Orphaned != nil {
		orphaned = 0
		if *in.Orphaned {
			orphaned = 1
		}
	}
	rows, err := tx.QueryContext(ctx, `
  WITH annotations AS (
   SELECT a.id,a.record_type,a.target_id,COALESCE(a.revision_id,'') AS revision_id,
    a.body,a.author,a.created_at,a.updated_at,
    CASE WHEN a.revision_id IS NULL THEN
     CASE WHEN EXISTS(SELECT 1 FROM backend_graph_records_documents g
      WHERE g.project_id=a.project_id AND g.revision_id=? AND g.record_type=a.record_type AND g.id=a.target_id)
      THEN 'current' ELSE 'orphaned' END
     WHEN a.revision_id=? THEN 'current' ELSE 'historical' END AS target_status
   FROM backend_annotations a WHERE a.project_id=? AND a.deleted_at IS NULL
  )
  SELECT id,record_type,target_id,revision_id,body,author,created_at,updated_at,target_status FROM annotations
  WHERE id>? AND (?='' OR id=?) AND (?='' OR target_id=?)
   AND (?='' OR record_type=?) AND (?='' OR revision_id=?)
   AND (?=-1 OR (target_status='orphaned')=?)
  ORDER BY id LIMIT ?`,
		p.CurrentRevisionID, p.CurrentRevisionID, projectID, after,
		in.AnnotationID, in.AnnotationID, in.TargetID, in.TargetID, in.RecordType, in.RecordType, in.RevisionID, in.RevisionID,
		orphaned, orphaned, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	page := &AnnotationPage{ProjectID: p.ID, ProjectVersion: p.Version, Items: []Annotation{}}
	for rows.Next() {
		note, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == limit {
			page.NextCursor = encodeAnnotationCursor(p, filterHash, page.Items[limit-1].ID)
			break
		}
		page.Items = append(page.Items, *note)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return page, nil
}
func scanAnnotation(row interface{ Scan(...any) error }) (*Annotation, error) {
	var note Annotation
	var created, updated string
	err := row.Scan(&note.ID, &note.Target.RecordType, &note.Target.ID, &note.Target.RevisionID, &note.Body, &note.Author, &created, &updated, &note.TargetStatus)
	if err != nil {
		return nil, err
	}
	note.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	note.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return nil, err
	}
	return &note, nil
}
