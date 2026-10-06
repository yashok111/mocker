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
