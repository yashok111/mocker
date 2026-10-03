package backendmodel

import "slices"

// Contextual values require the exact addressed route, including the message
// UUID and endpoint. A producer address and consumer address remain distinct.
func validateLineageValueTargetForSchema(ref LineageValueRef, nodes map[string]Node, edges map[string]Edge, schema string) error {
	if ref.Kind != "event_field" {
		return validateLineageValueTarget(ref, nodes)
	}
	if schema != EventsSchemaVersion {
		return invalid("seed", "Contextual event refs require source5")
	}
	field, ok := nodes[ref.NodeID]
	if !ok {
		return notFound()
	}
	if field.Kind != "event_field" || field.ParentID == nil || nodes[*field.ParentID].Kind != "message" {
		return invalid("seed", "Event field requires its exact message")
	}
	endpoint := nodes[ref.EndpointID]
	route, ok := edges[ref.RouteID]
	if !ok {
		return invalid("seed", "Event route does not exist")
	}
	message := *field.ParentID
	switch route.Kind {
	case "emits":
		if eventsEmit(endpoint.Kind, endpoint.Attributes, false) && route.From == endpoint.ID && route.To == message && (nodes[runtimeString(route.Attributes["channelId"])].Kind == "channel" || eventsUnresolved(nodes[runtimeString(route.Attributes["channelId"])], "channel")) {
			return nil
		}
	case "delivered_to":
		if endpoint.Kind == "consumer" && route.To == endpoint.ID && nodes[route.From].Kind == "channel" && runtimeString(route.Attributes["messageId"]) == message {
			return nil
		}
	}
	return invalid("seed", "Event value requires the exact producer or consumer message route")
}
func validateEventsLineageMapping(n Node, a LineageMappingAttributes, nodes map[string]Node, edges map[string]Edge, handles map[string][]Edge) error {
	if !contextualLineageMapping(n.Kind, n.Attributes, false) {
		return nil
	}
	if n.ParentID == nil {
		return semantic("parentId", "Contextual mapping requires a parent")
	}
	parent := nodes[*n.ParentID]
	refs := append(slices.Clone(a.Sources), a.Destination)
	for _, ref := range refs {
		if err := validateLineageValueTargetForSchema(ref, nodes, edges, EventsSchemaVersion); err != nil {
			return err
		}
	}
	if a.Transport != nil {
		return validateEventsLineageTransport(n, parent, a, nodes, edges)
	}
	if a.Destination.Kind == "event_field" {
		return validateEventsLineageSerialization(n, parent, a, nodes, edges)
	}
	return validateEventsLineageDeserialization(n, parent, a, nodes, edges, handles)
}
func validateEventsLineageTransport(n, parent Node, a LineageMappingAttributes, nodes map[string]Node, edges map[string]Edge) error {
	if parent.Kind != "consumer" || a.Destination.Kind != "event_field" || a.Destination.EndpointID != parent.ID || len(a.Sources) == 0 {
		return semantic("transport", "Transport requires producer event sources and consumer destination parent")
	}
	emits := edges[a.Transport.EmitsEdgeID]
	delivery := edges[a.Transport.DeliveryEdgeID]
	if emits.Kind != "emits" || delivery.Kind != "delivered_to" || a.Destination.RouteID != delivery.ID || delivery.To != parent.ID || runtimeString(emits.Attributes["channelId"]) != delivery.From || emits.To != runtimeString(delivery.Attributes["messageId"]) {
		return semantic("transport", "Transport requires the exact emits/message/channel/consumer tuple")
	}
	if err := validateEventsLineageTransportSources(a.Sources, emits, nodes); err != nil {
		return err
	}
	unknown := runtimeString(emits.Attributes["deliveryStatus"]) != "declared" || runtimeString(delivery.Attributes["deliveryStatus"]) != "declared" || nodes[delivery.From].Kind != "channel"
	if unknown && (n.Freshness == nil || n.Freshness.Status != "stale") && (a.AnalysisStatus == "complete" || a.Transform.Kind != "unknown_transform") {
		return semantic("transport", "Unknown route requires a partial unknown transform")
	}
	return nil
}
func validateEventsLineageSerialization(n, parent Node, a LineageMappingAttributes, nodes map[string]Node, edges map[string]Edge) error {
	ref := a.Destination
	route := edges[ref.RouteID]
	if !eventsEmit(parent.Kind, parent.Attributes, false) || parent.ID != ref.EndpointID || route.Kind != "emits" || (n.Freshness == nil || n.Freshness.Status != "stale") && (runtimeString(route.Attributes["deliveryStatus"]) != "declared" || nodes[runtimeString(route.Attributes["channelId"])].Kind != "channel") {
		return semantic("destination", "Serialization requires the parent emit and declared concrete route")
	}
	for _, source := range a.Sources {
		if source.Kind != "port" || source.NodeID != parent.ID {
			return semantic("sources", "Serialization requires local producer ports")
		}
	}
	return nil
}
func validateEventsLineageDeserialization(n, parent Node, a LineageMappingAttributes, nodes map[string]Node, edges map[string]Edge, handles map[string][]Edge) error {
	if parent.Kind != "flow_step" || a.Destination.Kind != "port" || a.Destination.NodeID != parent.ID || parent.ParentID == nil {
		return semantic("destination", "Deserialization requires a local destination port in its parent step")
	}
	for _, source := range a.Sources {
		if source.Kind != "event_field" || edges[source.RouteID].Kind != "delivered_to" {
			return semantic("sources", "Deserialization requires consumer-scoped event sources")
		}
		route := edges[source.RouteID]
		if runtimeString(route.Attributes["deliveryStatus"]) != "declared" && (n.Freshness == nil || n.Freshness.Status != "stale") && (a.AnalysisStatus == "complete" || a.Transform.Kind != "unknown_transform") {
			return semantic("sources", "Unknown delivery requires a partial unknown transform")
		}
		if !eventsConsumerFlow(source.EndpointID, *parent.ParentID, nodes, handles) {
			return semantic("parentId", "Deserialization requires an explicitly handled consumer flow")
		}
	}
	return nil
}

func eventsConsumerFlow(consumer, flow string, nodes map[string]Node, handles map[string][]Edge) bool {
	owner := nodes[flow]
	if owner.Kind != "flow" || owner.ParentID == nil {
		return false
	}
	for _, e := range handles[consumer] {
		if e.Kind == "handles" && e.From == consumer && e.To == *owner.ParentID && nodes[e.To].Kind == "handler" {
			return true
		}
	}
	return false
}

func validateEventsLineageTransportSources(sources []LineageValueRef, emits Edge, nodes map[string]Node) error {
	for _, ref := range sources {
		if ref.Kind != "event_field" || ref.EndpointID != emits.From || ref.RouteID != emits.ID || nodes[ref.NodeID].ParentID == nil || *nodes[ref.NodeID].ParentID != emits.To {
			return semantic("transport", "Every transport source must address the exact emission message")
		}
	}
	return nil
}
