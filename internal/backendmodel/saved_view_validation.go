package backendmodel

import (
	"context"
	"encoding/json/v2"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

func savedUnsupported() error {
	return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Saved view requires a supported pinned source or database proposal"}
}
func resolveSavedReferences(ctx context.Context, q importReader, pid string, in BackendReadTarget, s SavedViewState) (*SavedViewPins, error) {
	target, err := resolveBackendTarget(ctx, q, pid, in)
	if err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, q, pid, target.revisionID)
	if err != nil {
		return nil, err
	}
	pins := &SavedViewPins{RevisionID: state.Revision.ID, SemanticHash: state.Revision.SemanticHash, Proposal: target.pins}
	return validateSavedGraphReferences(target, state, pins, s, false)
}
func validateSavedGraphReferences(target *resolvedBackendTarget, state *RevisionState, pins *SavedViewPins, s SavedViewState, effective bool) (*SavedViewPins, error) {
	if s.Flow != nil && (target.proposal != nil || !isRuntimeSchema(state.Revision.SchemaVersion) && !effective) {
		return nil, savedUnsupported()
	}
	if s.Database != nil && !isRelationalSchema(state.Revision.SchemaVersion) && !effective {
		return nil, savedUnsupported()
	}
	g := newSavedGraphRefs(target, state)
	if f := s.Flow; f != nil {
		if err := g.validateFlow(f); err != nil {
			return nil, err
		}
		return pins, nil
	}
	if err := g.validateDatabase(s.Database); err != nil {
		return nil, err
	}
	return pins, nil
}

// savedGraphRefs is the pinned graph a saved view's references resolve in:
// the revision's records with a proposal's designed records laid over them.
type savedGraphRefs struct {
	target   *resolvedBackendTarget
	state    *RevisionState
	nodes    map[string]Node
	edges    map[string]Edge
	ds       Node
	facetKey string
}

func newSavedGraphRefs(target *resolvedBackendTarget, state *RevisionState) *savedGraphRefs {
	g := &savedGraphRefs{target: target, state: state, nodes: map[string]Node{}, edges: map[string]Edge{}}
	for _, n := range state.Nodes {
		g.nodes[n.ID] = n
	}
	for _, e := range state.Edges {
		g.edges[e.ID] = e
	}
	if target.proposal == nil {
		return g
	}
	for _, o := range target.draft.Overlays {
		if o.Base == nil {
			if o.RecordType == "node" {
				g.nodes[o.SubjectID] = Node{ID: o.SubjectID, Kind: o.Kind, Name: o.Name, ParentID: o.ParentID}
			} else {
				g.edges[o.SubjectID] = Edge{ID: o.SubjectID, Kind: o.Kind, From: o.FromID, To: o.ToID}
			}
		} else if o.RecordType == "edge" {
			e := g.edges[o.SubjectID]
			e.From, e.To = o.FromID, o.ToID
			g.edges[e.ID] = e
		}
	}
	return g
}

func (g *savedGraphRefs) node(id, kind, path string) (Node, error) {
	n, ok := g.nodes[id]
	if !ok {
		return Node{}, notFound()
	}
	if kind != "" && n.Kind != kind {
		return Node{}, invalid(path, "Reference has the wrong kind")
	}
	return n, nil
}

func (g *savedGraphRefs) validateFlow(f *SavedFlowViewState) error {
	flow, err := g.flowScope(f)
	if err != nil {
		return err
	}
	for _, pos := range f.Positions {
		n, err := g.node(pos.NodeID, "flow_step", "state.positions.nodeId")
		if err != nil {
			return err
		}
		if n.ParentID == nil || *n.ParentID != flow.ID {
			return invalid("state.positions.nodeId", "Step must directly belong to the selected flow")
		}
	}
	for _, id := range f.CollapsedGroupIDs {
		if _, err := g.node(id, "transaction", "state.collapsedGroupIds"); err != nil {
			return err
		}
		if !g.flowTransaction(flow, id) {
			return invalid("state.collapsedGroupIds", "Group must be a known transaction of a step in this flow")
		}
	}
	if f.Selection != nil {
		if f.Selection.RecordType == "node" {
			if _, err := g.node(f.Selection.ID, "", "state.selection.id"); err != nil {
				return err
			}
		} else if _, ok := g.edges[f.Selection.ID]; !ok {
			return notFound()
		}
	}
	return nil
}

// flowScope resolves the entrypoint, flow and data node a flow view is
// scoped to and returns the flow (zero when none is selected).
func (g *savedGraphRefs) flowScope(f *SavedFlowViewState) (Node, error) {
	var flow Node
	var err error
	if f.Scope.EntrypointID != "" {
		entry, err := g.node(f.Scope.EntrypointID, "", "state.scope.entrypointId")
		if err != nil {
			return Node{}, err
		}
		if !sourceRuntimeEntrypoint(g.state.Revision.SchemaVersion, entry.Kind) {
			return Node{}, invalid("state.scope.entrypointId", "Expected a supported pinned entrypoint")
		}
	}
	if f.Scope.FlowID != "" {
		flow, err = g.node(f.Scope.FlowID, "flow", "state.scope.flowId")
		if err != nil {
			return Node{}, err
		}
	}
	if f.Scope.EntrypointID != "" && flow.ID != "" && !g.entrypointHandlesFlow(f.Scope.EntrypointID, flow) {
		return Node{}, invalid("state.scope.flowId", "Flow must be a child of the entrypoint's pinned handler")
	}
	if f.Scope.DataNodeID != "" {
		n, err := g.node(f.Scope.DataNodeID, "", "state.scope.dataNodeId")
		if err != nil {
			return Node{}, err
		}
		if !slices.Contains([]string{"table", "column", "view"}, n.Kind) {
			return Node{}, invalid("state.scope.dataNodeId", "Expected a supported relational data node")
		}
	}
	return flow, nil
}

func (g *savedGraphRefs) entrypointHandlesFlow(entrypointID string, flow Node) bool {
	related := false
	for _, e := range g.edges {
		h := g.nodes[e.To]
		if e.From == entrypointID && e.Kind == "handles" && h.Kind == "handler" && flow.ParentID != nil && *flow.ParentID == h.ID {
			related = true
		}
	}
	return related
}

// flowTransaction reports whether id is a known transaction of a step that
// directly belongs to flow.
func (g *savedGraphRefs) flowTransaction(flow Node, id string) bool {
	known := false
	for _, n := range g.nodes {
		if n.Kind == "flow_step" && n.ParentID != nil && *n.ParentID == flow.ID {
			var tc runtimeTransactionContext
			if err := json.Unmarshal(n.Attributes["transactionContext"], &tc); err == nil && tc.Status == "known" && tc.TransactionID == id {
				known = true
			}
		}
	}
	return known
}

func (g *savedGraphRefs) validateDatabase(d *SavedDatabaseViewState) error {
	ds, err := g.node(d.Scope.DatastoreID, "datastore", "state.scope.datastoreId")
	if err != nil {
		return err
	}
	if !relationalSubject(ds.Kind, ds.Attributes, false) {
		return savedUnsupported()
	}
	if g.target.proposal != nil && (d.Scope.DatastoreID != g.target.proposal.DatastoreID || d.Scope.FacetKey != g.target.proposal.FacetKey) {
		return invalid("state.scope", "Proposal scope must match its creation binding")
	}
	g.ds, g.facetKey = ds, d.Scope.FacetKey
	if !g.hasFacet(ds) {
		return invalid("state.scope.facetKey", "Facet does not exist in this datastore")
	}
	if d.Filters.RelationshipTableID != "" {
		if err := g.validateDatabaseNode(d.Filters.RelationshipTableID, "table", "state.filters.relationshipTableId"); err != nil {
			return err
		}
	}
	for _, pos := range d.Positions {
		if err := g.validateDatabaseNode(pos.NodeID, "table", "state.positions.nodeId"); err != nil {
			return err
		}
	}
	for _, id := range d.CollapsedGroupIDs {
		if err := g.validateDatabaseNode(id, "db_schema", "state.collapsedGroupIds"); err != nil {
			return err
		}
	}
	if sel := d.Selection; sel != nil {
		if sel.RecordType == "node" {
			return g.validateDatabaseNode(sel.ID, "", "state.selection.id")
		}
		return g.validateDatabaseEdge(sel.ID)
	}
	return nil
}

// hasFacet reports whether a record carries the selected facet: the
// proposal's effective facet on a proposal read, the stored one otherwise.
func (g *savedGraphRefs) hasFacet(n Node) bool {
	if g.target.proposal != nil {
		f, err := effectiveProposalFacet(*g.target.proposal, new(g.target.draft.ID), n.ID, n.Kind, n.Attributes, g.target.overlay(n.ID))
		return err == nil && f != nil
	}
	fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
	return err == nil && fs[g.facetKey] != nil
}

// inStore walks the parent chain to the selected datastore; a cycle or a
// dangling parent answers false.
func (g *savedGraphRefs) inStore(id string) bool {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		n, ok := g.nodes[id]
		if !ok {
			return false
		}
		if n.Kind == "datastore" {
			return n.ID == g.ds.ID
		}
		if n.ParentID == nil {
			return false
		}
		id = *n.ParentID
	}
	return false
}

func (g *savedGraphRefs) validateDatabaseNode(id, kind, path string) error {
	n, err := g.node(id, kind, path)
	if err != nil {
		return err
	}
	if !g.inStore(id) || !g.hasFacet(n) {
		return invalid(path, "Reference is outside the selected datastore/facet")
	}
	return nil
}

func (g *savedGraphRefs) validateDatabaseEdge(id string) error {
	e, ok := g.edges[id]
	if !ok {
		return notFound()
	}
	if !g.inStore(e.From) || !g.inStore(e.To) {
		return invalid("state.selection.id", "Edge is outside the selected datastore")
	}
	switch e.Kind {
	case "contains":
		if !g.hasFacet(g.nodes[e.From]) || !g.hasFacet(g.nodes[e.To]) {
			return invalid("state.selection.id", "Edge is outside the selected facet")
		}
	case "references":
		if !g.edgeHasFacet(e) {
			return invalid("state.selection.id", "Edge is outside the selected facet")
		}
	default:
		return invalid("state.selection.id", "Expected a relational edge")
	}
	return nil
}

func (g *savedGraphRefs) edgeHasFacet(e Edge) bool {
	if g.target.proposal != nil {
		f, err := effectiveProposalFacet(*g.target.proposal, new(g.target.draft.ID), e.ID, e.Kind, e.Attributes, g.target.overlay(e.ID))
		return err == nil && f != nil
	}
	fs, _, err := relationalFacetObject(e.Kind, e.Attributes)
	return err == nil && fs[g.facetKey] != nil
}

func validateSavedViewTarget(t BackendReadTarget) error {
	if t.ChangeProposal != nil || t.ImportCandidate != nil {
		return invalid("target", "SavedView-v1 rejects full/staged targets")
	}
	if (t.RevisionID == "") == (t.Proposal == nil) {
		return invalid("target", "Select exactly one source or pinned proposal")
	}
	if t.Proposal == nil {
		if !ValidID(t.RevisionID) {
			return invalid("target.revisionId", "Use a canonical nonzero UUID")
		}
	} else if !ValidID(t.Proposal.ProposalID) || !ValidID(t.Proposal.ProposalRevisionID) {
		return invalid("target.proposal", "Use canonical nonzero UUIDs")
	}
	return nil
}
func savedViewText(s, path string, empty bool) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 200 || strings.ContainsFunc(s, unicode.IsControl) || !empty && s == "" {
		return invalid(path, "Use at most 200 Unicode characters without control characters")
	}
	return nil
}
func validateSavedViewState(s SavedViewState) error {
	if (s.Flow == nil) == (s.Database == nil) {
		return invalid("state", "Select exactly one state variant")
	}
	var selection *SavedViewSelection
	var positions []SavedViewPosition
	var groups []string
	if f := s.Flow; f != nil {
		if err := validateSavedFlowState(f); err != nil {
			return err
		}
		selection, positions, groups = f.Selection, f.Positions, f.CollapsedGroupIDs
		if f.Scope.FlowID == "" && (len(positions) > 0 || len(groups) > 0) {
			return invalid("state.scope.flowId", "Positions and groups require a selected flow")
		}
	} else {
		d := s.Database
		if err := validateSavedDatabaseState(d); err != nil {
			return err
		}
		selection, positions, groups = d.Selection, d.Positions, d.CollapsedGroupIDs
	}
	if err := validateSavedSelection(selection); err != nil {
		return err
	}
	if err := validateSavedPositions(positions); err != nil {
		return err
	}
	return validateSavedGroups(groups)
}

func validateSavedFlowState(f *SavedFlowViewState) error {
	if f.Kind != "flow" {
		return invalid("state.kind", "Expected flow")
	}
	for path, id := range map[string]string{"entrypointId": f.Scope.EntrypointID, "flowId": f.Scope.FlowID, "dataNodeId": f.Scope.DataNodeID} {
		if id != "" && !ValidID(id) {
			return invalid("state.scope."+path, "Use a canonical UUID")
		}
	}
	if err := savedViewText(f.Filters.Search, "state.filters.search", true); err != nil {
		return err
	}
	for path, kind := range map[string]string{"accessKind": f.Filters.AccessKind, "reverseAccessKind": f.Filters.ReverseAccessKind} {
		if kind != "" && !runtimeAccessKind(kind) {
			return invalid("state.filters."+path, "Expected reads, writes, deletes or empty")
		}
	}
	return nil
}

func validateSavedDatabaseState(d *SavedDatabaseViewState) error {
	if d.Kind != "database" {
		return invalid("state.kind", "Expected database")
	}
	if !ValidID(d.Scope.DatastoreID) {
		return invalid("state.scope.datastoreId", "Use a canonical UUID")
	}
	if err := savedViewText(d.Scope.FacetKey, "state.scope.facetKey", false); err != nil {
		return err
	}
	if err := savedViewText(d.Filters.Search, "state.filters.search", true); err != nil {
		return err
	}
	if id := d.Filters.RelationshipTableID; id != "" && !ValidID(id) {
		return invalid("state.filters.relationshipTableId", "Use a canonical UUID")
	}
	return nil
}

func validateSavedSelection(selection *SavedViewSelection) error {
	if selection == nil {
		return nil
	}
	if selection.RecordType != "node" && selection.RecordType != "edge" {
		return invalid("state.selection.recordType", "Expected node or edge")
	}
	if !ValidID(selection.ID) {
		return invalid("state.selection.id", "Use a canonical UUID")
	}
	return nil
}

func validateSavedPositions(positions []SavedViewPosition) error {
	if positions == nil || len(positions) > 200 {
		return invalid("state.positions", "Use a nonnull array of at most 200 positions")
	}
	seen := map[string]bool{}
	for _, p := range positions {
		if !ValidID(p.NodeID) || seen[p.NodeID] {
			return invalid("state.positions.nodeId", "Use unique canonical UUIDs")
		}
		seen[p.NodeID] = true
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) || math.Abs(p.X) > 1000000 || math.Abs(p.Y) > 1000000 {
			return invalid("state.positions", "Coordinates must be finite and within [-1000000,1000000]")
		}
	}
	return nil
}

func validateSavedGroups(groups []string) error {
	if groups == nil || len(groups) > 200 {
		return invalid("state.collapsedGroupIds", "Use a nonnull array of at most 200 groups")
	}
	seen := map[string]bool{}
	for _, id := range groups {
		if !ValidID(id) || seen[id] {
			return invalid("state.collapsedGroupIds", "Use unique canonical UUIDs")
		}
		seen[id] = true
	}
	return nil
}
