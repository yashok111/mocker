package designscenario

import (
	"cmp"
	"context"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
)

func finalizeEventMap(ctx context.Context, out EventMapAnalysis) (EventMapAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return out, err
	}
	sortEventMap(&out)
	reasons := map[string]bool{}
	mark := func(reason string) { out.Complete = false; reasons[reason] = true }
	capEventMap(&out, mark)
	if err := fitEventMapBytes(ctx, &out, mark); err != nil {
		return out, err
	}
	out.Coverage.NodesReturned = len(out.Nodes)
	out.Coverage.EdgesReturned = len(out.Edges)
	out.Coverage.DiagnosticsReturned = len(out.Diagnostics)
	for _, reason := range []string{"nodes", "edges", "diagnostics", "output"} {
		if reasons[reason] {
			out.Coverage.TruncatedReasons = append(out.Coverage.TruncatedReasons, reason)
		}
	}
	return out, ctx.Err()
}

// sortEventMap orders every list deterministically and drops repeated ids.
func sortEventMap(out *EventMapAnalysis) {
	slices.SortFunc(out.Nodes, func(a, b EventMapNode) int {
		if n := cmp.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	out.Nodes = slices.CompactFunc(out.Nodes, func(a, b EventMapNode) bool { return a.ID == b.ID })
	slices.SortFunc(out.Edges, func(a, b EventMapEdge) int {
		if n := cmp.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	out.Edges = slices.CompactFunc(out.Edges, func(a, b EventMapEdge) bool { return a.ID == b.ID })
	slices.SortFunc(out.Diagnostics, func(a, b EventMapDiagnostic) int {
		if n := cmp.Compare(a.Code, b.Code); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})
	out.Diagnostics = slices.CompactFunc(out.Diagnostics, func(a, b EventMapDiagnostic) bool { return a.ID == b.ID })
}

// capEventMap applies the count limits; an edge or element reference that
// points at a dropped node is dropped or cleared with it.
func capEventMap(out *EventMapAnalysis, mark func(string)) {
	if len(out.Nodes) > MaxEventMapNodes {
		out.Nodes = out.Nodes[:MaxEventMapNodes]
		mark("nodes")
	}
	retained := eventMapNodeIDs(out.Nodes)
	edges := out.Edges[:0]
	for _, e := range out.Edges {
		if retained[e.Source] && retained[e.Target] {
			edges = append(edges, e)
		}
	}
	out.Edges = edges
	if len(out.Edges) > MaxEventMapEdges {
		out.Edges = out.Edges[:MaxEventMapEdges]
		mark("edges")
	}
	if len(out.Diagnostics) > MaxEventMapDiagnostics {
		out.Diagnostics = out.Diagnostics[:MaxEventMapDiagnostics]
		mark("diagnostics")
	}
	for i := range out.Diagnostics {
		if !retained[out.Diagnostics[i].ElementID] {
			out.Diagnostics[i].ElementID = ""
		}
	}
}

func eventMapNodeIDs(nodes []EventMapNode) map[string]bool {
	retained := map[string]bool{}
	for _, n := range nodes {
		retained[n.ID] = true
	}
	return retained
}

// eventMapBudget is the encoded-size allowance shared by nodes, edges and
// diagnostics, in that order.
type eventMapBudget struct {
	ctx       context.Context
	remaining int
}

func (b *eventMapBudget) take(value any) (bool, error) {
	if err := b.ctx.Err(); err != nil {
		return false, err
	}
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return false, err
	}
	if len(raw)+1 > b.remaining {
		return false, nil
	}
	b.remaining -= len(raw) + 1
	return true, nil
}

func fitEventMapBytes(ctx context.Context, out *EventMapAnalysis, mark func(string)) error {
	// Reserve ample room for the report envelope, coverage and truncation reasons.
	budget := &eventMapBudget{ctx: ctx, remaining: MaxEventMapBytes - 4096}
	keptNodes := out.Nodes[:0]
	for _, n := range out.Nodes {
		ok, err := budget.take(n)
		if err != nil {
			return err
		}
		if !ok {
			mark("output")
			break
		}
		keptNodes = append(keptNodes, n)
	}
	out.Nodes = keptNodes
	retained := eventMapNodeIDs(out.Nodes)
	keptEdges := out.Edges[:0]
	for _, e := range out.Edges {
		if !retained[e.Source] || !retained[e.Target] {
			continue
		}
		ok, err := budget.take(e)
		if err != nil {
			return err
		}
		if !ok {
			mark("output")
			break
		}
		keptEdges = append(keptEdges, e)
	}
	out.Edges = keptEdges
	keptDiagnostics := out.Diagnostics[:0]
	for _, d := range out.Diagnostics {
		if !retained[d.ElementID] {
			d.ElementID = ""
		}
		ok, err := budget.take(d)
		if err != nil {
			return err
		}
		if !ok {
			mark("output")
			break
		}
		keptDiagnostics = append(keptDiagnostics, d)
	}
	out.Diagnostics = keptDiagnostics
	return nil
}
