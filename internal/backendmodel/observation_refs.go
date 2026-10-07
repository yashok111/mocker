package backendmodel

import (
	"context"
	"slices"
)

// ResolveObservationRef shares the diagram artifact owner/namespace checks. An
// equal-looking row in a copied artifact is not an identity match.
func ResolveObservationRef(ctx context.Context, g *EffectiveGraphSnapshot, ref DiagramRef) (bool, error) {
	if e := validateDiagramRef(ref); e != nil {
		return false, e
	}
	if ref.Kind == "record" {
		if ref.RecordType == "node" {
			return slices.ContainsFunc(g.State.Nodes, func(n Node) bool { return n.ID == ref.ID }), nil
		}
		return slices.ContainsFunc(g.State.Edges, func(n Edge) bool { return n.ID == ref.ID }), nil
	}
	return newDiagramArtifactResolver(ctx, g).resolve(ref)
}

// ObservationRefResolver answers ResolveObservationRef for many refs against
// one graph: record refs through node/edge ID sets built once, artifact refs
// through one shared (memoising) artifact resolver. Correlate resolved every
// record of a set — up to 100,000 — with a linear scan of the graph each
// (review 2026-10-06, F34).
type ObservationRefResolver struct {
	ctx          context.Context
	graph        *EffectiveGraphSnapshot
	nodes, edges map[string]bool
	artifacts    *diagramArtifactResolver
}

func NewObservationRefResolver(ctx context.Context, g *EffectiveGraphSnapshot) *ObservationRefResolver {
	return &ObservationRefResolver{ctx: ctx, graph: g}
}

// Resolve gives the same answer as ResolveObservationRef(ctx, g, ref).
func (r *ObservationRefResolver) Resolve(ref DiagramRef) (bool, error) {
	if e := validateDiagramRef(ref); e != nil {
		return false, e
	}
	if ref.Kind == "record" {
		if r.nodes == nil {
			r.nodes = make(map[string]bool, len(r.graph.State.Nodes))
			r.edges = make(map[string]bool, len(r.graph.State.Edges))
			for _, n := range r.graph.State.Nodes {
				r.nodes[n.ID] = true
			}
			for _, e := range r.graph.State.Edges {
				r.edges[e.ID] = true
			}
		}
		if ref.RecordType == "node" {
			return r.nodes[ref.ID], nil
		}
		return r.edges[ref.ID], nil
	}
	if r.artifacts == nil {
		r.artifacts = newDiagramArtifactResolver(r.ctx, r.graph)
	}
	return r.artifacts.resolve(ref)
}
