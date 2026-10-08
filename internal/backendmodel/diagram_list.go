package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"strings"
)

type DiagramListInput struct {
	SubjectID  string `json:"subjectId,omitempty"`
	TargetHash string `json:"targetHash,omitempty"`
	Order      string `json:"order,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}
type DiagramSummary struct {
	Name       string            `json:"name"`
	Target     BackendReadTarget `json:"target"`
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Pin        DiagramPin        `json:"pin"`
	TargetHash string            `json:"targetHash"`
}
type DiagramViewSummary struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version int64  `json:"version"`
	Name    string `json:"name"`
}
type DiagramListPage struct {
	Items          []DiagramSummary `json:"items"`
	NextCursor     string           `json:"nextCursor"`
	CatalogVersion int64            `json:"catalogVersion"`
}
type DiagramViewListPage struct {
	Items          []DiagramViewSummary `json:"items"`
	NextCursor     string               `json:"nextCursor"`
	CatalogVersion int64                `json:"catalogVersion"`
}

func validateDiagramListInput(in DiagramListInput) error {
	if in.SubjectID != "" && !ValidID(in.SubjectID) {
		return invalid("subjectId", "Expected exact record ID")
	}
	if in.TargetHash != "" && (len(in.TargetHash) != 64 || strings.Trim(in.TargetHash, "0123456789abcdef") != "") {
		return invalid("targetHash", "Expected exact target hash")
	}
	if in.Order != "" && in.Order != "asc" && in.Order != "desc" {
		return invalid("order", "Use asc or desc")
	}
	if in.Kind != "" && in.Kind != "architecture" && in.Kind != "interactions" && in.Kind != "lifecycle" && in.Kind != "business_map" {
		return diagramUnsupported()
	}
	if in.Limit < 1 || in.Limit > 500 {
		return invalid("limit", "Use 1–500")
	}
	return nil
}

func diagramListScope(ctx context.Context, q importReader, pid, kind string, in DiagramListInput) (string, int64, error) {
	if err := validateDiagramListInput(in); err != nil {
		return "", 0, err
	}
	var owner string
	if err := q.QueryRowContext(ctx, `SELECT id FROM backend_projects WHERE id=?`, pid).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
		return "", 0, notFound()
	} else if err != nil {
		return "", 0, err
	}
	var catalog int64
	err := q.QueryRowContext(ctx, `SELECT version FROM backend_diagram_catalog WHERE project_id=?`, pid).Scan(&catalog)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	// The cursor binds the state of ITS catalog only. backend_diagram_catalog
	// is one counter that diagram and view writes both advance, so binding it
	// made a view write reject an in-flight diagram listing and the reverse,
	// although the two catalog cursors are documented as separate (review
	// 2026-10-06, F107). Neither table ever loses a row and every write bumps
	// a row's version (a view rename included), so (rows, sum of versions)
	// changes on every write to that catalog and on no other. catalogVersion
	// in the response stays the shared counter.
	table := "backend_diagrams"
	if kind == "diagram-views" {
		table = "backend_diagram_views"
	}
	var rows, versions int64
	if err = q.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(version),0) FROM `+table+` WHERE project_id=?`, pid).Scan(&rows, &versions); err != nil {
		return "", 0, err
	}
	in.Cursor = ""
	hash, err := requestDigest(struct {
		Project, Kind string
		Page          DiagramListInput
		Rows          int64
		Versions      int64
	}{pid, kind, in, rows, versions})
	return hash, catalog, err
}
func (r *Repo) ListDiagrams(ctx context.Context, pid string, in DiagramListInput) (*DiagramListPage, error) {
	if in.Limit == 0 {
		in.Limit = 100
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	scope, catalog, err := diagramListScope(ctx, tx, pid, "diagrams", in)
	if err != nil {
		return nil, err
	}

	from := ` FROM backend_diagrams d JOIN backend_diagram_versions_documents v ON v.project_id=d.project_id AND v.diagram_id=d.id AND v.version=d.version WHERE d.project_id=? AND (?='' OR d.kind=?) AND (?='' OR v.target_hash=?) AND (?='' OR EXISTS (SELECT 1 FROM json_tree(v.document,'$.document.payload') ref WHERE CASE WHEN ref.type='object' THEN json_extract(ref.value,'$.kind')='record' AND json_extract(ref.value,'$.id')=? ELSE 0 END) OR EXISTS (SELECT 1 FROM json_each(v.document,'$.document.payload.elements') element, json_each(element.value,'$.membership.nodeIds') member WHERE member.type='text' AND member.value=?))`
	args := []any{pid, in.Kind, in.Kind, in.TargetHash, in.TargetHash, in.SubjectID, in.SubjectID, in.SubjectID}
	var total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil {
		return nil, err
	}
	offset, err := diagramOffset(in.Cursor, scope, total)
	if err != nil {
		return nil, err
	}
	order := "d.kind,d.id"
	if in.Order == "desc" {
		order = "d.id DESC"
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.kind,v.version,v.content_hash,v.target_hash,d.target_json,
 coalesce((SELECT json_extract(value,'$.label') FROM json_each(v.document,'$.document.payload.elements') WHERE json_extract(value,'$.role')='software_system' LIMIT 1),(SELECT json_extract(value,'$.label') FROM json_each(v.document,'$.document.payload.elements') WHERE json_extract(value,'$.role')='command' LIMIT 1),(SELECT name FROM backend_graph_records_documents WHERE project_id=d.project_id AND revision_id=json_extract(d.target_json,'$.revisionId') AND record_type='node' AND id=json_extract(v.document,'$.document.payload.entity.id') LIMIT 1),json_extract(v.document,'$.document.payload.steps[0].label'),json_extract(v.document,'$.document.payload.elements[0].label'),json_extract(v.document,'$.document.payload.participants[0].label'),json_extract(v.document,'$.document.payload.states[0].label'),d.kind)
`+from+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, in.Limit, offset)...)

	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []DiagramSummary{}
	for rows.Next() {
		var item DiagramSummary
		var target string
		if err = rows.Scan(&item.ID, &item.Kind, &item.Pin.Version, &item.Pin.ContentHash, &item.TargetHash, &target, &item.Name); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(target), &item.Target); err != nil {
			return nil, err
		}
		item.Pin.ID = item.ID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &DiagramListPage{Items: items, NextCursor: diagramNext(scope, min(offset+in.Limit, total), total), CatalogVersion: catalog}, nil
}

func (r *Repo) ListDiagramViews(ctx context.Context, pid string, in DiagramListInput) (*DiagramViewListPage, error) {
	if in.SubjectID != "" || in.TargetHash != "" {
		return nil, invalid("filter", "Record filters apply to mappings, not saved views")
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	scope, catalog, err := diagramListScope(ctx, tx, pid, "diagram-views", in)
	if err != nil {
		return nil, err
	}
	order := "kind,id"
	if in.Order == "desc" {
		order = "id DESC"
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,version,name FROM backend_diagram_views WHERE project_id=? AND (?='' OR kind=?) ORDER BY `+order, pid, in.Kind, in.Kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	all := []DiagramViewSummary{}
	for rows.Next() {
		var item DiagramViewSummary
		if err = rows.Scan(&item.ID, &item.Kind, &item.Version, &item.Name); err != nil {
			return nil, err
		}
		all = append(all, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	offset, err := diagramOffset(in.Cursor, scope, len(all))
	if err != nil {
		return nil, err
	}
	end := min(offset+in.Limit, len(all))
	return &DiagramViewListPage{Items: all[offset:end], NextCursor: diagramNext(scope, end, len(all)), CatalogVersion: catalog}, nil
}
