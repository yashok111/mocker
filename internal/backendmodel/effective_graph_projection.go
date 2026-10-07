package backendmodel

import (
	"context"
	"slices"
	"strings"
)

func graphReadTarget(in GraphQueryInput) BackendReadTarget {
	return BackendReadTarget{RevisionID: in.RevisionID, Proposal: in.Proposal, ChangeProposal: in.ChangeProposal, ImportCandidate: in.ImportCandidate}
}
func (r *Repo) queryEffectiveGraph(ctx context.Context, pid string, in GraphQueryInput) (*GraphPage, error) {
	graph, err := r.ResolveEffectiveGraph(ctx, pid, graphReadTarget(in))
	if err != nil {
		return nil, err
	}
	if err := validateEffectiveGraphQuery(in, graph.Pins.StructuralSchemaVersion); err != nil {
		return nil, err
	}
	if in.ServiceID != "" {
		if err := validateWorkspaceService(graph, in.ServiceID); err != nil {
			return nil, err
		}
	}
	filter := in
	filter.Cursor = ""
	filter.Limit = 0
	scope, err := requestDigest(struct {
		Pins  EffectiveGraphPins
		Query GraphQueryInput
	}{graph.Pins, filter})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "effective-graph", pid, scope, true)
	if err != nil {
		return nil, err
	}
	out := &GraphPage{Total: new(0), Target: new(graph.Target), Pins: new(graph.Pins), ViewSchemaVersion: graph.Pins.ViewSchemaVersion, Nodes: []Node{}, Edges: []Edge{}, Origins: []EffectiveFieldOrigin{}, Identities: []EffectiveIdentity{}, BaselineEvidence: []EffectiveEvidenceBasis{}}
	if graph.Source != nil {
		out.Source = sourceVectorReadContext(graph.Source)
	}
	page := effectiveGraphPage{graph: graph, in: in, out: out, pid: pid, scope: scope, after: after, limit: limit}
	var ids []string
	if in.RecordType == "nodes" {
		ids, err = page.nodes(ctx)
	} else {
		ids, err = page.edges(ctx)
	}
	if err != nil {
		return nil, err
	}
	appendEffectiveGraphSidecars(out, graph, ids)
	return out, nil
}

// effectiveGraphPage fills one cursor page of nodes or edges: every match
// counts toward Total, only those past the cursor and within the limit
// are returned, and the first one beyond the limit sets NextCursor.
type effectiveGraphPage struct {
	graph      *EffectiveGraphSnapshot
	in         GraphQueryInput
	out        *GraphPage
	pid, scope string
	after      string
	limit      int
}

func (p effectiveGraphPage) nodes(ctx context.Context) ([]string, error) {
	ids := []string{}
	nodes := slices.Clone(p.graph.State.Nodes)
	slices.SortFunc(nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	for _, n := range nodes {
		if !effectiveNodeMatches(n, p.in) {
			continue
		}
		match, e := workspaceMatches(ctx, p.graph, p.in, "node", n.ID)
		if e != nil {
			return nil, e
		}
		if !match {
			continue
		}
		*p.out.Total++
		if n.ID <= p.after {
			continue
		}
		if len(p.out.Nodes) == p.limit {
			p.out.NextCursor = encodeGraphPage("effective-graph", p.pid, p.scope, p.out.Nodes[len(p.out.Nodes)-1].ID)
			continue
		}
		n = effectiveNodeRecord(p.graph, n)
		p.out.Nodes = append(p.out.Nodes, n)
		ids = append(ids, n.ID)
	}
	return ids, nil
}

func (p effectiveGraphPage) edges(ctx context.Context) ([]string, error) {
	ids := []string{}
	edges := slices.Clone(p.graph.State.Edges)
	slices.SortFunc(edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	for _, e := range edges {
		if !effectiveEdgeMatches(e, p.in) {
			continue
		}
		match, err := workspaceMatches(ctx, p.graph, p.in, "edge", e.ID)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		*p.out.Total++
		if e.ID <= p.after {
			continue
		}
		if len(p.out.Edges) == p.limit {
			p.out.NextCursor = encodeGraphPage("effective-graph", p.pid, p.scope, p.out.Edges[len(p.out.Edges)-1].ID)
			continue
		}
		e = effectiveEdgeRecord(p.graph, e)
		p.out.Edges = append(p.out.Edges, e)
		ids = append(ids, e.ID)
	}
	return ids, nil
}

func appendEffectiveGraphSidecars(out *GraphPage, graph *EffectiveGraphSnapshot, ids []string) {
	for _, edge := range out.Edges {
		if name, ok := graph.EdgeNames[edge.ID]; ok {
			out.EdgeNames = append(out.EdgeNames, EffectiveEdgeName{ID: edge.ID, Name: name})
		}
	}
	for _, origin := range graph.Origins {
		if slices.Contains(ids, origin.SubjectID) {
			out.Origins = append(out.Origins, origin)
		}
	}
	for _, identity := range graph.Identities {
		ref := changeIdentityRef(identity.Target)
		if slices.Contains(ids, ref.ID) {
			out.Identities = append(out.Identities, identity)
		}
	}
	for _, e := range graph.BaselineEvidence {
		if slices.Contains(ids, e.SubjectID) {
			out.BaselineEvidence = append(out.BaselineEvidence, e)
		}
	}

}
func validateEffectiveGraphQuery(in GraphQueryInput, schema string) error {
	if in.RecordType != "nodes" && in.RecordType != "edges" {
		return semantic("recordType", "Query must select nodes or edges")
	}
	if err := validateEffectiveGraphSelectors(in); err != nil {
		return err
	}
	profile := profileForSchema(schema)
	if schema == ComposedSchemaVersion {
		profile = ComposedProfile
	}
	kinds := SupportedNodeKindsForProfile(profile)
	if in.RecordType == "edges" {
		kinds = SupportedEdgeKindsForProfile(profile)
	}
	if in.Kind != "" && !slices.Contains(kinds, in.Kind) {
		return semantic("kind", "Unsupported kind filter")
	}
	for _, id := range []string{in.ParentID, in.From, in.To} {
		if id != "" && !ValidID(id) {
			return semantic("selectors", "ID selectors must be UUIDs")
		}
	}
	if len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return semantic("search", "Invalid search string")
	}
	return nil
}
func validateEffectiveGraphSelectors(in GraphQueryInput) error {
	if err := validateWorkspaceFilters(in); err != nil {
		return err
	}
	if in.ID != "" && (workspaceFiltered(in) || !ValidID(in.ID) || in.Cursor != "" || in.Kind != "" || in.Search != "" || in.ParentID != "" || in.From != "" || in.To != "") {
		return invalid("id", "ID selector cannot be combined with filters or cursor")
	}
	if in.RecordType == "nodes" && (in.From != "" || in.To != "") || in.RecordType == "edges" && (in.Search != "" || in.ParentID != "") {
		return semantic("selectors", "Selectors are not valid for this record type")
	}
	return nil
}

func effectiveNodeMatches(n Node, in GraphQueryInput) bool {
	return (in.ID == "" || n.ID == in.ID) && (in.Kind == "" || n.Kind == in.Kind) && (in.ParentID == "" || n.ParentID != nil && *n.ParentID == in.ParentID) && (in.Search == "" || strings.Contains(strings.ToLower(n.Name), strings.ToLower(in.Search)))
}
func effectiveEdgeMatches(e Edge, in GraphQueryInput) bool {
	return (in.ID == "" || e.ID == in.ID) && (in.Kind == "" || e.Kind == in.Kind) && (in.From == "" || e.From == in.From) && (in.To == "" || e.To == in.To)
}
func effectiveNodeRecord(graph *EffectiveGraphSnapshot, n Node) Node {
	if graph.Source != nil && (graph.Target.ChangeProposal != nil || graph.Source.SourceVector != nil) {
		n.Source = sourceRecordReadContext(graph.Source, "node", n.ID)
		n.Ownership = nil
		n.Freshness = nil
		n.ExternalKey = ""
	}
	return n
}
func effectiveEdgeRecord(graph *EffectiveGraphSnapshot, e Edge) Edge {
	if graph.Source != nil && (graph.Target.ChangeProposal != nil || graph.Source.SourceVector != nil) {
		e.Source = sourceRecordReadContext(graph.Source, "edge", e.ID)
		e.Ownership = nil
		e.Freshness = nil
		e.ExternalKey = ""
	}
	return e
}
