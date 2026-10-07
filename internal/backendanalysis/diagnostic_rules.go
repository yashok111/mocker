package backendanalysis

import (
	"encoding/json/v2"
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func nodeSubject(id string) []backendmodel.ChangeRecordRef {
	return []backendmodel.ChangeRecordRef{{RecordType: "node", ID: id}}
}
func (d *diagnosticEvaluator) inventoryComplete() bool {
	if d.inventory != nil {
		return *d.inventory
	}
	value := d.computeInventoryComplete()
	d.inventory = &value
	return value
}
func (d *diagnosticEvaluator) computeInventoryComplete() bool {
	c := d.graph.State.Revision.Coverage
	if c.Status != "complete" || len(c.Gaps) > 0 {
		return false
	}
	for _, n := range d.graph.State.Nodes {
		if !d.tick() {
			return false
		}
		if slices.Contains([]string{"query", "flow", "flow_step", "call_site", "consumer", "job", "unresolved_target"}, n.Kind) {
			if n.Kind == "unresolved_target" || !d.proof("node", n.ID) {
				return false
			}
			for _, key := range []string{"analysisStatus", "dispatchStatus", "exitStatus"} {
				if v := attr(n.Attributes, key); v != "" && v != "complete" {
					return false
				}
			}
		}
	}
	return true
}
func (d *diagnosticEvaluator) nodeRules(n backendmodel.Node) {
	current := d.proof("node", n.ID)
	if _, hasFacets := n.Attributes["facets"]; hasFacets {
		comparison, err := backendmodel.CompareRelationalFacets(n.Kind, n.Attributes, false)
		if err == nil && comparison != nil {
			status, certainty := "absent", "confirmed"
			if comparison.Status == "different" {
				status = "present"
			}
			if comparison.Status == "unknown" || !current {
				status = "unknown"
				certainty = "unknown"
			}
			d.check("relational_drift", "node:"+n.ID, status, certainty, "Imported relational facets disagree; deployment truth is not selected", nodeSubject(n.ID), []string{"known_facet_values", "current_source_proof"}, nil, comparison)
		}
	}
	if n.Kind == "table" {
		used := false
		for _, e := range d.graph.State.Edges {
			if !d.tick() {
				return
			}
			if (e.To == n.ID || (d.nodes[e.To].ParentID != nil && *d.nodes[e.To].ParentID == n.ID)) && slices.Contains([]string{"reads", "writes", "deletes"}, e.Kind) {
				used = true
				break
			}
		}
		// A found access already proves use; the complete inventory is needed
		// only to claim the table unused. Downgrading a used table on a partial
		// import produced a false "No access found" finding, a gap and a review
		// event for every used table (review 2026-10-06, F136).
		status, certainty := "absent", "confirmed"
		if !used {
			status = "present"
			if !current || !d.inventoryComplete() {
				status = "unknown"
				certainty = "unknown"
			}
		}
		d.check("unused_table", n.ID, status, certainty, "No access found in the inspected supported scope", nodeSubject(n.ID), []string{"complete_current_access_inventory"}, nil, n.Attributes)
	}
	if slices.Contains([]string{"call_site", "consumer", "job", "flow_step"}, n.Kind) && attr(n.Attributes, "dispatchStatus") != "" {
		status, certainty := "present", "possible"
		if attr(n.Attributes, "dispatchStatus") == "complete" {
			status = "absent"
			certainty = "confirmed"
		}
		if !current {
			status = "unknown"
			certainty = "unknown"
		}
		d.check("ambiguous_dispatch", n.ID, status, certainty, "Declared dispatch remainder is unresolved; names cannot resolve it", nodeSubject(n.ID), []string{"explicit_dispatch_remainder"}, []string{"runtime_dispatch_unverified"}, n.Attributes)
	}
	if n.Kind != "flow_step" {
		return
	}
	if attr(n.Attributes, "stepKind") == "loop" {
		d.loopRule(n)
	}
	if attr(n.Attributes, "stepKind") == "raise" {
		d.outcomeRule(n)
	}
	for _, e := range d.out[n.ID] {
		if e.Kind == "emits" {
			d.emitRule(n, e)
		}
	}
}

// A supported loop witness must return to the exact loop through current local
// control edges. Merely being reachable after a loop is not an iteration witness.
func (d *diagnosticEvaluator) path(from, to, flow string) ([]backendmodel.ChangeRecordRef, bool) {
	type entry struct {
		id   string
		path []backendmodel.ChangeRecordRef
	}
	q := []entry{{from, nodeSubject(from)}}
	seen := map[string]bool{from: true}
	for head := 0; head < len(q) && head < 1000; head++ {
		if d.ctx.Err() != nil {
			return nil, false
		}
		v := q[head]
		if v.id == to {
			return v.path, true
		}
		for _, e := range d.out[v.id] {
			if !d.tick() {
				return nil, false
			}
			n := d.nodes[e.To]
			if !slices.Contains([]string{"next", "branch", "error", "returns"}, e.Kind) || seen[e.To] || n.ParentID == nil || *n.ParentID != flow || !d.proof("edge", e.ID) || !d.proof("node", e.To) {
				continue
			}
			seen[e.To] = true
			p := slices.Clone(v.path)
			p = append(p, backendmodel.ChangeRecordRef{RecordType: "edge", ID: e.ID}, backendmodel.ChangeRecordRef{RecordType: "node", ID: e.To})
			q = append(q, entry{e.To, p})
		}
	}
	if len(q) >= 1000 {
		d.report.Complete = false
		d.report.Gaps = append(d.report.Gaps, "control_witness_budget")
	}
	return nil, false
}
func (d *diagnosticEvaluator) loopRule(loop backendmodel.Node) {
	if loop.ParentID == nil {
		return
	}
	for _, n := range d.graph.State.Nodes {
		if !d.tick() {
			return
		}
		if n.ParentID == nil || *n.ParentID != *loop.ParentID || !slices.Contains([]string{"query", "call"}, attr(n.Attributes, "stepKind")) {
			continue
		}
		forward, a := d.path(loop.ID, n.ID, *loop.ParentID)
		back, b := d.path(n.ID, loop.ID, *loop.ParentID)
		if !a || !b {
			continue
		}
		status, certainty := "present", "possible"
		if !d.proof("node", loop.ID) {
			status = "unknown"
			certainty = "unknown"
		}
		d.check("possible_n_plus_one", loop.ID+":"+n.ID, status, certainty, "Query or call on a supported loop path; batching and latency are unknown", append(forward, back...), []string{"current_loop_control_cycle"}, []string{"batching_unknown", "no_latency_claim"}, []any{loop.Attributes, n.Attributes, forward, back})
	}
}
func (d *diagnosticEvaluator) outcomeRule(n backendmodel.Node) {
	if n.ParentID == nil {
		return
	}
	flow := d.nodes[*n.ParentID]
	var exits []string
	_ = json.Unmarshal(flow.Attributes["exitStepIds"], &exits)
	status, certainty := "absent", "confirmed"
	handled := false
	for _, e := range d.out[n.ID] {
		if slices.Contains([]string{"error", "returns", "next"}, e.Kind) {
			handled = true
		}
	}
	if !handled && !slices.Contains(exits, n.ID) {
		status = "present"
	}
	if !d.proof("node", n.ID) || !d.proof("node", flow.ID) || attr(flow.Attributes, "exitStatus") != "complete" || !d.inventoryComplete() {
		status = "unknown"
		certainty = "unknown"
	}
	d.check("unhandled_outcome", n.ID, status, certainty, "Supported error terminal is absent from the declared exit inventory", nodeSubject(n.ID), []string{"complete_current_exit_inventory", "resolved_dispatch"}, nil, []any{n.Attributes, flow.Attributes})
}
func (d *diagnosticEvaluator) emitRule(n backendmodel.Node, emit backendmodel.Edge) {
	var tx struct {
		Status string `json:"status"`
		ID     string `json:"transactionId"`
	}
	_ = json.Unmarshal(n.Attributes["transactionContext"], &tx)
	if tx.Status != "known" || n.ParentID == nil {
		return
	}
	for _, commit := range d.graph.State.Nodes {
		if !d.tick() {
			return
		}
		if attr(commit.Attributes, "stepKind") != "transaction_commit" || commit.ParentID == nil || *commit.ParentID != *n.ParentID {
			continue
		}
		var other struct {
			Status string `json:"status"`
			ID     string `json:"transactionId"`
		}
		_ = json.Unmarshal(commit.Attributes["transactionContext"], &other)
		if tx != other {
			continue
		}
		path, ok := d.path(n.ID, commit.ID, *n.ParentID)
		if !ok {
			continue
		}
		status, certainty := "present", "possible"
		if !d.proof("node", n.ID) || !d.proof("node", tx.ID) || !d.proof("edge", emit.ID) {
			status = "unknown"
			certainty = "unknown"
		}
		path = append(path, backendmodel.ChangeRecordRef{RecordType: "edge", ID: emit.ID}, backendmodel.ChangeRecordRef{RecordType: "node", ID: tx.ID})
		d.check("possible_emit_before_commit", emit.ID+":"+commit.ID, status, certainty, "Source control reaches local commit after emit; delivery and atomicity are unverified", path, []string{"current_local_transaction", "current_emit_to_commit_witness"}, []string{"runtime_order_unverified", "delivery_atomicity_unverified"}, []any{tx, path})
	}
}
func (d *diagnosticEvaluator) transactionCriteria() {
	for _, c := range d.graph.Criteria {
		if c.Kind != "field_equals" || !d.includes(c.RecordType, c.ID, "") {
			continue
		}
		// Match the typed attribute name, never criterion descriptions or labels.
		var selector backendmodel.EffectivePropertySelector
		if json.Unmarshal(c.Selector, &selector) != nil || selector.Source == nil {
			continue
		}
		if selector.Source.Kind != "attributes" || selector.Source.Group != "transactionContext" {
			continue
		}
		r := fieldCriterionCheck(d.graph, c, RuleResult{Object: ObjectAddress{RecordType: c.RecordType, ID: c.ID}, Status: "unknown", Certainty: "unknown"})
		status := "unknown"
		if r.Status == "passed" {
			status = "absent"
		}
		if r.Status == "violation" || r.Status == "check_required" {
			status = "present"
		}
		d.check("transaction_expectation", c.ID+":"+c.Key, status, r.Certainty, "Explicit transaction criterion differs from current context", []backendmodel.ChangeRecordRef{{RecordType: c.RecordType, ID: c.ID}}, []string{"explicit_transaction_criterion", "typed_current_context"}, nil, c)
	}
}

func (d *diagnosticEvaluator) edgeRules(edge backendmodel.Edge) {
	if !d.includes("edge", edge.ID, edge.Kind) {
		return
	}
	if edge.Kind == "emits" && !d.includes("node", edge.From, d.nodes[edge.From].Kind) {
		d.emitRule(d.nodes[edge.From], edge)
	}
	if _, ok := edge.Attributes["facets"]; !ok {
		return
	}
	comparison, err := backendmodel.CompareRelationalFacets(edge.Kind, edge.Attributes, true)
	if err != nil || comparison == nil {
		d.report.Gaps = append(d.report.Gaps, "unsupported_relational_facet:"+edge.ID)
		return
	}
	status, certainty := "absent", "confirmed"
	if comparison.Status == "different" {
		status = "present"
	}
	if comparison.Status == "unknown" || !d.proof("edge", edge.ID) {
		status = "unknown"
		certainty = "unknown"
	}
	d.check("relational_drift", "edge:"+edge.ID, status, certainty, "Imported relational relationship facets disagree; deployment truth is not selected", []backendmodel.ChangeRecordRef{{RecordType: "edge", ID: edge.ID}}, []string{"known_facet_values", "current_source_proof"}, nil, comparison)
}
