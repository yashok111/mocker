package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

func loadLegacyEffectiveGraph(ctx context.Context, q importReader, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	legacy, err := resolveBackendTarget(ctx, q, pid, target)
	if err != nil {
		return nil, err
	}
	out, err := loadSourceEffectiveGraph(ctx, q, pid, legacy.revisionID)
	if err != nil {
		return nil, err
	}
	out.Target = target
	nodes := map[string]Node{}
	edges := map[string]Edge{}
	for _, n := range out.State.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range out.State.Edges {
		edges[e.ID] = e
	}
	for _, o := range legacy.draft.Overlays {
		raw, err := json.Marshal(o.Values)
		if err != nil {
			return nil, err
		}
		if o.RecordType == "node" {
			n, ok := nodes[o.SubjectID]
			if !ok {
				n = Node{ID: o.SubjectID, Kind: o.Kind, Name: o.Name, ParentID: o.ParentID, Attributes: map[string]jsontext.Value{}, EvidenceIDs: []string{}}
			}
			payload, err := changeWithFacet(sourceNodePayload(n), o.FacetKey, raw)
			if err != nil {
				return nil, err
			}
			n.Attributes = payload.Attributes
			nodes[n.ID] = n
		} else {
			e, ok := edges[o.SubjectID]
			if !ok {
				e = Edge{ID: o.SubjectID, Kind: o.Kind, Attributes: map[string]jsontext.Value{}, EvidenceIDs: []string{}}
			}
			e.From, e.To = o.FromID, o.ToID
			payload, err := changeWithFacet(sourceEdgePayload(e), o.FacetKey, raw)
			if err != nil {
				return nil, err
			}
			e.Attributes = payload.Attributes
			edges[e.ID] = e
		}
	}
	out.State.Nodes = []Node{}
	out.State.Edges = []Edge{}
	for _, n := range nodes {
		out.State.Nodes = append(out.State.Nodes, n)
	}
	for _, e := range edges {
		out.State.Edges = append(out.State.Edges, e)
	}
	slices.SortFunc(out.State.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(out.State.Edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	out.Pins.ViewSchemaVersion = ProposalDocumentVersion
	out.Pins.EffectiveSemanticHash = legacy.draft.SemanticHash
	if err := finishEffectivePins(out, out.Source.SourceVector); err != nil {
		return nil, err
	}
	return out, nil
}
