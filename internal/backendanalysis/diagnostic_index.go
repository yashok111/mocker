package backendanalysis

import (
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// diagnosticIndex holds the per-evaluation lookups the diagnostic rules used
// to rebuild by rescanning the whole graph (review 2026-10-06, F138, F144):
//
//   - accessed: every node ID a reads/writes/deletes edge targets, and that
//     target's parent. unused_table rescanned all edges per table with one
//     visit each, O(tables × edges), and exhausted the default 50,000-visit
//     budget at 50 unused tables over 1,000 edges, after which every later node
//     went unchecked and the report was marked incomplete.
//   - children: nodes by parent, in graph order. loopRule and emitRule rescanned
//     all nodes per loop or emit step.
//   - service: scope.service membership, memoised along the parent chain.
//     includes() called objectInService per node and per edge, and each call
//     rebuilt a full node map and scanned every edge outside any budget:
//     ~4×10^8 map insertions at 20k nodes.
//
// Building them is O(nodes + edges) once, like d.out, and charges no visits:
// visits measure rule work, not the cost of looking a record up.
type diagnosticIndex struct {
	accessed map[string]bool
	children map[string][]backendmodel.Node
	edges    map[string]backendmodel.Edge
	service  map[string]bool
}

func (d *diagnosticEvaluator) accessed(id string) bool {
	if d.index.accessed == nil {
		d.index.accessed = map[string]bool{}
		for _, e := range d.graph.State.Edges {
			if !slices.Contains([]string{"reads", "writes", "deletes"}, e.Kind) {
				continue
			}
			d.index.accessed[e.To] = true
			if parent := d.nodes[e.To].ParentID; parent != nil {
				d.index.accessed[*parent] = true
			}
		}
	}
	return d.index.accessed[id]
}

func (d *diagnosticEvaluator) childrenOf(parent string) []backendmodel.Node {
	if d.index.children == nil {
		d.index.children = map[string][]backendmodel.Node{}
		for _, n := range d.graph.State.Nodes {
			if n.ParentID != nil {
				d.index.children[*n.ParentID] = append(d.index.children[*n.ParentID], n)
			}
		}
	}
	return d.index.children[parent]
}

// objectInScopeService is objectInService (diff.go) over the indexes: an edge
// belongs when the FIRST edge with its ID has an end in the service, a node
// when some ancestor (itself included) is a service whose ID or name is the
// scope's service.
func (d *diagnosticEvaluator) objectInScopeService(a ObjectAddress, service string) bool {
	if a.RecordType == "edge" {
		if d.index.edges == nil {
			d.index.edges = map[string]backendmodel.Edge{}
			for _, e := range d.graph.State.Edges {
				if _, seen := d.index.edges[e.ID]; !seen {
					d.index.edges[e.ID] = e
				}
			}
		}
		e, ok := d.index.edges[a.ID]
		return ok && (d.nodeInScopeService(e.From, service) || d.nodeInScopeService(e.To, service))
	}
	return d.nodeInScopeService(a.ID, service)
}

func (d *diagnosticEvaluator) nodeInScopeService(id, service string) bool {
	if d.index.service == nil {
		d.index.service = map[string]bool{}
	}
	// Walk up until a memoised answer, a matching service, a missing node, a
	// root or a cycle; every node on the walk shares that answer, because each
	// one's own walk is a suffix of this one.
	chain := []string{}
	seen := map[string]bool{}
	result := false
	for id != "" && !seen[id] {
		if known, ok := d.index.service[id]; ok {
			result = known
			break
		}
		seen[id] = true
		chain = append(chain, id)
		n, ok := d.nodes[id]
		if !ok {
			break
		}
		if n.Kind == "service" && (n.ID == service || n.Name == service) {
			result = true
			break
		}
		if n.ParentID == nil {
			break
		}
		id = *n.ParentID
	}
	for _, member := range chain {
		d.index.service[member] = result
	}
	return result
}
