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
	mappings := map[string]o.DiagramCorrelationRow{}
	compatible := map[string]bool{}
	sourceCompatibility := map[string]bool{}
	for _, set := range data.Sets {
		if set.Pin.Side != metrics.Input.Side {
			continue
		}
		if set.Correlation.DiagramScope == nil || !reflect.DeepEqual(*set.Correlation.DiagramScope, scope) {
			return nil, fault(422, "diagram_pin_mismatch", "Overlay does not belong to the exact diagram scope")
		}
		out.Pins = append(out.Pins, set.Pin)
		identity := observationIdentity(set.Version.Context)
		sourceCompatibility[identity] = set.Correlation.SourceCompatible
		for _, row := range set.Correlation.DiagramRows {
			key := identity + "/" + row.RecordID
			if old, ok := mappings[key]; ok && !reflect.DeepEqual(old, row) {
				return nil, fault(422, "diagram_mapping_conflict", "Conflicting mappings for overlapping evidence")
			}
			mappings[key] = row
			compatible[key] = set.Correlation.SourceCompatible
		}
	}
	spans := map[string][]uniqueObservation{}
	selected := map[string]uniqueObservation{}
	for _, r := range rows {
		if !slices.Contains(metrics.Input.ExecutionIDs, r.Record.ExecutionID) || controlRecord(r.Record) {
			continue
		}
		if r.Record.Type == "span" {
			k := r.Record.TraceID + "/" + r.Record.SpanID
			spans[k] = append(spans[k], r)
		}
		selected[r.Key] = r
		row := mappings[observationIdentity(r.Context)+"/"+r.Record.ID]
		if row.Basis == "" || row.Basis == "unresolved" || !compatible[observationIdentity(r.Context)+"/"+r.Record.ID] {
			out.Gaps = append(out.Gaps, "unresolved mapping: "+r.Record.ID)
			row.Selectors = []bm.DiagramScopeSelector{}
			row.Basis = "unresolved"
		}
		out.Elements = append(out.Elements, ObservedElement{ID: digest([]byte(r.Key)), RecordID: r.Record.ID, ExecutionID: r.Record.ExecutionID, TraceID: r.Record.TraceID, SpanID: r.Record.SpanID, Selectors: row.Selectors, Basis: row.Basis})
	}
	for _, r := range rows {
		if _, ok := selected[r.Key]; !ok || r.Record.Type != "span" {
			continue
		}
		id := digest([]byte(r.Key))
		if r.Record.ParentSpanID != "" {
			peers := spans[r.Record.TraceID+"/"+r.Record.ParentSpanID]
			if len(peers) == 1 {
				out.Relations = append(out.Relations, ObservedRelation{From: digest([]byte(peers[0].Key)), To: id, Kind: "containment"})
			} else {
				out.Gaps = append(out.Gaps, "missing or ambiguous parent: "+r.Record.ID)
			}
		}
		if r.Record.Links == nil {
			continue
		}
		for _, link := range *r.Record.Links {
			peers := spans[link.TraceID+"/"+link.SpanID]
			if len(peers) != 1 {
				out.Gaps = append(out.Gaps, "missing or ambiguous link peer: "+r.Record.ID)
				continue
			}
			peer := peers[0]
			from := digest([]byte(peer.Key))
			relation := ObservedRelation{From: from, To: id, Kind: "association"}
			if link.Relation == "follows_from" {
				a, b := peer.Record.Attrs, r.Record.Attrs
				proof := link.Proof
				builds := peer.Context.Source.Status == "known" && r.Context.Source.Status == "known" && (peer.Context.Source.ServiceID != r.Context.Source.ServiceID || peer.Context.Source.BuildID == r.Context.Source.BuildID && peer.Context.Source.SourceFilesHash == r.Context.Source.SourceFilesHash)
				if proof != nil && proof.Profile == "orders-message-causal-v1" && proof.PredecessorEvent == "send" && proof.SuccessorEvent == "receive" && causalProfile(peer.Context) && causalProfile(r.Context) && a != nil && b != nil && a.MessageRole == "send" && b.MessageRole == "receive" && a.MessageIDHash == proof.MessageIDHash && b.MessageIDHash == proof.MessageIDHash && builds && sourceCompatibility[observationIdentity(peer.Context)] && sourceCompatibility[observationIdentity(r.Context)] && reflect.DeepEqual(peer.Context.Environment, r.Context.Environment) {
					relation = ObservedRelation{From: from + "/send", To: id + "/receive", Kind: "event_precedence", Proof: proof}
				} else {
					out.Gaps = append(out.Gaps, "unsupported or mismatched causal witness: "+r.Record.ID)
				}
			}
			out.Relations = append(out.Relations, relation)
		}
	}
	// Cycles among instrumented spans are invalid witnesses. Drop their asserted
	// event order conservatively; associations/containment remain visible.
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
