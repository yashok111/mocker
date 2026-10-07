package backendmodel

import (
	"slices"
	"strings"
)

type exploreIndex struct {
	graph    *EffectiveGraphSnapshot
	nodes    map[string]Node
	children map[string]int
	intent   map[string]bool
}

func newExploreIndex(g *EffectiveGraphSnapshot) *exploreIndex {
	index := &exploreIndex{graph: g, nodes: make(map[string]Node, len(g.State.Nodes)), children: map[string]int{}, intent: map[string]bool{}}
	for _, n := range g.State.Nodes {
		index.nodes[n.ID] = n
		if n.ParentID != nil {
			index.children[*n.ParentID]++
		}
	}
	for _, o := range g.Origins {
		if o.RecordType == "node" && o.Kind == "intent" {
			index.intent[o.SubjectID] = true
		}
	}
	return index
}
func (x *exploreIndex) validate(in ExploreInput) error {
	if !slices.Contains([]string{"overview", "children", "collections", "objects", "neighborhood"}, in.Mode) {
		return invalid("mode", "Unknown exploration mode")
	}
	for _, id := range []string{in.ScopeID, in.SelectedID} {
		if id != "" {
			if _, ok := x.nodes[id]; !ok {
				return notFound()
			}
		}
	}
	if in.Mode == "neighborhood" && in.ScopeID == "" {
		return invalid("scopeId", "Neighborhood requires a scope")
	}
	if in.Group != "" && in.Group != "ungrouped" && !strings.HasPrefix(in.Group, "prefix:") && !strings.HasPrefix(in.Group, "tag:") {
		return invalid("group", "Unknown collection")
	}
	return nil
}
func (x *exploreIndex) card(n Node) ExploreNode {
	item := compactExploreNode(n, x.children[n.ID])
	if x.graph.Target.ChangeProposal != nil || x.graph.Target.Proposal != nil {
		item.Origin = "base"
		if x.intent[n.ID] {
			item.Origin = "intent"
		}
	}
	return item
}
func (x *exploreIndex) page(in ExploreInput) *ExplorePage {
	g := x.graph
	out := &ExplorePage{Target: g.Target, TargetHash: g.Pins.TargetHash, SemanticHash: g.Pins.EffectiveSemanticHash, CoverageStatus: g.State.Revision.Coverage.Status, Nodes: []ExploreNode{}, Edges: []ExploreEdge{}, Groups: []ExploreGroup{}, Counts: map[string]int{}, Ancestors: []ExploreNode{}}
	scope, ok := x.nodes[in.ScopeID]
	if !ok {
		return out
	}
	out.Scope = new(x.card(scope))
	seen := map[string]bool{}
	for scope.ParentID != nil && !seen[scope.ID] {
		seen[scope.ID] = true
		p, exists := x.nodes[*scope.ParentID]
		if !exists {
			break
		}
		out.Ancestors = append(out.Ancestors, x.card(p))
		scope = p
	}
	slices.Reverse(out.Ancestors)
	return out
}
func (x *exploreIndex) within(n Node, scope string) bool {
	if scope == "" {
		return true
	}
	seen := map[string]bool{}
	for n.ParentID != nil && !seen[n.ID] {
		seen[n.ID] = true
		if *n.ParentID == scope {
			return true
		}
		p, ok := x.nodes[*n.ParentID]
		if !ok {
			break
		}
		n = p
	}
	return false
}
func (x *exploreIndex) match(in ExploreInput, counts map[string]int) []Node {
	neighbors := map[string]bool{in.ScopeID: true}
	if in.Mode == "neighborhood" {
		for _, e := range x.graph.State.Edges {
			if e.From == in.ScopeID {
				neighbors[e.To] = true
			}
			if e.To == in.ScopeID {
				neighbors[e.From] = true
			}
		}
	}
	matches := []Node{}
	for _, n := range x.graph.State.Nodes {
		if len(in.NodeIDs) > 0 && !slices.Contains(in.NodeIDs, n.ID) {
			continue
		}
		included := x.within(n, in.ScopeID)
		if in.Mode == "neighborhood" {
			included = neighbors[n.ID]
		}
		if !included {
			continue
		}
		counts[n.Kind]++
		if exploreNodeMatches(n, in) {
			matches = append(matches, n)
		}
	}
	slices.SortFunc(matches, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	return matches
}
func exploreNodeMatches(n Node, in ExploreInput) bool {
	switch in.Mode {
	case "overview":
		if !slices.Contains([]string{"system", "service", "datastore", "external_system"}, n.Kind) {
			return false
		}
	case "children":
		if n.ParentID == nil || *n.ParentID != in.ScopeID {
			return false
		}
	case "collections":
		if n.Kind != "http_operation" {
			return false
		}
	}
	if in.Kind != "" && n.Kind != in.Kind {
		return false
	}
	path := runtimeAttributeString(n.Attributes, "path")
	summary, _, tags := exploreOperationMetadata(n)
	if !exploreGroupMatches(in.Group, path, tags) {
		return false
	}
	text := n.Name + " " + path + " " + summary + " " + runtimeAttributeString(n.Attributes, "method") + " " + n.ExternalKey
	return in.Search == "" || strings.Contains(strings.ToLower(text), strings.ToLower(in.Search))
}
func exploreGroupMatches(group, path string, tags []string) bool {
	if prefix, ok := strings.CutPrefix(group, "prefix:"); ok {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	if tag, ok := strings.CutPrefix(group, "tag:"); ok {
		return slices.Contains(tags, tag)
	}
	return group != "ungrouped" || (path == "" && len(tags) == 0)
}
func fillExploreGroups(out *ExplorePage, nodes []Node, limit int, after, pid, scope string) {
	groups := map[string]*ExploreGroup{}
	add := func(id, label, basis string) {
		v := groups[id]
		if v == nil {
			v = &ExploreGroup{ID: id, Label: label, Basis: basis}
			groups[id] = v
		}
		v.Count++
	}
	for _, n := range nodes {
		path := runtimeAttributeString(n.Attributes, "path")
		_, _, tags := exploreOperationMetadata(n)
		if path != "" {
			parts := strings.Split(strings.Trim(path, "/"), "/")
			size := 1
			if len(parts) >= 3 && parts[0] == "api" {
				size = 3
			}
			prefix := "/" + strings.Join(parts[:size], "/")
			add("prefix:"+prefix, prefix, "path_prefix")
		}
		for _, tag := range slices.Compact(slices.Sorted(slices.Values(tags))) {
			add("tag:"+tag, tag, "api_tag")
		}
		if path == "" && len(tags) == 0 {
			add("ungrouped", "Не распределено", "ungrouped")
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out.Total = len(keys)
	for _, key := range keys {
		if key <= after {
			continue
		}
		if len(out.Groups) == limit {
			out.NextCursor = encodeGraphPage("explore", pid, scope, out.Groups[len(out.Groups)-1].ID)
			break
		}
		out.Groups = append(out.Groups, *groups[key])
	}
}
func compactExploreEdge(e Edge) ExploreEdge {
	return ExploreEdge{ID: e.ID, Kind: e.Kind, From: e.From, To: e.To, Label: runtimeAttributeString(e.Attributes, "label")}
}
func (x *exploreIndex) nodesPage(out *ExplorePage, nodes []Node, limit int, after, scope string) {
	visible := map[string]bool{}
	for _, n := range nodes {
		if n.ID <= after {
			continue
		}
		if len(out.Nodes) == limit {
			out.NextCursor = encodeGraphPage("explore", x.graph.State.Revision.ProjectID, scope, out.Nodes[len(out.Nodes)-1].ID)
			break
		}
		out.Nodes = append(out.Nodes, x.card(n))
		visible[n.ID] = true
	}
	for _, e := range x.graph.State.Edges {
		if visible[e.From] && visible[e.To] {
			out.EdgeTotal++
			if len(out.Edges) < 300 {
				out.Edges = append(out.Edges, compactExploreEdge(e))
			}
		}
	}
}
func (x *exploreIndex) incident(nodes []Node, anchor string) []Edge {
	matched := map[string]bool{anchor: true}
	for _, n := range nodes {
		matched[n.ID] = true
	}
	edges := []Edge{}
	for _, e := range x.graph.State.Edges {
		if (e.From == anchor && matched[e.To]) || (e.To == anchor && matched[e.From]) {
			edges = append(edges, e)
		}
	}
	slices.SortFunc(edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	return edges
}
func (x *exploreIndex) neighborhood(out *ExplorePage, nodes []Node, anchor string, limit int, after, scope string) {
	incident := x.incident(nodes, anchor)
	out.Nodes = append(out.Nodes, x.card(x.nodes[anchor]))
	visible := map[string]bool{anchor: true}
	out.EdgeTotal = len(incident)
	if !slices.ContainsFunc(nodes, func(n Node) bool { return n.ID == anchor }) {
		out.Total++
	}
	for _, e := range incident {
		if e.ID <= after {
			continue
		}
		if len(out.Edges) == limit-1 {
			out.NextCursor = encodeGraphPage("explore", x.graph.State.Revision.ProjectID, scope, out.Edges[len(out.Edges)-1].ID)
			break
		}
		out.Edges = append(out.Edges, compactExploreEdge(e))
		other := e.From
		if other == anchor {
			other = e.To
		}
		if !visible[other] {
			if n, ok := x.nodes[other]; ok {
				out.Nodes = append(out.Nodes, x.card(n))
				visible[other] = true
			}
		}
	}
}
