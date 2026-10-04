package backendmodel

func lineagePolicyForSchema(schema string) string {
	if schema == EventsSchemaVersion {
		return "field-lineage-traversal-v2"
	}
	return LineageTraversalPolicy
}
func (r lineageProofReader) eventRef(p *lineageProof, ref LineageValueRef) error {
	route, ok := r.edges[ref.RouteID]
	if !ok {
		p.status = runtimeWorseStatus(p.status, "unresolved")
		p.add("unresolved_event_route", true)
		return nil
	}
	if err := r.edge(p, route); err != nil {
		return err
	}
	if len(route.EvidenceIDs) == 0 {
		p.add("missing_event_route_proof", true)
	}
	if runtimeString(route.Attributes["deliveryStatus"]) != "declared" {
		p.add("unknown_delivery", true)
	}
	ids := []string{ref.EndpointID, route.From, route.To}
	if route.Kind == "emits" {
		ids = append(ids, runtimeString(route.Attributes["channelId"]))
	} else {
		ids = append(ids, runtimeString(route.Attributes["messageId"]))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		n, ok := r.nodes[id]
		if !ok || n.Kind == "unresolved_target" {
			p.status = runtimeWorseStatus(p.status, "unresolved")
			p.add("unresolved_event_endpoint", true)
			continue
		}
		if err := r.node(p, n, nil); err != nil {
			return err
		}
		if len(n.EvidenceIDs) == 0 {
			p.add("missing_event_endpoint_proof", true)
		}
		if err := r.owner(p, n); err != nil {
			return err
		}
	}
	return nil
}
func (r lineageProofReader) eventMapping(p *lineageProof, m lineageIndexedMapping) error {
	if m.attrs.Transport != nil || m.node.ParentID == nil {
		return nil
	}
	parent := r.nodes[*m.node.ParentID]
	if parent.Kind != "flow_step" || parent.ParentID == nil {
		return nil
	}
	for _, ref := range m.attrs.Sources {
		if ref.Kind != "event_field" || r.edges[ref.RouteID].Kind != "delivered_to" {
			continue
		}
		flow := r.nodes[*parent.ParentID]
		if flow.ParentID == nil {
			p.add("unresolved_dispatch", true)
			continue
		}
		found := false
		for _, edge := range r.handles[ref.EndpointID] {
			if edge.Kind == "handles" && edge.From == ref.EndpointID && edge.To == *flow.ParentID {
				found = true
				if err := r.edge(p, edge); err != nil {
					return err
				}
				if len(edge.EvidenceIDs) == 0 {
					p.add("missing_dispatch_proof", true)
				}
				if err := r.node(p, r.nodes[edge.To], nil); err != nil {
					return err
				}
			}
		}
		if !found {
			p.add("unresolved_dispatch", true)
		}
		if err := r.node(p, flow, nil); err != nil {
			return err
		}
		if err := r.owner(p, flow); err != nil {
			return err
		}
	}
	// The complete hyperedge remains intact; no co-input is inferred from a seed.
	return nil
}
