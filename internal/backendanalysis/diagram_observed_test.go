package backendanalysis

import (
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
)

func TestDiagramObservedEventProofAndUnknownOrder(t *testing.T) {
	for _, kind := range []string{"architecture", "interactions", "lifecycle", "business_map"} {
		t.Run(kind, func(t *testing.T) {
			data := metricData()
			scope := bm.DiagramScope{Pin: bm.DiagramPin{ID: kind, Version: 1, ContentHash: "exact"}, ScopeHash: "scope"}
			data.Sets[0].Correlation.DiagramScope = &scope
			data.Sets[0].Correlation.SourceCompatible = true
			c := data.Sets[0].Version.Context
			c.Producer.SchemaVersion = "orders-message-causal-v1"
			data.Sets[0].Version.Context = c
			a := data.Sets[0].Records[0]
			a.Attrs = &o.Attributes{MessageRole: "send", MessageIDHash: strings.Repeat("a", 64)}
			b := a
			b.ID = "receiver"
			b.TraceID = "other-trace"
			b.SpanID = "receiver"
			b.Attrs = &o.Attributes{MessageRole: "receive", MessageIDHash: a.Attrs.MessageIDHash}
			b.StartTimeUnixNano = "1"
			b.EndTimeUnixNano = "2"
			b.Links = &[]o.SpanLink{{TraceID: a.TraceID, SpanID: a.SpanID, Relation: "follows_from", Proof: &o.LinkProof{Profile: "orders-message-causal-v1", MessageIDHash: a.Attrs.MessageIDHash, PredecessorEvent: "send", SuccessorEvent: "receive"}}}
			data.Sets[0].Records = []o.Record{a}
			second := data.Sets[0]
			second.Records = []o.Record{b}
			data.Sets = append(data.Sets, second, data.Sets[0])
			m, e := MeasureScenario(t.Context(), measureInput(), data)
			if e != nil {
				t.Fatal(e)
			}
			got, e := ObserveDiagram(t.Context(), scope, data, *m)
			if e != nil || len(got.Elements) != 2 || len(got.Relations) != 1 || got.Relations[0].Kind != "event_precedence" || !strings.HasSuffix(got.Relations[0].From, "/send") {
				t.Fatal(got, e)
			}
			(*b.Links)[0].Relation = "association"
			(*b.Links)[0].Proof = nil
			data.Sets[1].Records = []o.Record{b}
			got, e = ObserveDiagram(t.Context(), scope, data, *m)
			if e != nil || got.Relations[0].Kind != "association" {
				t.Fatal(got, e)
			}
			data.Sets = data.Sets[1:2]
			got, e = ObserveDiagram(t.Context(), scope, data, *m)
			if e != nil || len(got.Relations) != 0 || len(got.Gaps) == 0 {
				t.Fatal("missing peer invented", got, e)
			}
		})
	}
}
