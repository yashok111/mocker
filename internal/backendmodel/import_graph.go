package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"
)

type graphCandidate struct {
	Sources            []SourceSnapshot   `json:"sources"`
	SourceChanges      []SourceChange     `json:"sourceChanges"`
	IdentityDecisions  []IdentityDecision `json:"identityDecisions"`
	DeletionDecisions  []DeletionDecision `json:"deletionDecisions"`
	ComparisonSummary  *ComparisonSummary `json:"comparisonSummary"`
	ReconciliationGaps []string           `json:"reconciliationGaps"`
	StaleCounts        StaleCounts        `json:"staleCounts"`
	Nodes              []Node             `json:"nodes"`
	Edges              []Edge             `json:"edges"`
	Evidence           []Evidence         `json:"evidence"`
	Coverage           Coverage           `json:"coverage"`
}

func prepareGraph(ctx context.Context, q importReader, s *ImportSession) (*graphCandidate, []ImportDiagnostic, error) {
	g := &graphCandidate{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}}
	d := []ImportDiagnostic{}
	add := func(p, m string) { d = append(d, ImportDiagnostic{Code: "backend_graph_invalid", Path: p, Message: m}) }
	if err := validateManifest(s.Manifest); err != nil {
		return nil, nil, err
	}
	if err := validateInventory(s.Inventory); err != nil {
		return nil, nil, err
	}
	base, err := loadRevisionState(ctx, q, s.ProjectID, s.BaseRevisionID)
	if err != nil {
		return nil, nil, err
	}
	ids := map[string]string{}
	if s.Mode == "reconcile" {
		for _, n := range base.Nodes {
			ids["node\x00"+n.ExternalKey] = n.ID
		}
		for _, e := range base.Edges {
			ids["edge\x00"+e.ExternalKey] = e.ID
		}
		for _, e := range base.Evidence {
			ids["evidence\x00"+e.ExternalKey] = e.ID
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT record_type,external_key,id FROM backend_import_identities WHERE session_id=? ORDER BY id`, s.ID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var typ, key, id string
		if err := rows.Scan(&typ, &key, &id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids[typ+"\x00"+key] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	commands := []ImportCommand{}
	rows, err = q.QueryContext(ctx, `SELECT r.document FROM backend_import_records r JOIN backend_import_identities i ON i.session_id=r.session_id AND i.record_type=r.record_type AND i.external_key=r.external_key WHERE r.session_id=? ORDER BY i.id`, s.ID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return nil, nil, err
		}
		var c ImportCommand
		if err := json.Unmarshal([]byte(b), &c); err != nil {
			rows.Close()
			return nil, nil, err
		}
		commands = append(commands, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	present := map[string]bool{}
	if s.Mode == "reconcile" {
		for _, n := range base.Nodes {
			present["node\x00"+n.ExternalKey] = true
		}
		for _, e := range base.Edges {
			present["edge\x00"+e.ExternalKey] = true
		}
		for _, e := range base.Evidence {
			present["evidence\x00"+e.ExternalKey] = true
		}
	}
	for _, c := range commands {
		typ, key, _ := commandAddress(c)
		present[typ+"\x00"+key] = true
	}
	resolve := func(typ, key, p string) string {
		address := typ + "\x00" + key
		if !present[address] {
			add(p, "Reference points to a missing "+typ+" record")
		}
		return ids[address]
	}
	refs := func(keys []string, p string) []string {
		out := []string{}
		for _, k := range keys {
			out = append(out, resolve("evidence", k, p))
		}
		return out
	}
	for _, c := range commands {
		if err := validateCommand(c, s); err != nil {
			return nil, nil, err
		}
		switch c.Op {
		case "upsert_node":
			n := c.Node
			id := ids["node\x00"+n.ExternalKey]
			var parent *string
			if n.ParentKey != nil {
				parent = new(resolve("node", *n.ParentKey, "nodes/"+id+"/parentId"))
			}
			g.Nodes = append(g.Nodes, Node{ID: id, ExternalKey: n.ExternalKey, Kind: n.Kind, Name: n.Name, ParentID: parent, Attributes: n.Attributes, EvidenceIDs: refs(n.EvidenceKeys, "nodes/"+id+"/evidenceIds")})
		case "upsert_edge":
			e := c.Edge
			id := ids["edge\x00"+e.ExternalKey]
			g.Edges = append(g.Edges, Edge{ID: id, ExternalKey: e.ExternalKey, Kind: e.Kind, From: resolve("node", e.FromKey, "edges/"+id+"/from"), To: resolve("node", e.ToKey, "edges/"+id+"/to"), Attributes: e.Attributes, EvidenceIDs: refs(e.EvidenceKeys, "edges/"+id+"/evidenceIds")})
		case "upsert_evidence":
			e := c.Evidence
			id := ids["evidence\x00"+e.ExternalKey]
			g.Evidence = append(g.Evidence, Evidence{ID: id, ExternalKey: e.ExternalKey, SubjectID: resolve(e.SubjectType, e.SubjectKey, "evidence/"+id+"/subjectId"), PropertyPath: e.PropertyPath, Method: e.Method, Status: e.Status, Source: e.Source, Explanation: e.Explanation, Snippet: e.Snippet})
		}
	}
	if selectedProfile(s.Profile) == RelationalProfile {
		for i := range g.Nodes {
			n := &g.Nodes[i]
			n.Attributes, err = resolveRelationalAttributes(n.Kind, n.Attributes, false, s, resolve)
			if err != nil {
				return nil, nil, err
			}
		}
		for i := range g.Edges {
			e := &g.Edges[i]
			e.Attributes, err = resolveRelationalAttributes(e.Kind, e.Attributes, true, s, resolve)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if err := overlayGraph(ctx, q, s, base, g, commands, ids, &d); err != nil {
		return nil, nil, err
	}
	nodes := map[string]Node{}
	subjects := map[string][]string{}
	properties := map[string]any{}
	evidence := map[string]Evidence{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		subjects[n.ID] = n.EvidenceIDs
		properties[n.ID] = n
		if n.Kind != "unresolved_target" && len(n.EvidenceIDs) == 0 {
			add("nodes/"+n.ID+"/evidenceIds", "Known imported nodes require source evidence")
		}
	}
	for _, e := range g.Edges {
		subjects[e.ID] = e.EvidenceIDs
		properties[e.ID] = e
		if len(e.EvidenceIDs) == 0 {
			add("edges/"+e.ID+"/evidenceIds", "Imported edges require source evidence")
		}
	}
	for _, e := range g.Evidence {
		evidence[e.ID] = e
		if !slices.Contains(subjects[e.SubjectID], e.ID) {
			add("evidence/"+e.ID+"/subjectId", "Evidence must be listed by its subject")
		}
		if e.PropertyPath != nil && !pointerExists(properties[e.SubjectID], *e.PropertyPath) {
			add("evidence/"+e.ID+"/propertyPath", "Property path must be a JSON Pointer to an existing subject property")
		}
	}
	for id, refs := range subjects {
		for _, eid := range refs {
			if e, ok := evidence[eid]; !ok || e.SubjectID != id {
				add("subjects/"+id+"/evidenceIds", "Subject evidence must belong to that subject")
			}
		}
	}
	parents := map[string]string{}
	for _, e := range g.Edges {
		from, fok := nodes[e.From]
		to, tok := nodes[e.To]
		if !fok || !tok {
			add("edges/"+e.ID, "Edge endpoints must survive in the final graph")
			continue
		}
		valid := false
		switch e.Kind {
		case "contains":
			valid = slices.Contains([]string{"system", "service", "module"}, from.Kind) || slices.Contains([]string{"external_system", "datastore"}, from.Kind) && slices.Contains([]string{"module", "symbol", "handler"}, to.Kind)
			if selectedProfile(s.Profile) == RelationalProfile {
				if relationalValid, applies := relationalContains(from, to); applies {
					valid = relationalValid
				}
			}
			if _, ok := parents[e.To]; ok {
				add("edges/"+e.ID, "A node may have only one incoming contains edge")
			}
			parents[e.To] = e.From
		case "handles":
			valid = from.Kind == "http_operation" && slices.Contains([]string{"handler", "symbol", "unresolved_target"}, to.Kind)
		case "calls":
			valid = slices.Contains([]string{"symbol", "handler"}, from.Kind) && slices.Contains([]string{"symbol", "handler", "external_system", "unresolved_target"}, to.Kind)
		case "references":
			valid = selectedProfile(s.Profile) == RelationalProfile && from.Kind == "constraint" && (to.Kind == "table" || to.Kind == "unresolved_target")
		case "derived_from":
			valid = slices.Contains([]string{"symbol", "module", "unresolved_target"}, to.Kind)
		}
		if !valid {
			add("edges/"+e.ID, "Edge kind does not support these endpoint kinds")
		}
	}
	for _, n := range g.Nodes {
		if n.ParentID != nil && parents[n.ID] != *n.ParentID {
			add("nodes/"+n.ID+"/parentId", "Provided parent must agree with its sole incoming contains edge")
		}
	}
	colors := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if colors[id] == 1 {
			return false
		}
		if colors[id] == 2 {
			return true
		}
		colors[id] = 1
		if parent := parents[id]; parent != "" && !visit(parent) {
			return false
		}
		colors[id] = 2
		return true
	}
	for _, n := range g.Nodes {
		if !visit(n.ID) {
			add("nodes/"+n.ID, "Contains hierarchy must be acyclic")
			break
		}
	}
	if selectedProfile(s.Profile) == RelationalProfile {
		if err := validateRelationalGraph(ctx, q, s, g, &d); err != nil {
			return nil, nil, err
		}
	}
	g.Coverage = Coverage{Status: "complete", KnownObjects: int64(len(g.Nodes)), Gaps: []string{}}
	for _, x := range s.Inventory {
		if x.Status != "complete" {
			g.Coverage.Status = "partial"
			g.Coverage.Gaps = append(g.Coverage.Gaps, x.Category+": "+x.Status)
			g.Coverage.Gaps = append(g.Coverage.Gaps, x.Gaps...)
			if x.Reason != "" {
				g.Coverage.Gaps = append(g.Coverage.Gaps, x.Reason)
			}
		}
	}
	if s.Manifest.Snapshot.Consistency != "verified" {
		g.Coverage.Status = "partial"
		g.Coverage.Gaps = append(g.Coverage.Gaps, "Source snapshot consistency is unverified.")
	}
	if unresolvedCount(g) > 0 {
		g.Coverage.Status = "partial"
		g.Coverage.Gaps = append(g.Coverage.Gaps, "Unresolved nodes or evidence remain.")
	}
	finishReconciliation(s, g)
	semantic, err := candidateJSON(s, g)
	if err != nil {
		return nil, nil, err
	}
	if len(g.Nodes) > MaxRevisionNodes || len(g.Edges) > MaxRevisionEdges || len(g.Evidence) > MaxRevisionEvidence || len(semantic) > MaxRevisionBytes {
		return nil, nil, limitFault("Revision semantic limit exceeded")
	}
	slices.SortFunc(d, func(a, b ImportDiagnostic) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.Code != b.Code {
			return strings.Compare(a.Code, b.Code)
		}
		return strings.Compare(a.Message, b.Message)
	})
	return g, d, nil
}
func pointerExists(subject any, p string) bool {
	if subject == nil {
		return false
	}
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	b, err := json.Marshal(subject)
	if err != nil {
		return false
	}
	var current jsontext.Value = b
	for part := range strings.SplitSeq(p[1:], "/") {
		var key strings.Builder
		for i := 0; i < len(part); i++ {
			if part[i] == '~' {
				i++
				if i == len(part) || part[i] != '0' && part[i] != '1' {
					return false
				}
				if part[i] == '0' {
					key.WriteByte('~')
				} else {
					key.WriteByte('/')
				}
			} else {
				key.WriteByte(part[i])
			}
		}
		k := key.String()
		if len(current) == 0 {
			return false
		}
		switch current[0] {
		case '{':
			var m map[string]jsontext.Value
			if json.Unmarshal(current, &m) != nil {
				return false
			}
			var ok bool
			current, ok = m[k]
			if !ok {
				return false
			}
		case '[':
			var a []jsontext.Value
			if json.Unmarshal(current, &a) != nil {
				return false
			}
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(a) || strconv.Itoa(i) != k {
				return false
			}
			current = a[i]
		default:
			return false
		}
	}
	return true
}
func unresolvedCount(g *graphCandidate) int64 {
	var n int64
	for _, x := range g.Nodes {
		if x.Kind == "unresolved_target" {
			n++
		}
	}
	for _, e := range g.Evidence {
		if e.Status == "unresolved" {
			n++
		}
	}
	return n
}
func candidateJSON(s *ImportSession, g *graphCandidate) ([]byte, error) {
	foundation, err := canonicalJSON(struct {
		ProjectID      string          `json:"projectId"`
		BaseRevisionID string          `json:"baseRevisionId"`
		Version        int64           `json:"version"`
		RepositoryID   string          `json:"repositoryId"`
		SnapshotID     string          `json:"snapshotId"`
		Manifest       SourceManifest  `json:"manifest"`
		Inventory      []InventoryItem `json:"inventory"`
		Graph          *graphCandidate `json:"graph"`
		GraphScope     *GraphScope     `json:"graphScope"`
		Mode           string          `json:"mode"`
	}{s.ProjectID, s.BaseRevisionID, s.Version, s.RepositoryID, s.SnapshotID, s.Manifest, s.Inventory, g, s.GraphScope, s.Mode})
	if err != nil || selectedProfile(s.Profile) == GraphProfile {
		return foundation, err
	}
	return canonicalJSON(struct {
		ModelSchemaVersion string                  `json:"modelSchemaVersion"`
		Profile            string                  `json:"profile"`
		ProfileExtension   *ImportProfileExtension `json:"profileExtension"`
		Candidate          jsontext.Value          `json:"candidate"`
	}{modelSchemaVersion(s.Profile), s.Profile, s.ProfileExtension, foundation})
}
func (r *Repo) PreviewImport(ctx context.Context, pid, sid string, in PreviewImportInput) (*ImportPreview, error) {
	result := new(ImportPreview)
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		s, err := loadSession(ctx, tx, pid, sid)
		if err != nil {
			return err
		}
		if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
			return err
		}
		if err := requireCollectible(s); err != nil {
			return err
		}
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
		if err != nil {
			return err
		}
		if in.BaseRevisionID != p.CurrentRevisionID {
			return importConflict("backend_import_base_conflict", "Preview base must be the current revision", p.Version)
		}
		s.BaseRevisionID = in.BaseRevisionID
		if err := requireImportBase(ctx, tx, s); err != nil {
			return err
		}
		s.BaseRevisionID = in.BaseRevisionID
		s.Version++
		g, diagnostics, err := prepareGraph(ctx, tx, s)
		if err != nil {
			return err
		}
		s.CandidateHash = nil
		s.State = "needs_resolution"
		if len(diagnostics) == 0 {
			b, err := candidateJSON(s, g)
			if err != nil {
				return err
			}
			s.CandidateHash = new(hashBytes(b))
			s.State = "ready"
		}
		s.UpdatedAt = time.Now().UTC()
		*result = ImportPreview{ModelSchemaVersion: modelSchemaVersion(s.Profile), ProfileExtension: s.ProfileExtension, SessionID: s.ID, Version: s.Version, State: s.State, CandidateHash: s.CandidateHash, ComparisonSummary: g.ComparisonSummary, SourceChangeCount: int64(len(g.SourceChanges)), IdentityDecisionCount: int64(len(g.IdentityDecisions)), DeletionDecisionCount: int64(len(g.DeletionDecisions)), Summary: ImportSummary{Nodes: int64(len(g.Nodes)), Edges: int64(len(g.Edges)), Evidence: int64(len(g.Evidence)), Unresolved: unresolvedCount(g)}, Diagnostics: diagnostics}
		if err := saveSession(ctx, tx, s); err != nil {
			return err
		}
		if err := savePreview(ctx, tx, s, result, g); err != nil {
			return err
		}
		return r.checkStaging(ctx, tx, pid, 0)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (r *Repo) CommitImport(ctx context.Context, pid, sid string, in CommitImportInput) (*ImportCommitResult, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "import:" + pid + ":" + sid + ":commit"
	result := new(ImportCommitResult)
	readTx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer readTx.Rollback()
	found, err := readImportReceipt(ctx, readTx, scope, in.IdempotencyKey, digest, result)
	if err != nil {
		return nil, err
	}
	if found {
		return result, nil
	}
	// Prepare a consistent staging snapshot outside the write transaction. The
	// version and hash are checked again under the serialized write lock.
	s, err := loadSession(ctx, readTx, pid, sid)
	if err != nil {
		return nil, err
	}
	if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
		return nil, err
	}
	if s.State != "ready" || s.CandidateHash == nil {
		return nil, importConflict("backend_import_state_conflict", "Preview must be ready before commit", s.Version)
	}
	if !validHash(in.CandidateHash) || *s.CandidateHash != in.CandidateHash {
		return nil, importConflict("backend_import_hash_conflict", "Candidate hash differs from saved preview", s.Version)
	}
	g, diagnostics, err := prepareGraph(ctx, readTx, s)
	if err != nil {
		return nil, err
	}
	if len(diagnostics) > 0 {
		return nil, importConflict("backend_import_hash_conflict", "Staged graph differs from ready preview", s.Version)
	}
	b, err := candidateJSON(s, g)
	if err != nil {
		return nil, err
	}
	if hashBytes(b) != in.CandidateHash {
		return nil, importConflict("backend_import_hash_conflict", "Staged graph differs from ready preview", s.Version)
	}
	if err := readTx.Rollback(); err != nil {
		return nil, err
	}
	err = r.importMutation(ctx, scope, in.IdempotencyKey, in, result, func(tx *sql.Tx) error {
		current, err := loadSession(ctx, tx, pid, sid)
		if err != nil {
			return err
		}
		if err := requireSessionVersion(current, in.ExpectedImportVersion); err != nil {
			return err
		}
		if current.State != "ready" || current.CandidateHash == nil || *current.CandidateHash != in.CandidateHash {
			return importConflict("backend_import_hash_conflict", "Ready preview changed before commit", current.Version)
		}
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
		if err != nil {
			return err
		}
		if err := requireProjectVersion(p, in.ExpectedVersion); err != nil {
			return err
		}
		if p.CurrentRevisionID != current.BaseRevisionID {
			return importConflict("backend_import_base_conflict", "Current project revision changed", p.Version)
		}
		if err := requireImportBase(ctx, tx, current); err != nil {
			return err
		}
		now := time.Now().UTC()
		rev := Revision{ID: uuid.NewV7().String(), ProjectID: pid, ParentRevisionID: new(current.BaseRevisionID), SchemaVersion: modelSchemaVersion(current.Profile), SemanticHash: hashBytes(b), SourceSnapshotIDs: sourceIDs(g.Sources), ArtifactPins: []ArtifactPin{}, Coverage: g.Coverage, Author: "agent", Summary: "Source-backed foundation graph import", CreatedAt: now}
		doc, err := json.Marshal(rev)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, rev.ID, pid, string(doc))
		if err != nil {
			return err
		}
		for _, n := range g.Nodes {
			doc, err := json.Marshal(n)
			if err != nil {
				return err
			}
			parent := ""
			if n.ParentID != nil {
				parent = *n.ParentID
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,document) VALUES(?,?,'node',?,?,?,?,?)`, pid, rev.ID, n.ID, n.Kind, n.Name, parent, string(doc)); err != nil {
				return err
			}
		}
		for _, e := range g.Edges {
			doc, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,from_id,to_id,document) VALUES(?,?,'edge',?,?,?,?,?)`, pid, rev.ID, e.ID, e.Kind, e.From, e.To, string(doc)); err != nil {
				return err
			}
		}
		for _, e := range g.Evidence {
			doc, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,subject_id,document) VALUES(?,?,'evidence',?,?,?)`, pid, rev.ID, e.ID, e.SubjectID, string(doc)); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_repositories(id,project_id,logical_name) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, current.RepositoryID, pid, current.Manifest.RepositoryName); err != nil {
			return err
		}
		if err := publishBindings(ctx, tx, current, g, rev.ID); err != nil {
			return err
		}
		coverage := RevisionCoverage{Coverage: g.Coverage, Inventory: current.Inventory, Snapshots: g.Sources, StaleCounts: g.StaleCounts, ReconciliationGaps: g.ReconciliationGaps}
		decisions, err := json.Marshal(struct {
			Identity []IdentityDecision `json:"identity"`
			Deletion []DeletionDecision `json:"deletion"`
		}{g.IdentityDecisions, g.DeletionDecisions})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_revision_decisions(revision_id,document) VALUES(?,?)`, rev.ID, string(decisions)); err != nil {
			return err
		}
		sourceDoc, err := json.Marshal(coverage)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_revision_sources(revision_id,document) VALUES(?,?)`, rev.ID, string(sourceDoc)); err != nil {
			return err
		}
		p.Version++
		p.CurrentRevisionID = rev.ID
		p.UpdatedAt = now
		if current.Mode != "reconcile" {
			p.Repositories = append(p.Repositories, Repository{ID: current.RepositoryID, LogicalName: current.Manifest.RepositoryName})
		}
		if _, err := tx.ExecContext(ctx, `UPDATE backend_projects SET version=?,current_revision_id=?,updated_at=? WHERE id=?`, p.Version, rev.ID, now.Format(time.RFC3339Nano), pid); err != nil {
			return err
		}
		current.State = "committed"
		current.Version++
		current.UpdatedAt = now
		if err := saveSession(ctx, tx, current); err != nil {
			return err
		}
		*result = ImportCommitResult{Project: *p, Revision: rev, SessionID: sid}
		response, _ := json.Marshal(result)
		return r.checkStaging(ctx, tx, pid, int64(len(response)))
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
