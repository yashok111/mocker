package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

type DatabaseQueryInput struct {
	ResponseMode                string                     `json:"responseMode,omitempty"`
	Section                     string                     `json:"section,omitempty"`
	ChangeProposal              *ProposalReadTarget        `json:"changeProposal,omitzero"`
	ImportCandidate             *ImportCandidateReadTarget `json:"importCandidate,omitzero"`
	RevisionID                  string                     `json:"revisionId,omitempty"`
	Proposal                    *ProposalReadTarget        `json:"proposal,omitzero"`
	DatastoreID                 string                     `json:"datastoreId"`
	FacetKey                    string                     `json:"facetKey"`
	RecordType                  string                     `json:"recordType"`
	Search                      string                     `json:"search,omitempty"`
	TableID                     string                     `json:"tableId,omitempty"`
	Limit                       int                        `json:"limit,omitzero"`
	Cursor                      string                     `json:"cursor,omitempty"`
	searchPresent, tablePresent bool
}

// UnmarshalJSON retains optional-selector presence: even an empty forbidden
// selector is invalid. Direct Go zero limit means omitted; wire zero never does.
func (in *DatabaseQueryInput) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("body", "Expected a strict database query object")
	}
	type input DatabaseQueryInput
	var decoded input
	if err := decodeReadQuery(b, []string{"datastoreId", "facetKey", "recordType"}, []string{"search", "tableId", "limit", "cursor", "responseMode", "section"}, &decoded); err != nil {
		return err
	}
	*in = DatabaseQueryInput(decoded)
	if _, present := m["responseMode"]; present && in.ResponseMode == "" {
		return invalid("responseMode", "Explicit responseMode cannot be empty")
	}
	if _, present := m["section"]; present && in.Section == "" {
		return invalid("section", "Explicit section cannot be empty")
	}
	_, in.searchPresent = m["search"]
	_, in.tablePresent = m["tableId"]
	return nil
}
func (in DatabaseQueryInput) MarshalJSON() ([]byte, error) {
	type input DatabaseQueryInput
	b, err := json.Marshal(input(in))
	if err != nil {
		return nil, err
	}
	m, err := relationalObject(b)
	if err != nil {
		return nil, err
	}
	if in.searchPresent {
		m["search"], err = json.Marshal(in.Search)
		if err != nil {
			return nil, err
		}
	}
	if in.tablePresent {
		m["tableId"], err = json.Marshal(in.TableID)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(m)
}

type Cardinality struct {
	Min   *int64   `json:"min"`
	Max   *string  `json:"max"`
	Basis []string `json:"basis"`
}
type DatabaseColumnPair struct {
	FromColumnID string `json:"fromColumnId"`
	ToColumnID   string `json:"toColumnId"`
}
type TableItem struct {
	TableID       string   `json:"tableId"`
	SchemaID      string   `json:"schemaId"`
	QualifiedName string   `json:"qualifiedName"`
	ColumnCount   int64    `json:"columnCount"`
	FacetKeys     []string `json:"facetKeys"`
	DriftStatus   string   `json:"driftStatus"`
}
type RelationshipItem struct {
	RuntimeStatus     string               `json:"runtimeStatus,omitempty"`
	EffectiveFacet    *EffectiveFacet      `json:"effectiveFacet,omitzero"`
	EdgeID            string               `json:"edgeId"`
	ConstraintID      string               `json:"constraintId"`
	SourceTableID     string               `json:"sourceTableId"`
	TargetTableID     *string              `json:"targetTableId"`
	ColumnPairs       []DatabaseColumnPair `json:"columnPairs"`
	EvidenceIDs       []string             `json:"evidenceIds"`
	SourceCardinality Cardinality          `json:"sourceCardinality"`
	TargetCardinality Cardinality          `json:"targetCardinality"`
	Status            string               `json:"status"`
	TargetReason      *string              `json:"targetReason"`
}
type DatabasePage struct {
	CoverageSummary   *CoverageSummary           `json:"coverageSummary,omitzero"`
	LimitationSummary *DatabaseLimitationSummary `json:"limitationSummary,omitzero"`
	LimitationItems   []DatabaseLimitation       `json:"limitationItems,omitempty"`
	Target            *BackendReadTarget         `json:"target,omitzero"`
	Pins              *EffectiveGraphPins        `json:"pins,omitzero"`
	ViewSchemaVersion string                     `json:"viewSchemaVersion,omitempty"`
	ProposalPins      *ProposalReadPins          `json:"proposalPins,omitzero"`
	ProjectID         string                     `json:"projectId"`
	RevisionID        string                     `json:"revisionId"`
	SemanticHash      string                     `json:"semanticHash"`
	DatastoreID       string                     `json:"datastoreId"`
	FacetKey          string                     `json:"facetKey"`
	RecordType        string                     `json:"recordType"`
	Coverage          RevisionCoverage           `json:"coverage"`
	FacetStatus       string                     `json:"facetStatus"`
	Limitations       []string                   `json:"limitations"`
	TableItems        []TableItem                `json:"tableItems"`
	RelationshipItems []RelationshipItem         `json:"relationshipItems"`
	NextCursor        string                     `json:"nextCursor"`
}

type databaseProjection struct {
	limitationDetails map[string]DatabaseLimitation
	effective         *EffectiveGraphSnapshot
	source            *SourceGraphSnapshot
	sourceProof       map[*relationalFacet]lineageProof
	proposal          *resolvedBackendTarget
	in                DatabaseQueryInput
	nodes             map[string]Node
	children          map[string][]Node
	facets            map[string]map[string]*relationalFacet
	evidence          map[string]Evidence
	stores            map[string]string
	page              *DatabasePage
	observed          map[string]bool
	limitations       map[string]bool
	uniqueResults     map[string]databaseUniqueness
}
type databaseUniqueness struct {
	unique, complete bool
	basis            []string
}

func (p *databaseProjection) selected(id string) *relationalFacet { return p.facets[id][p.in.FacetKey] }
func (p *databaseProjection) limitation(subject, code, message string) {
	p.recordLimitation(subject, code, message)
	if !p.limitations[message] {
		p.limitations[message] = true
		p.page.Limitations = append(p.page.Limitations, message)
	}
	p.page.FacetStatus = "unknown"
}
func (p *databaseProjection) stale(subject, code, message string) {
	p.recordLimitation(subject, code, message)
	if !p.limitations[message] {
		p.limitations[message] = true
		p.page.Limitations = append(p.page.Limitations, message)
	}
	if p.page.FacetStatus == "current" {
		p.page.FacetStatus = "stale"
	}
}
func (p *databaseProjection) observe(id string) *relationalFacet {
	f := p.selected(id)
	if p.observed[id] {
		return f
	}
	p.observed[id] = true
	if f == nil {
		p.limitation(id, "missing_facet", "Missing selected facet proof for "+id)
		return nil
	}
	if p.source != nil || p.effective != nil {
		return p.observeSourceFacet(id, f)
	}
	if p.designedSubject(id) {
		// A designed record has no analysis or proof to be incomplete;
		// the page already carries the desired-structure limitation, and
		// flagging it here made every proposal page "Incomplete selected
		// analysis" with facetStatus unknown (F81).
		return f
	}
	if f.Freshness != nil && f.Freshness.Status == "stale" {
		p.stale(id, "stale_facet", "Stale selected facet for "+id)
	}
	if f.AnalysisStatus != "complete" || f.ColumnsStatus != "" && f.ColumnsStatus != "complete" || f.ConstraintsStatus != "" && f.ConstraintsStatus != "complete" {
		p.limitation(id, "incomplete_analysis", "Incomplete selected analysis for "+id+": "+strings.Join(f.Gaps, "; "))
	}
	for _, proofID := range f.EvidenceIDs {
		e, ok := p.evidence[proofID]
		if !ok || e.Status != "explicit" || e.Source.SnapshotID != f.SourceSnapshotID {
			p.limitation(id, "missing_evidence", "Missing or inferred selected proof "+proofID+" for "+id)
		} else if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.stale(id, "stale_evidence", "Stale selected proof "+proofID+" for "+id)
		}
	}
	return f
}

// designedSubject reports a record the proposal creates: it has an overlay
// and no base, so its facet is synthetic — desired values, no freshness, no
// evidence, no analysis status (review 2026-10-06, F81).
func (p *databaseProjection) designedSubject(id string) bool {
	if p.proposal == nil {
		return false
	}
	o := p.proposal.overlay(id)
	return o != nil && o.Base == nil
}

func (p *databaseProjection) proofCurrent(f *relationalFacet) bool {
	if p.source != nil || p.effective != nil {
		proof, ok := p.sourceProof[f]
		return ok && (proof.status == "explicit" || proof.status == "desired") && !proof.boundary
	}
	if f == nil || f.Freshness == nil || f.Freshness.Status != "current" || len(f.EvidenceIDs) == 0 {
		return false
	}
	for _, id := range f.EvidenceIDs {
		e, ok := p.evidence[id]
		if !ok || e.Status != "explicit" || e.Source.SnapshotID != f.SourceSnapshotID || e.Freshness != nil && e.Freshness.Status != "current" {
			return false
		}
	}
	return true
}
func (p *databaseProjection) proofStale(f *relationalFacet) bool {
	if p.source != nil || p.effective != nil {
		proof, ok := p.sourceProof[f]
		return ok && proof.status == "stale"
	}
	if f == nil {
		return false
	}
	if f.Freshness != nil && f.Freshness.Status == "stale" {
		return true
	}
	for _, id := range f.EvidenceIDs {
		if e := p.evidence[id]; e.Freshness != nil && e.Freshness.Status == "stale" {
			return true
		}
	}
	return false
}

func (r *Repo) QueryDatabase(ctx context.Context, pid string, in DatabaseQueryInput) (*DatabasePage, error) {
	return r.queryDatabaseWithEffective(ctx, pid, in, nil)
}
func (r *Repo) queryDatabaseWithEffective(ctx context.Context, pid string, in DatabaseQueryInput, effective *EffectiveGraphSnapshot) (*DatabasePage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseCompactInput(in); err != nil {
		return nil, err
	}
	target, effective, err := r.resolveDatabaseTarget(ctx, pid, in, effective)
	if err != nil {
		return nil, err
	}
	if err := validateDatabaseSelection(&in, target); err != nil {
		return nil, err
	}
	state, err := r.databaseState(ctx, pid, in.RevisionID, effective)
	if err != nil {
		return nil, err
	}
	p, err := r.newDatabaseProjection(ctx, pid, in, effective, target, state)
	if err != nil {
		return nil, err
	}
	if target.proposal != nil {
		overlayDesignedStructure(state, target)
	}
	if err := p.index(ctx, state); err != nil {
		return nil, err
	}
	coverage, summary, err := r.databaseReadCoverage(ctx, pid, in)
	if err != nil {
		return nil, err
	}
	p.page = &DatabasePage{ProjectID: pid, RevisionID: in.RevisionID, SemanticHash: state.Revision.SemanticHash, DatastoreID: in.DatastoreID, FacetKey: in.FacetKey, RecordType: in.RecordType, Coverage: *coverage, FacetStatus: "current", Limitations: []string{}, TableItems: []TableItem{}, RelationshipItems: []RelationshipItem{}}
	p.page.CoverageSummary = summary
	if effective != nil {
		p.page.Target = new(effective.Target)
		p.page.Pins = new(effective.Pins)
		p.page.SemanticHash = effective.Pins.EffectiveSemanticHash
	}
	if err := p.decodeSelection(ctx, state); err != nil {
		return nil, err
	}
	if target.proposal != nil {
		if err := p.applyDesiredFacets(); err != nil {
			return nil, err
		}
	}
	p.observe(in.DatastoreID)
	if err := p.collectTables(ctx, state.Nodes); err != nil {
		return nil, err
	}
	if in.RecordType == "relationships" {
		if err := p.collectRelationships(ctx, state.Edges); err != nil {
			return nil, err
		}
	}
	scope, err := databaseCursorScope(pid, in, state.Revision.SemanticHash, target, effective)
	if err != nil {
		return nil, err
	}
	return p.finishDatabasePage(ctx, pid, scope)
}

// resolveDatabaseTarget picks the graph a database read projects: a composed
// revision and every staged target read through the effective graph, every
// other read through the plain revision or proposal target. It returns the
// effective graph it resolved, or the one the caller passed in.
func (r *Repo) resolveDatabaseTarget(ctx context.Context, pid string, in DatabaseQueryInput, effective *EffectiveGraphSnapshot) (*resolvedBackendTarget, *EffectiveGraphSnapshot, error) {
	var err error
	if effective == nil && in.RevisionID != "" && in.Proposal == nil && in.ChangeProposal == nil {
		revision, e := r.Revision(ctx, pid, in.RevisionID)
		if e != nil {
			return nil, nil, e
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			effective, err = r.ResolveEffectiveGraph(ctx, pid, BackendReadTarget{RevisionID: in.RevisionID})
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if effective != nil {
		return &resolvedBackendTarget{revisionID: effective.State.Revision.ID}, effective, nil
	}
	if in.ChangeProposal != nil || in.ImportCandidate != nil {
		selector := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
		if err := rejectStagedView(selector); err != nil {
			return nil, nil, err
		}
		effective, err = r.ResolveEffectiveGraph(ctx, pid, selector)
		if err != nil {
			return nil, nil, err
		}
		return &resolvedBackendTarget{revisionID: effective.State.Revision.ID}, effective, nil
	}
	target, err := r.resolveBackendTarget(ctx, pid, BackendReadTarget{RevisionID: in.RevisionID, Proposal: in.Proposal})
	if err != nil {
		return nil, nil, err
	}
	return target, nil, nil
}

// validateDatabaseSelection checks the request against its resolved target
// and pins in.RevisionID to the revision the target resolved to. The
// proposal baseline check runs first: it is the one rule that needs the target.
func validateDatabaseSelection(in *DatabaseQueryInput, target *resolvedBackendTarget) error {
	if target.proposal != nil && (in.DatastoreID != target.proposal.DatastoreID || in.FacetKey != target.proposal.FacetKey) {
		return invalid("selection", "Proposal datastore and facet must match its baseline selection")
	}
	in.RevisionID = target.revisionID
	if !ValidID(in.DatastoreID) {
		return notFound()
	}
	if !externalKey(in.FacetKey) {
		return invalid("facetKey", "Use a valid relational facet key")
	}
	if !slices.Contains([]string{"tables", "relationships"}, in.RecordType) {
		return invalid("recordType", "Select tables or relationships")
	}
	return validateDatabaseSelectors(*in)
}

// validateDatabaseSelectors judges the optional table and search selectors;
// presence, not value, decides whether a selector is legal for the record type.
func validateDatabaseSelectors(in DatabaseQueryInput) error {
	if in.RecordType == "tables" && (in.TableID != "" || in.tablePresent) || in.RecordType == "relationships" && (in.Search != "" || in.searchPresent) {
		return invalid("selectors", "Selectors are not valid for this record type")
	}
	if !utf8.ValidString(in.Search) || len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return invalid("search", "Invalid search string")
	}
	if in.tablePresent && in.TableID == "" || in.TableID != "" && !ValidID(in.TableID) {
		return invalid("tableId", "Table selector must be a canonical UUID")
	}
	return nil
}

func (r *Repo) databaseState(ctx context.Context, pid, revisionID string, effective *EffectiveGraphSnapshot) (*RevisionState, error) {
	if effective != nil {
		return new(effective.State), nil
	}
	return loadRevisionState(ctx, r.db.R, pid, revisionID)
}

// newDatabaseProjection builds the empty projection; a composed revision read
// without an effective graph still proves its facets through the source graph.
func (r *Repo) newDatabaseProjection(ctx context.Context, pid string, in DatabaseQueryInput, effective *EffectiveGraphSnapshot, target *resolvedBackendTarget, state *RevisionState) (*databaseProjection, error) {
	p := &databaseProjection{effective: effective, proposal: target, in: in, nodes: map[string]Node{}, children: map[string][]Node{}, facets: map[string]map[string]*relationalFacet{}, evidence: map[string]Evidence{}, stores: map[string]string{}, observed: map[string]bool{}, limitations: map[string]bool{}, uniqueResults: map[string]databaseUniqueness{}}
	if effective != nil {
		p.source = effective.Source
		p.sourceProof = map[*relationalFacet]lineageProof{}
	} else if state.Revision.SchemaVersion == ComposedSchemaVersion {
		var err error
		p.source, err = r.ResolveSourceGraph(ctx, pid, in.RevisionID)
		if err != nil {
			return nil, err
		}
		p.sourceProof = map[*relationalFacet]lineageProof{}
	}
	return p, nil
}

// overlayDesignedStructure adds the records a proposal designs and re-points
// the edges it moves, on copies of the state's slices, in ID order.
func overlayDesignedStructure(state *RevisionState, target *resolvedBackendTarget) {
	state.Nodes = append([]Node{}, state.Nodes...)
	state.Edges = append([]Edge{}, state.Edges...)
	for _, o := range target.draft.Overlays {
		if o.Base == nil {
			if o.RecordType == "node" {
				state.Nodes = append(state.Nodes, Node{ID: o.SubjectID, Kind: o.Kind, Name: o.Name, ParentID: o.ParentID})
			} else {
				state.Edges = append(state.Edges, Edge{ID: o.SubjectID, Kind: o.Kind, From: o.FromID, To: o.ToID})
			}
		} else if o.RecordType == "edge" {
			for i := range state.Edges {
				if state.Edges[i].ID == o.SubjectID {
					state.Edges[i].From, state.Edges[i].To = o.FromID, o.ToID
				}
			}
		}
	}
	slices.SortFunc(state.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(state.Edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
}

// index builds the node tree and each node's datastore, refusing a read
// whose datastore or table selector does not resolve inside this revision.
func (p *databaseProjection) index(ctx context.Context, state *RevisionState) error {
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.nodes[n.ID] = n
		if n.ParentID != nil {
			p.children[*n.ParentID] = append(p.children[*n.ParentID], n)
		}
	}
	ds, ok := p.nodes[p.in.DatastoreID]
	if !ok {
		return notFound()
	}
	if (p.effective == nil && !isRelationalSchema(state.Revision.SchemaVersion) && state.Revision.SchemaVersion != ComposedSchemaVersion) || ds.Kind != "datastore" || !relationalSubject(ds.Kind, ds.Attributes, false) {
		return &FaultError{Status: 422, Code: "backend_relational_unavailable", Message: "Pinned revision has no relational datastore descriptor"}
	}
	for id := range p.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.storeOf(id)
	}
	if p.in.TableID != "" {
		n, ok := p.nodes[p.in.TableID]
		if !ok {
			return notFound()
		}
		if n.Kind != "table" || p.stores[n.ID] != p.in.DatastoreID {
			return invalid("tableId", "Table selector is outside the selected datastore")
		}
	}
	return nil
}

// storeOf memoises the datastore a node sits under ("" when none); the
// placeholder written before recursing stops a parent cycle.
func (p *databaseProjection) storeOf(id string) string {
	if s, ok := p.stores[id]; ok {
		return s
	}
	n, ok := p.nodes[id]
	if !ok {
		return ""
	}
	p.stores[id] = ""
	if n.Kind == "datastore" {
		p.stores[id] = id
	} else if n.ParentID != nil {
		p.stores[id] = p.storeOf(*n.ParentID)
	}
	return p.stores[id]
}

// decodeFacets decodes one record's relational facets and, on a source-backed
// read, the proof of each. A record the proposal designs has no stored facet.
func (p *databaseProjection) decodeFacets(id, kind string, attrs map[string]jsontext.Value, edge bool) error {
	if o := p.proposal.overlay(id); o != nil && o.Base == nil {
		return nil
	}
	if !relationalSubject(kind, attrs, edge) {
		return nil
	}
	fs, _, err := relationalFacetObject(kind, attrs)
	if err != nil {
		return err
	}
	p.facets[id] = map[string]*relationalFacet{}
	for key, raw := range fs {
		f, err := decodeRelationalFacetMode(kind, raw, true, p.source == nil && p.effective == nil)
		if err != nil {
			return err
		}
		p.facets[id][key] = f
		if p.source != nil || p.effective != nil {
			proof, err := p.facetProof(id, key, edge)
			if err != nil {
				return err
			}
			p.sourceProof[f] = proof
		}
	}
	return nil
}

func (p *databaseProjection) facetProof(id, key string, edge bool) (lineageProof, error) {
	typ := "node"
	if edge {
		typ = "edge"
	}
	if p.effective != nil {
		return effectiveFacetProof(p.effective, typ, id, key)
	}
	return sourceRecordProof(p.source, typ, id, &LineageValueRef{Kind: "column", NodeID: id, FacetKey: key})
}

// decodeSelection decodes the facets a page can reach: every record of the
// selected datastore, plus the tables (and their children) its FKs target.
func (p *databaseProjection) decodeSelection(ctx context.Context, state *RevisionState) error {
	for _, e := range state.Evidence {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.evidence[e.ID] = e
	}
	targetTables, err := p.referencedTables(ctx, state.Edges)
	if err != nil {
		return err
	}
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.stores[n.ID] == p.in.DatastoreID || targetTables[n.ID] || n.ParentID != nil && targetTables[*n.ParentID] {
			if err := p.decodeFacets(n.ID, n.Kind, n.Attributes, false); err != nil {
				return err
			}
		}
	}
	for _, e := range state.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Kind == "references" && p.stores[e.From] == p.in.DatastoreID {
			if err := p.decodeFacets(e.ID, e.Kind, e.Attributes, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *databaseProjection) referencedTables(ctx context.Context, edges []Edge) (map[string]bool, error) {
	targetTables := map[string]bool{}
	for _, e := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Kind == "references" && p.stores[e.From] == p.in.DatastoreID {
			targetTables[e.To] = true
		}
	}
	return targetTables, nil
}

// applyDesiredFacets lays each proposal overlay's desired values over the
// selected facet, keeping the source facet's common analysis fields.
func (p *databaseProjection) applyDesiredFacets() error {
	for _, o := range p.proposal.draft.Overlays {
		raw, err := json.Marshal(o.Values)
		if err != nil {
			return err
		}
		f := &relationalFacet{}
		if err := json.Unmarshal(raw, f); err != nil {
			return err
		}
		if source := p.selected(o.SubjectID); source != nil {
			f.relationalFacetCommon = source.relationalFacetCommon
		}
		if p.facets[o.SubjectID] == nil {
			p.facets[o.SubjectID] = map[string]*relationalFacet{}
		}
		p.facets[o.SubjectID][p.in.FacetKey] = f
	}
	p.page.ViewSchemaVersion, p.page.ProposalPins = ProposalDocumentVersion, p.proposal.pins
	p.page.Limitations = append(p.page.Limitations, "Desired structure; existing data, writers and runtime enforcement are unverified")
	return nil
}

// collectTables observes every structural record of the datastore (an
// observation records its limitations even when no table item is listed)
// and lists the tables that match the search.
func (p *databaseProjection) collectTables(ctx context.Context, nodes []Node) error {
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.stores[n.ID] != p.in.DatastoreID || !slices.Contains([]string{"table", "db_schema", "constraint", "index"}, n.Kind) {
			continue
		}
		f := p.observe(n.ID)
		if n.Kind != "table" || f == nil {
			continue
		}
		count := p.observedColumnCount(n.ID)
		if p.in.RecordType != "tables" || !strings.Contains(strings.ToLower(f.QualifiedName), strings.ToLower(p.in.Search)) {
			continue
		}
		comparison, err := compareRelationalFacetsMode(n.Kind, n.Attributes, false, p.source == nil && p.effective == nil)
		if err != nil {
			return err
		}
		p.page.TableItems = append(p.page.TableItems, TableItem{TableID: n.ID, SchemaID: *n.ParentID, QualifiedName: f.QualifiedName, ColumnCount: count, FacetKeys: slices.Sorted(maps.Keys(p.facets[n.ID])), DriftStatus: comparison.Status})
	}
	return nil
}

func (p *databaseProjection) observedColumnCount(table string) int64 {
	var count int64
	for _, c := range p.children[table] {
		if c.Kind == "column" && p.observe(c.ID) != nil {
			count++
		}
	}
	return count
}

func (p *databaseProjection) collectRelationships(ctx context.Context, edges []Edge) error {
	for _, e := range edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Kind != "references" || p.stores[e.From] != p.in.DatastoreID {
			continue
		}
		f := p.observe(e.ID)
		cf := p.observe(e.From)
		if f == nil || cf == nil {
			continue
		}
		from := p.nodes[e.From]
		if p.in.TableID != "" && *from.ParentID != p.in.TableID && e.To != p.in.TableID {
			continue
		}
		item := p.relationship(e, f, cf)
		p.page.RelationshipItems = append(p.page.RelationshipItems, item)
	}
	return nil
}

// databaseCursorScope is the digest a page cursor binds to. Presence decides
// selector legality, while the cursor binds normalized semantic filters:
// empty and omitted table-search values select the same data.
func databaseCursorScope(pid string, in DatabaseQueryInput, semanticHash string, target *resolvedBackendTarget, effective *EffectiveGraphSnapshot) (string, error) {
	scope, err := requestDigest(struct {
		ProjectID    string `json:"projectId"`
		RevisionID   string `json:"revisionId"`
		SemanticHash string `json:"semanticHash"`
		DatastoreID  string `json:"datastoreId"`
		FacetKey     string `json:"facetKey"`
		RecordType   string `json:"recordType"`
		Search       string `json:"search"`
		TableID      string `json:"tableId"`
		ResponseMode string `json:"responseMode,omitempty"`
		Section      string `json:"section,omitempty"`
	}{pid, in.RevisionID, semanticHash, in.DatastoreID, in.FacetKey, in.RecordType, in.Search, in.TableID, in.ResponseMode, in.Section})
	if err != nil {
		return "", err
	}
	if target.pins != nil {
		scope, err = requestDigest(struct {
			SourceScope string
			Pins        *ProposalReadPins
		}{scope, target.pins})
		if err != nil {
			return "", err
		}
	}
	if effective != nil {
		scope, err = requestDigest(struct {
			Scope string
			Pins  EffectiveGraphPins
		}{scope, effective.Pins})
		if err != nil {
			return "", err
		}
	}
	return scope, nil
}

func (p *databaseProjection) paginate(pid, scope string, limit int, after string) {
	if p.in.RecordType == "tables" {
		p.page.TableItems = slices.DeleteFunc(p.page.TableItems, func(t TableItem) bool { return t.TableID <= after })
		if len(p.page.TableItems) > limit {
			p.page.NextCursor = encodeGraphPage("database", pid, scope, p.page.TableItems[limit-1].TableID)
			p.page.TableItems = p.page.TableItems[:limit]
		}
		return
	}
	p.page.RelationshipItems = slices.DeleteFunc(p.page.RelationshipItems, func(e RelationshipItem) bool { return e.EdgeID <= after })
	if len(p.page.RelationshipItems) > limit {
		p.page.NextCursor = encodeGraphPage("database", pid, scope, p.page.RelationshipItems[limit-1].EdgeID)
		p.page.RelationshipItems = p.page.RelationshipItems[:limit]
	}
}

func (p *databaseProjection) relationship(e Edge, f, cf *relationalFacet) RelationshipItem {
	item := p.relationshipItem(e, f)
	designed := p.proposal != nil && p.proposal.proposal != nil
	item.Status = p.relationshipStatus(e, f, cf, designed)
	fromColumns, toColumns, missing, currentColumns := p.relationshipColumns(&item, f)
	if missing {
		item.Status = "unresolved"
		if item.TargetReason == nil {
			item.TargetReason = new("Missing selected source, target or column proof")
		}
		p.limitation(e.ID, "unresolved_relationship", "Unresolved selected relationship "+e.ID+": "+*item.TargetReason)
	}
	if item.Status != "explicit" || !currentColumns || !p.proofCurrent(p.selected(item.SourceTableID)) || item.TargetTableID != nil && !p.proofCurrent(p.selected(*item.TargetTableID)) {
		limitation := "Current explicit selected FK/table/column proof unavailable for " + e.ID
		item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, limitation)
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, limitation)
		return p.projectRelationship(item, e)
	}
	item.SourceCardinality.Min = new(int64(0))
	item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, "Declared relationship "+e.ID+" does not require a source row for every target")
	targetUnique := p.relationshipMaxima(&item, fromColumns, toColumns)
	matchSimple := rawStringEquals(f.MatchType.Value, "simple") && f.MatchType.Status == "known"
	enforced := scalarBool(cf.Deferrable, false) && scalarBool(cf.InitiallyDeferred, false) && matchSimple
	allNotNull, nullable, unknown := p.sourceNullability(&item, fromColumns, designed)
	switch {
	case enforced && targetUnique && (nullable || allNotNull && !unknown):
		item.TargetCardinality.Min = new(int64(1))
		if nullable {
			item.TargetCardinality.Min = new(int64(0))
		}
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, p.enforcedMinimumBasis(e, designed))
	case matchSimple && nullable:
		// A NULL source row references no target row whatever the
		// enforcement, deferrability or target key, so one known nullable
		// source column proves min0 under known MATCH SIMPLE, as
		// database.md promises; only min1 needs the enforcement and
		// target-key gates (review 2026-10-06, F82).
		item.TargetCardinality.Min = new(int64(0))
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, "Known nullable source column under MATCH SIMPLE "+e.From+" leaves a source row without a target")
	default:
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, "Minimum unknown: selected nullability, nondeferrable MATCH SIMPLE enforcement or target key is not established for "+e.ID)
	}
	return p.projectRelationship(item, e)
}

// relationshipItem is the item before any status or cardinality decision:
// identity, column pairs and the proof that names it.
func (p *databaseProjection) relationshipItem(e Edge, f *relationalFacet) RelationshipItem {
	constraint := p.nodes[e.From]
	proofIDs := slices.Clone(f.EvidenceIDs)
	if p.source != nil {
		proofIDs = slices.Clone(p.sourceProof[f].evidenceIDs)
	}
	item := RelationshipItem{EdgeID: e.ID, ConstraintID: e.From, SourceTableID: *constraint.ParentID, ColumnPairs: []DatabaseColumnPair{}, EvidenceIDs: proofIDs, SourceCardinality: Cardinality{Basis: []string{}}, TargetCardinality: Cardinality{Basis: []string{}}, Status: "explicit"}
	if to := p.nodes[e.To]; to.Kind == "table" {
		item.TargetTableID = new(to.ID)
	}
	for _, pair := range f.ColumnPairs {
		item.ColumnPairs = append(item.ColumnPairs, DatabaseColumnPair{FromColumnID: pair.FromColumnID, ToColumnID: pair.ToColumnID})
	}
	if f.TargetReason != "" {
		item.TargetReason = new(f.TargetReason)
	}
	return item
}

// relationshipStatus decides explicit/stale/inferred from the FK and its
// constraint's proof; a designed FK the proposal also designs counts as
// declared, and a designed read never reports stale.
func (p *databaseProjection) relationshipStatus(e Edge, f, cf *relationalFacet, designed bool) string {
	declared := p.proofCurrent(f) && p.proofCurrent(cf)
	if designed && p.proposal.overlay(e.ID) != nil && p.proposal.overlay(e.From) != nil {
		declared = true
	}
	if !designed && (p.proofStale(f) || p.proofStale(cf)) {
		return "stale"
	}
	if !declared {
		return "inferred"
	}
	return "explicit"
}

// relationshipColumns observes both tables and every paired column, in that
// order, and reports whether any of them is missing and whether every
// column's nullability is currently proved.
func (p *databaseProjection) relationshipColumns(item *RelationshipItem, f *relationalFacet) (fromColumns, toColumns []string, missing, currentColumns bool) {
	missing = p.observe(item.SourceTableID) == nil
	if item.TargetTableID == nil {
		missing = true
	} else if p.observe(*item.TargetTableID) == nil {
		missing = true
	}
	fromColumns, toColumns = make([]string, 0, len(f.ColumnPairs)), make([]string, 0, len(f.ColumnPairs))
	currentColumns = true
	for _, pair := range f.ColumnPairs {
		fromColumns = append(fromColumns, pair.FromColumnID)
		toColumns = append(toColumns, pair.ToColumnID)
		for _, id := range []string{pair.FromColumnID, pair.ToColumnID} {
			col := p.observe(id)
			if col == nil {
				missing = true
			}
			if !p.propertyCurrent(id, "/nullable") {
				currentColumns = false
			}
		}
	}
	return fromColumns, toColumns, missing, currentColumns
}

// relationshipMaxima sets both maximum cardinalities and reports whether the
// target columns are unique, which the minimum decision needs.
func (p *databaseProjection) relationshipMaxima(item *RelationshipItem, fromColumns, toColumns []string) bool {
	// Covering, not ordered: the referenced columns are unique as a set
	// whenever they contain a complete key, in any declared order (review
	// 2026-10-06, F78, projection half). The designer's FK admission in
	// proposal_evaluator.go keeps the exact ordered match.
	targetUnique, _, targetBasis := p.unique(*item.TargetTableID, toColumns, false)
	item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, targetBasis...)
	if targetUnique {
		item.TargetCardinality.Max = new("1")
	}
	sourceUnique, complete, sourceBasis := p.unique(item.SourceTableID, fromColumns, false)
	item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, sourceBasis...)
	if sourceUnique {
		item.SourceCardinality.Max = new("1")
	} else if complete {
		item.SourceCardinality.Max = new("many")
	}
	return targetUnique
}

// sourceNullability records each source column's nullability basis and
// classifies the set: all NOT NULL, any known nullable, any unknown.
func (p *databaseProjection) sourceNullability(item *RelationshipItem, fromColumns []string, designed bool) (allNotNull, nullable, unknown bool) {
	allNotNull = true
	for _, id := range fromColumns {
		n := p.selected(id).Nullable
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, p.nullableBasis(id, n, designed))
		if scalarBool(n, true) {
			nullable = true
			allNotNull = false
		} else if !scalarBool(n, false) {
			unknown = true
			allNotNull = false
		}
	}
	return allNotNull, nullable, unknown
}

func (p *databaseProjection) nullableBasis(id string, n *relationalScalar, designed bool) string {
	basis := "Selected source column " + id + " nullable " + n.Status + " " + string(n.Value) + " with evidence " + p.selectedEvidence(p.selected(id))
	if effectiveBasis := p.effectiveNullableBasis(id, n.Value); effectiveBasis != "" {
		basis = effectiveBasis
	}
	if designed {
		if overlay := p.proposal.overlay(id); overlay != nil && overlay.PropertyOrigins["/nullable"].Kind == "intent" {
			basis = "Desired column " + id + " nullable " + string(n.Value) + " from command " + overlay.PropertyOrigins["/nullable"].CommandID + "; runtime is unverified"
		}
	}
	return basis
}

func (p *databaseProjection) enforcedMinimumBasis(e Edge, designed bool) string {
	basis := "Current explicit nondeferrable MATCH SIMPLE " + e.From + " with selected source nullability"
	if p.effectiveRelationshipIntent(e) {
		basis = "Desired nondeferrable MATCH SIMPLE " + e.From + " with desired nullability; runtime enforcement is unverified"
	}
	if designed {
		basis = "Designed nondeferrable MATCH SIMPLE " + e.From + " with desired nullability; runtime enforcement is unverified"
	}
	return basis
}
func scalarBool(s *relationalScalar, want bool) bool {
	return s != nil && s.Status == "known" && string(s.Value) == fmt.Sprint(want)
}
func (p *databaseProjection) unique(table string, columns []string, ordered bool) (unique, complete bool, basis []string) {
	keyColumns := slices.Clone(columns)
	if !ordered {
		slices.Sort(keyColumns)
	}
	key := table + "\x00" + fmt.Sprint(ordered) + "\x00" + strings.Join(keyColumns, "\x00")
	if result, ok := p.uniqueResults[key]; ok {
		return result.unique, result.complete, result.basis
	}
	defer func() { p.uniqueResults[key] = databaseUniqueness{unique: unique, complete: complete, basis: basis} }()
	tf := p.selected(table)
	complete = p.proofCurrent(tf) && tf.ConstraintsStatus == "complete"
	basis = []string{}
	for _, n := range p.children[table] {
		if n.Kind != "constraint" && n.Kind != "index" {
			continue
		}
		f := p.selected(n.ID)
		if f == nil {
			complete = false
			basis = append(basis, "Missing selected key proof "+n.ID)
			continue
		}
		if p.designedNonKey(n, f) {
			continue
		}
		if !p.proofCurrent(f) {
			complete = false
			basis = append(basis, "Stale or inferred selected constraint/index proof "+n.ID)
			continue
		}
		candidate, ok, incomplete := uniqueKeyCandidate(n, f)
		if incomplete {
			complete = false
		}
		if !ok {
			continue
		}
		if !p.proofCurrent(f) || f.AnalysisStatus != "complete" || !p.keyColumnsCurrent(candidate) {
			complete = false
			basis = append(basis, "Incomplete, stale or inferred selected key "+n.ID)
			continue
		}
		if !keyColumnsMatch(candidate, columns, ordered) {
			basis = append(basis, "Current explicit nonmatching global key "+n.ID+" with evidence "+p.selectedEvidence(f))
			continue
		}
		unique = true
		basis = append(basis, "Current explicit complete key "+n.ID+" with evidence "+p.selectedEvidence(f))
	}
	if !unique {
		if complete {
			basis = append(basis, "Complete selected constraint inventory for "+table+" has no matching global unique key")
		} else {
			basis = append(basis, "Incomplete selected uniqueness basis for "+table)
		}
	}
	return
}

// designedNonKey reports a designed constraint of a non-key kind (a designed
// FK): it can never be a unique key, so it is skipped before the proof check
// its synthetic facet always fails: it made the source table's complete
// inventory incomplete, and every FK from that table lost its proved source
// max "many" (review 2026-10-06, F81). A designed key still fails the check:
// intent proves no uniqueness.
func (p *databaseProjection) designedNonKey(n Node, f *relationalFacet) bool {
	return n.Kind == "constraint" && p.designedSubject(n.ID) && f.ConstraintKind != "primary_key" && f.ConstraintKind != "unique"
}

// uniqueKeyCandidate reads the columns a constraint or index would make
// unique. ok is false when the record is no unique key (or one over an
// expression); incomplete marks a record whose uniqueness cannot be decided,
// which makes the table's key inventory incomplete.
func uniqueKeyCandidate(n Node, f *relationalFacet) (candidate []string, ok, incomplete bool) {
	if n.Kind == "constraint" {
		if f.ConstraintKind != "primary_key" && f.ConstraintKind != "unique" {
			return nil, false, false
		}
		return f.ColumnIDs, true, false
	}
	if scalarBool(f.Unique, false) {
		return nil, false, false
	}
	if !scalarBool(f.Unique, true) || f.Predicate.Status != "known" {
		return nil, false, true
	}
	if string(f.Predicate.Value) != "null" {
		return nil, false, false
	}
	for _, term := range f.Terms {
		if term.ColumnID == "" {
			candidate = nil
			break
		}
		candidate = append(candidate, term.ColumnID)
	}
	return candidate, len(candidate) > 0, false
}

func (p *databaseProjection) keyColumnsCurrent(candidate []string) bool {
	current := true
	for _, id := range candidate {
		if !p.proofCurrent(p.selected(id)) {
			current = false
		}
	}
	return current
}

// keyColumnsMatch: ordered is the exact ordered match a designed FK
// reference must make. Otherwise the question is whether the columns are
// globally unique, and a complete key on any subset of them answers it: set
// equality answered "many" for an FK over {id, tenant_id} although the PK on
// {id} makes it unique (review 2026-10-06, F77).
func keyColumnsMatch(candidate, columns []string, ordered bool) bool {
	if ordered {
		return slices.Equal(candidate, columns)
	}
	for _, id := range candidate {
		if !slices.Contains(columns, id) {
			return false
		}
	}
	return len(candidate) > 0
}

func (p *databaseProjection) observeSourceFacet(id string, f *relationalFacet) *relationalFacet {
	proof := p.sourceProof[f]
	if proof.status == "desired" {
		p.page.FacetStatus = "desired"
	} else if proof.status == "stale" {
		p.stale(id, "stale_property", "Stale selected source property for "+id)
	} else if proof.status != "explicit" {
		p.limitation(id, "missing_property", "Missing explicit selected source property proof for "+id)
	}
	for reason := range proof.reasons {
		if reason == "desired_structure" {
			p.page.Limitations = append(p.page.Limitations, "Desired structure; source proof does not confirm this intended value")
			continue
		}
		if proof.status == "stale" {
			p.stale(id, "stale_property", reason)
		} else {
			p.limitation(id, "property_limitation", reason)
		}
	}
	if f.AnalysisStatus != "complete" || f.ColumnsStatus != "" && f.ColumnsStatus != "complete" || f.ConstraintsStatus != "" && f.ConstraintsStatus != "complete" {
		p.limitation(id, "incomplete_analysis", "Incomplete selected analysis for "+id)
	}
	return f
}
