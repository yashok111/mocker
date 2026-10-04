package backendmodel

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func resolveAnnotationTarget(ctx context.Context, tx *sql.Tx, p *Project, target AnnotationTarget) error {
	revisionID := target.RevisionID
	if revisionID == "" {
		revisionID = p.CurrentRevisionID
	}
	var found bool
	err := tx.QueryRowContext(ctx, `
  SELECT EXISTS(
   SELECT 1 FROM backend_graph_records g
   JOIN backend_revisions r ON r.id=g.revision_id AND r.project_id=g.project_id
   WHERE g.project_id=? AND g.revision_id=? AND g.record_type=? AND g.id=?
  )`, p.ID, revisionID, target.RecordType, target.ID).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return notFound()
	}
	return nil
}

func applyAnnotationCommand(ctx context.Context, tx *sql.Tx, p *Project, c Command, actor string, now time.Time) error {
	switch c.Type {
	case "create_annotation":
		return createAnnotation(ctx, tx, p, c, actor, now)
	case "update_annotation":
		return updateAnnotation(ctx, tx, p, c, actor, now)
	case "remove_annotation":
		// Keep the last edit timestamps, author and exact binding in the tombstone.
		result, err := tx.ExecContext(ctx, `
   UPDATE backend_annotations SET body='',deleted_at=?
   WHERE project_id=? AND id=? AND deleted_at IS NULL`, now.Format(time.RFC3339Nano), p.ID, c.AnnotationID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return notFound()
		}
	}
	return nil
}

func createAnnotation(ctx context.Context, tx *sql.Tx, p *Project, c Command, actor string, now time.Time) error {
	var allocated bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backend_annotations WHERE id=?)`, c.AnnotationID).Scan(&allocated); err != nil {
		return err
	}
	if allocated {
		return &FaultError{Status: 409, Code: "backend_annotation_conflict", Message: "Annotation identity is already allocated"}
	}
	if err := resolveAnnotationTarget(ctx, tx, p, *c.Target); err != nil {
		return err
	}
	timestamp := now.Format(time.RFC3339Nano)
	_, err := tx.ExecContext(ctx, `
  INSERT INTO backend_annotations(id,project_id,record_type,target_id,revision_id,body,author,created_at,updated_at)
  VALUES(?,?,?,?,NULLIF(?,''),?,?,?,?)`,
		c.AnnotationID, p.ID, c.Target.RecordType, c.Target.ID, c.Target.RevisionID, c.Body, actor, timestamp, timestamp)
	return err
}

func updateAnnotation(ctx context.Context, tx *sql.Tx, p *Project, c Command, actor string, now time.Time) error {
	var previous AnnotationTarget
	err := tx.QueryRowContext(ctx, `
  SELECT record_type,target_id,COALESCE(revision_id,'') FROM backend_annotations
  WHERE project_id=? AND id=? AND deleted_at IS NULL`, p.ID, c.AnnotationID).
		Scan(&previous.RecordType, &previous.ID, &previous.RevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		return err
	}
	// An unchanged binding permits editing an unbound annotation after source deletion.
	if previous != *c.Target {
		if err := resolveAnnotationTarget(ctx, tx, p, *c.Target); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
  UPDATE backend_annotations SET record_type=?,target_id=?,revision_id=NULLIF(?,''),body=?,author=?,updated_at=?
  WHERE project_id=? AND id=?`,
		c.Target.RecordType, c.Target.ID, c.Target.RevisionID, c.Body, actor, now.Format(time.RFC3339Nano), p.ID, c.AnnotationID)
	return err
}

func checkAnnotationQuota(ctx context.Context, tx *sql.Tx, projectID string) error {
	var identities, activeBytes int64
	err := tx.QueryRowContext(ctx, `
  SELECT count(*),COALESCE(sum(CASE WHEN deleted_at IS NULL THEN length(CAST(body AS BLOB)) ELSE 0 END),0)
  FROM backend_annotations WHERE project_id=?`, projectID).Scan(&identities, &activeBytes)
	if err != nil {
		return err
	}
	if identities > MaxAnnotationIdentities || activeBytes > MaxAnnotationTextBytes {
		return &FaultError{Status: 409, Code: "backend_annotation_quota", Message: "Annotation identity or active text quota exceeded"}
	}
	return nil
}
