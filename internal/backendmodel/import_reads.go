package backendmodel

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"slices"
	"strconv"
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
	defer func() { _ = rows.Close() }()
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
		s.Profile = selectedProfile(s.Profile)
		out.Items = append(out.Items, s)
	}
	return out, rows.Err()
}
func (r *Repo) Import(ctx context.Context, pid, sid string, in ListInput) (*ImportStatus, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
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
	// Acceptance order, keyed by accepted_version (unique per session):
	// recovery resumes from acceptedBatches in the order the server
	// accepted them, and ordering by the caller-chosen batch_id text
	// inverted it (review 2026-10-06, F89). The cursor is the last
	// accepted_version; an older batch-id cursor is refused as invalid.
	var afterVersion int64
	if after != "" {
		afterVersion, err = strconv.ParseInt(after, 10, 64)
		if err != nil || afterVersion < 0 {
			return nil, invalid("cursor", "Invalid batch cursor")
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT batch_id,payload_hash,accepted_version FROM backend_import_batches WHERE session_id=? AND accepted_version>? ORDER BY accepted_version LIMIT ?`, sid, afterVersion, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b BatchSummary
		if err := rows.Scan(&b.BatchID, &b.PayloadHash, &b.AcceptedVersion); err != nil {
			return nil, err
		}
		if len(out.AcceptedBatches) == limit {
			out.NextCursor = encodeGraphPage("batches", pid, sid, strconv.FormatInt(out.AcceptedBatches[limit-1].AcceptedVersion, 10))
			break
		}
		out.AcceptedBatches = append(out.AcceptedBatches, b)
	}
	return out, rows.Err()
}
func (r *Repo) QueryGraph(ctx context.Context, pid string, in GraphQueryInput) (*GraphPage, error) {
	effective, err := r.graphReadIsEffective(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	if effective {
		return r.queryEffectiveGraph(ctx, pid, in)
	}
	target, err := r.resolveBackendTarget(ctx, pid, BackendReadTarget{RevisionID: in.RevisionID, Proposal: in.Proposal})
	if err != nil {
		return nil, err
	}
	revision, err := r.Revision(ctx, pid, target.revisionID)
	if err != nil {
		return nil, err
	}
	if err := validateGraphIDSelector(in); err != nil {
		return nil, err
	}
	coverage, err := r.RevisionCoverage(ctx, pid, target.revisionID)
	if err != nil {
		return nil, err
	}
	metadata := RevisionState{Revision: *revision, Sources: coverage.Snapshots}
	typ, err := validateGraphQuery(in, revision.SchemaVersion)
	if err != nil {
		return nil, err
	}
	scope, err := graphCursorScope(in, target)
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "graph", pid, scope, true)
	if err != nil {
		return nil, err
	}
	query, args, afterArg, err := graphRecordQuery(pid, in, target, typ, after)
	if err != nil {
		return nil, err
	}
	countArgs := append([]any(nil), args...)
	countArgs[afterArg] = ""
	countQuery := strings.Replace(query, "SELECT document,id FROM ", "SELECT count(*) FROM ", 1)
	var total int
	if err := r.db.R.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, err
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	page := graphPageReader{target: target, typ: typ, metadata: metadata, schemaVersion: revision.SchemaVersion, out: &GraphPage{Total: &total, Nodes: []Node{}, Edges: []Edge{}}}
	if err := page.open(ctx, r, pid); err != nil {
		return nil, err
	}
	count := 0
	last := ""
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var doc, id string
		if err := rows.Scan(&doc, &id); err != nil {
			return nil, err
		}
		if count == limit {
			page.out.NextCursor = encodeGraphPage("graph", pid, scope, last)
			break
		}
		if err := page.add(doc, id); err != nil {
			return nil, err
		}
		last = id
		count++
	}
	return page.out, rows.Err()
}

// graphReadIsEffective reports a read the effective graph must answer: any
// staged or workspace-filtered target, and a plain read of a composed revision.
func (r *Repo) graphReadIsEffective(ctx context.Context, pid string, in GraphQueryInput) (bool, error) {
	if in.ChangeProposal != nil || in.ImportCandidate != nil || workspaceFiltered(in) {
		return true, nil
	}
	if in.Proposal == nil && in.RevisionID != "" {
		revision, err := r.Revision(ctx, pid, in.RevisionID)
		if err != nil {
			return false, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return true, nil
		}
	}
	return false, nil
}

// validateGraphIDSelector: an exact ID read is a lookup, not a page, so it
// takes no filter and no cursor.
func validateGraphIDSelector(in GraphQueryInput) error {
	if in.ID != "" && (!ValidID(in.ID) || in.Cursor != "" || in.Kind != "" || in.Search != "" || in.ParentID != "" || in.From != "" || in.To != "") {
		return invalid("id", "ID selector cannot be combined with filters or cursor")
	}
	return nil
}

// validateGraphQuery checks the record type, its selectors and the kind
// filter against the kinds the revision's profile supports, and returns the
// record type as stored ("node" or "edge").
func validateGraphQuery(in GraphQueryInput, schemaVersion string) (string, error) {
	if !slices.Contains([]string{"nodes", "edges"}, in.RecordType) {
		return "", semantic("recordType", "Query must select nodes or edges")
	}
	if in.RecordType == "nodes" && (in.From != "" || in.To != "") || in.RecordType == "edges" && (in.Search != "" || in.ParentID != "") {
		return "", semantic("selectors", "Selectors are not valid for this record type")
	}
	typ := "node"
	if in.RecordType == "edges" {
		typ = "edge"
	}
	if in.Kind != "" && !slices.Contains(graphQueryKinds(schemaVersion, typ), in.Kind) {
		return "", semantic("kind", "Unsupported kind filter")
	}
	for _, id := range []string{in.ParentID, in.From, in.To} {
		if id != "" && !ValidID(id) {
			return "", semantic("selectors", "ID selectors must be UUIDs")
		}
	}
	if len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return "", semantic("search", "Invalid search string")
	}
	return typ, nil
}

func graphQueryKinds(schemaVersion, typ string) []string {
	if isRelationalSchema(schemaVersion) || schemaVersion == ComposedSchemaVersion {
		profile := profileForSchema(schemaVersion)
		if schemaVersion == ComposedSchemaVersion {
			profile = ComposedProfile
		}
		if typ == "edge" {
			return SupportedEdgeKindsForProfile(profile)
		}
		return SupportedNodeKindsForProfile(profile)
	}
	if typ == "edge" {
		return SupportedEdgeKinds()
	}
	return SupportedNodeKinds()
}

// graphCursorScope binds a cursor to the query without its paging fields and,
// on a proposal read, to the proposal pins.
func graphCursorScope(in GraphQueryInput, target *resolvedBackendTarget) (string, error) {
	scopeInput := in
	scopeInput.Cursor = ""
	scopeInput.Limit = 0
	if target.pins != nil {
		return requestDigest(struct {
			Query GraphQueryInput
			Pins  *ProposalReadPins
		}{scopeInput, target.pins})
	}
	return requestDigest(scopeInput)
}

// graphRecordQuery builds the filtered page query (without ORDER/LIMIT) and
// reports which argument carries the cursor, so the count query can blank it.
func graphRecordQuery(pid string, in GraphQueryInput, target *resolvedBackendTarget, typ, after string) (string, []any, int, error) {
	records, args, err := target.graphRecords(pid, typ)
	if err != nil {
		return "", nil, 0, err
	}
	prefix, table := "", records
	if target.proposal != nil {
		prefix, table = records, "records"
		args = append(args, pid, target.revisionID, typ)
	}
	query := prefix + `SELECT document,id FROM ` + table + ` WHERE project_id=? AND revision_id=? AND record_type=? AND id>?` //nolint:gosec // prefix and table come from graphRecords, never from the request; every value binds as ?
	afterArg := len(args)
	args = append(args, after)
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
	return query, args, afterArg, nil
}

// graphPageReader decodes the page's rows into nodes or edges, with the
// source context of a composed revision and the projection of a proposal.
type graphPageReader struct {
	target        *resolvedBackendTarget
	typ           string
	metadata      RevisionState
	schemaVersion string
	sourceGraph   *SourceGraphSnapshot
	out           *GraphPage
}

func (g *graphPageReader) open(ctx context.Context, r *Repo, pid string) error {
	if g.schemaVersion == ComposedSchemaVersion {
		var err error
		g.sourceGraph, err = r.ResolveSourceGraph(ctx, pid, g.target.revisionID)
		if err != nil {
			return err
		}
		g.out.ViewSchemaVersion = ComposedSchemaVersion
		g.out.Source = sourceVectorReadContext(g.sourceGraph)
	}
	if g.target.proposal != nil {
		g.out.ViewSchemaVersion, g.out.ProposalPins = ProposalDocumentVersion, g.target.pins
		g.out.ProposalProjection = &ProposalGraphProjection{Nodes: []ProposalProjectedNode{}, Edges: []ProposalProjectedEdge{}}
	}
	return nil
}

// add decodes one row. An empty document is a record the proposal designs:
// it exists only as its projection.
func (g *graphPageReader) add(doc, id string) error {
	switch {
	case doc == "":
		return g.addDesigned(id)
	case g.typ == "node":
		return g.addNode(doc)
	default:
		return g.addEdge(doc)
	}
}

func (g *graphPageReader) addDesigned(id string) error {
	overlay := g.target.overlay(id)
	if g.typ == "node" {
		projected, err := projectProposalNode(*g.target.proposal, new(g.target.draft.ID), nil, overlay)
		if err != nil {
			return err
		}
		g.out.ProposalProjection.Nodes = append(g.out.ProposalProjection.Nodes, *projected)
		return nil
	}
	projected, err := projectProposalEdge(*g.target.proposal, new(g.target.draft.ID), nil, overlay)
	if err != nil {
		return err
	}
	g.out.ProposalProjection.Edges = append(g.out.ProposalProjection.Edges, *projected)
	return nil
}

func (g *graphPageReader) addNode(doc string) error {
	var n Node
	if err := json.Unmarshal([]byte(doc), &n); err != nil {
		return err
	}
	deriveMetadata(g.metadata, &n.Ownership, &n.Freshness)
	if isRelationalSchema(g.schemaVersion) {
		var err error
		n.FacetComparison, err = CompareRelationalFacets(n.Kind, n.Attributes, false)
		if err != nil {
			return err
		}
	}
	if g.sourceGraph != nil {
		n.Source = sourceRecordReadContext(g.sourceGraph, "node", n.ID)
	}
	g.out.Nodes = append(g.out.Nodes, n)
	if g.target.proposal != nil {
		projected, err := projectProposalNode(*g.target.proposal, new(g.target.draft.ID), &n, g.target.overlay(n.ID))
		if err != nil {
			return err
		}
		g.out.ProposalProjection.Nodes = append(g.out.ProposalProjection.Nodes, *projected)
	}
	return nil
}

func (g *graphPageReader) addEdge(doc string) error {
	var e Edge
	if err := json.Unmarshal([]byte(doc), &e); err != nil {
		return err
	}
	deriveMetadata(g.metadata, &e.Ownership, &e.Freshness)
	if isRelationalSchema(g.schemaVersion) {
		var err error
		e.FacetComparison, err = CompareRelationalFacets(e.Kind, e.Attributes, true)
		if err != nil {
			return err
		}
	}
	if g.sourceGraph != nil {
		e.Source = sourceRecordReadContext(g.sourceGraph, "edge", e.ID)
	}
	g.out.Edges = append(g.out.Edges, e)
	if g.target.proposal != nil {
		projected, err := projectProposalEdge(*g.target.proposal, new(g.target.draft.ID), &e, g.target.overlay(e.ID))
		if err != nil {
			return err
		}
		g.out.ProposalProjection.Edges = append(g.out.ProposalProjection.Edges, *projected)
	}
	return nil
}
func (r *Repo) Node(ctx context.Context, pid, rid, nid string) (*Node, error) {
	if !ValidID(pid) || !ValidID(rid) || !ValidID(nid) {
		return nil, notFound()
	}
	var doc string
	err := r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='node' AND id=?`, pid, rid, nid).Scan(&doc)
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
	revision, err := r.Revision(ctx, pid, rid)
	if err != nil {
		return nil, err
	}
	deriveMetadata(RevisionState{Revision: *revision, Sources: coverage.Snapshots}, &n.Ownership, &n.Freshness)
	if isRelationalSchema(revision.SchemaVersion) {
		n.FacetComparison, err = CompareRelationalFacets(n.Kind, n.Attributes, false)
		if err != nil {
			return nil, err
		}
	}
	if revision.SchemaVersion == ComposedSchemaVersion {
		graph, err := r.ResolveSourceGraph(ctx, pid, rid)
		if err != nil {
			return nil, err
		}
		n.Source = sourceReadContext(graph, "node", n.ID, "")
	}
	return &n, nil
}
func (r *Repo) Evidence(ctx context.Context, pid, rid string, in EvidenceQueryInput) (*EvidencePage, error) {
	return r.evidence(ctx, pid, rid, in, rid+":"+in.SubjectID+":"+in.EvidenceID)
}
func (r *Repo) evidence(ctx context.Context, pid, rid string, in EvidenceQueryInput, scope string) (*EvidencePage, error) {
	revision, err := r.Revision(ctx, pid, rid)
	if err != nil {
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
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "evidence", pid, scope, true)
	if err != nil {
		return nil, err
	}
	query := `SELECT document,id FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='evidence' AND id>?`
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
	defer func() { _ = rows.Close() }()
	out := &EvidencePage{Items: []Evidence{}}
	var sourceGraph *SourceGraphSnapshot
	if revision.SchemaVersion == ComposedSchemaVersion {
		sourceGraph, err = r.ResolveSourceGraph(ctx, pid, rid)
		if err != nil {
			return nil, err
		}
		out.ViewSchemaVersion = ComposedSchemaVersion
	}
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
		deriveMetadata(RevisionState{Revision: *revision, Sources: coverage.Snapshots}, &e.Ownership, &e.Freshness)
		out.Items = append(out.Items, e)
		last = id
	}
	out.Source = evidencePageSourceContext(sourceGraph, out.Items)
	return out, rows.Err()
}

func evidencePageSourceContext(graph *SourceGraphSnapshot, items []Evidence) *SourceReadContext {
	if graph == nil {
		return nil
	}
	evidenceIDs := make([]string, 0, len(items))
	for _, item := range items {
		evidenceIDs = append(evidenceIDs, item.ID)
	}
	return sourceEvidenceReadContext(graph, evidenceIDs)
}

func (r *Repo) RevisionCoverage(ctx context.Context, pid, rid string) (*RevisionCoverage, error) {
	rev, err := r.Revision(ctx, pid, rid)
	if err != nil {
		return nil, err
	}
	var doc string
	err = r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, rid).Scan(&doc)
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
	if rev.SchemaVersion != ComposedSchemaVersion && len(out.Snapshots) == 1 && out.Snapshots[0].Role == "" {
		out.Snapshots[0].Role = "primary"
	}
	if out.ReconciliationGaps == nil {
		out.ReconciliationGaps = []string{}
	}
	if rev.SchemaVersion == ComposedSchemaVersion {
		var source SourceRevisionContext
		if err := json.Unmarshal([]byte(doc), &source); err != nil {
			return nil, err
		}
		graph, err := r.ResolveSourceGraph(ctx, pid, rid)
		if err != nil {
			return nil, err
		}
		out.Source = sourceVectorReadContext(graph)
		out.ViewSchemaVersion = ComposedSchemaVersion
	}
	return &out, nil
}
