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
	reasons := map[string]bool{}
	mark := func(reason string) { out.Complete = false; reasons[reason] = true }
	if len(out.Nodes) > MaxEventMapNodes {
		out.Nodes = out.Nodes[:MaxEventMapNodes]
		mark("nodes")
	}
	retained := map[string]bool{}
	for _, n := range out.Nodes {
		retained[n.ID] = true
	}
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
	// Reserve ample room for the report envelope, coverage and truncation reasons.
	budget := MaxEventMapBytes - 4096
	trim := func(value any) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		raw, err := jsonx.Marshal(value)
		if err != nil {
			return false, err
		}
		if len(raw)+1 > budget {
			return false, nil
		}
		budget -= len(raw) + 1
		return true, nil
	}
	keptNodes := out.Nodes[:0]
	for _, n := range out.Nodes {
		ok, err := trim(n)
		if err != nil {
			return out, err
		}
		if !ok {
			mark("output")
			break
		}
		keptNodes = append(keptNodes, n)
	}
	out.Nodes = keptNodes
	retained = map[string]bool{}
	for _, n := range out.Nodes {
		retained[n.ID] = true
	}
	keptEdges := out.Edges[:0]
	for _, e := range out.Edges {
		if !retained[e.Source] || !retained[e.Target] {
			continue
		}
		ok, err := trim(e)
		if err != nil {
			return out, err
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
		ok, err := trim(d)
		if err != nil {
			return out, err
		}
		if !ok {
			mark("output")
			break
		}
		keptDiagnostics = append(keptDiagnostics, d)
	}
	out.Diagnostics = keptDiagnostics
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
