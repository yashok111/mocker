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
	RevisionID                  string              `json:"revisionId,omitempty"`
	Proposal                    *ProposalReadTarget `json:"proposal,omitzero"`
	DatastoreID                 string              `json:"datastoreId"`
	FacetKey                    string              `json:"facetKey"`
	RecordType                  string              `json:"recordType"`
	Search                      string              `json:"search,omitempty"`
	TableID                     string              `json:"tableId,omitempty"`
	Limit                       int                 `json:"limit,omitzero"`
	Cursor                      string              `json:"cursor,omitempty"`
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
	if err := decodeReadQuery(b, []string{"datastoreId", "facetKey", "recordType"}, []string{"search", "tableId", "limit", "cursor"}, &decoded); err != nil {
		return err
	}
	*in = DatabaseQueryInput(decoded)
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
	ViewSchemaVersion string             `json:"viewSchemaVersion,omitempty"`
	ProposalPins      *ProposalReadPins  `json:"proposalPins,omitzero"`
	ProjectID         string             `json:"projectId"`
	RevisionID        string             `json:"revisionId"`
	SemanticHash      string             `json:"semanticHash"`
	DatastoreID       string             `json:"datastoreId"`
	FacetKey          string             `json:"facetKey"`
	RecordType        string             `json:"recordType"`
	Coverage          RevisionCoverage   `json:"coverage"`
	FacetStatus       string             `json:"facetStatus"`
	Limitations       []string           `json:"limitations"`
	TableItems        []TableItem        `json:"tableItems"`
	RelationshipItems []RelationshipItem `json:"relationshipItems"`
	NextCursor        string             `json:"nextCursor"`
}

type databaseProjection struct {
	proposal      *resolvedBackendTarget
	in            DatabaseQueryInput
	nodes         map[string]Node
	children      map[string][]Node
	facets        map[string]map[string]*relationalFacet
	evidence      map[string]Evidence
	stores        map[string]string
	page          *DatabasePage
	observed      map[string]bool
	limitations   map[string]bool
	uniqueResults map[string]databaseUniqueness
}
type databaseUniqueness struct {
	unique, complete bool
	basis            []string
}

func (p *databaseProjection) selected(id string) *relationalFacet { return p.facets[id][p.in.FacetKey] }
func (p *databaseProjection) limitation(message string) {
	if !p.limitations[message] {
		p.limitations[message] = true
		p.page.Limitations = append(p.page.Limitations, message)
	}
	p.page.FacetStatus = "unknown"
}
func (p *databaseProjection) stale(message string) {
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
		p.limitation("Missing selected facet proof for " + id)
		return nil
	}
	if f.Freshness != nil && f.Freshness.Status == "stale" {
		p.stale("Stale selected facet for " + id)
	}
	if f.AnalysisStatus != "complete" || f.ColumnsStatus != "" && f.ColumnsStatus != "complete" || f.ConstraintsStatus != "" && f.ConstraintsStatus != "complete" {
		p.limitation("Incomplete selected analysis for " + id + ": " + strings.Join(f.Gaps, "; "))
	}
	for _, proofID := range f.EvidenceIDs {
		e, ok := p.evidence[proofID]
		if !ok || e.Status != "explicit" || e.Source.SnapshotID != f.SourceSnapshotID {
			p.limitation("Missing or inferred selected proof " + proofID + " for " + id)
		} else if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.stale("Stale selected proof " + proofID + " for " + id)
		}
	}
	return f
}
func (p *databaseProjection) proofCurrent(f *relationalFacet) bool {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := r.resolveBackendTarget(ctx, pid, BackendReadTarget{RevisionID: in.RevisionID, Proposal: in.Proposal})
	if err != nil {
		return nil, err
	}
	if target.proposal != nil && (in.DatastoreID != target.proposal.DatastoreID || in.FacetKey != target.proposal.FacetKey) {
		return nil, invalid("selection", "Proposal datastore and facet must match its baseline selection")
	}
	in.RevisionID = target.revisionID
	if !ValidID(in.DatastoreID) {
		return nil, notFound()
	}
	if !externalKey(in.FacetKey) {
		return nil, invalid("facetKey", "Use a valid relational facet key")
	}
	if !slices.Contains([]string{"tables", "relationships"}, in.RecordType) {
		return nil, invalid("recordType", "Select tables or relationships")
	}
	if in.RecordType == "tables" && (in.TableID != "" || in.tablePresent) || in.RecordType == "relationships" && (in.Search != "" || in.searchPresent) {
		return nil, invalid("selectors", "Selectors are not valid for this record type")
	}
	if !utf8.ValidString(in.Search) || len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return nil, invalid("search", "Invalid search string")
	}
	if in.tablePresent && in.TableID == "" || in.TableID != "" && !ValidID(in.TableID) {
		return nil, invalid("tableId", "Table selector must be a canonical UUID")
	}
	state, err := loadRevisionState(ctx, r.db.R, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	p := &databaseProjection{proposal: target, in: in, nodes: map[string]Node{}, children: map[string][]Node{}, facets: map[string]map[string]*relationalFacet{}, evidence: map[string]Evidence{}, stores: map[string]string{}, observed: map[string]bool{}, limitations: map[string]bool{}, uniqueResults: map[string]databaseUniqueness{}}
	if target.proposal != nil {
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
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.nodes[n.ID] = n
		if n.ParentID != nil {
			p.children[*n.ParentID] = append(p.children[*n.ParentID], n)
		}
	}
	ds, ok := p.nodes[in.DatastoreID]
	if !ok {
		return nil, notFound()
	}
	if state.Revision.SchemaVersion != "2" || ds.Kind != "datastore" || !relationalSubject(ds.Kind, ds.Attributes, false) {
		return nil, &FaultError{Status: 422, Code: "backend_relational_unavailable", Message: "Pinned revision has no relational datastore descriptor"}
	}
	var store func(string) string
	store = func(id string) string {
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
			p.stores[id] = store(*n.ParentID)
		}
		return p.stores[id]
	}
	for id := range p.nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		store(id)
	}
	if in.TableID != "" {
		n, ok := p.nodes[in.TableID]
		if !ok {
			return nil, notFound()
		}
		if n.Kind != "table" || p.stores[n.ID] != in.DatastoreID {
			return nil, invalid("tableId", "Table selector is outside the selected datastore")
		}
	}
	coverage, err := r.RevisionCoverage(ctx, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	p.page = &DatabasePage{ProjectID: pid, RevisionID: in.RevisionID, SemanticHash: state.Revision.SemanticHash, DatastoreID: in.DatastoreID, FacetKey: in.FacetKey, RecordType: in.RecordType, Coverage: *coverage, FacetStatus: "current", Limitations: []string{}, TableItems: []TableItem{}, RelationshipItems: []RelationshipItem{}}
	decode := func(id, kind string, attrs map[string]jsontext.Value, edge bool) error {
		if o := target.overlay(id); o != nil && o.Base == nil {
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
			f, err := decodeRelationalFacet(kind, raw, true)
			if err != nil {
				return err
			}
			p.facets[id][key] = f
		}
		return nil
	}
	for _, e := range state.Evidence {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.evidence[e.ID] = e
	}
	targetTables := map[string]bool{}
	for _, e := range state.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Kind == "references" && p.stores[e.From] == in.DatastoreID {
			targetTables[e.To] = true
		}
	}
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p.stores[n.ID] == in.DatastoreID || targetTables[n.ID] || n.ParentID != nil && targetTables[*n.ParentID] {
			if err := decode(n.ID, n.Kind, n.Attributes, false); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range state.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Kind == "references" && p.stores[e.From] == in.DatastoreID {
			if err := decode(e.ID, e.Kind, e.Attributes, true); err != nil {
				return nil, err
			}
		}
	}
	if target.proposal != nil {
		for _, o := range target.draft.Overlays {
			raw, err := json.Marshal(o.Values)
			if err != nil {
				return nil, err
			}
			f := &relationalFacet{}
			if err := json.Unmarshal(raw, f); err != nil {
				return nil, err
			}
			if source := p.selected(o.SubjectID); source != nil {
				f.relationalFacetCommon = source.relationalFacetCommon
			}
			if p.facets[o.SubjectID] == nil {
				p.facets[o.SubjectID] = map[string]*relationalFacet{}
			}
			p.facets[o.SubjectID][in.FacetKey] = f
		}
		p.page.ViewSchemaVersion, p.page.ProposalPins = ProposalDocumentVersion, target.pins
		p.page.Limitations = append(p.page.Limitations, "Desired structure; existing data, writers and runtime enforcement are unverified")
	}
	p.observe(in.DatastoreID)
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if p.stores[n.ID] != in.DatastoreID || !slices.Contains([]string{"table", "db_schema", "constraint", "index"}, n.Kind) {
			continue
		}
		f := p.observe(n.ID)
		if n.Kind != "table" {
			continue
		}
		if f == nil {
			continue
		}
		var count int64
		for _, c := range p.children[n.ID] {
			if c.Kind == "column" && p.observe(c.ID) != nil {
				count++
			}
		}
		if in.RecordType != "tables" || !strings.Contains(strings.ToLower(f.QualifiedName), strings.ToLower(in.Search)) {
			continue
		}
		comparison, err := CompareRelationalFacets(n.Kind, n.Attributes, false)
		if err != nil {
			return nil, err
		}
		p.page.TableItems = append(p.page.TableItems, TableItem{TableID: n.ID, SchemaID: *n.ParentID, QualifiedName: f.QualifiedName, ColumnCount: count, FacetKeys: slices.Sorted(maps.Keys(p.facets[n.ID])), DriftStatus: comparison.Status})
	}
	if in.RecordType == "relationships" {
		for _, e := range state.Edges {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if e.Kind != "references" || p.stores[e.From] != in.DatastoreID {
				continue
			}
			f := p.observe(e.ID)
			cf := p.observe(e.From)
			if f == nil || cf == nil {
				continue
			}
			from := p.nodes[e.From]
			if in.TableID != "" && *from.ParentID != in.TableID && e.To != in.TableID {
				continue
			}
			item := p.relationship(e, f, cf)
			p.page.RelationshipItems = append(p.page.RelationshipItems, item)
		}
	}
	// Presence decides selector legality, while the cursor binds normalized
	// semantic filters. Empty and omitted table-search values select the same data.
	scope, err := requestDigest(struct {
		ProjectID    string `json:"projectId"`
		RevisionID   string `json:"revisionId"`
		SemanticHash string `json:"semanticHash"`
		DatastoreID  string `json:"datastoreId"`
		FacetKey     string `json:"facetKey"`
		RecordType   string `json:"recordType"`
		Search       string `json:"search"`
		TableID      string `json:"tableId"`
	}{pid, in.RevisionID, state.Revision.SemanticHash, in.DatastoreID, in.FacetKey, in.RecordType, in.Search, in.TableID})
	if err != nil {
		return nil, err
	}
	if target.pins != nil {
		scope, err = requestDigest(struct {
			SourceScope string
			Pins        *ProposalReadPins
		}{scope, target.pins})
		if err != nil {
			return nil, err
		}
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "database", pid, scope, true)
	if err != nil {
		return nil, err
	}
	if in.RecordType == "tables" {
		p.page.TableItems = slices.DeleteFunc(p.page.TableItems, func(t TableItem) bool { return t.TableID <= after })
		if len(p.page.TableItems) > limit {
			p.page.NextCursor = encodeGraphPage("database", pid, scope, p.page.TableItems[limit-1].TableID)
			p.page.TableItems = p.page.TableItems[:limit]
		}
	} else {
		p.page.RelationshipItems = slices.DeleteFunc(p.page.RelationshipItems, func(e RelationshipItem) bool { return e.EdgeID <= after })
		if len(p.page.RelationshipItems) > limit {
			p.page.NextCursor = encodeGraphPage("database", pid, scope, p.page.RelationshipItems[limit-1].EdgeID)
			p.page.RelationshipItems = p.page.RelationshipItems[:limit]
		}
	}
	slices.Sort(p.page.Limitations)
	p.page.Limitations = slices.Compact(p.page.Limitations)
	return p.page, nil
}

func (p *databaseProjection) relationship(e Edge, f, cf *relationalFacet) RelationshipItem {
	constraint := p.nodes[e.From]
	item := RelationshipItem{EdgeID: e.ID, ConstraintID: e.From, SourceTableID: *constraint.ParentID, ColumnPairs: []DatabaseColumnPair{}, EvidenceIDs: slices.Clone(f.EvidenceIDs), SourceCardinality: Cardinality{Basis: []string{}}, TargetCardinality: Cardinality{Basis: []string{}}, Status: "explicit"}
	if to := p.nodes[e.To]; to.Kind == "table" {
		item.TargetTableID = new(to.ID)
	}
	for _, pair := range f.ColumnPairs {
		item.ColumnPairs = append(item.ColumnPairs, DatabaseColumnPair{FromColumnID: pair.FromColumnID, ToColumnID: pair.ToColumnID})
	}
	if f.TargetReason != "" {
		item.TargetReason = new(f.TargetReason)
	}
	designed := p.proposal != nil && p.proposal.proposal != nil
	declared := p.proofCurrent(f) && p.proofCurrent(cf)
	if designed && p.proposal.overlay(e.ID) != nil && p.proposal.overlay(e.From) != nil {
		declared = true
	}
	if !designed && (p.proofStale(f) || p.proofStale(cf)) {
		item.Status = "stale"
	} else if !declared {
		item.Status = "inferred"
	}
	missing := p.observe(item.SourceTableID) == nil
	if item.TargetTableID == nil {
		missing = true
	} else if p.observe(*item.TargetTableID) == nil {
		missing = true
	}
	fromColumns, toColumns := []string{}, []string{}
	currentColumns := true
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
	if missing {
		item.Status = "unresolved"
		if item.TargetReason == nil {
			item.TargetReason = new("Missing selected source, target or column proof")
		}
		p.limitation("Unresolved selected relationship " + e.ID + ": " + *item.TargetReason)
	}
	if item.Status != "explicit" || !currentColumns || !p.proofCurrent(p.selected(item.SourceTableID)) || item.TargetTableID != nil && !p.proofCurrent(p.selected(*item.TargetTableID)) {
		limitation := "Current explicit selected FK/table/column proof unavailable for " + e.ID
		item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, limitation)
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, limitation)
		return p.projectRelationship(item, e)
	}
	item.SourceCardinality.Min = new(int64(0))
	item.SourceCardinality.Basis = append(item.SourceCardinality.Basis, "Declared relationship "+e.ID+" does not require a source row for every target")
	targetUnique, _, targetBasis := p.unique(*item.TargetTableID, toColumns, true)
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
	enforced := scalarBool(cf.Deferrable, false) && scalarBool(cf.InitiallyDeferred, false) && rawStringEquals(f.MatchType.Value, "simple") && f.MatchType.Status == "known"
	allNotNull, nullable, unknown := true, false, false
	for _, id := range fromColumns {
		n := p.selected(id).Nullable
		basis := "Selected source column " + id + " nullable " + n.Status + " " + string(n.Value) + " with evidence " + strings.Join(p.selected(id).EvidenceIDs, ",")
		if designed {
			if overlay := p.proposal.overlay(id); overlay != nil && overlay.PropertyOrigins["/nullable"].Kind == "intent" {
				basis = "Desired column " + id + " nullable " + string(n.Value) + " from command " + overlay.PropertyOrigins["/nullable"].CommandID + "; runtime is unverified"
			}
		}
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, basis)
		if scalarBool(n, true) {
			nullable = true
			allNotNull = false
		} else if !scalarBool(n, false) {
			unknown = true
			allNotNull = false
		}
	}
	if enforced && targetUnique && (nullable || allNotNull && !unknown) {
		item.TargetCardinality.Min = new(int64(1))
		if nullable {
			item.TargetCardinality.Min = new(int64(0))
		}
		basis := "Current explicit nondeferrable MATCH SIMPLE " + e.From + " with selected source nullability"
		if designed {
			basis = "Designed nondeferrable MATCH SIMPLE " + e.From + " with desired nullability; runtime enforcement is unverified"
		}
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, basis)
	} else {
		item.TargetCardinality.Basis = append(item.TargetCardinality.Basis, "Minimum unknown: selected nullability, nondeferrable MATCH SIMPLE enforcement or target key is not established for "+e.ID)
	}
	return p.projectRelationship(item, e)
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
	match := func(candidate []string) bool {
		if ordered {
			return slices.Equal(candidate, columns)
		}
		a, b := slices.Clone(candidate), slices.Clone(columns)
		slices.Sort(a)
		slices.Sort(b)
		return slices.Equal(a, b)
	}
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
		if !p.proofCurrent(f) {
			complete = false
			basis = append(basis, "Stale or inferred selected constraint/index proof "+n.ID)
			continue
		}
		var candidate []string
		if n.Kind == "constraint" {
			if f.ConstraintKind != "primary_key" && f.ConstraintKind != "unique" {
				continue
			}
			candidate = f.ColumnIDs
		} else {
			if scalarBool(f.Unique, false) {
				continue
			}
			if !scalarBool(f.Unique, true) {
				complete = false
				continue
			}
			if f.Predicate.Status != "known" {
				complete = false
				continue
			}
			if string(f.Predicate.Value) != "null" {
				continue
			}
			for _, term := range f.Terms {
				if term.ColumnID == "" {
					candidate = nil
					break
				}
				candidate = append(candidate, term.ColumnID)
			}
			if len(candidate) == 0 {
				continue
			}
		}
		keyColumnsCurrent := true
		for _, id := range candidate {
			if !p.proofCurrent(p.selected(id)) {
				keyColumnsCurrent = false
			}
		}
		if !p.proofCurrent(f) || f.AnalysisStatus != "complete" || !keyColumnsCurrent {
			complete = false
			basis = append(basis, "Incomplete, stale or inferred selected key "+n.ID)
			continue
		}
		if !match(candidate) {
			basis = append(basis, "Current explicit nonmatching global key "+n.ID+" with evidence "+strings.Join(f.EvidenceIDs, ","))
			continue
		}
		unique = true
		basis = append(basis, "Current explicit complete key "+n.ID+" with evidence "+strings.Join(f.EvidenceIDs, ","))
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
