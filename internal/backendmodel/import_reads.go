package backendmodel

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
)

type graphCursor struct {
	Resource  string `json:"resource"`
	ProjectID string `json:"projectId"`
	Scope     string `json:"scope"`
	After     string `json:"after"`
}

func decodeGraphPage(limit int, cursor, resource, pid, scope string, uuidAfter bool) (int, string, error) {
	if limit < 0 || limit > MaxGraphPageSize {
		return 0, "", invalid("limit", "limit must be between 1 and 500")
	}
	if limit == 0 {
		limit = DefaultGraphPageSize
	}
	if cursor == "" {
		return limit, "", nil
	}
	if len(cursor) > 1024 {
		return 0, "", invalid("cursor", "Invalid cursor")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return 0, "", invalid("cursor", "Invalid cursor")
	}
	var c graphCursor
	if err := json.Unmarshal(b, &c, json.RejectUnknownMembers(true)); err != nil || c.Resource != resource || c.ProjectID != pid || c.Scope != scope || uuidAfter && !ValidID(c.After) || !uuidAfter && validateKey(c.After) != nil {
		return 0, "", invalid("cursor", "Cursor does not match this resource or filter")
	}
	return limit, c.After, nil
}
func encodeGraphPage(resource, pid, scope, after string) string {
	b, _ := json.Marshal(graphCursor{Resource: resource, ProjectID: pid, Scope: scope, After: after})
	return base64.RawURLEncoding.EncodeToString(b)
}
func (r *Repo) Imports(ctx context.Context, pid string, in ListInput) (*ImportPage, error) {
	if _, err := r.Get(ctx, pid); err != nil {
		return nil, err
	}
	limit, after, err := decodePage(in, "imports", pid)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT document FROM backend_import_sessions WHERE project_id=? AND id>? ORDER BY id LIMIT ?`, pid, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &ImportPage{Items: []ImportSession{}}
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			out.NextCursor = encodePage("imports", pid, out.Items[limit-1].ID)
			break
		}
		var s ImportSession
		if err := json.Unmarshal([]byte(doc), &s); err != nil {
			return nil, err
		}
		if s.Mode == "" {
			s.Mode = "initial"
		}
		out.Items = append(out.Items, s)
	}
	return out, rows.Err()
}
func (r *Repo) Import(ctx context.Context, pid, sid string, in ListInput) (*ImportStatus, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s, err := loadSession(ctx, tx, pid, sid)
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "batches", pid, sid, false)
	if err != nil {
		return nil, err
	}
	out := &ImportStatus{Session: *s, AcceptedBatches: []BatchSummary{}}
	if err := loadSavedStatus(ctx, tx, out); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT batch_id,payload_hash,accepted_version FROM backend_import_batches WHERE session_id=? AND batch_id>? ORDER BY batch_id LIMIT ?`, sid, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b BatchSummary
		if err := rows.Scan(&b.BatchID, &b.PayloadHash, &b.AcceptedVersion); err != nil {
			return nil, err
		}
		if len(out.AcceptedBatches) == limit {
			out.NextCursor = encodeGraphPage("batches", pid, sid, out.AcceptedBatches[limit-1].BatchID)
			break
		}
		out.AcceptedBatches = append(out.AcceptedBatches, b)
	}
	return out, rows.Err()
}
func (r *Repo) QueryGraph(ctx context.Context, pid string, in GraphQueryInput) (*GraphPage, error) {
	if _, err := r.Revision(ctx, pid, in.RevisionID); err != nil {
		return nil, err
	}
	if in.ID != "" && (!ValidID(in.ID) || in.Cursor != "" || in.Kind != "" || in.Search != "" || in.ParentID != "" || in.From != "" || in.To != "") {
		return nil, invalid("id", "ID selector cannot be combined with filters or cursor")
	}
	coverage, err := r.RevisionCoverage(ctx, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	metadata := RevisionState{Sources: coverage.Snapshots}
	if !slices.Contains([]string{"nodes", "edges"}, in.RecordType) {
		return nil, semantic("recordType", "Query must select nodes or edges")
	}
	if in.RecordType == "nodes" && (in.From != "" || in.To != "") || in.RecordType == "edges" && (in.Search != "" || in.ParentID != "") {
		return nil, semantic("selectors", "Selectors are not valid for this record type")
	}
	kinds := SupportedNodeKinds()
	typ := "node"
	if in.RecordType == "edges" {
		kinds = SupportedEdgeKinds()
		typ = "edge"
	}
	if in.Kind != "" && !slices.Contains(kinds, in.Kind) {
		return nil, semantic("kind", "Unsupported kind filter")
	}
	for _, id := range []string{in.ParentID, in.From, in.To} {
		if id != "" && !ValidID(id) {
			return nil, semantic("selectors", "ID selectors must be UUIDs")
		}
	}
	if len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return nil, semantic("search", "Invalid search string")
	}
	scopeInput := in
	scopeInput.Cursor = ""
	scopeInput.Limit = 0
	scope, err := requestDigest(scopeInput)
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "graph", pid, scope, true)
	if err != nil {
		return nil, err
	}
	query := `SELECT document,id FROM backend_graph_records WHERE project_id=? AND revision_id=? AND record_type=? AND id>?`
	args := []any{pid, in.RevisionID, typ, after}
	for _, f := range []struct{ column, value string }{{"id", in.ID}, {"kind", in.Kind}, {"parent_id", in.ParentID}, {"from_id", in.From}, {"to_id", in.To}} {
		if f.value != "" {
			query += " AND " + f.column + "=?"
			args = append(args, f.value)
		}
	}
	if in.Search != "" {
		query += ` AND instr(lower(name),lower(?))>0`
		args = append(args, in.Search)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &GraphPage{Nodes: []Node{}, Edges: []Edge{}}
	count := 0
	last := ""
	for rows.Next() {
		var doc, id string
		if err := rows.Scan(&doc, &id); err != nil {
			return nil, err
		}
		if count == limit {
			out.NextCursor = encodeGraphPage("graph", pid, scope, last)
			break
		}
		if typ == "node" {
			var n Node
			if err := json.Unmarshal([]byte(doc), &n); err != nil {
				return nil, err
			}
			deriveMetadata(metadata, &n.Ownership, &n.Freshness)
			out.Nodes = append(out.Nodes, n)
		} else {
			var e Edge
			if err := json.Unmarshal([]byte(doc), &e); err != nil {
				return nil, err
			}
			deriveMetadata(metadata, &e.Ownership, &e.Freshness)
			out.Edges = append(out.Edges, e)
		}
		last = id
		count++
	}
	return out, rows.Err()
}
func (r *Repo) Node(ctx context.Context, pid, rid, nid string) (*Node, error) {
	if !ValidID(pid) || !ValidID(rid) || !ValidID(nid) {
		return nil, notFound()
	}
	var doc string
	err := r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_graph_records WHERE project_id=? AND revision_id=? AND record_type='node' AND id=?`, pid, rid, nid).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var n Node
	if err := json.Unmarshal([]byte(doc), &n); err != nil {
		return nil, err
	}
	coverage, err := r.RevisionCoverage(ctx, pid, rid)
	if err != nil {
		return nil, err
	}
	deriveMetadata(RevisionState{Sources: coverage.Snapshots}, &n.Ownership, &n.Freshness)
	return &n, nil
}
func (r *Repo) Evidence(ctx context.Context, pid, rid string, in EvidenceQueryInput) (*EvidencePage, error) {
	if _, err := r.Revision(ctx, pid, rid); err != nil {
		return nil, err
	}
	if in.EvidenceID != "" && (!ValidID(in.EvidenceID) || in.SubjectID != "" || in.Cursor != "") {
		return nil, invalid("evidenceId", "Evidence selector cannot be combined with filters or cursor")
	}
	coverage, err := r.RevisionCoverage(ctx, pid, rid)
	if err != nil {
		return nil, err
	}
	if in.SubjectID != "" && !ValidID(in.SubjectID) {
		return nil, semantic("subjectId", "Subject ID must be a UUID")
	}
	scope := rid + ":" + in.SubjectID + ":" + in.EvidenceID
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "evidence", pid, scope, true)
	if err != nil {
		return nil, err
	}
	query := `SELECT document,id FROM backend_graph_records WHERE project_id=? AND revision_id=? AND record_type='evidence' AND id>?`
	args := []any{pid, rid, after}
	if in.EvidenceID != "" {
		query += ` AND id=?`
		args = append(args, in.EvidenceID)
	}
	if in.SubjectID != "" {
		query += ` AND subject_id=?`
		args = append(args, in.SubjectID)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &EvidencePage{Items: []Evidence{}}
	last := ""
	for rows.Next() {
		var doc, id string
		if err := rows.Scan(&doc, &id); err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeGraphPage("evidence", pid, scope, last)
			break
		}
		var e Evidence
		if err := json.Unmarshal([]byte(doc), &e); err != nil {
			return nil, err
		}
		deriveMetadata(RevisionState{Sources: coverage.Snapshots}, &e.Ownership, &e.Freshness)
		out.Items = append(out.Items, e)
		last = id
	}
	return out, rows.Err()
}
func (r *Repo) RevisionCoverage(ctx context.Context, pid, rid string) (*RevisionCoverage, error) {
	rev, err := r.Revision(ctx, pid, rid)
	if err != nil {
		return nil, err
	}
	var doc string
	err = r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources WHERE revision_id=?`, rid).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return &RevisionCoverage{Coverage: rev.Coverage, Inventory: []InventoryItem{}, Snapshots: []SourceSnapshot{}, ReconciliationGaps: []string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var out RevisionCoverage
	if err := json.Unmarshal([]byte(doc), &out); err != nil {
		return nil, err
	}
	if len(out.Snapshots) == 1 && out.Snapshots[0].Role == "" {
		out.Snapshots[0].Role = "primary"
	}
	if out.ReconciliationGaps == nil {
		out.ReconciliationGaps = []string{}
	}
	return &out, nil
}
