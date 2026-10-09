package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

type eventsWitnessBuilder struct {
	projection                          *eventsProjection
	witness                             EventsWitness
	nodes, edges, evidence, limitations map[string]bool
}

func (p *eventsProjection) witness(nodeIDs, edgeIDs []string) EventsWitness {
	b := eventsWitnessBuilder{projection: p, witness: EventsWitness{Provenance: "source", NodeIDs: []string{}, EdgeIDs: []string{}, EvidenceIDs: []string{}, Status: "explicit", Limitations: []string{}}, nodes: map[string]bool{}, edges: map[string]bool{}, evidence: map[string]bool{}, limitations: map[string]bool{}}
	for _, id := range nodeIDs {
		if id == "" || b.nodes[id] {
			continue
		}
		if !b.reserve() {
			break
		}
		b.nodes[id] = true
		b.node(id)
	}
	for _, id := range edgeIDs {
		if id == "" || b.edges[id] {
			continue
		}
		if !b.reserve() {
			break
		}
		b.edges[id] = true
		b.edge(id)
	}
	b.witness.NodeIDs = runtimeSortedKeys(b.nodes)
	b.witness.EdgeIDs = runtimeSortedKeys(b.edges)
	b.witness.EvidenceIDs = runtimeSortedKeys(b.evidence)
	b.witness.Limitations = runtimeSortedKeys(b.limitations)
	return b.witness
}
func (b *eventsWitnessBuilder) observe(ids []string, fresh *AssertionFreshness) {
	selected := []string{}
	for _, id := range ids {
		if b.evidence[id] {
			continue
		}
		if len(b.evidence) == EventsMaxWitnessRecords {
			b.projection.truncations["witness_limit"] = true
			b.witness.Status = runtimeWorseStatus(b.witness.Status, "inferred")
			b.limitations["Evidence witness budget exhausted; remaining proof is unavailable"] = true
			break
		}
		b.evidence[id] = true
		selected = append(selected, id)
	}
	// A reused evidence list has already contributed its status. Freshness
	// still belongs to this record and must be checked independently.
	if len(ids) != 0 && len(selected) == 0 {
		if fresh != nil && fresh.Status == "stale" {
			b.witness.Status = runtimeWorseStatus(b.witness.Status, "stale")
			b.limitations["Stale source assertion retained from an earlier snapshot"] = true
		}
		return
	}
	b.witness.Status = runtimeWorseStatus(b.witness.Status, (&runtimeFlowProjection{evidence: b.projection.evidence}).recordStatus(selected, fresh, b.limitations))
}
func (b *eventsWitnessBuilder) reserve() bool {
	if len(b.nodes)+len(b.edges) < EventsMaxWitnessRecords {
		return true
	}
	b.projection.truncations["witness_limit"] = true
	b.witness.Status = runtimeWorseStatus(b.witness.Status, "inferred")
	b.limitations["Witness record budget exhausted; remaining proof is unavailable"] = true
	return false
}
func (b *eventsWitnessBuilder) node(id string) {
	n, ok := b.projection.nodes[id]
	if !ok {
		b.witness.Status = "unresolved"
		b.limitations["Missing source node "+id] = true
		return
	}
	b.sourceObserve("node", n.ID, n.EvidenceIDs, n.Freshness)
	if n.Kind == "unresolved_target" {
		b.witness.Status = "unresolved"
		b.limitations["Unresolved source target "+id] = true
	}
	for _, key := range []string{"analysisStatus", "dispatchStatus", "exitStatus"} {
		value := runtimeAttributeString(n.Attributes, key)
		if value != "" && value != "complete" {
			b.limitations[id+": "+key+"="+value] = true
			if value == "unknown" {
				b.witness.Status = runtimeWorseStatus(b.witness.Status, "unresolved")
			} else {
				b.witness.Status = runtimeWorseStatus(b.witness.Status, "inferred")
			}
		}
	}
	for _, gap := range runtimeAttributeStrings(n.Attributes, "gaps") {
		b.limitations[id+": "+gap] = true
	}
	for _, key := range []string{"reason", "dispatchReason"} {
		if reason := runtimeAttributeString(n.Attributes, key); reason != "" {
			b.limitations[id+": "+reason] = true
		}
	}
}
func (b *eventsWitnessBuilder) edge(id string) {
	e, ok := b.projection.edges[id]
	if !ok {
		b.witness.Status = "unresolved"
		b.limitations["Missing source edge "+id] = true
		return
	}
	b.sourceObserve("edge", e.ID, e.EvidenceIDs, e.Freshness)
	if slices.Contains([]string{"emits", "delivered_to"}, e.Kind) && runtimeAttributeString(e.Attributes, "deliveryStatus") != "declared" {
		b.witness.Status = runtimeWorseStatus(b.witness.Status, "unresolved")
		b.limitations["Unknown configured delivery "+id] = true
		if reason := runtimeAttributeString(e.Attributes, "deliveryReason"); reason != "" {
			b.limitations[reason] = true
		}
	}
}

// combine merges the dispatch witnesses of target id into base. The dispatch
// side is folded ONCE per target and cached: route() runs this for every route
// item, and folding the k dispatch entries into each route re-observed the
// whole growing union k times per route — R × k × union witness observations,
// tens of millions for one channel at the 5,000-item cap, none of it charged
// to a budget (review 2026-10-06, F113). Now each route pays one merge. The
// merged ID set, worst status and limitation union are the same; only which
// IDs survive a witness_limit cut may differ, and that cut is reported.
func (p *eventsProjection) combine(base EventsWitness, id string, dispatch []EventsDispatch) EventsWitness {
	if len(dispatch) == 0 {
		return base
	}
	folded, ok := p.combinedCache[id]
	if !ok {
		folded = dispatch[0].Witness
		for _, d := range dispatch[1:] {
			folded = p.combineWitness(folded, d.Witness)
		}
		if p.combinedCache == nil {
			p.combinedCache = map[string]EventsWitness{}
		}
		p.combinedCache[id] = folded
	}
	return p.combineWitness(base, folded)
}
func eventsWitnessReason(w *EventsWitness, reason, status string) {
	w.Status = runtimeWorseStatus(w.Status, status)
	w.Limitations = append(w.Limitations, reason)
	slices.Sort(w.Limitations)
	w.Limitations = slices.Compact(w.Limitations)
}
func eventsScalar(attrs map[string]jsontext.Value, key string) EventsScalar {
	var value EventsScalar
	if json.Unmarshal(attrs[key], &value) != nil {
		return EventsScalar{Status: "unknown", Reason: "Configured value unavailable"}
	}
	return value
}
func (p *eventsProjection) dispatch(id string) []EventsDispatch {
	if cached, ok := p.dispatchCache[id]; ok {
		return cached
	}
	result := []EventsDispatch{}
	for _, e := range p.out[id] {
		if p.ctx.Err() != nil {
			break
		}
		if e.Kind != "handles" {
			continue
		}
		if len(result) == EventsMaxWitnessRecords {
			p.truncations["witness_limit"] = true
			break
		}
		if !p.auxiliary() {
			break
		}
		origin := p.witness([]string{id}, nil)
		originNode := p.nodes[id]
		originProof := (&runtimeFlowProjection{effective: p.effective, source: p.source, evidence: p.evidence}).sourceRecordStatus("node", originNode.ID, nil, originNode.EvidenceIDs, originNode.Freshness, map[string]bool{})
		d := EventsDispatch{HandlesEdgeID: e.ID, FlowIDs: []string{}, Witness: p.witness([]string{e.To}, []string{e.ID})}
		originDiagnostic := origin
		originDiagnostic.Status = originProof
		p.dispatchDiagnostic(&d, "origin_proof", "node", id, originDiagnostic)
		p.dispatchDiagnostic(&d, "handler_proof", "node", e.To, p.witness([]string{e.To}, nil))
		p.dispatchDiagnostic(&d, "handles_proof", "edge", e.ID, p.witness(nil, []string{e.ID}))
		h, ok := p.nodes[e.To]
		if !ok || h.Kind != "handler" {
			d.UnresolvedTargetID = e.To
			eventsWitnessReason(&d.Witness, "Unresolved configured handler "+e.To, "unresolved")
		} else {
			d.HandlerID = e.To
			// A stale/unknown handle is a boundary; never follow it into a flow.
			if d.Witness.Status == "explicit" && originProof == "explicit" {
				p.dispatchFlows(&d, h)
			} else {
				p.sourceDispatchFlows(&d, h)
			}
		}
		d.Witness = p.combineWitness(d.Witness, origin)
		slices.Sort(d.FlowIDs)
		result = append(result, d)
	}
	if len(result) == 0 {
		result = append(result, EventsDispatch{FlowIDs: []string{}, Witness: p.witness([]string{id}, nil)})
		eventsWitnessReason(&result[0].Witness, "Missing discovered handler for "+id, "unresolved")
		p.dispatchDiagnostic(&result[0], "missing_binding", "node", id, result[0].Witness)
	}
	if p.truncations["auxiliary_limit"] || len(result) == EventsMaxWitnessRecords {
		eventsWitnessReason(&result[len(result)-1].Witness, "Dispatch witness construction incomplete", "inferred")
	}
	p.dispatchCache[id] = result
	return result
}

// Diagnostics explain qualification without widening dispatch admission. Share
// a bounded detail budget so many failed flow proofs cannot multiply evidence.
func (p *eventsProjection) dispatchDiagnostic(d *EventsDispatch, gate, typ, id string, w EventsWitness) {
	if w.Status == "explicit" {
		return
	}
	remaining := EventsMaxWitnessRecords
	for _, item := range d.Diagnostics {
		remaining -= 1 + len(item.EvidenceIDs)
	}
	if remaining <= 0 {
		p.truncations["witness_limit"] = true
		return
	}
	ids := w.EvidenceIDs
	if len(ids) > remaining-1 {
		ids = ids[:remaining-1]
		p.truncations["witness_limit"] = true
	}
	d.Diagnostics = append(d.Diagnostics, EventsDispatchDiagnostic{Gate: gate, RecordType: typ, SubjectID: id, Status: w.Status, EvidenceIDs: slices.Clone(ids)})
}
func (p *eventsProjection) combineWitness(a, b EventsWitness) EventsWitness {
	nodes, edges := slices.Clone(a.NodeIDs), slices.Clone(a.EdgeIDs)
	remaining := EventsMaxWitnessRecords - len(nodes) - len(edges)
	for _, id := range b.NodeIDs {
		if slices.Contains(nodes, id) {
			continue
		}
		if remaining == 0 {
			p.truncations["witness_limit"] = true
			eventsWitnessReason(&a, "Witness record budget exhausted", "inferred")
			break
		}
		nodes = append(nodes, id)
		remaining--
	}
	for _, id := range b.EdgeIDs {
		if slices.Contains(edges, id) {
			continue
		}
		if remaining == 0 {
			p.truncations["witness_limit"] = true
			eventsWitnessReason(&a, "Witness record budget exhausted", "inferred")
			break
		}
		edges = append(edges, id)
		remaining--
	}
	w := p.witness(nodes, edges)
	w.Status = runtimeWorseStatus(w.Status, runtimeWorseStatus(a.Status, b.Status))
	w.Limitations = append(w.Limitations, a.Limitations...)
	w.Limitations = append(w.Limitations, b.Limitations...)
	slices.Sort(w.Limitations)
	w.Limitations = slices.Compact(w.Limitations)
	return w
}
func (p *eventsProjection) related(consumer string) []EventsRelatedRoute {
	if cached, ok := p.relatedCache[consumer]; ok {
		return cached
	}
	result := []EventsRelatedRoute{}
	for _, e := range p.out[consumer] {
		if p.ctx.Err() != nil {
			break
		}
		if !slices.Contains([]string{"retries", "dead_letters"}, e.Kind) {
			continue
		}
		if len(result) == EventsMaxWitnessRecords {
			p.truncations["witness_limit"] = true
			break
		}
		if !p.auxiliary() {
			break
		}
		message := runtimeAttributeString(e.Attributes, "messageId")
		item := EventsRelatedRoute{Kind: e.Kind, EdgeID: e.ID, ConsumerID: consumer, ChannelID: e.To, MessageID: message, Reason: runtimeAttributeString(e.Attributes, "reason"), Witness: p.witness([]string{consumer, e.To, message}, []string{e.ID})}
		if e.Kind == "retries" {
			item.Delay = eventsScalar(e.Attributes, "delay")
			item.MaxAttempts = eventsScalar(e.Attributes, "maxAttempts")
		}
		result = append(result, item)
	}
	p.relatedCache[consumer] = result
	return result
}
func (p *eventsProjection) emitContext(producer string) *EventsEmitContext {
	if cached, ok := p.emitCache[producer]; ok {
		return cached
	}
	n, ok := p.nodes[producer]
	if !ok {
		return nil
	}
	result := &EventsEmitContext{Limitations: []string{"Local transaction membership and source control reachability do not establish transaction/delivery atomicity"}}
	if n.ParentID != nil {
		result.FlowID = *n.ParentID
	}
	if json.Unmarshal(n.Attributes["transactionContext"], &result.Transaction) != nil {
		result.Transaction = EventsTransactionContext{Status: "unknown", Reason: "Local transaction context unavailable"}
	}
	flow := p.nodes[result.FlowID]
	entry := runtimeAttributeString(flow.Attributes, "entryStepId")
	if entry != "" {
		result.ControlWitness = p.controlWitness(entry, producer, result.FlowID)
	}
	p.emitCache[producer] = result
	return result
}
func (p *eventsProjection) controlWitness(entry, target, flow string) *EventsWitness {
	type path struct {
		node         string
		nodes, edges []string
	}
	queue := []path{{node: entry, nodes: []string{flow, entry}}}
	seen := map[string]bool{entry: true}
	for head := 0; head < len(queue); head++ {
		if p.ctx.Err() != nil {
			return nil
		}
		w := queue[head]
		proof := p.witness(w.nodes, w.edges)
		if proof.Status != "explicit" {
			continue
		}
		if w.node == target {
			return &proof
		}
		for _, e := range p.out[w.node] {
			if !slices.Contains([]string{"next", "branch", "error", "returns"}, e.Kind) || seen[e.To] {
				continue
			}
			n, ok := p.nodes[e.To]
			if !ok || n.Kind != "flow_step" || n.ParentID == nil || *n.ParentID != flow {
				continue
			}
			if len(seen) == EventsMaxWitnessRecords || len(w.nodes)+len(w.edges)+2 > EventsMaxWitnessRecords {
				p.truncations["witness_limit"] = true
				return nil
			}
			if !p.auxiliary() {
				return nil
			}
			// The prefix is already verified. Settle only an explicit next
			// edge/node, so an unverified first branch cannot hide an alternate.
			if p.witness([]string{e.To}, []string{e.ID}).Status != "explicit" {
				continue
			}
			seen[e.To] = true
			queue = append(queue, path{node: e.To, nodes: append(slices.Clone(w.nodes), e.To), edges: append(slices.Clone(w.edges), e.ID)})
		}
	}
	return nil
}

func (p *eventsProjection) dispatchFlows(d *EventsDispatch, h Node) {
	for _, contains := range p.out[h.ID] {
		if p.ctx.Err() != nil {
			break
		}
		f, exists := p.nodes[contains.To]
		if contains.Kind != "contains" || !exists || f.Kind != "flow" || f.ParentID == nil || *f.ParentID != h.ID {
			continue
		}
		if len(d.FlowIDs) == EventsMaxWitnessRecords {
			p.truncations["witness_limit"] = true
			eventsWitnessReason(&d.Witness, "Handler flow witness budget exhausted", "inferred")
			break
		}
		if !p.auxiliary() {
			eventsWitnessReason(&d.Witness, "Dispatch construction budget exhausted", "inferred")
			break
		}
		d.FlowIDs = append(d.FlowIDs, f.ID)
		p.dispatchDiagnostic(d, "flow_proof", "node", f.ID, p.witness([]string{f.ID}, nil))
		p.dispatchDiagnostic(d, "flow_ownership_proof", "edge", contains.ID, p.witness(nil, []string{contains.ID}))
		next := p.witness([]string{f.ID}, []string{contains.ID})
		d.Witness = p.combineWitness(d.Witness, next)
	}
	if len(d.FlowIDs) == 0 {
		eventsWitnessReason(&d.Witness, "Missing discovered handler flow "+h.ID, "unresolved")
		p.dispatchDiagnostic(d, "missing_flow", "node", h.ID, d.Witness)
	}
}

func (b *eventsWitnessBuilder) sourceObserve(typ, id string, ids []string, fresh *AssertionFreshness) {
	if b.projection.effective != nil {
		proof, err := effectiveRecordProof(b.projection.effective, typ, id, nil)
		if err != nil {
			b.witness.Status = "unresolved"
			b.limitations[err.Error()] = true
			return
		}
		b.witness.Status = runtimeWorseStatus(b.witness.Status, proof.status)
		for reason := range proof.reasons {
			b.limitations[reason] = true
		}
		for _, eid := range proof.evidenceIDs {
			if len(b.evidence) == EventsMaxWitnessRecords {
				b.projection.truncations["witness_limit"] = true
				break
			}
			b.evidence[eid] = true
		}
		if proof.status == "desired" {
			b.witness.Provenance = "desired"
		}
		return
	}
	if b.projection.source == nil {
		b.observe(ids, fresh)
		return
	}
	proof, err := sourceRecordProof(b.projection.source, typ, id, nil)
	if err != nil {
		b.witness.Status = "unresolved"
		b.limitations[err.Error()] = true
		return
	}
	b.witness.Status = runtimeWorseStatus(b.witness.Status, proof.status)
	for reason := range proof.reasons {
		b.limitations[reason] = true
	}
	for _, eid := range proof.evidenceIDs {
		if len(b.evidence) == EventsMaxWitnessRecords {
			b.projection.truncations["witness_limit"] = true
			break
		}
		b.evidence[eid] = true
	}
}

func (p *eventsProjection) sourceDispatchFlows(d *EventsDispatch, h Node) {
	// Lexical ownership is navigable source context even when the
	// binding is not admitted as verified dispatch. Never fold these
	// bodies into FlowIDs or the execution witness.
	for _, ownership := range p.out[h.ID] {
		flow, exists := p.nodes[ownership.To]
		if ownership.Kind != "contains" || !exists || flow.Kind != "flow" || flow.ParentID == nil || *flow.ParentID != h.ID {
			continue
		}
		if len(d.SourceFlowIDs) == EventsMaxWitnessRecords || !p.auxiliary() {
			p.truncations["witness_limit"] = true
			break
		}
		d.SourceFlowIDs = append(d.SourceFlowIDs, flow.ID)
	}
	slices.Sort(d.SourceFlowIDs)
	d.SourceFlowIDs = slices.Compact(d.SourceFlowIDs)
}
