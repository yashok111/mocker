package backendanalysis

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type RuleResult struct {
	RuleID        string           `json:"ruleId"`
	Version       string           `json:"version"`
	Prerequisites []string         `json:"prerequisites"`
	Object        ObjectAddress    `json:"object"`
	Status        string           `json:"status"`
	Severity      string           `json:"severity"`
	Certainty     string           `json:"certainty"`
	Message       string           `json:"message"`
	Evidence      []ProofReference `json:"evidence"`
}
type ProofReference struct {
	Side                  string                          `json:"side"`
	TargetHash            string                          `json:"targetHash"`
	EffectiveSemanticHash string                          `json:"effectiveSemanticHash"`
	BaseRevisionID        string                          `json:"baseRevisionId"`
	Status                string                          `json:"status"`
	EvidenceIDs           []string                        `json:"evidenceIds"`
	Assertions            []backendmodel.BaseAssertionRef `json:"assertions"`
	Reasons               []string                        `json:"reasons"`
}

func proofReference(side string, g *backendmodel.EffectiveGraphSnapshot, p backendmodel.EffectiveAnalysisProof) ProofReference {
	return ProofReference{Side: side, TargetHash: g.Pins.TargetHash, EffectiveSemanticHash: g.Pins.EffectiveSemanticHash, BaseRevisionID: g.Pins.BaseRevisionID, Status: p.Status, EvidenceIDs: p.EvidenceIDs, Assertions: p.Assertions, Reasons: p.Reasons}
}
func recordProof(g *backendmodel.EffectiveGraphSnapshot, object ObjectAddress) backendmodel.EffectiveAnalysisProof {
	p, err := backendmodel.EffectiveRecordAnalysisProof(g, backendmodel.ChangeRecordRef{RecordType: object.RecordType, ID: object.ID})
	if err != nil {
		return backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{err.Error()}}
	}
	return p
}
func ruleFor(c DiffChange) RuleResult {
	r := RuleResult{RuleID: "structural-change", Version: "1", Prerequisites: []string{"exact_before_after_pins", "declared_structural_change"}, Object: c.Object, Status: "potential", Severity: "review", Certainty: "possible", Message: "Review statically affected consumers"}
	paths := strings.Join(c.Paths, " ")
	switch {
	case c.Facet != "behavior":
		r.RuleID = "proof-or-intent-change"
		r.Status = "information"
		r.Certainty = "confirmed"
		r.Message = "Proof or desired criteria changed independently of behavior"
	case strings.Contains(paths, "/nativeType") || strings.Contains(paths, "/typeFamily"):
		r.RuleID = "native-type-change"
		r.Status = "unknown"
		r.Certainty = "unknown"
		r.Message = "Exact native type changed; dialect-dependent compatibility is not established"
		r.Prerequisites = append(r.Prerequisites, "supported_dialect_compatibility_rule")
	case strings.Contains(paths, "/nullable") && containsKnown(c.After, "nullable", []byte("false")):
		r.RuleID = "not-null-data-check"
		r.Status = "check_required"
		r.Message = "Check existing data for null values before adding NOT NULL"
	case c.Kind == "index" && c.Operation == "removed":
		r.RuleID = "index-removal-performance-check"
		r.Status = "check_required"
		r.Message = "Measure affected queries; index removal does not establish a slowdown"
	case strings.Contains(paths, "/unique") || c.Kind == "constraint" && containsString(c.After, "constraintKind", "unique"):
		r.RuleID = "unique-data-check"
		r.Status = "check_required"
		r.Message = "Check existing data for duplicate keys"
	case foreignKeyChange(c, paths):
		r.RuleID = "foreign-key-change"
		r.Message = "Review ordered column pairs and declared referential actions"
	case slices.Contains([]string{"table", "column"}, c.Kind) && c.Operation == "removed":
		r.RuleID = "relational-object-removal"
	case c.Kind == "field_mapping":
		r.RuleID = "mapping-change"
	case c.Kind == "query":
		r.RuleID = "query-change"
	case slices.Contains([]string{"api_field", "event_field", "representation_field"}, c.Kind):
		r.RuleID = "contract-field-change"
	case slices.Contains([]string{"flow", "flow_step", "emits", "delivered_to", "consumer", "job"}, c.Kind):
		r.RuleID = "flow-event-change"
	}
	return r
}
func containsKnown(raw []byte, key string, value []byte) bool {
	return walkJSON(raw, func(m map[string]jsontext.Value) bool {
		var v struct {
			Status string         `json:"status"`
			Value  jsontext.Value `json:"value"`
		}
		return json.Unmarshal(m[key], &v) == nil && v.Status == "known" && bytes.Equal(v.Value, value)
	})
}
func containsString(raw []byte, key, value string) bool {
	return walkJSON(raw, func(m map[string]jsontext.Value) bool { return stringValue(m[key]) == value })
}
func walkJSON(raw []byte, match func(map[string]jsontext.Value) bool) bool {
	var m map[string]jsontext.Value
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	if match(m) {
		return true
	}
	for _, v := range m {
		if len(v) > 0 && v[0] == '{' && walkJSON(v, match) {
			return true
		}
	}
	return false
}
func stringValue(raw []byte) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func declaredChecks(g *backendmodel.EffectiveGraphSnapshot) []RuleResult {
	out := make([]RuleResult, 0, len(g.Criteria))
	for _, c := range g.Criteria {
		r := RuleResult{RuleID: "criterion/" + c.Kind, Version: "1", Object: ObjectAddress{RecordType: c.RecordType, ID: c.ID}, Status: "unknown", Severity: "review", Certainty: "unknown", Message: c.Description, Prerequisites: []string{"explicit_declared_criterion", c.Key}}
		if c.Kind == "field_equals" {
			out = append(out, fieldCriterionCheck(g, c, r))
			continue
		}
		found, kind := criterionObject(g, c)
		applicable := c.Kind == "object_exists" || c.Kind == "object_absent" || c.Kind == "edge_exists"
		if applicable {
			proof := criterionProof(g, c, found)
			if !supported(proof) {
				out = append(out, r)
				continue
			}
			r.Evidence = []ProofReference{proofReference("after", g, proof)}
			passes := found
			if c.Kind == "object_absent" {
				passes = !found
			} else if c.ObjectKind != "" {
				passes = passes && kind == c.ObjectKind
			} else if c.EdgeKind != "" {
				passes = passes && kind == c.EdgeKind
			}
			r.Status = "passed"
			r.Certainty = "confirmed"
			if !passes {
				r.Status = "check_required"
				if c.Required {
					r.Status = "violation"
					r.Severity = "error"
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func foreignKeyChange(c DiffChange, paths string) bool {
	return c.Kind == "foreign_key" || c.Kind == "references" || strings.Contains(paths, "/columnPairs") || strings.Contains(paths, "/deleteAction") || strings.Contains(paths, "/updateAction")
}

func criterionObject(g *backendmodel.EffectiveGraphSnapshot, c backendmodel.ChangeCriterion) (bool, string) {
	found := false
	var kind string
	for _, n := range g.State.Nodes {
		if c.RecordType == "node" && n.ID == c.ID {
			found = true
			kind = n.Kind
		}
	}
	for _, e := range g.State.Edges {
		if (c.RecordType == "edge" || c.Kind == "edge_exists") && e.ID == c.ID {
			found = true
			kind = e.Kind
			if c.Kind == "edge_exists" && (e.From != c.From || e.To != c.To) {
				found = false
			}
		}
	}
	return found, kind
}

func fieldCriterionCheck(g *backendmodel.EffectiveGraphSnapshot, c backendmodel.ChangeCriterion, r RuleResult) RuleResult {
	var selector backendmodel.EffectivePropertySelector
	if c.Expected == nil || json.Unmarshal(c.Selector, &selector) != nil {
		return r
	}
	var actual backendmodel.SourcePropertyValue
	found := false
	if selector.Source != nil {
		var payload backendmodel.SourceAssertionPayload
		for _, n := range g.State.Nodes {
			if c.RecordType == "node" && n.ID == c.ID {
				found = true
				payload = backendmodel.SourceAssertionPayload{RecordType: "node", Kind: n.Kind, Name: n.Name, ParentID: n.ParentID, Attributes: n.Attributes}
			}
		}
		for _, e := range g.State.Edges {
			if c.RecordType == "edge" && e.ID == c.ID {
				found = true
				payload = backendmodel.SourceAssertionPayload{RecordType: "edge", Kind: e.Kind, From: e.From, To: e.To, Attributes: e.Attributes}
			}
		}
		if !found {
			return r
		}
		var err error
		actual, err = backendmodel.SelectSourceProperty(payload, *selector.Source)
		if err != nil {
			return r
		}
	} else if selector.EdgeName {
		value, ok := g.EdgeNames[c.ID]
		actual.Present = ok
		if ok {
			actual.Value, _ = canonical(value)
		}
	} else {
		return r
	}
	p, err := backendmodel.EffectivePropertyAnalysisProof(g, backendmodel.ChangeRecordRef{RecordType: c.RecordType, ID: c.ID}, selector)
	if err != nil || !supported(p) {
		return r
	}
	r.Evidence = []ProofReference{proofReference("after", g, p)}
	r.Prerequisites = append(r.Prerequisites, "typed_selected_value", "current_or_desired_proof")
	r.Certainty = "confirmed"
	r.Status = "passed"
	a, _ := canonical(actual)
	b, _ := canonical(c.Expected)
	if !bytes.Equal(a, b) {
		r.Status = "check_required"
		if c.Required {
			r.Status = "violation"
			r.Severity = "error"
		}
	}
	return r
}

func criterionProof(g *backendmodel.EffectiveGraphSnapshot, c backendmodel.ChangeCriterion, found bool) backendmodel.EffectiveAnalysisProof {
	if !found {
		if g.State.Revision.Coverage.Status == "complete" {
			return backendmodel.EffectiveAnalysisProof{Status: "explicit", Reasons: []string{"absence_in_exact_complete_snapshot"}}
		}
		return backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{"partial_source_cannot_prove_absence"}}
	}
	typ := c.RecordType
	if c.Kind == "edge_exists" {
		typ = "edge"
	}
	return recordProof(g, ObjectAddress{RecordType: typ, ID: c.ID})
}
