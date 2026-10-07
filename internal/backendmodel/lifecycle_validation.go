package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/statediagram"
)

// MaxLifecycleGuardTextBytes bounds an opaque guard by what the builder can
// derive from the largest guard the state-diagram owner admits: a 2048-byte
// pointer and MaxJSON bytes of equalsJSON, each at most doubled by JSON
// string escaping, plus the member names. The former 4096 cap rejected a
// whole build_backend_lifecycle candidate for any guard above it with a
// message that did not mention length (review 2026-10-06, F102).
const MaxLifecycleGuardTextBytes = 2*(2048+statediagram.MaxJSON) + 64

func validateLifecycle(d DiagramDocument) error {
	p := d.Lifecycle
	if p == nil || d.Interactions != nil || d.Payload.Elements != nil || d.Payload.Links != nil || d.Payload.PrimarySystemID != "" {
		return invalid("payload", "Exactly one lifecycle payload required")
	}
	if err := validateLifecycleHeader(p); err != nil {
		return err
	}
	c := lifecycleClaims{ids: map[string]bool{}, states: map[string]bool{}}
	for _, s := range p.States {
		if err := c.state(s); err != nil {
			return err
		}
	}
	for _, tr := range p.Transitions {
		if err := c.transition(tr); err != nil {
			return err
		}
	}
	triples := map[string]bool{}
	for _, rule := range p.Rules {
		if err := c.rule(rule, triples); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return &FaultError{Status: 413, Code: "backend_diagram_limit", Message: "Diagram exceeds 1 MiB"}
	}
	return nil
}

// validateLifecycleHeader checks everything outside the state, transition
// and rule arrays: their bounds, the entity, the state fields and coverage.
func validateLifecycleHeader(p *LifecyclePayload) error {
	if p.States == nil || p.Transitions == nil || p.Rules == nil || len(p.States)+len(p.Rules) > 1000 || len(p.Transitions) > 3000 {
		return invalid("payload", "Required arrays exceed lifecycle bounds")
	}
	if err := validateDiagramRef(p.Entity); err != nil {
		return err
	}
	if len(p.StateFields) == 0 || len(p.StateFields) > 100 {
		return invalid("stateFields", "Select 1–100 exact fields")
	}
	if err := validateLifecycleRefs(p.StateFields); err != nil {
		return err
	}
	if !validAPIText(p.CompoundMappingReason, 0, 4096) || len(p.StateFields) > 1 && strings.TrimSpace(p.CompoundMappingReason) == "" {
		return invalid("compoundMappingReason", "Multiple fields require an explicit mapping reason")
	}
	if !slices.Contains([]string{"partial", "declared_complete"}, p.Coverage) {
		return invalid("coverage", "Explicit coverage declaration required")
	}
	return validateDiagramOrigin(p.CoverageOrigin)
}

// lifecycleClaims holds the identities already claimed in one lifecycle;
// states, transitions and rules share one ID space.
type lifecycleClaims struct {
	ids    map[string]bool
	states map[string]bool
}

func (c lifecycleClaims) claim(id string) error {
	if !ValidID(id) || c.ids[id] {
		return invalid("id", "Invalid or duplicate lifecycle identity")
	}
	c.ids[id] = true
	return nil
}

func (c lifecycleClaims) endpoints(from, to string) error {
	if !c.states[from] || !c.states[to] {
		return invalid("endpoints", "Transition endpoints must belong to this lifecycle")
	}
	return nil
}

func (c lifecycleClaims) state(s LifecycleState) error {
	if err := c.claim(s.ID); err != nil {
		return err
	}
	c.states[s.ID] = true
	if err := validateDiagramBase(s.ID, s.Label, s.Origin, s.Refs); err != nil {
		return err
	}
	if s.Value != nil {
		raw := jsontext.Value(s.Value.JSON)
		trim := strings.TrimSpace(s.Value.JSON)
		if len(trim) == 0 || len(trim) > 65536 || !raw.IsValid() || trim[0] == '{' || trim[0] == '[' {
			return invalid("value", "One lossless JSON scalar required")
		}
	}
	return nil
}

func (c lifecycleClaims) transition(tr LifecycleTransition) error {
	if err := c.claim(tr.ID); err != nil {
		return err
	}
	if err := c.endpoints(tr.From, tr.To); err != nil {
		return err
	}
	if err := validateDiagramBase(tr.ID, tr.Label, tr.Origin, tr.Refs); err != nil {
		return err
	}
	for _, refs := range [][]DiagramRef{tr.Triggers, tr.Writes, tr.Events} {
		if err := validateLifecycleRefs(refs); err != nil {
			return err
		}
	}
	return validateLifecycleGuard(tr.Guard)
}

func validateLifecycleGuard(g LifecycleGuard) error {
	if g.Kind == "none" {
		if g.Text != "" {
			return invalid("guard", "None guard cannot carry text")
		}
	} else if g.Kind != "opaque" {
		return invalid("guard", "Only none or opaque guards supported")
	} else if !validAPIText(g.Text, 1, MaxLifecycleGuardTextBytes) {
		return invalid("guard", "Opaque guard text is empty, invalid or exceeds the guard byte bound")
	}
	return nil
}

// rule checks one authored rule; triples refuses a second rule over the same
// from/to/trigger, whatever its verdict.
func (c lifecycleClaims) rule(rule LifecycleRule, triples map[string]bool) error {
	if err := c.claim(rule.ID); err != nil {
		return err
	}
	if err := c.endpoints(rule.From, rule.To); err != nil {
		return err
	}
	if rule.Origin.Kind != "authored" || !slices.Contains([]string{"allowed", "forbidden"}, rule.Verdict) {
		return invalid("rule", "Rules are explicit authored allowed/forbidden declarations")
	}
	if err := validateDiagramOrigin(rule.Origin); err != nil {
		return err
	}
	if err := validateDiagramRef(rule.Trigger); err != nil {
		return err
	}
	key, _ := requestDigest(struct {
		From, To string
		Trigger  DiagramRef
	}{rule.From, rule.To, rule.Trigger})
	if triples[key] {
		return invalid("rule", "Duplicate or conflicting from/to/trigger rule")
	}
	triples[key] = true
	return nil
}
func validateLifecycleRefs(refs []DiagramRef) error {
	if refs == nil || len(refs) > 100 {
		return invalid("refs", "Nonnull bounded reference array required")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if err := validateDiagramRef(ref); err != nil {
			return err
		}
		key, _ := requestDigest(ref)
		if seen[key] {
			return invalid("refs", "Duplicate reference")
		}
		seen[key] = true
	}
	return nil
}
