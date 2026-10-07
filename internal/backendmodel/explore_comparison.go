package backendmodel

import (
	"slices"
	"strings"
)

type ExploreChange struct {
	ID         string `json:"id"`
	RecordType string `json:"recordType"`
	Change     string `json:"change"`
}

func exploreNodeValue(n Node) any {
	return struct {
		Kind, Name string
		Parent     *string
		Attributes any
	}{n.Kind, n.Name, n.ParentID, n.Attributes}
}
func exploreEdgeValue(e Edge) any {
	return struct {
		Kind, From, To string
		Attributes     any
	}{e.Kind, e.From, e.To, e.Attributes}
}
func exploreDifferent(a, b any) (bool, error) {
	left, err := requestDigest(a)
	if err != nil {
		return false, err
	}
	right, err := requestDigest(b)
	return left != right, err
}

func exploreStateValues(state RevisionState) map[string]any {
	values := make(map[string]any, len(state.Nodes)+len(state.Edges))
	for _, n := range state.Nodes {
		values["node:"+n.ID] = exploreNodeValue(n)
	}
	for _, e := range state.Edges {
		values["edge:"+e.ID] = exploreEdgeValue(e)
	}
	return values
}
func exploreChanges(before, after RevisionState) ([]ExploreChange, error) {
	old, next := exploreStateValues(before), exploreStateValues(after)
	changes := []ExploreChange{}
	add := func(key, change string) {
		kind, id, _ := strings.Cut(key, ":")
		changes = append(changes, ExploreChange{id, kind, change})
	}
	for key, value := range next {
		previous, ok := old[key]
		if !ok {
			add(key, "added")
			continue
		}
		different, err := exploreDifferent(previous, value)
		if err != nil {
			return nil, err
		}
		if different {
			add(key, "changed")
		}
	}
	for key := range old {
		if _, ok := next[key]; !ok {
			add(key, "removed")
		}
	}
	slices.SortFunc(changes, func(a, b ExploreChange) int { return strings.Compare(a.RecordType+":"+a.ID, b.RecordType+":"+b.ID) })
	return changes, nil
}
func exploreComparisonPage(out *ExplorePage, changes []ExploreChange, pid, scope string, limit int, after string) {
	for _, change := range changes {
		key := change.RecordType + ":" + change.ID
		if key <= after {
			continue
		}
		if len(out.Changes) == limit {
			last := out.Changes[len(out.Changes)-1]
			out.NextCursor = encodeGraphPage("explore", pid, scope, last.RecordType+":"+last.ID)
			break
		}
		out.Changes = append(out.Changes, change)
	}
}
func indexExploreEdges(edges []Edge) map[string]Edge {
	index := make(map[string]Edge, len(edges))
	for _, e := range edges {
		index[e.ID] = e
	}
	return index
}
func exploreComparisonRecords(out *ExplorePage, g *EffectiveGraphSnapshot) {
	before, after := newExploreIndex(&EffectiveGraphSnapshot{State: g.Source.State}), newExploreIndex(g)
	oldEdges, newEdges := indexExploreEdges(g.Source.State.Edges), indexExploreEdges(g.State.Edges)
	visible := map[string]bool{}
	for _, c := range out.Changes {
		if c.RecordType == "node" {
			visible[c.ID] = true
			continue
		}
		if e, ok := oldEdges[c.ID]; ok {
			out.BeforeEdges = append(out.BeforeEdges, compactExploreEdge(e))
			visible[e.From] = true
			visible[e.To] = true
		}
		if e, ok := newEdges[c.ID]; ok {
			out.Edges = append(out.Edges, compactExploreEdge(e))
			visible[e.From] = true
			visible[e.To] = true
		}
	}
	ids := make([]string, 0, len(visible))
	for id := range visible {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if n, ok := before.nodes[id]; ok {
			out.BeforeNodes = append(out.BeforeNodes, before.card(n))
		}
		if n, ok := after.nodes[id]; ok {
			out.Nodes = append(out.Nodes, after.card(n))
		}
	}
	out.EdgeTotal = len(out.Edges)
}

// Comparison is derived from the proposal's own immutable baseline. It never
// compares heads or treats absence from a paginated page as deletion.
func projectExploreComparison(g *EffectiveGraphSnapshot, in ExploreInput) (*ExplorePage, error) {
	if g.Target.ChangeProposal == nil || g.Source == nil {
		return nil, invalid("target", "Comparison requires an exact full proposal")
	}
	changes, err := exploreChanges(g.Source.State, g.State)
	if err != nil {
		return nil, err
	}
	size := in.Limit
	if size == 0 {
		size = 12
	}
	size = min(size, 25)
	scope := g.Pins.TargetHash + ":comparison"
	limit, after, err := decodeExplorePage(size, in.Cursor, g.State.Revision.ProjectID, scope)
	if err != nil {
		return nil, err
	}
	out := &ExplorePage{Target: g.Target, TargetHash: g.Pins.TargetHash, SemanticHash: g.Pins.EffectiveSemanticHash, CoverageStatus: g.State.Revision.Coverage.Status, Nodes: []ExploreNode{}, Edges: []ExploreEdge{}, Groups: []ExploreGroup{}, Counts: map[string]int{}, Ancestors: []ExploreNode{}, Total: len(changes), BeforeTarget: new(BackendReadTarget{RevisionID: g.Pins.BaseRevisionID}), BeforeNodes: []ExploreNode{}, BeforeEdges: []ExploreEdge{}, Changes: []ExploreChange{}}
	exploreComparisonPage(out, changes, g.State.Revision.ProjectID, scope, limit, after)
	exploreComparisonRecords(out, g)
	return out, nil
}
