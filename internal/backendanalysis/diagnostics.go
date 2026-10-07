package backendanalysis

import (
	"context"
	"encoding/json/v2"
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type DiagnosticInput struct {
	Scope        Scope
	DiagramScope *backendmodel.DiagramScope
	Diagram      *backendmodel.DiagramVersion
	Limits       Limits
}
type DiagnosticReport struct {
	Findings []backendmodel.Finding      `json:"findings"`
	Checks   []backendmodel.FindingCheck `json:"checks"`
	Gaps     []string                    `json:"gaps"`
	Complete bool                        `json:"complete"`
	// certainties keeps each check's own certainty by fingerprint. FindingCheck
	// has no certainty field, and a hard-coded "unknown" made every
	// scope.certainty filter drop all checks and then all findings
	// (review 2026-10-06, F137).
	certainties map[string]string
}
type diagnosticEvaluator struct {
	proofCache map[ObjectAddress]bool
	inventory  *bool
	visits     int
	bytes      int64
	ctx        context.Context
	graph      *backendmodel.EffectiveGraphSnapshot
	input      DiagnosticInput
	report     DiagnosticReport
	nodes      map[string]backendmodel.Node
	out        map[string][]backendmodel.Edge
	selected   map[ObjectAddress]bool
	scopeKey   string
}

func EvaluateDiagnostics(ctx context.Context, g *backendmodel.EffectiveGraphSnapshot, in DiagnosticInput) (*DiagnosticReport, error) {
	if g == nil {
		return nil, malformed("Exact graph required")
	}
	if in.DiagramScope != nil && (in.Diagram == nil || in.DiagramScope.TargetHash != g.Pins.TargetHash || in.Diagram.Pin != in.DiagramScope.Pin) {
		return nil, fault(422, "scope_mismatch", "Diagram scope differs from analysis target")
	}
	d := &diagnosticEvaluator{ctx: ctx, graph: g, input: in, proofCache: map[ObjectAddress]bool{}, nodes: map[string]backendmodel.Node{}, out: map[string][]backendmodel.Edge{}, report: DiagnosticReport{Findings: []backendmodel.Finding{}, Checks: []backendmodel.FindingCheck{}, Gaps: []string{}, Complete: true, certainties: map[string]string{}}}
	limitsRaw, _ := json.Marshal(in.Limits)
	_ = json.Unmarshal(limitsRaw, &d.input.Limits)
	if len(g.State.Nodes)+len(g.State.Edges) > 250000 {
		d.report.Complete = false
		d.report.Gaps = append(d.report.Gaps, "source_traversal_budget")
		return &d.report, nil
	}
	for _, n := range g.State.Nodes {
		d.nodes[n.ID] = n
	}
	for _, e := range g.State.Edges {
		d.out[e.From] = append(d.out[e.From], e)
	}
	for id := range d.out {
		slices.SortFunc(d.out[id], func(a, b backendmodel.Edge) int {
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})
	}
	scope := in.Scope
	scope.Certainty = ""
	identity := struct {
		Scope     Scope
		DiagramID string
		Selectors []backendmodel.DiagramScopeSelector
	}{Scope: scope}
	if in.DiagramScope != nil {
		identity.DiagramID = in.DiagramScope.Pin.ID
		identity.Selectors = in.DiagramScope.Selectors
		d.selected = map[ObjectAddress]bool{}
		for _, ref := range in.DiagramScope.SourceRefs {
			if ref.Kind == "record" {
				d.selected[ObjectAddress{RecordType: ref.RecordType, ID: ref.ID}] = true
			}
		}
		for _, gap := range in.DiagramScope.Gaps {
			d.report.Gaps = append(d.report.Gaps, gap.Code)
		}
		if in.DiagramScope.Truncated {
			d.report.Complete = false
		}
	}
	d.scopeKey, _ = requestHash(identity)
	nodes := slices.Clone(g.State.Nodes)
	slices.SortFunc(nodes, func(a, b backendmodel.Node) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !d.includes("node", n.ID, n.Kind) {
			continue
		}
		if !d.tick() {
			break
		}
		d.nodeRules(n)
	}
	edges := slices.Clone(g.State.Edges)
	slices.SortFunc(edges, func(a, b backendmodel.Edge) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for _, edge := range edges {
		if !d.tick() {
			break
		}
		d.edgeRules(edge)
	}
	d.transactionCriteria()
	d.diagramRules()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(d.report.Findings, func(a, b backendmodel.Finding) int {
		if a.Fingerprint < b.Fingerprint {
			return -1
		}
		if a.Fingerprint > b.Fingerprint {
			return 1
		}
		return 0
	})
	slices.Sort(d.report.Gaps)
	d.report.Gaps = slices.Compact(d.report.Gaps)
	return &d.report, nil
}
func (d *diagnosticEvaluator) includes(typ, id, kind string) bool {
	a := ObjectAddress{RecordType: typ, ID: id}
	return (d.selected == nil || d.selected[a]) && scopeObjectSelected(d.input.Scope, a, kind) && (d.input.Scope.Service == "" || objectInService(d.graph, a, d.input.Scope.Service))
}
func (d *diagnosticEvaluator) tick() bool {
	d.visits++
	if d.visits > d.input.Limits.DependencyVisits || d.ctx.Err() != nil {
		d.report.Complete = false
		d.report.Gaps = append(d.report.Gaps, "diagnostic_visit_budget")
		return false
	}
	return true
}
func (d *diagnosticEvaluator) proof(typ, id string) bool {
	a := ObjectAddress{RecordType: typ, ID: id}
	if value, ok := d.proofCache[a]; ok {
		return value
	}
	value := recordProof(d.graph, a).Status == "explicit"
	d.proofCache[a] = value
	return value
}
func (d *diagnosticEvaluator) check(rule, identity, status, certainty, message string, subjects []backendmodel.ChangeRecordRef, prerequisites, gaps []string, basis any) {
	fp, _ := requestHash(struct{ Policy, Rule, Identity string }{"diagnostics-v1", rule, identity})
	if len(d.report.Checks) >= d.input.Limits.Records {
		d.report.Complete = false
		d.report.Gaps = append(d.report.Gaps, "check_limit")
		return
	}
	d.report.Checks = append(d.report.Checks, backendmodel.FindingCheck{Fingerprint: fp, Status: status, ScopeKey: d.scopeKey})
	d.report.certainties[fp] = certainty
	if status == "absent" {
		return
	}
	if status == "unknown" {
		d.report.Gaps = append(d.report.Gaps, rule+":"+identity)
	}
	if len(d.report.Findings) >= d.input.Limits.Findings {
		d.report.Complete = false
		d.report.Gaps = append(d.report.Gaps, "finding_limit")
		return
	}
	evidence := []string{}
	for _, s := range subjects {
		p := recordProof(d.graph, ObjectAddress{RecordType: s.RecordType, ID: s.ID})
		evidence = append(evidence, p.EvidenceIDs...)
	}
	slices.Sort(evidence)
	evidence = slices.Compact(evidence)
	hash, _ := requestHash(struct {
		Target    string
		Scope     string
		Basis     any
		Evidence  []string
		Certainty string
	}{d.graph.Pins.TargetHash, func() string {
		if d.input.DiagramScope != nil {
			return d.input.DiagramScope.ScopeHash
		}
		return d.scopeKey
	}(), basis, evidence, certainty})
	if gaps == nil {
		gaps = []string{}
	}
	finding := backendmodel.Finding{Witness: backendmodel.FindingWitness{Records: subjects, FacetDifferences: []backendmodel.FacetPairDifference{}}, Fingerprint: fp, BasisHash: hash, Rule: rule, RuleVersion: "1", Severity: "review", Certainty: certainty, Subjects: subjects, EvidenceIDs: evidence, Prerequisites: prerequisites, Gaps: gaps, Message: message, TargetHash: d.graph.Pins.TargetHash, DiagramScope: d.input.DiagramScope}
	if comparison, ok := basis.(*backendmodel.FacetComparison); ok {
		finding.Witness.FacetDifferences = comparison.Pairs
	}
	raw, err := canonical(finding)
	if err != nil || d.bytes+int64(len(raw)) > d.input.Limits.ResultBytes/2 {
		d.report.Complete = false
		d.report.Gaps = append(d.report.Gaps, "diagnostic_byte_budget")
		return
	}
	d.bytes += int64(len(raw))
	d.report.Findings = append(d.report.Findings, finding)
}
func diagnosticSnapshot(ctx context.Context, in *ImmutableInput, g *backendmodel.EffectiveGraphSnapshot) (*TerminalSnapshot, error) {
	report, err := EvaluateDiagnostics(ctx, g, DiagnosticInput{Scope: in.Scope, DiagramScope: in.DiagramScope, Diagram: in.DiagnosticDiagram, Limits: in.Limits})
	if err != nil {
		return nil, err
	}
	r := newReport(in)
	r.before = g
	r.after = g
	r.services = objectServices(g, g)
	accepted := map[string]bool{}
	for _, c := range report.Checks {
		// The check carries the same certainty as its finding, so one filter
		// accepts or omits both together (review 2026-10-06, F137).
		certainty := report.certainties[c.Fingerprint]
		if certainty == "" {
			certainty = "unknown"
		}
		if r.add("checks", ObjectAddress{RecordType: "diagnostic", ID: c.Fingerprint}, "diagnostic_check", certainty, 0, c) {
			accepted[c.Fingerprint] = true
		}
	}
	for _, f := range report.Findings {
		if !accepted[f.Fingerprint] {
			continue
		}
		a := ObjectAddress{RecordType: "diagnostic", ID: f.Fingerprint}
		if len(f.Subjects) > 0 {
			a = ObjectAddress{RecordType: f.Subjects[0].RecordType, ID: f.Subjects[0].ID}
		}
		r.add("findings", a, f.Rule, f.Certainty, 0, f)
	}
	for _, gap := range report.Gaps {
		r.gap(gap, ObjectAddress{RecordType: "diagnostic", ID: gap})
	}
	if !report.Complete {
		r.truncate("diagnostic_budget")
	}
	r.potential = len(report.Findings) > 0
	terminal, err := r.finish(g, g, nil)
	if err != nil {
		return nil, err
	}
	terminal.Snapshot.Manifest.DiagramScope = in.DiagramScope
	return terminal, nil
}
