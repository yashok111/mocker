package backendanalysis

import (
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func (d *diagnosticEvaluator) diagramSelected(id string) bool {
	if d.input.DiagramScope == nil {
		return false
	}
	return slices.ContainsFunc(d.input.DiagramScope.Selectors, func(s backendmodel.DiagramScopeSelector) bool { return s.Kind == "semantic" && s.ID == id })
}
func (d *diagnosticEvaluator) diagramRules() {
	v := d.input.Diagram
	if v == nil {
		return
	}
	for _, gap := range v.Gaps {
		if !d.diagramSelected(gap.SubjectID) || !strings.HasPrefix(gap.Code, "historical_ref:") {
			continue
		}
		status, certainty := "present", "confirmed"
		if !d.inventoryComplete() {
			status = "unknown"
			certainty = "unknown"
		}
		// Scope retains the exact orphan ref, whose digest is the server-derived gap suffix.
		var missing *backendmodel.DiagramRef
		for _, ref := range d.input.DiagramScope.SourceRefs {
			h, _ := requestHash(ref)
			if gap.Code == "historical_ref:"+h {
				missing = &ref
				break
			}
		}
		if missing == nil {
			continue
		}
		count := len(d.report.Findings)
		d.check("diagram_broken_reference", v.Pin.ID+":"+gap.SubjectID+":"+gap.Code, status, certainty, "Explicit mapping retains an unavailable implementation reference", []backendmodel.ChangeRecordRef{}, []string{"server_retained_fork_reference", "complete_target_inventory"}, []string{gap.Code}, struct {
			Pin backendmodel.DiagramPin
			Ref backendmodel.DiagramRef
		}{v.Pin, *missing})
		if len(d.report.Findings) > count {
			f := &d.report.Findings[len(d.report.Findings)-1]
			f.Witness.MissingRef = missing
			pin := v.Pin
			if v.Provenance.Fork != nil {
				pin = v.Provenance.Fork.Source
			}
			f.Witness.OriginalDiagramPin = &pin
		}
	}
	p := v.Document.Lifecycle
	if p == nil {
		return
	}
	for _, rule := range p.Rules {
		if rule.Verdict != "forbidden" {
			continue
		}
		for _, tr := range p.Transitions {
			if !d.tick() {
				return
			}
			if !d.diagramSelected(rule.ID) && !d.diagramSelected(tr.ID) {
				continue
			}
			if rule.From != tr.From || rule.To != tr.To {
				continue
			}
			trigger, _ := requestHash(rule.Trigger)
			if !slices.ContainsFunc(tr.Triggers, func(r backendmodel.DiagramRef) bool { h, _ := requestHash(r); return h == trigger }) {
				continue
			}
			// An authored transition or a historic source assertion cannot prove a source discrepancy.
			status, certainty := "present", "confirmed"
			if tr.Guard.Kind == "opaque" {
				certainty = "possible"
			}
			supported := tr.Origin.Kind == "source_assertion" && len(tr.Origin.Evidence) > 0 && len(tr.Triggers) == 1
			if rule.Trigger.Kind != "record" || !d.proof(rule.Trigger.RecordType, rule.Trigger.ID) {
				supported = false
			}
			for _, ev := range tr.Origin.Evidence {
				if ev.RevisionID != d.graph.Pins.BaseRevisionID || !slices.ContainsFunc(d.graph.State.Evidence, func(actual backendmodel.Evidence) bool {
					return actual.ID == ev.EvidenceID && actual.SubjectID == ev.SubjectID && actual.Status == "explicit"
				}) {
					supported = false
				}
			}
			from, to := (*backendmodel.LifecycleState)(nil), (*backendmodel.LifecycleState)(nil)
			for i := range p.States {
				state := &p.States[i]
				if state.ID == tr.From {
					from = state
				}
				if state.ID == tr.To {
					to = state
				}
			}
			if from == nil || to == nil {
				supported = false
			} else if from.Value != nil && to.Value != nil && from.Value.JSON == to.Value.JSON && from.ID != to.ID {
				supported = false
			}
			subjects := []backendmodel.ChangeRecordRef{}
			for _, ref := range tr.Refs {
				if ref.Kind == "record" {
					subjects = append(subjects, backendmodel.ChangeRecordRef{RecordType: ref.RecordType, ID: ref.ID})
					supported = supported && d.proof(ref.RecordType, ref.ID)
				}
			}
			if len(subjects) == 0 {
				supported = false
			}
			for _, gap := range v.Gaps {
				if gap.SubjectID == tr.ID && strings.HasPrefix(gap.Code, "historical_") {
					supported = false
				}
			}
			if !supported {
				status = "unknown"
				certainty = "unknown"
			}
			count := len(d.report.Findings)
			d.check("lifecycle_forbidden_transition", v.Pin.ID+":"+rule.ID+":"+tr.ID, status, certainty, "Positive mapped source transition conflicts with authored forbidden intent; runtime violation is unverified", subjects, []string{"positive_current_source_transition", "exact_from_to_trigger_mapping"}, []string{"runtime_unverified"}, []any{v.Pin, rule, tr})
			if len(d.report.Findings) > count {
				f := &d.report.Findings[len(d.report.Findings)-1]
				f.Witness.RuleID = rule.ID
				f.Witness.TransitionID = tr.ID
			}
		}
	}
}
