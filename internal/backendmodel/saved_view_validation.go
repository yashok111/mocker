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
	var err error
	if s.Flow != nil && (target.proposal != nil || !isRuntimeSchema(state.Revision.SchemaVersion) && !effective) {
		return nil, savedUnsupported()
	}
	if s.Database != nil && !isRelationalSchema(state.Revision.SchemaVersion) && !effective {
		return nil, savedUnsupported()
	}
	nodes := map[string]Node{}
	edges := map[string]Edge{}
	for _, n := range state.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range state.Edges {
		edges[e.ID] = e
	}
	if target.proposal != nil {
		for _, o := range target.draft.Overlays {
			if o.Base == nil {
				if o.RecordType == "node" {
					nodes[o.SubjectID] = Node{ID: o.SubjectID, Kind: o.Kind, Name: o.Name, ParentID: o.ParentID}
				} else {
					edges[o.SubjectID] = Edge{ID: o.SubjectID, Kind: o.Kind, From: o.FromID, To: o.ToID}
				}
			} else if o.RecordType == "edge" {
				e := edges[o.SubjectID]
				e.From, e.To = o.FromID, o.ToID
				edges[e.ID] = e
			}
		}
	}
	node := func(id, kind, path string) (Node, error) {
		n, ok := nodes[id]
		if !ok {
			return Node{}, notFound()
		}
		if kind != "" && n.Kind != kind {
			return Node{}, invalid(path, "Reference has the wrong kind")
		}
		return n, nil
	}
	if f := s.Flow; f != nil {
		var flow Node
		if f.Scope.EntrypointID != "" {
			entry, err := node(f.Scope.EntrypointID, "", "state.scope.entrypointId")
			if err != nil {
				return nil, err
			}
			if !sourceRuntimeEntrypoint(state.Revision.SchemaVersion, entry.Kind) {
				return nil, invalid("state.scope.entrypointId", "Expected a supported pinned entrypoint")
			}
		}
		if f.Scope.FlowID != "" {
			flow, err = node(f.Scope.FlowID, "flow", "state.scope.flowId")
			if err != nil {
				return nil, err
			}
		}
		if f.Scope.EntrypointID != "" && flow.ID != "" {
			related := false
			for _, e := range edges {
				h := nodes[e.To]
				if e.From == f.Scope.EntrypointID && e.Kind == "handles" && h.Kind == "handler" && flow.ParentID != nil && *flow.ParentID == h.ID {
					related = true
				}
			}
			if !related {
				return nil, invalid("state.scope.flowId", "Flow must be a child of the entrypoint's pinned handler")
			}
		}
		if f.Scope.DataNodeID != "" {
			n, err := node(f.Scope.DataNodeID, "", "state.scope.dataNodeId")
			if err != nil {
				return nil, err
			}
			if !slices.Contains([]string{"table", "column", "view"}, n.Kind) {
				return nil, invalid("state.scope.dataNodeId", "Expected a supported relational data node")
			}
		}
		for _, pos := range f.Positions {
			n, err := node(pos.NodeID, "flow_step", "state.positions.nodeId")
			if err != nil {
				return nil, err
			}
			if n.ParentID == nil || *n.ParentID != flow.ID {
				return nil, invalid("state.positions.nodeId", "Step must directly belong to the selected flow")
			}
		}
		for _, id := range f.CollapsedGroupIDs {
			if _, err := node(id, "transaction", "state.collapsedGroupIds"); err != nil {
				return nil, err
			}
			known := false
			for _, n := range nodes {
				if n.Kind == "flow_step" && n.ParentID != nil && *n.ParentID == flow.ID {
					var tc runtimeTransactionContext
					if err := json.Unmarshal(n.Attributes["transactionContext"], &tc); err == nil && tc.Status == "known" && tc.TransactionID == id {
						known = true
					}
				}
			}
			if !known {
				return nil, invalid("state.collapsedGroupIds", "Group must be a known transaction of a step in this flow")
			}
		}
		if f.Selection != nil {
			if f.Selection.RecordType == "node" {
				if _, err := node(f.Selection.ID, "", "state.selection.id"); err != nil {
					return nil, err
				}
			} else if _, ok := edges[f.Selection.ID]; !ok {
				return nil, notFound()
			}
		}
		return pins, nil
	}
	d := s.Database
	ds, err := node(d.Scope.DatastoreID, "datastore", "state.scope.datastoreId")
	if err != nil {
		return nil, err
	}
	if !relationalSubject(ds.Kind, ds.Attributes, false) {
		return nil, savedUnsupported()
	}
	if target.proposal != nil && (d.Scope.DatastoreID != target.proposal.DatastoreID || d.Scope.FacetKey != target.proposal.FacetKey) {
		return nil, invalid("state.scope", "Proposal scope must match its creation binding")
	}
	hasFacet := func(n Node) bool {
		if target.proposal != nil {
			f, err := effectiveProposalFacet(*target.proposal, new(target.draft.ID), n.ID, n.Kind, n.Attributes, target.overlay(n.ID))
			return err == nil && f != nil
		}
		fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
		return err == nil && fs[d.Scope.FacetKey] != nil
	}
	if !hasFacet(ds) {
		return nil, invalid("state.scope.facetKey", "Facet does not exist in this datastore")
	}
	inStore := func(id string) bool {
		seen := map[string]bool{}
		for id != "" && !seen[id] {
			seen[id] = true
			n, ok := nodes[id]
			if !ok {
				return false
			}
			if n.Kind == "datastore" {
				return n.ID == ds.ID
			}
			if n.ParentID == nil {
				return false
			}
			id = *n.ParentID
		}
		return false
	}
	validateNode := func(id, kind, path string) error {
		n, err := node(id, kind, path)
		if err != nil {
			return err
		}
		if !inStore(id) || !hasFacet(n) {
			return invalid(path, "Reference is outside the selected datastore/facet")
		}
		return nil
	}
	if d.Filters.RelationshipTableID != "" {
		if err := validateNode(d.Filters.RelationshipTableID, "table", "state.filters.relationshipTableId"); err != nil {
			return nil, err
		}
	}
	for _, pos := range d.Positions {
		if err := validateNode(pos.NodeID, "table", "state.positions.nodeId"); err != nil {
			return nil, err
		}
	}
	for _, id := range d.CollapsedGroupIDs {
		if err := validateNode(id, "db_schema", "state.collapsedGroupIds"); err != nil {
			return nil, err
		}
	}
	if sel := d.Selection; sel != nil {
		if sel.RecordType == "node" {
			if err := validateNode(sel.ID, "", "state.selection.id"); err != nil {
				return nil, err
			}
		} else {
			e, ok := edges[sel.ID]
			if !ok {
				return nil, notFound()
			}
			if !inStore(e.From) || !inStore(e.To) {
				return nil, invalid("state.selection.id", "Edge is outside the selected datastore")
			}
			if e.Kind == "contains" {
				if !hasFacet(nodes[e.From]) || !hasFacet(nodes[e.To]) {
					return nil, invalid("state.selection.id", "Edge is outside the selected facet")
				}
			} else if e.Kind == "references" {
				if target.proposal != nil {
					f, err := effectiveProposalFacet(*target.proposal, new(target.draft.ID), e.ID, e.Kind, e.Attributes, target.overlay(e.ID))
					if err != nil || f == nil {
						return nil, invalid("state.selection.id", "Edge is outside the selected facet")
					}
				} else {
					fs, _, err := relationalFacetObject(e.Kind, e.Attributes)
					if err != nil || fs[d.Scope.FacetKey] == nil {
						return nil, invalid("state.selection.id", "Edge is outside the selected facet")
					}
				}
			} else {
				return nil, invalid("state.selection.id", "Expected a relational edge")
			}
		}
	}
	return pins, nil
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
		selection, positions, groups = f.Selection, f.Positions, f.CollapsedGroupIDs
		if f.Scope.FlowID == "" && (len(positions) > 0 || len(groups) > 0) {
			return invalid("state.scope.flowId", "Positions and groups require a selected flow")
		}
	} else {
		d := s.Database
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
		selection, positions, groups = d.Selection, d.Positions, d.CollapsedGroupIDs
	}
	if selection != nil {
		if selection.RecordType != "node" && selection.RecordType != "edge" {
			return invalid("state.selection.recordType", "Expected node or edge")
		}
		if !ValidID(selection.ID) {
			return invalid("state.selection.id", "Use a canonical UUID")
		}
	}
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
	if groups == nil || len(groups) > 200 {
		return invalid("state.collapsedGroupIds", "Use a nonnull array of at most 200 groups")
	}
	clear(seen)
	for _, id := range groups {
		if !ValidID(id) || seen[id] {
			return invalid("state.collapsedGroupIds", "Use unique canonical UUIDs")
		}
		seen[id] = true
	}
	return nil
}
