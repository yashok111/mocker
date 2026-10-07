package backendanalysis

import (
	"context"
	"reflect"
	"slices"
	"strings"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
)

type ObservedElement struct {
	ID          string                    `json:"id"`
	RecordID    string                    `json:"recordId"`
	ExecutionID string                    `json:"executionId"`
	TraceID     string                    `json:"traceId"`
	SpanID      string                    `json:"spanId"`
	Selectors   []bm.DiagramScopeSelector `json:"selectors"`
	Basis       string                    `json:"basis"`
}
type ObservedRelation struct {
	From  string       `json:"from"`
	To    string       `json:"to"`
	Kind  string       `json:"kind"`
	Proof *o.LinkProof `json:"proof,omitzero"`
}
type DiagramObserved struct {
	Policy       string               `json:"policy"`
	DiagramScope bm.DiagramScope      `json:"diagramScope"`
	Pins         []o.AnalysisPin      `json:"pins"`
	Measurements ScenarioMeasurements `json:"measurements"`
	Elements     []ObservedElement    `json:"elements"`
	Relations    []ObservedRelation   `json:"relations"`
	Gaps         []string             `json:"gaps"`
	Limitations  []string             `json:"limitations"`
}

func causalProfile(c o.ObservationContext) bool {
	return c.Producer.SchemaVersion == "orders-message-causal-v1" || c.Producer.AdapterVersion == "orders-message-causal-v1"
}
func ObserveDiagram(ctx context.Context, scope bm.DiagramScope, data PinnedMeasurementData, metrics ScenarioMeasurements) (*DiagramObserved, error) {
	out := &DiagramObserved{Policy: "backend-observed-sequence-v1", DiagramScope: scope, Pins: []o.AnalysisPin{}, Measurements: metrics, Elements: []ObservedElement{}, Relations: []ObservedRelation{}, Gaps: []string{}, Limitations: []string{"Static/design alternatives remain unverified", "Event precedence does not order whole-span completion", "One execution does not establish whole lifecycle or branch coverage"}}
	rows, err := uniqueObservations(ctx, data, metrics.Input.Side)
	if err != nil {
		return nil, err
	}
	v := &overlay{out: out, mappings: map[string]o.DiagramCorrelationRow{}, compatible: map[string]bool{}, sourceCompatibility: map[string]bool{}, spans: map[string][]uniqueObservation{}, selected: map[string]uniqueObservation{}}
	if err = v.loadMappings(data, scope, metrics.Input.Side); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if slices.Contains(metrics.Input.ExecutionIDs, r.Record.ExecutionID) && !controlRecord(r.Record) {
			v.addElement(r)
		}
	}
	for _, r := range rows {
		if _, ok := v.selected[r.Key]; ok && r.Record.Type == "span" {
			v.addRelations(r)
		}
	}
	v.dropCausalCycles()
	slices.SortFunc(out.Relations, func(a, b ObservedRelation) int {
		x, _ := canonical(a)
		y, _ := canonical(b)
		return strings.Compare(string(x), string(y))
	})
	out.Relations = slices.CompactFunc(out.Relations, func(a, b ObservedRelation) bool { return reflect.DeepEqual(a, b) })
	slices.Sort(out.Gaps)
	out.Gaps = slices.Compact(out.Gaps)
	return out, nil
}

// overlay builds one diagram overlay from the pinned sets' diagram mappings
// and the selected executions' records.
type overlay struct {
	out                 *DiagramObserved
	mappings            map[string]o.DiagramCorrelationRow
	compatible          map[string]bool
	sourceCompatibility map[string]bool
	spans               map[string][]uniqueObservation
	selected            map[string]uniqueObservation
}

// loadMappings indexes the side's diagram rows by observation identity and
// record. Every set must correlate exactly this scope, and overlapping
// evidence must map identically.
func (v *overlay) loadMappings(data PinnedMeasurementData, scope bm.DiagramScope, side string) error {
	for _, set := range data.Sets {
		if set.Pin.Side != side {
			continue
		}
		if set.Correlation.DiagramScope == nil || !reflect.DeepEqual(*set.Correlation.DiagramScope, scope) {
			return fault(422, "diagram_pin_mismatch", "Overlay does not belong to the exact diagram scope")
		}
		v.out.Pins = append(v.out.Pins, set.Pin)
		identity := observationIdentity(set.Version.Context)
		v.sourceCompatibility[identity] = set.Correlation.SourceCompatible
		for _, row := range set.Correlation.DiagramRows {
			key := identity + "/" + row.RecordID
			if old, ok := v.mappings[key]; ok && !reflect.DeepEqual(old, row) {
				return fault(422, "diagram_mapping_conflict", "Conflicting mappings for overlapping evidence")
			}
			v.mappings[key] = row
			v.compatible[key] = set.Correlation.SourceCompatible
		}
	}
	return nil
}

// addElement places one selected record on the diagram, or records it as an
// unresolved mapping.
func (v *overlay) addElement(r uniqueObservation) {
	if r.Record.Type == "span" {
		k := r.Record.TraceID + "/" + r.Record.SpanID
		v.spans[k] = append(v.spans[k], r)
	}
	v.selected[r.Key] = r
	key := observationIdentity(r.Context) + "/" + r.Record.ID
	row := v.mappings[key]
	if row.Basis == "" || row.Basis == "unresolved" || !v.compatible[key] {
		v.out.Gaps = append(v.out.Gaps, "unresolved mapping: "+r.Record.ID)
		row.Selectors = []bm.DiagramScopeSelector{}
		row.Basis = "unresolved"
	}
	v.out.Elements = append(v.out.Elements, ObservedElement{ID: digest([]byte(r.Key)), RecordID: r.Record.ID, ExecutionID: r.Record.ExecutionID, TraceID: r.Record.TraceID, SpanID: r.Record.SpanID, Selectors: row.Selectors, Basis: row.Basis})
}

// addRelations relates a selected span to its unique parent (containment)
// and to each unique link peer.
func (v *overlay) addRelations(r uniqueObservation) {
	out := v.out
	id := digest([]byte(r.Key))
	if r.Record.ParentSpanID != "" {
		peers := v.spans[r.Record.TraceID+"/"+r.Record.ParentSpanID]
		if len(peers) == 1 {
			out.Relations = append(out.Relations, ObservedRelation{From: digest([]byte(peers[0].Key)), To: id, Kind: "containment"})
		} else {
			out.Gaps = append(out.Gaps, "missing or ambiguous parent: "+r.Record.ID)
		}
	}
	if r.Record.Links == nil {
		return
	}
	for _, link := range *r.Record.Links {
		peers := v.spans[link.TraceID+"/"+link.SpanID]
		if len(peers) != 1 {
			out.Gaps = append(out.Gaps, "missing or ambiguous link peer: "+r.Record.ID)
			continue
		}
		peer := peers[0]
		from := digest([]byte(peer.Key))
		relation := ObservedRelation{From: from, To: id, Kind: "association"}
		if link.Relation == "follows_from" {
			if v.causalWitness(peer, r, link.Proof) {
				relation = ObservedRelation{From: from + "/send", To: id + "/receive", Kind: "event_precedence", Proof: link.Proof}
			} else {
				out.Gaps = append(out.Gaps, "unsupported or mismatched causal witness: "+r.Record.ID)
			}
		}
		out.Relations = append(out.Relations, relation)
	}
}

// causalWitness admits a follows_from link as event precedence only for the
// causal message profile: the send and receive of the very message the proof
// names, from compatible, comparable builds in the same environment.
func (v *overlay) causalWitness(peer, r uniqueObservation, proof *o.LinkProof) bool {
	if proof == nil || proof.Profile != "orders-message-causal-v1" || proof.PredecessorEvent != "send" || proof.SuccessorEvent != "receive" || !causalProfile(peer.Context) || !causalProfile(r.Context) {
		return false
	}
	a, b := peer.Record.Attrs, r.Record.Attrs
	if a == nil || b == nil || a.MessageRole != "send" || b.MessageRole != "receive" || a.MessageIDHash != proof.MessageIDHash || b.MessageIDHash != proof.MessageIDHash {
		return false
	}
	ps, rs := peer.Context.Source, r.Context.Source
	builds := ps.Status == "known" && rs.Status == "known" && (ps.ServiceID != rs.ServiceID || ps.BuildID == rs.BuildID && ps.SourceFilesHash == rs.SourceFilesHash)
	return builds && v.sourceCompatibility[observationIdentity(peer.Context)] && v.sourceCompatibility[observationIdentity(r.Context)] && reflect.DeepEqual(peer.Context.Environment, r.Context.Environment)
}

// dropCausalCycles: cycles among instrumented spans are invalid witnesses.
// Drop their asserted event order conservatively; associations/containment
// remain visible.
func (v *overlay) dropCausalCycles() {
	out := v.out
	adjacency := map[string][]string{}
	indegree := map[string]int{}
	for _, edge := range out.Relations {
		if edge.Kind == "event_precedence" {
			a := strings.TrimSuffix(edge.From, "/send")
			b := strings.TrimSuffix(edge.To, "/receive")
			adjacency[a] = append(adjacency[a], b)
			indegree[b]++
			if _, ok := indegree[a]; !ok {
				indegree[a] = 0
			}
		}
	}
	queue := []string{}
	for id, n := range indegree {
		if n == 0 {
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, to := range adjacency[id] {
			indegree[to]--
			if indegree[to] == 0 {
				queue = append(queue, to)
			}
		}
	}
	out.Relations = slices.DeleteFunc(out.Relations, func(e ObservedRelation) bool {
		if e.Kind == "event_precedence" && indegree[strings.TrimSuffix(e.To, "/receive")] > 0 {
			out.Gaps = append(out.Gaps, "causal cycle: order unavailable")
			return true
		}
		return false
	})
}
