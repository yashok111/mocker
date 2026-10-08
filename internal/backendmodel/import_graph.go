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

	"github.com/yashok111/mocker/internal/backendblob"
)

type graphCandidate struct {
	// Admission measures complete retained source6 content; its candidate JSON
	// contains commitments only and is not the graph's semantic byte footprint.
	semanticBytes      int64
	ArtifactContextV3  *ArtifactContextV3  `json:"artifactContextV3,omitzero"`
	Composed           *composedCandidate  `json:"-"`
	ArtifactPins       []ArtifactPin       `json:"artifactPins,omitempty"`
	APIArtifactContext *APIArtifactContext `json:"apiArtifactContext,omitzero"`
	ArtifactContext    *ArtifactContext    `json:"artifactContext,omitzero"`
	Sources            []SourceSnapshot    `json:"sources"`
	SourceChanges      []SourceChange      `json:"sourceChanges"`
	IdentityDecisions  []IdentityDecision  `json:"identityDecisions"`
	DeletionDecisions  []DeletionDecision  `json:"deletionDecisions"`
	ComparisonSummary  *ComparisonSummary  `json:"comparisonSummary"`
	ReconciliationGaps []string            `json:"reconciliationGaps"`
	StaleCounts        StaleCounts         `json:"staleCounts"`
	Nodes              []Node              `json:"nodes"`
	Edges              []Edge              `json:"edges"`
	Evidence           []Evidence          `json:"evidence"`
	Coverage           Coverage            `json:"coverage"`
}

func prepareGraph(ctx context.Context, q importReader, s *ImportSession) (*graphCandidate, []ImportDiagnostic, error) {
	if s.Mode == "composed" {
		candidate, diagnostics, err := prepareComposedGraph(ctx, q, s)
		if err != nil {
			return nil, nil, err
		}
		return candidate.Graph, diagnostics, nil
	}
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
	pr := &graphPreparation{d: []ImportDiagnostic{}, ids: map[string]string{}, present: map[string]bool{}}
	if s.Mode == "reconcile" {
		pr.indexBase(base)
	}
	if err := readImportIdentities(ctx, q, s.ID, pr.ids); err != nil {
		return nil, nil, err
	}
	commands, err := readImportCommands(ctx, q, s.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range commands {
		typ, key, _ := commandAddress(c)
		pr.present[typ+"\x00"+key] = true
	}
	g, err := pr.assemble(s, commands)
	if err != nil {
		return nil, nil, err
	}
	if err := resolveProfileAttributes(s, g, pr.resolve); err != nil {
		return nil, nil, err
	}
	if err := overlayGraph(ctx, q, s, base, g, commands, pr.ids, &pr.d); err != nil {
		return nil, nil, err
	}
	nodes := pr.checkEvidenceLinks(g)
	parents := pr.checkEdgeEndpoints(s, g, nodes)
	pr.checkParents(g, parents)
	if err := validateProfileGraphs(ctx, q, s, g, &pr.d); err != nil {
		return nil, nil, err
	}
	g.Coverage = importCoverage(s, g)
	finishReconciliation(s, g)
	if err := carryAPIArtifactContext(ctx, s, base, g); err != nil {
		return nil, nil, err
	}
	semantic, err := candidateJSON(s, g)
	if err != nil {
		return nil, nil, err
	}
	if revisionOverLimits(g, semantic) {
		return nil, nil, limitFault("Revision semantic limit exceeded")
	}
	g.semanticBytes = int64(len(semantic))
	sortImportDiagnostics(pr.d)
	return g, pr.d, nil
}

func revisionOverLimits(g *graphCandidate, semantic []byte) bool {
	return len(g.Nodes) > MaxRevisionNodes || len(g.Edges) > MaxRevisionEdges || len(g.Evidence) > MaxRevisionEvidence || len(semantic) > MaxRevisionBytes
}

// sortImportDiagnostics orders diagnostics by path, code and message, so
// a preview is byte-stable whatever order the checks ran in.
func sortImportDiagnostics(d []ImportDiagnostic) {
	slices.SortFunc(d, func(a, b ImportDiagnostic) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.Code != b.Code {
			return strings.Compare(a.Code, b.Code)
		}
		return strings.Compare(a.Message, b.Message)
	})
}

// graphPreparation is the state prepareGraph threads through its phases:
// the diagnostics collected so far and the key -> UUID resolution every
// reference in the session goes through.
type graphPreparation struct {
	d       []ImportDiagnostic
	ids     map[string]string // "<type>\x00<external key>" -> UUID
	present map[string]bool   // "<type>\x00<external key>" the final graph will hold
}

func (pr *graphPreparation) add(p, m string) {
	pr.d = append(pr.d, ImportDiagnostic{Code: "backend_graph_invalid", Path: p, Message: m})
}

// indexBase makes every base record resolvable and present: a reconcile
// session may reference what it does not resubmit.
func (pr *graphPreparation) indexBase(base *RevisionState) {
	for _, n := range base.Nodes {
		pr.ids["node\x00"+n.ExternalKey] = n.ID
	}
	for _, e := range base.Edges {
		pr.ids["edge\x00"+e.ExternalKey] = e.ID
	}
	for _, e := range base.Evidence {
		pr.ids["evidence\x00"+e.ExternalKey] = e.ID
	}
	for _, n := range base.Nodes {
		pr.present["node\x00"+n.ExternalKey] = true
	}
	for _, e := range base.Edges {
		pr.present["edge\x00"+e.ExternalKey] = true
	}
	for _, e := range base.Evidence {
		pr.present["evidence\x00"+e.ExternalKey] = true
	}
}

// resolve maps an external key to its UUID and reports a reference to a
// record that will not exist; it is handed to the profile resolvers too.
func (pr *graphPreparation) resolve(typ, key, p string) string {
	address := typ + "\x00" + key
	if !pr.present[address] {
		pr.add(p, "Reference points to a missing "+typ+" record")
	}
	return pr.ids[address]
}

func (pr *graphPreparation) refs(keys []string, p string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, pr.resolve("evidence", k, p))
	}
	return out
}

// assemble turns the staged commands into graph records, resolving every
// key reference; a command that fails validation aborts the preparation.
func (pr *graphPreparation) assemble(s *ImportSession, commands []ImportCommand) (*graphCandidate, error) {
	g := &graphCandidate{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}}
	ids := pr.ids
	for _, c := range commands {
		if err := validateCommand(c, s); err != nil {
			return nil, err
		}
		switch c.Op {
		case "upsert_node":
			n := c.Node
			id := ids["node\x00"+n.ExternalKey]
			var parent *string
			if n.ParentKey != nil {
				parent = new(pr.resolve("node", *n.ParentKey, "nodes/"+id+"/parentId"))
			}
			g.Nodes = append(g.Nodes, Node{ID: id, ExternalKey: n.ExternalKey, Kind: n.Kind, Name: n.Name, ParentID: parent, Attributes: n.Attributes, EvidenceIDs: pr.refs(n.EvidenceKeys, "nodes/"+id+"/evidenceIds")})
		case "upsert_edge":
			e := c.Edge
			id := ids["edge\x00"+e.ExternalKey]
			g.Edges = append(g.Edges, Edge{ID: id, ExternalKey: e.ExternalKey, Kind: e.Kind, From: pr.resolve("node", e.FromKey, "edges/"+id+"/from"), To: pr.resolve("node", e.ToKey, "edges/"+id+"/to"), Attributes: e.Attributes, EvidenceIDs: pr.refs(e.EvidenceKeys, "edges/"+id+"/evidenceIds")})
		case "upsert_evidence":
			e := c.Evidence
			id := ids["evidence\x00"+e.ExternalKey]
			g.Evidence = append(g.Evidence, Evidence{ID: id, ExternalKey: e.ExternalKey, SubjectID: pr.resolve(e.SubjectType, e.SubjectKey, "evidence/"+id+"/subjectId"), PropertyPath: e.PropertyPath, Method: e.Method, Status: e.Status, Source: e.Source, Explanation: e.Explanation, Snippet: e.Snippet})
		}
	}
	return g, nil
}

// resolveProfileAttributes lets each selected profile rewrite the keys
// inside its own attributes into UUIDs, relational first, as before.
func resolveProfileAttributes(s *ImportSession, g *graphCandidate, resolve func(typ, key, p string) string) error {
	profile := selectedProfile(s.Profile)
	if hasRelationalProfile(profile) {
		relational := func(kind string, attrs map[string]jsontext.Value, edge bool, resolve func(typ, key, p string) string) (map[string]jsontext.Value, error) {
			return resolveRelationalAttributes(kind, attrs, edge, s, resolve)
		}
		if err := resolveGraphAttributes(g, true, relational, resolve); err != nil {
			return err
		}
	}
	if hasRuntimeProfile(profile) {
		resolver := resolveRuntimeAttributes
		if profile == EventsProfile {
			resolver = resolveEventsAttributes
		}
		if err := resolveGraphAttributes(g, true, resolver, resolve); err != nil {
			return err
		}
	}
	if profile == LineageProfile {
		lineage := func(kind string, attrs map[string]jsontext.Value, _ bool, resolve func(typ, key, p string) string) (map[string]jsontext.Value, error) {
			return resolveLineageAttributes(kind, attrs, resolve)
		}
		if err := resolveGraphAttributes(g, false, lineage, resolve); err != nil {
			return err
		}
	}
	return nil
}

// resolveGraphAttributes runs one profile resolver over every node and,
// when the profile has edge attributes, every edge, stopping at the first error.
func resolveGraphAttributes(g *graphCandidate, edges bool, resolver func(string, map[string]jsontext.Value, bool, func(typ, key, p string) string) (map[string]jsontext.Value, error), resolve func(typ, key, p string) string) error {
	var err error
	for i := range g.Nodes {
		n := &g.Nodes[i]
		n.Attributes, err = resolver(n.Kind, n.Attributes, false, resolve)
		if err != nil {
			return err
		}
	}
	if !edges {
		return nil
	}
	for i := range g.Edges {
		e := &g.Edges[i]
		e.Attributes, err = resolver(e.Kind, e.Attributes, true, resolve)
		if err != nil {
			return err
		}
	}
	return nil
}

// checkEvidenceLinks requires every subject to cite evidence and every
// evidence record to belong to the subject that cites it. It returns the
// surviving nodes by ID for the endpoint checks that follow.
func (pr *graphPreparation) checkEvidenceLinks(g *graphCandidate) map[string]Node {
	nodes := map[string]Node{}
	subjects := map[string][]string{}
	properties := map[string]any{}
	evidence := map[string]Evidence{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		subjects[n.ID] = n.EvidenceIDs
		properties[n.ID] = n
		if n.Kind != "unresolved_target" && len(n.EvidenceIDs) == 0 {
			pr.add("nodes/"+n.ID+"/evidenceIds", "Known imported nodes require source evidence")
		}
	}
	for _, e := range g.Edges {
		subjects[e.ID] = e.EvidenceIDs
		properties[e.ID] = e
		if len(e.EvidenceIDs) == 0 {
			pr.add("edges/"+e.ID+"/evidenceIds", "Imported edges require source evidence")
		}
	}
	for _, e := range g.Evidence {
		evidence[e.ID] = e
		if !slices.Contains(subjects[e.SubjectID], e.ID) {
			pr.add("evidence/"+e.ID+"/subjectId", "Evidence must be listed by its subject")
		}
		if e.PropertyPath != nil && !pointerExists(properties[e.SubjectID], *e.PropertyPath) {
			pr.add("evidence/"+e.ID+"/propertyPath", "Property path must be a JSON Pointer to an existing subject property")
		}
	}
	for id, refs := range subjects {
		for _, eid := range refs {
			if e, ok := evidence[eid]; !ok || e.SubjectID != id {
				pr.add("subjects/"+id+"/evidenceIds", "Subject evidence must belong to that subject")
			}
		}
	}
	return nodes
}

// checkEdgeEndpoints validates each edge's endpoint kinds and returns the
// contains hierarchy (child -> parent) it implies.
func (pr *graphPreparation) checkEdgeEndpoints(s *ImportSession, g *graphCandidate, nodes map[string]Node) map[string]string {
	parents := map[string]string{}
	for _, e := range g.Edges {
		from, fok := nodes[e.From]
		to, tok := nodes[e.To]
		if !fok || !tok {
			pr.add("edges/"+e.ID, "Edge endpoints must survive in the final graph")
			continue
		}
		valid := foundationEdgeValid(s, e, from, to)
		if e.Kind == "contains" {
			if _, ok := parents[e.To]; ok {
				pr.add("edges/"+e.ID, "A node may have only one incoming contains edge")
			}
			parents[e.To] = e.From
		}
		valid = profileEdgeValid(s, e, from, to, valid)
		if !valid {
			pr.add("edges/"+e.ID, "Edge kind does not support these endpoint kinds")
		}
	}
	return parents
}

// foundationEdgeValid is the foundation graph's endpoint rule per edge
// kind, with the relational profile's say over contains edges.
func foundationEdgeValid(s *ImportSession, e Edge, from, to Node) bool {
	switch e.Kind {
	case "contains":
		valid := slices.Contains([]string{"system", "service", "module"}, from.Kind) || slices.Contains([]string{"external_system", "datastore"}, from.Kind) && slices.Contains([]string{"module", "symbol", "handler"}, to.Kind)
		if hasRelationalProfile(selectedProfile(s.Profile)) {
			if relationalValid, applies := relationalContains(from, to); applies {
				valid = relationalValid
			}
		}
		return valid
	case "handles":
		return from.Kind == "http_operation" && slices.Contains([]string{"handler", "symbol", "unresolved_target"}, to.Kind)
	case "calls":
		return slices.Contains([]string{"symbol", "handler"}, from.Kind) && slices.Contains([]string{"symbol", "handler", "external_system", "unresolved_target"}, to.Kind)
	case "references":
		return hasRelationalProfile(selectedProfile(s.Profile)) && from.Kind == "constraint" && (to.Kind == "table" || to.Kind == "unresolved_target")
	case "derived_from":
		return slices.Contains([]string{"symbol", "module", "unresolved_target"}, to.Kind)
	}
	return false
}

// profileEdgeValid lets the runtime, lineage and events profiles override
// the foundation verdict for the edges they define, in that order.
func profileEdgeValid(s *ImportSession, e Edge, from, to Node, valid bool) bool {
	profile := selectedProfile(s.Profile)
	if hasRuntimeProfile(profile) {
		if runtimeValid, applies := runtimeEndpoints(e, from, to); applies {
			valid = runtimeValid
		}
	}
	if hasLineageProfile(profile) && e.Kind == "contains" {
		if lineageValid, applies := lineageContains(from, to); applies {
			valid = lineageValid
		}
	}
	if profile == EventsProfile {
		if eventValid, applies := eventsEndpoints(e, from, to); applies {
			valid = eventValid
		}
	}
	return valid
}

// checkParents requires every declared parent to agree with the contains
// edges and the contains hierarchy to be acyclic.
func (pr *graphPreparation) checkParents(g *graphCandidate, parents map[string]string) {
	for _, n := range g.Nodes {
		if n.ParentID != nil && parents[n.ID] != *n.ParentID {
			pr.add("nodes/"+n.ID+"/parentId", "Provided parent must agree with its sole incoming contains edge")
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
			pr.add("nodes/"+n.ID, "Contains hierarchy must be acyclic")
			break
		}
	}
}

// validateProfileGraphs runs each selected profile's whole-graph rules.
func validateProfileGraphs(ctx context.Context, q importReader, s *ImportSession, g *graphCandidate, d *[]ImportDiagnostic) error {
	profile := selectedProfile(s.Profile)
	if hasRelationalProfile(profile) {
		if err := validateRelationalGraph(ctx, q, s, g, d); err != nil {
			return err
		}
	}
	if hasRuntimeProfile(profile) {
		if err := validateRuntimeGraph(ctx, q, s, g, d); err != nil {
			return err
		}
	}
	if hasLineageProfile(profile) {
		if err := validateLineageGraph(ctx, s, g, d); err != nil {
			return err
		}
	}
	if profile == EventsProfile {
		if err := validateEventsGraph(ctx, s, g, d); err != nil {
			return err
		}
	}
	return nil
}

// importCoverage is complete only when every inventory category is, the
// snapshot is verified and nothing stayed unresolved.
func importCoverage(s *ImportSession, g *graphCandidate) Coverage {
	c := Coverage{Status: "complete", KnownObjects: int64(len(g.Nodes)), Gaps: []string{}}
	for _, x := range s.Inventory {
		if x.Status != "complete" {
			c.Status = "partial"
			c.Gaps = append(c.Gaps, x.Category+": "+x.Status)
			c.Gaps = append(c.Gaps, x.Gaps...)
			if x.Reason != "" {
				c.Gaps = append(c.Gaps, x.Reason)
			}
		}
	}
	if s.Manifest.Snapshot.Consistency != "verified" {
		c.Status = "partial"
		c.Gaps = append(c.Gaps, "Source snapshot consistency is unverified.")
	}
	if unresolvedCount(g) > 0 {
		c.Status = "partial"
		c.Gaps = append(c.Gaps, "Unresolved nodes or evidence remain.")
	}
	return c
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
		k, ok := unescapePointerToken(part)
		if !ok {
			return false
		}
		if current, ok = pointerChild(current, k); !ok {
			return false
		}
	}
	return true
}

// unescapePointerToken decodes RFC 6901 "~0" and "~1"; any other "~"
// sequence makes the pointer invalid.
func unescapePointerToken(part string) (string, bool) {
	var key strings.Builder
	for i := 0; i < len(part); i++ {
		if part[i] != '~' {
			key.WriteByte(part[i])
			continue
		}
		i++
		if i == len(part) || part[i] != '0' && part[i] != '1' {
			return "", false
		}
		if part[i] == '0' {
			key.WriteByte('~')
		} else {
			key.WriteByte('/')
		}
	}
	return key.String(), true
}

// pointerChild steps one pointer token into an object member or a
// canonical array index.
func pointerChild(current jsontext.Value, k string) (jsontext.Value, bool) {
	if len(current) == 0 {
		return nil, false
	}
	switch current[0] {
	case '{':
		var m map[string]jsontext.Value
		if json.Unmarshal(current, &m) != nil {
			return nil, false
		}
		child, ok := m[k]
		return child, ok
	case '[':
		var a []jsontext.Value
		if json.Unmarshal(current, &a) != nil {
			return nil, false
		}
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i >= len(a) || strconv.Itoa(i) != k {
			return nil, false
		}
		return a[i], true
	}
	return nil, false
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
	if g.Composed != nil {
		return source6CandidateJSON(s, g.Composed)
	}
	if g.ArtifactContext != nil {
		if _, err := EncodeArtifactContext(*g.ArtifactContext, g.ArtifactPins); err != nil {
			return nil, err
		}
	}
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
		var semanticBytes *int64
		if len(diagnostics) == 0 {
			b, err := candidateJSON(s, g)
			if err != nil {
				return err
			}
			s.CandidateHash = new(hashBytes(b))
			semanticBytes = new(g.semanticBytes)
			s.State = "ready"
		}
		s.UpdatedAt = time.Now().UTC()
		*result = ImportPreview{ModelSchemaVersion: modelSchemaVersion(s.Profile), ProfileExtension: s.ProfileExtension, SessionID: s.ID, Version: s.Version, State: s.State, CandidateHash: s.CandidateHash, ComparisonSummary: g.ComparisonSummary, SourceChangeCount: int64(len(g.SourceChanges)), IdentityDecisionCount: int64(len(g.IdentityDecisions)), DeletionDecisionCount: int64(len(g.DeletionDecisions)), Summary: ImportSummary{Nodes: int64(len(g.Nodes)), Edges: int64(len(g.Edges)), Evidence: int64(len(g.Evidence)), Unresolved: unresolvedCount(g)}, Diagnostics: diagnostics}
		if g.Composed != nil {
			result.AffectedScope = g.Composed.IncrementalScope
		}
		result.Preflight, err = PlanImport(ImportPreflightInput{Profile: selectedProfile(s.Profile), Counts: ImportCardinalities{Nodes: result.Summary.Nodes, Edges: result.Summary.Edges, Evidence: result.Summary.Evidence}, SemanticBytes: semanticBytes, Surfaces: []string{"graph", "events", "data_access", "lineage", "architecture"}})
		if err != nil {
			return err
		}
		result.Preflight.Basis = "candidate"
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
	defer func() { _ = readTx.Rollback() }()
	found, err := readImportReceipt(ctx, readTx, scope, in.IdempotencyKey, digest, result)
	if err != nil {
		return nil, err
	}
	if found {
		return result, nil
	}
	// Prepare a consistent staging snapshot outside the write transaction. The
	// version and hash are checked again under the serialized write lock.
	g, b, err := stageImportCommit(ctx, readTx, pid, sid, in)
	if err != nil {
		return nil, err
	}
	if err := readTx.Rollback(); err != nil {
		return nil, err
	}
	err = r.importMutation(ctx, scope, in.IdempotencyKey, in, result, func(tx *sql.Tx) error {
		return publishImportCommit(ctx, tx, pid, sid, in, g, b, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// stageImportCommit rebuilds the ready preview's graph from a read snapshot
// and proves it is byte-identical to what the preview hashed. It returns
// the graph and its candidate JSON for the write transaction to publish.
func stageImportCommit(ctx context.Context, readTx *sql.Tx, pid, sid string, in CommitImportInput) (*graphCandidate, []byte, error) {
	s, err := loadSession(ctx, readTx, pid, sid)
	if err != nil {
		return nil, nil, err
	}
	if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
		return nil, nil, err
	}
	if s.State != "ready" || s.CandidateHash == nil {
		return nil, nil, importConflict("backend_import_state_conflict", "Preview must be ready before commit", s.Version)
	}
	if !validHash(in.CandidateHash) || *s.CandidateHash != in.CandidateHash {
		return nil, nil, importConflict("backend_import_hash_conflict", "Candidate hash differs from saved preview", s.Version)
	}
	g, diagnostics, err := prepareGraph(ctx, readTx, s)
	if err != nil {
		return nil, nil, err
	}
	if len(diagnostics) > 0 {
		return nil, nil, importConflict("backend_import_hash_conflict", "Staged graph differs from ready preview", s.Version)
	}
	b, err := candidateJSON(s, g)
	if err != nil {
		return nil, nil, err
	}
	if hashBytes(b) != in.CandidateHash {
		return nil, nil, importConflict("backend_import_hash_conflict", "Staged graph differs from ready preview", s.Version)
	}
	return g, b, nil
}

// publishImportCommit is the serialized half of CommitImport: it rechecks
// the session and project under the write lock, then writes the revision,
// moves the project head and closes the session.
func publishImportCommit(ctx context.Context, tx *sql.Tx, pid, sid string, in CommitImportInput, g *graphCandidate, b []byte, result *ImportCommitResult) error {
	current, p, err := loadImportCommitTarget(ctx, tx, pid, sid, in)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	rev, err := importedRevision(pid, current, g, b, now)
	if err != nil {
		return err
	}
	if err := writeImportedRevision(ctx, tx, pid, rev, g); err != nil {
		return err
	}
	if err := writeImportedRevisionContext(ctx, tx, pid, rev.ID, current, g); err != nil {
		return err
	}
	p.Version++
	p.CurrentRevisionID = rev.ID
	p.UpdatedAt = now
	if current.Mode != "reconcile" && (current.Mode != "composed" || current.SourceScope.Kind == "add_repository") {
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
	// No staging check here: the commit closes the session, so it only
	// frees staging, and the revision it wrote is bounded by the revision
	// limits. The check used to run after every revision row was written
	// and rolled a finished commit back once the project's lifetime sum
	// crossed the cap (review 2026-10-06, F83/F172).
	return nil
}

func loadImportCommitTarget(ctx context.Context, tx *sql.Tx, pid, sid string, in CommitImportInput) (*ImportSession, *Project, error) {
	current, err := loadSession(ctx, tx, pid, sid)
	if err != nil {
		return nil, nil, err
	}
	if err := requireSessionVersion(current, in.ExpectedImportVersion); err != nil {
		return nil, nil, err
	}
	if current.State != "ready" || current.CandidateHash == nil || *current.CandidateHash != in.CandidateHash {
		return nil, nil, importConflict("backend_import_hash_conflict", "Ready preview changed before commit", current.Version)
	}
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
	if err != nil {
		return nil, nil, err
	}
	if err := requireProjectVersion(p, in.ExpectedVersion); err != nil {
		return nil, nil, err
	}
	if p.CurrentRevisionID != current.BaseRevisionID {
		return nil, nil, importConflict("backend_import_base_conflict", "Current project revision changed", p.Version)
	}
	if err := requireImportBase(ctx, tx, current); err != nil {
		return nil, nil, err
	}
	return current, p, nil
}

// importedRevision is the revision header a commit writes; composed and
// artifact-pinned graphs hash their semantics their own way.
func importedRevision(pid string, current *ImportSession, g *graphCandidate, b []byte, now time.Time) (Revision, error) {
	rev := Revision{ID: uuid.NewV7().String(), ProjectID: pid, ParentRevisionID: new(current.BaseRevisionID), SchemaVersion: modelSchemaVersion(current.Profile), SemanticHash: hashBytes(b), SourceSnapshotIDs: sourceIDs(g.Sources), ArtifactPins: []ArtifactPin{}, Coverage: g.Coverage, Author: "agent", Summary: "Source-backed foundation graph import", CreatedAt: now}
	var err error
	if g.Composed != nil {
		rev.SemanticHash, err = source6SemanticHash(g.Composed.Source)
		if err != nil {
			return rev, err
		}
		rev.ArtifactPins = g.ArtifactPins
	} else if len(g.ArtifactPins) > 0 {
		rev.ArtifactPins = g.ArtifactPins
		rev.SemanticHash, err = importedArtifactSemanticHash(g)
		if err != nil {
			return rev, err
		}
	}
	return rev, nil
}

// writeImportedRevision writes the revision row and every graph record.
func writeImportedRevision(ctx context.Context, tx *sql.Tx, pid string, rev Revision, g *graphCandidate) error {
	doc, err := json.Marshal(rev)
	if err != nil {
		return err
	}
	_, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, rev.ID, pid, string(doc))
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
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,document) VALUES(?,?,'node',?,?,?,?,?)`, pid, rev.ID, n.ID, n.Kind, n.Name, parent, string(doc)); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		doc, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,from_id,to_id,document) VALUES(?,?,'edge',?,?,?,?,?)`, pid, rev.ID, e.ID, e.Kind, e.From, e.To, string(doc)); err != nil {
			return err
		}
	}
	for _, e := range g.Evidence {
		doc, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if g.Composed != nil {
			doc = g.Composed.Source.RawEvidence[e.ID]
		}
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,subject_id,document) VALUES(?,?,'evidence',?,?,?)`, pid, rev.ID, e.ID, e.SubjectID, string(doc)); err != nil {
			return err
		}
	}
	return nil
}

// writeImportedRevisionContext writes everything a revision carries beside
// its records: the repository, the identity bindings, the decisions, the
// source coverage and the artifact context.
func writeImportedRevisionContext(ctx context.Context, tx *sql.Tx, pid, revisionID string, current *ImportSession, g *graphCandidate) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_repositories(id,project_id,logical_name) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, current.RepositoryID, pid, current.Manifest.RepositoryName); err != nil {
		return err
	}
	if err := publishBindings(ctx, tx, current, g, revisionID); err != nil {
		return err
	}
	coverage := RevisionCoverage{Coverage: g.Coverage, Inventory: current.Inventory, Snapshots: g.Sources, StaleCounts: g.StaleCounts, ReconciliationGaps: g.ReconciliationGaps}
	if g.Composed != nil {
		if err := saveComposedContext(ctx, tx, pid, revisionID, g.Composed); err != nil {
			return err
		}
	}
	decisions, err := json.Marshal(struct {
		Identity []IdentityDecision `json:"identity"`
		Deletion []DeletionDecision `json:"deletion"`
	}{g.IdentityDecisions, g.DeletionDecisions})
	if err == nil && g.Composed != nil {
		decisions, err = source6RevisionDecisions(current, g.Composed)
	}
	if err != nil {
		return err
	}
	if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_revision_decisions(revision_id,document) VALUES(?,?)`, revisionID, string(decisions)); err != nil {
		return err
	}
	sourceDoc, err := json.Marshal(coverage)
	if err == nil && g.Composed != nil {
		sourceDoc, err = json.Marshal(SourceRevisionContext{RevisionCoverage: coverage, ViewSchemaVersion: ComposedSchemaVersion, SourceVector: *g.Composed.Source.SourceVector, ClaimCurrentness: g.Composed.Source.Currentness, SourceContentHash: g.Composed.Source.SourceContentHash})
	}
	if err != nil {
		return err
	}
	if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_revision_sources(revision_id,document) VALUES(?,?)`, revisionID, string(sourceDoc)); err != nil {
		return err
	}
	return saveImportedArtifactContext(ctx, tx, revisionID, g)
}

func importedArtifactSemanticHash(g *graphCandidate) (string, error) {
	if g.ArtifactContextV3 != nil {
		return ArtifactContextV3SemanticHash(*g.ArtifactContextV3)
	}
	if g.ArtifactContext != nil {
		c := g.ArtifactContext
		return ArtifactSemanticHash(c.SourceContentHash, c.SourceSemanticHash, g.ArtifactPins, c.APIBindings, c.EditorBindings)
	} else {
		return APIArtifactSemanticHash(g.APIArtifactContext.SourceContentHash, g.APIArtifactContext.SourceSemanticHash, g.ArtifactPins, g.APIArtifactContext.Bindings)
	}
}

func saveImportedArtifactContext(ctx context.Context, tx *sql.Tx, revisionID string, g *graphCandidate) error {
	if c := g.ArtifactContextV3; c != nil {
		raw, err := EncodeArtifactContextV3(*c)
		if err != nil {
			return err
		}
		_, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES(?,?,?,?)`, revisionID, c.SourceContentHash, c.SourceSemanticHash, string(raw))
		return err
	}
	if g.ArtifactContext != nil {
		if err := saveArtifactContext(ctx, tx, revisionID, *g.ArtifactContext, g.ArtifactPins); err != nil {
			return err
		}
	} else if g.APIArtifactContext != nil {
		if err := saveAPIArtifactContext(ctx, tx, revisionID, *g.APIArtifactContext); err != nil {
			return err
		}
	}
	return nil
}

// readImportIdentities folds the identities this session allocated into ids,
// keyed "<record type>\x00<external key>" the way prepareGraph resolves them.
func readImportIdentities(ctx context.Context, q importReader, sessionID string, ids map[string]string) error {
	rows, err := q.QueryContext(ctx, `SELECT record_type,external_key,id FROM backend_import_identities WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var typ, key, id string
		if err := rows.Scan(&typ, &key, &id); err != nil {
			return err
		}
		ids[typ+"\x00"+key] = id
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

// readImportCommands returns the session's staged commands in identity
// allocation order, the order prepareGraph must replay them in.
func readImportCommands(ctx context.Context, q importReader, sessionID string) ([]ImportCommand, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.document FROM backend_import_records r JOIN backend_import_identities i ON i.session_id=r.session_id AND i.record_type=r.record_type AND i.external_key=r.external_key WHERE r.session_id=? ORDER BY i.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	commands := []ImportCommand{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var c ImportCommand
		if err := json.Unmarshal([]byte(b), &c); err != nil {
			return nil, err
		}
		commands = append(commands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return commands, rows.Close()
}
