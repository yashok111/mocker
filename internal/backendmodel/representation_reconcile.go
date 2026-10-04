package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"slices"
)

func representationContains(from, to Node) (bool, bool) {
	switch {
	case representationOwner(to.Kind):
		return slices.Contains([]string{"system", "service", "module"}, from.Kind), true
	case to.Kind == "representation_field":
		return representationOwner(from.Kind), true
	case to.Kind == "field_mapping" && representationOwner(from.Kind):
		return true, true
	}
	return false, false
}

func validateRepresentationLineageMapping(n Node, a LineageMappingAttributes, nodes map[string]Node, edges map[string]Edge, handles map[string][]Edge) error {
	if !representationMapping(n.Kind, n.Attributes, false) {
		return validateEventsLineageMapping(n, a, nodes, edges, handles)
	}
	if n.ParentID == nil {
		return semantic("parentId", "A representation mapping requires a parent")
	}
	if a.Transport != nil {
		return validateEventsLineageTransport(n, nodes[*n.ParentID], a, nodes, edges)
	}
	for _, ref := range append(slices.Clone(a.Sources), a.Destination) {
		if ref.Kind != "event_field" {
			continue
		}
		if err := validateLineageValueTargetForSchema(ref, nodes, edges, ComposedSchemaVersion); err != nil {
			return err
		}
		if runtimeString(edges[ref.RouteID].Attributes["deliveryStatus"]) != "declared" && (a.AnalysisStatus == "complete" || a.Transform.Kind != "unknown_transform") {
			return semantic("sources", "An unknown event route requires a partial unknown transform")
		}
	}
	return nil
}

func validateRepresentationGraph(ctx context.Context, g SourceStructuralGraph, d *[]ImportDiagnostic) error {
	nodes := map[string]Node{}
	contains := map[string][]Edge{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range g.Edges {
		if e.Kind == "contains" {
			contains[e.To] = append(contains[e.To], e)
		}
	}
	selectors := map[string]bool{}
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !representationSubject(n.Kind, false) {
			continue
		}
		path := "nodes/" + n.ID
		parent := Node{}
		if n.ParentID != nil {
			parent = nodes[*n.ParentID]
		}
		valid, _ := representationContains(parent, n)
		if !valid || len(contains[n.ID]) != 1 || contains[n.ID][0].From != parent.ID {
			*d = append(*d, ImportDiagnostic{Code: "backend_graph_invalid", Path: path + "/parentId", Message: "Representation requires one agreeing contains edge and its typed parent"})
		}
		if n.Kind != "representation_field" {
			continue
		}
		raw, err := canonicalValue(n.Attributes["selector"])
		if err != nil {
			return err
		}
		key := parent.ID + "\x00" + string(raw)
		if selectors[key] {
			*d = append(*d, ImportDiagnostic{Code: "backend_graph_invalid", Path: path + "/attributes/selector", Message: "Duplicate representation selector within its parent"})
		}
		selectors[key] = true
	}
	return nil
}

// representationReferences preserves every contextual event address component
// while adding representation field references to the source6 schema visitor.
func representationReferences(kind string, a map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	if err := validateRepresentationAttributes(kind, a, edge, persisted); err != nil {
		return nil, err
	}
	if kind != "field_mapping" {
		return []relationalReference{}, nil
	}
	sources, _ := relationalArray(a["sources"], MaxLineageSources)
	refs := []relationalReference{}
	add := func(path, typ, kind, value string) {
		ref := relationalReference{Path: path, Kind: kind, RecordType: typ, Key: value}
		if persisted {
			ref.ID, ref.Key = value, ""
		}
		refs = append(refs, ref)
	}
	for i, raw := range append(slices.Clone(sources), a["destination"]) {
		m, _ := relationalObject(raw)
		path := "/attributes/destination/"
		if i < len(sources) {
			path = fmt.Sprintf("/attributes/sources/%d/", i)
		}
		key := runtimeReferenceName("nodeKey", persisted)
		refKind := runtimeString(m["kind"])
		add(path+key, "node", refKind, runtimeString(m[key]))
		if refKind == "event_field" {
			for _, member := range []struct{ key, kind, typ string }{{"endpointKey", "event_endpoint", "node"}, {"routeKey", "event_route", "edge"}} {
				key := runtimeReferenceName(member.key, persisted)
				add(path+key, member.typ, member.kind, runtimeString(m[key]))
			}
		}
	}
	if raw, ok := a["transport"]; ok {
		tr, _ := relationalObject(raw)
		for _, member := range []struct{ key, kind string }{{"emitsEdgeKey", "emits"}, {"deliveryEdgeKey", "delivered_to"}} {
			key := runtimeReferenceName(member.key, persisted)
			add("/attributes/transport/"+key, "edge", member.kind, runtimeString(tr[key]))
		}
	}
	return refs, nil
}
