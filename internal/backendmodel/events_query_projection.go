package backendmodel

import (
	"encoding/json/v2"
	"slices"
)

func (p *eventsProjection) matchesRoute(r EventsReferences) bool {
	return p.in.SeedNodeID == "" || slices.Contains([]string{r.ProducerID, r.MessageID, r.ChannelID, r.ConsumerID}, p.in.SeedNodeID)
}
func (p *eventsProjection) routes() error {
	deliveries := map[string][]Edge{}
	producers := map[string]bool{}
	emissions := map[string]bool{}
	consumerSeed := p.in.SeedNodeID != "" && p.nodes[p.in.SeedNodeID].Kind == "consumer"
	producerSeed := p.in.SeedNodeID != "" && p.nodes[p.in.SeedNodeID].Kind == "flow_step"
	if err := p.indexRoutes(deliveries, producers, emissions, producerSeed); err != nil {
		return err
	}
	if err := p.joinRoutes(deliveries, consumerSeed); err != nil {
		return err
	}
	if p.truncations["item_limit"] {
		return nil
	}
	if err := p.orphanRoutes(emissions); err != nil {
		return err
	}
	if p.truncations["item_limit"] {
		return nil
	}
	return p.unboundEmits(producers)
}
func (p *eventsProjection) indexRoutes(deliveries map[string][]Edge, producers, emissions map[string]bool, producerSeed bool) error {
	for _, id := range p.edgeIDs() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		e := p.edges[id]
		if e.Kind == "delivered_to" {
			message := runtimeAttributeString(e.Attributes, "messageId")
			// Consumer/message/channel selection narrows delivery buckets before
			// the join. A producer selects its emission below instead.
			if producerSeed || p.matchesRoute(EventsReferences{MessageID: message, ChannelID: e.From, ConsumerID: e.To}) {
				deliveries[message+"/"+e.From] = append(deliveries[message+"/"+e.From], e)
			}
		}
		if e.Kind == "emits" {
			producers[e.From] = true
			emissions[e.To+"/"+runtimeAttributeString(e.Attributes, "channelId")] = true
		}
	}
	return nil
}
func (p *eventsProjection) joinRoutes(deliveries map[string][]Edge, consumerSeed bool) error {
	for _, id := range p.edgeIDs() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		e := p.edges[id]
		if e.Kind != "emits" {
			continue
		}
		channel := runtimeAttributeString(e.Attributes, "channelId")
		r := EventsReferences{ProducerID: e.From, MessageID: e.To, ChannelID: channel, EmitsEdgeID: e.ID}
		if !consumerSeed && !p.matchesRoute(r) {
			continue
		}
		routes := deliveries[e.To+"/"+channel]
		if len(routes) == 0 {
			// A consumer-filtered empty bucket means this producer is excluded,
			// not that its configured route is missing from the inspected scope.
			if consumerSeed {
				continue
			}
			if !p.admit() {
				return nil
			}
			p.route(e.ID+"/", r, nil, "No matching configured route discovered in the pinned inspected scope")
			continue
		}
		for _, d := range routes {
			if err := p.ctx.Err(); err != nil {
				return err
			}
			r.ConsumerID = d.To
			r.DeliveryEdgeID = d.ID
			if !p.admit() {
				return nil
			}
			p.route(e.ID+"/"+d.ID, r, &d, "")
		}
	}
	return nil
}
func (p *eventsProjection) orphanRoutes(emissions map[string]bool) error {
	// A configured subscription remains visible even when no producer was found.
	for _, id := range p.edgeIDs() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		e := p.edges[id]
		if e.Kind != "delivered_to" {
			continue
		}
		message := runtimeAttributeString(e.Attributes, "messageId")
		if emissions[message+"/"+e.From] {
			continue
		}
		r := EventsReferences{MessageID: message, ChannelID: e.From, ConsumerID: e.To, DeliveryEdgeID: e.ID}
		if !p.matchesRoute(r) {
			continue
		}
		if !p.admit() {
			return nil
		}
		p.route("/"+e.ID, r, &e, "No producer discovered for this subscription in the pinned inspected scope")
	}
	return nil
}
func (p *eventsProjection) unboundEmits(producers map[string]bool) error {
	for _, id := range p.nodeIDs() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		n := p.nodes[id]
		if n.Kind != "flow_step" || runtimeAttributeString(n.Attributes, "stepKind") != "emit" || producers[id] {
			continue
		}
		r := EventsReferences{ProducerID: id}
		if !p.matchesRoute(r) {
			continue
		}
		if !p.admit() {
			return nil
		}
		p.route("node/"+id, r, nil, "No emission binding discovered for this emit step in the pinned inspected scope")
	}
	return nil
}

func (p *eventsProjection) route(key string, r EventsReferences, delivery *Edge, reason string) {
	w := p.witness([]string{r.ProducerID, r.MessageID, r.ChannelID, r.ConsumerID}, []string{r.EmitsEdgeID, r.DeliveryEdgeID})
	dispatch := []EventsDispatch{}
	related := []EventsRelatedRoute{}
	if r.ConsumerID != "" {
		dispatch = p.dispatch(r.ConsumerID)
		related = p.related(r.ConsumerID)
		w = p.combine(w, r.ConsumerID, dispatch)
	}
	condition, group := EventsScalar{Status: "unknown", Reason: "Configured route unavailable"}, EventsScalar{Status: "unknown", Reason: "Configured route unavailable"}
	if delivery != nil {
		condition = eventsScalar(delivery.Attributes, "condition")
		group = eventsScalar(delivery.Attributes, "group")
	}
	for _, value := range []EventsScalar{condition, group} {
		if value.Status != "known" && value.Reason != "" {
			w.Limitations = append(w.Limitations, value.Reason)
		}
	}
	context := p.emitContext(r.ProducerID)
	if reason != "" {
		eventsWitnessReason(&w, reason, "unresolved")
	}
	if w.Status != "explicit" {
		if reason == "" {
			reason = "Configured route or handler dispatch has unavailable source proof"
		}
		p.items = append(p.items, eventsOrderedItem{key: key, item: EventsItem{Kind: "boundary", Boundary: &EventsBoundaryItem{View: "routes", Reason: reason, References: r, Condition: condition, Group: group, Dispatch: dispatch, Related: related, EmitContext: context, Witness: w}}})
	} else {
		p.items = append(p.items, eventsOrderedItem{key: key, item: EventsItem{Kind: "route", Route: &EventsRouteItem{References: r, Condition: condition, Group: group, Dispatch: dispatch, Related: related, EmitContext: context, Witness: w}}})
	}
}
func (p *eventsProjection) jobs() error {
	for _, id := range p.nodeIDs() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		n := p.nodes[id]
		if n.Kind != "job" || p.in.ServiceID != "" && p.service(id) != p.in.ServiceID {
			continue
		}
		if !p.admit() {
			return nil
		}
		trigger := EventsTrigger{}
		if json.Unmarshal(n.Attributes["trigger"], &trigger) != nil {
			trigger = EventsTrigger{Kind: "unknown", Reason: "Configured trigger unavailable"}
		}
		dispatch := p.dispatch(id)
		w := p.combine(p.witness([]string{id}, nil), id, dispatch)
		for _, value := range []EventsScalar{trigger.Expression, trigger.Timezone, trigger.Duration} {
			if value.Status != "known" && value.Reason != "" {
				w.Limitations = append(w.Limitations, value.Reason)
			}
		}
		if trigger.Reason != "" {
			w.Limitations = append(w.Limitations, trigger.Reason)
		}
		r := EventsReferences{JobID: id}
		if w.Status != "explicit" {
			reason := "Configured job dispatch has unavailable source proof"
			if slices.ContainsFunc(dispatch, func(d EventsDispatch) bool { return len(d.FlowIDs) > 0 }) {
				reason = "Configured job binding is known; downstream behavior remains qualified"
			}
			p.items = append(p.items, eventsOrderedItem{key: id, item: EventsItem{Kind: "boundary", Boundary: &EventsBoundaryItem{View: "jobs", Reason: reason, References: r, Trigger: &trigger, Dispatch: dispatch, Related: []EventsRelatedRoute{}, Witness: w}}})
		} else {
			p.items = append(p.items, eventsOrderedItem{key: id, item: EventsItem{Kind: "job", Job: &EventsJobItem{References: r, Trigger: trigger, Dispatch: dispatch, Witness: w}}})
		}
	}
	return nil
}
func (p *eventsProjection) serviceCalls() error {
	for _, id := range p.nodeIDs() {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		n := p.nodes[id]
		if n.Kind != "flow_step" || runtimeAttributeString(n.Attributes, "stepKind") != "call" || p.in.ServiceID != "" && p.service(id) != p.in.ServiceID {
			continue
		}
		found := false
		for _, e := range p.out[id] {
			if err := p.ctx.Err(); err != nil {
				return err
			}
			if e.Kind != "calls" {
				continue
			}
			found = true
			target, exists := p.nodes[e.To]
			if exists && !slices.Contains([]string{"http_operation", "external_system", "unresolved_target"}, target.Kind) {
				continue
			}
			if exists && target.Kind == "unresolved_target" && runtimeAttributeString(target.Attributes, "expectedKind") != "http_operation" {
				continue
			}
			if !p.admit() {
				return nil
			}
			p.serviceCall(id, e, target, exists)
		}
		if !found {
			if !p.admit() {
				return nil
			}
			w := p.witness([]string{id}, nil)
			reason := "No call target binding discovered in the pinned inspected scope"
			eventsWitnessReason(&w, reason, "unresolved")
			p.items = append(p.items, eventsOrderedItem{key: "node/" + id, item: EventsItem{Kind: "boundary", Boundary: &EventsBoundaryItem{View: "service_calls", Reason: reason, References: EventsReferences{CallStepID: id}, Dispatch: []EventsDispatch{}, Related: []EventsRelatedRoute{}, Witness: w}}})
		}
	}
	return nil
}

func (p *eventsProjection) serviceCall(id string, e Edge, target Node, exists bool) {
	r := EventsReferences{CallStepID: id, CallsEdgeID: e.ID, TargetID: e.To}
	w := p.witness([]string{id, e.To}, []string{e.ID})
	dispatch := []EventsDispatch{}
	reason := ""
	if exists && target.Kind == "http_operation" {
		r.OperationID = e.To
		r.TargetServiceID = p.service(e.To)
		// Keep the exact call boundary without gaining a downstream flow witness.
		if w.Status == "explicit" {
			dispatch = p.dispatch(e.To)
			w = p.combine(w, e.To, dispatch)
		}
		if r.TargetServiceID == "" {
			eventsWitnessReason(&w, "Target operation owning service unavailable", "unresolved")
		}
	} else {
		reason = "External or unresolved service-call boundary"
		eventsWitnessReason(&w, reason, "unresolved")
	}
	if w.Status != "explicit" {
		if reason == "" {
			reason = "Service call or dispatch has unavailable source proof"
		}
		p.items = append(p.items, eventsOrderedItem{key: e.ID, item: EventsItem{Kind: "boundary", Boundary: &EventsBoundaryItem{View: "service_calls", Reason: reason, References: r, Dispatch: dispatch, Related: []EventsRelatedRoute{}, Witness: w}}})
	} else {
		p.items = append(p.items, eventsOrderedItem{key: e.ID, item: EventsItem{Kind: "service_call", ServiceCall: &EventsServiceCallItem{References: r, Dispatch: dispatch, Witness: w}}})
	}
}
