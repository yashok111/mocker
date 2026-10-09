package backendmodel

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func eventsQueryState(t *testing.T) *RevisionState {
	t.Helper()
	s := eventsRuntimeState(t)
	s.Nodes = nil
	s.Edges = nil
	s.Evidence = nil
	add := func(id int, kind string, parent int, attrs map[string]any) {
		runtimeQueryAddNode(t, s, id, kind, parent, attrs)
	}
	add(1, "service", 0, nil)
	add(2, "flow", 3, map[string]any{"entryStepId": runtimeQueryID(4)})
	add(3, "handler", 1, nil)
	add(4, "flow_step", 2, map[string]any{"stepKind": "emit", "transactionContext": map[string]any{"status": "none", "reason": "Outside transaction"}})
	add(5, "message", 1, nil)
	add(6, "channel", 1, nil)
	add(7, "consumer", 1, map[string]any{"dispatchStatus": "complete"})
	add(8, "handler", 1, nil)
	add(9, "flow", 8, nil)
	edge := func(id int, kind string, from, to int, attrs map[string]any) {
		runtimeQueryAddEdge(t, s, id, kind, from, to, attrs)
	}
	edge(100, "emits", 4, 5, map[string]any{"channelId": runtimeQueryID(6), "deliveryStatus": "declared"})
	edge(101, "delivered_to", 6, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared", "condition": map[string]any{"status": "known", "value": "always"}, "group": map[string]any{"status": "unknown", "reason": "Dynamic group"}})
	edge(102, "handles", 7, 8, nil)
	edge(103, "contains", 8, 9, nil)
	return s
}
func eventsQueryPage(t *testing.T, s *RevisionState, in EventsQueryInput) *EventsPage {
	t.Helper()
	in.RevisionID = s.Revision.ID
	p, err := projectEvents(t.Context(), s, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func eventsQueryRefs(i EventsItem) EventsReferences {
	switch i.Kind {
	case "route":
		return i.Route.References
	case "boundary":
		return i.Boundary.References
	case "job":
		return i.Job.References
	case "service_call":
		return i.ServiceCall.References
	}
	return EventsReferences{}
}

func TestEventsDispatchExplainsBindingAndDownstreamProofSeparately(t *testing.T) {
	for _, subject := range []int{102, 9} {
		t.Run(runtimeQueryID(subject), func(t *testing.T) {
			s := eventsQueryState(t)
			proofID := ""
			for i := range s.Evidence {
				if s.Evidence[i].SubjectID == runtimeQueryID(subject) {
					s.Evidence[i].Status = "inferred"
					proofID = s.Evidence[i].ID
				}
			}
			if proofID == "" {
				t.Fatal("fixture proof absent")
			}
			page := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
			b := page.Items[0].Boundary
			if b == nil || len(b.Dispatch) != 1 {
				t.Fatal("expected qualified route")
			}
			d := b.Dispatch[0]
			if (len(d.FlowIDs) != 0) != (subject == 9) {
				t.Fatal("diagnostic must preserve existing conservative dispatch admission")
			}
			raw, _ := json.Marshal(d)
			var envelope struct {
				SourceFlowIDs []string `json:"sourceFlowIds"`
				Diagnostics   []struct {
					Gate        string   `json:"gate"`
					SubjectID   string   `json:"subjectId"`
					Status      string   `json:"status"`
					EvidenceIDs []string `json:"evidenceIds"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			if subject == 102 && !slices.Contains(envelope.SourceFlowIDs, runtimeQueryID(9)) {
				t.Fatal("withheld dispatch hides separately stored source body")
			}
			want := "handles_proof"
			if subject == 9 {
				want = "flow_proof"
			}
			if !slices.ContainsFunc(envelope.Diagnostics, func(v struct {
				Gate        string   `json:"gate"`
				SubjectID   string   `json:"subjectId"`
				Status      string   `json:"status"`
				EvidenceIDs []string `json:"evidenceIds"`
			}) bool {
				return v.Gate == want && v.SubjectID == runtimeQueryID(subject) && v.Status == "inferred" && slices.Contains(v.EvidenceIDs, proofID)
			}) {
				t.Fatalf("missing exact %s diagnostic: %s", want, raw)
			}
		})
	}
}
func TestEventsQueryExactRoutesAndPureRead(t *testing.T) {
	s := eventsQueryState(t)
	runtimeQueryAddNode(t, s, 10, "channel", 1, nil)
	runtimeQueryAddEdge(t, s, 104, "emits", 4, 5, map[string]any{"channelId": runtimeQueryID(10), "deliveryStatus": "declared"})
	runtimeQueryAddEdge(t, s, 105, "delivered_to", 10, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	// Same-name unmatched channel is an orphan, never paired by message/name.
	runtimeQueryAddNode(t, s, 11, "channel", 1, nil)
	runtimeQueryAddEdge(t, s, 106, "delivered_to", 11, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	before, _ := json.Marshal(s, json.Deterministic(true))
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if len(p.Items) != 3 {
		t.Fatalf("want two exact routes plus orphan, got %+v", p)
	}
	pairs := map[string]string{}
	for _, i := range p.Items {
		r := eventsQueryRefs(i)
		pairs[r.EmitsEdgeID] = r.DeliveryEdgeID
	}
	if pairs[runtimeQueryID(100)] != runtimeQueryID(101) || pairs[runtimeQueryID(104)] != runtimeQueryID(105) || pairs[""] != runtimeQueryID(106) {
		t.Fatal("cross-channel bridge", pairs)
	}
	route := p.Items[1].Route // items sort by exact reference tuple: orphan then emits100 then emits104
	if route == nil || route.Dispatch[0].HandlerID != runtimeQueryID(8) || route.Dispatch[0].FlowIDs[0] != runtimeQueryID(9) {
		t.Fatal("exact dispatch missing", p.Items)
	}
	if !slices.Contains(route.Witness.EdgeIDs, runtimeQueryID(102)) || !slices.Contains(route.Witness.EdgeIDs, runtimeQueryID(103)) {
		t.Fatal("handler/flow witness missing", route)
	}
	if route.EmitContext == nil || route.EmitContext.Transaction.Status != "none" {
		t.Fatal("local emit context missing", route)
	}
	after, _ := json.Marshal(s, json.Deterministic(true))
	if string(before) != string(after) {
		t.Fatal("projection mutated input")
	}
}
func TestEventsQuerySelectorsStrictAndCursorPins(t *testing.T) {
	id := runtimeQueryID(900)
	for _, raw := range []string{
		`{"revisionId":"` + id + `","view":"routes","serviceId":""}`,
		`{"revisionId":"` + id + `","view":"jobs","seedNodeId":""}`,
		`{"revisionId":"` + id + `","view":"routes","seedNodeId":null}`,
		`{"revisionId":"` + id + `","view":"routes","limit":0}`,
		`{"revisionId":"` + id + `","view":"routes","limit":101}`,
		`{"revisionId":"` + id + `","view":"routes","cursor":null}`,
		`{"revisionId":"` + id + `","view":"routes","unexpected":true}`,
		`{"revisionId":"` + id + `","view":"routes","view":"jobs"}`,
	} {
		var in EventsQueryInput
		if json.Unmarshal([]byte(raw), &in) == nil {
			t.Fatal("accepted", raw)
		}
	}
	s := eventsQueryState(t)
	runtimeQueryAddEdge(t, s, 104, "delivered_to", 6, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	first := eventsQueryPage(t, s, EventsQueryInput{View: "routes", Limit: 1})
	if first.NextCursor == "" || first.Truncated || !first.Complete {
		t.Fatal("paging is not scan truncation", first)
	}
	second := eventsQueryPage(t, s, EventsQueryInput{View: "routes", Limit: 1, Cursor: first.NextCursor})
	if len(second.Items) != 1 || eventsQueryRefs(second.Items[0]).DeliveryEdgeID == eventsQueryRefs(first.Items[0]).DeliveryEdgeID {
		t.Fatal("bad second page", second)
	}
	for _, in := range []EventsQueryInput{{View: "routes", Limit: 2}, {View: "routes", Limit: 1, SeedNodeID: runtimeQueryID(7)}, {View: "jobs", Limit: 1}, {View: "service_calls", Limit: 1, ServiceID: runtimeQueryID(1)}} {
		in.RevisionID = s.Revision.ID
		in.Cursor = first.NextCursor
		if _, err := projectEvents(t.Context(), s, in); err == nil {
			t.Fatal("cursor accepted different selector", in)
		}
	}
	s.Revision.SemanticHash = strings.Repeat("b", 64)
	if _, err := projectEvents(t.Context(), s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes", Limit: 1, Cursor: first.NextCursor}); err == nil {
		t.Fatal("cursor accepted changed hash")
	}
}
func TestEventsQueryBoundariesRetryAndStaticJobs(t *testing.T) {
	s := eventsQueryState(t)
	s.Edges[2].Freshness = &AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
	runtimeQueryAddEdge(t, s, 104, "retries", 7, 6, map[string]any{"messageId": runtimeQueryID(5), "reason": "configured retry", "delay": map[string]any{"status": "known", "value": "5s"}, "maxAttempts": map[string]any{"status": "known", "value": 3}})
	runtimeQueryAddEdge(t, s, 105, "dead_letters", 7, 6, map[string]any{"messageId": runtimeQueryID(5), "reason": "configured DLQ"})
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes", SeedNodeID: runtimeQueryID(7)})
	if len(p.Items) != 1 || p.Items[0].Boundary == nil {
		t.Fatal("stale handle hidden", p)
	}
	b := p.Items[0].Boundary
	if b.Witness.Status != "stale" || len(b.Dispatch) != 1 || len(b.Dispatch[0].FlowIDs) != 0 || len(b.Related) != 2 {
		t.Fatal("stale boundary expanded or retry loop unfolded", b)
	}
	runtimeQueryAddNode(t, s, 20, "job", 1, map[string]any{"trigger": map[string]any{"kind": "cron", "expression": map[string]any{"status": "unknown", "reason": "Dynamic expression"}, "timezone": map[string]any{"status": "known", "value": "UTC"}}, "dispatchStatus": "complete"})
	runtimeQueryAddEdge(t, s, 106, "handles", 20, 8, nil)
	jobs := eventsQueryPage(t, s, EventsQueryInput{View: "jobs", ServiceID: runtimeQueryID(1)})
	if len(jobs.Items) != 1 || jobs.Items[0].Job == nil || jobs.Items[0].Job.Trigger.Kind != "cron" || jobs.Items[0].Job.Trigger.Expression.Status != "unknown" {
		t.Fatal("static trigger lost", jobs)
	}
}
func TestEventsQueryBudgetsAndCancellation(t *testing.T) {
	s := eventsQueryState(t)
	s.Edges = make([]Edge, EventsMaxExaminedEdges+1)
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if !p.Truncated || p.Complete || p.ExaminedEdgeCount != 0 || len(p.Items) != 0 || !slices.Contains(p.TruncationReasons, "edge_limit") {
		t.Fatal("unbounded or dishonest admission", p)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := projectEvents(ctx, s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s = eventsQueryState(t)
	s.Edges = s.Edges[2:]
	for i := range 72 {
		runtimeQueryAddEdge(t, s, 1000+i, "emits", 4, 5, map[string]any{"channelId": runtimeQueryID(6), "deliveryStatus": "declared"})
		runtimeQueryAddEdge(t, s, 2000+i, "delivered_to", 6, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	}
	p = eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if !p.Truncated || p.Complete || p.ConstructedItemCount != EventsMaxItems || !slices.Contains(p.TruncationReasons, "item_limit") {
		t.Fatal("tuple budget not admitted before construction", p)
	}
	again := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if !reflect.DeepEqual(p, again) {
		t.Fatal("nondeterministic bounded output")
	}
}

func TestEventsQueryServiceCallsExactOperationAndExternalBoundary(t *testing.T) {
	s := eventsQueryState(t)
	runtimeQueryAddNode(t, s, 20, "service", 0, nil)
	runtimeQueryAddNode(t, s, 21, "http_operation", 20, nil)
	runtimeQueryAddNode(t, s, 22, "flow_step", 2, map[string]any{"stepKind": "call"})
	runtimeQueryAddNode(t, s, 23, "external_system", 0, nil)
	runtimeQueryAddNode(t, s, 24, "flow_step", 2, map[string]any{"stepKind": "call"})
	runtimeQueryAddEdge(t, s, 110, "calls", 22, 21, nil)
	runtimeQueryAddEdge(t, s, 111, "handles", 21, 8, nil)
	runtimeQueryAddEdge(t, s, 112, "calls", 24, 23, nil)
	page := eventsQueryPage(t, s, EventsQueryInput{View: "service_calls", ServiceID: runtimeQueryID(1)})
	if len(page.Items) != 2 || page.Items[0].ServiceCall == nil || page.Items[1].Boundary == nil {
		t.Fatal("remote/external calls missing", page)
	}
	call := page.Items[0].ServiceCall
	if call.References.TargetServiceID != runtimeQueryID(20) || call.References.OperationID != runtimeQueryID(21) || !slices.Equal(call.Dispatch[0].FlowIDs, []string{runtimeQueryID(9)}) || !slices.Equal(call.Witness.EdgeIDs, []string{runtimeQueryID(103), runtimeQueryID(110), runtimeQueryID(111)}) {
		t.Fatal("exact call/handles/flow chain lost", call)
	}
	external := page.Items[1].Boundary
	if external.References.TargetID != runtimeQueryID(23) || len(external.Dispatch) != 0 {
		t.Fatal("external boundary expanded", external)
	}
	reverse := eventsQueryPage(t, s, EventsQueryInput{View: "service_calls", ServiceID: runtimeQueryID(20)})
	if len(reverse.Items) != 0 {
		t.Fatal("source-service filter applied to remote destination", reverse)
	}
}

// An otherwise explicit subject with >256 evidence references must not allocate
// an unbounded per-item proof list or report a complete explicit witness.
func TestEventsQueryEvidenceWitnessLimit(t *testing.T) {
	s := eventsQueryState(t)
	s.Nodes[3].EvidenceIDs = nil
	for i := range EventsMaxWitnessRecords + 1 {
		id := runtimeQueryID(500000 + i)
		s.Nodes[3].EvidenceIDs = append(s.Nodes[3].EvidenceIDs, id)
		s.Evidence = append(s.Evidence, Evidence{ID: id, SubjectID: s.Nodes[3].ID, Status: "explicit"})
	}
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if !p.Truncated || p.Complete || !slices.Contains(p.TruncationReasons, "witness_limit") || p.Items[0].Boundary == nil || len(p.Items[0].Boundary.Witness.EvidenceIDs) > EventsMaxWitnessRecords {
		t.Fatalf("evidence allocation limit hidden: %+v", p)
	}
}

func TestEventsQueryNodeAndScanBoundaryAdmission(t *testing.T) {
	s := eventsQueryState(t)
	for _, in := range []EventsQueryInput{{View: "routes", SeedNodeID: runtimeQueryID(1)}, {View: "jobs", ServiceID: runtimeQueryID(7)}} {
		in.RevisionID = s.Revision.ID
		_, err := projectEvents(t.Context(), s, in)
		f, ok := errors.AsType[*FaultError](err)
		if !ok || f.Status != 400 {
			t.Fatal("wrong selector kind", err)
		}
	}
	_, err := projectEvents(t.Context(), s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes", SeedNodeID: runtimeQueryID(500)})
	f, ok := errors.AsType[*FaultError](err)
	if !ok || f.Status != 404 {
		t.Fatal("missing selector", err)
	}
	s.Revision.SchemaVersion = "4"
	_, err = projectEvents(t.Context(), s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes"})
	f, ok = errors.AsType[*FaultError](err)
	if !ok || f.Status != 422 {
		t.Fatal("old profile gained events read", err)
	}
	s = eventsQueryState(t)
	original := len(s.Edges)
	for i := range EventsMaxExaminedEdges - original {
		s.Edges = append(s.Edges, Edge{ID: runtimeQueryID(10000 + i), Kind: "derived_from", From: runtimeQueryID(1), To: runtimeQueryID(1)})
	}
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if p.Truncated || !p.Complete || p.ExaminedEdgeCount != EventsMaxExaminedEdges || len(p.Items) != 1 {
		t.Fatal("exact edge admission limit rejected", p)
	}
	s.Edges = append(s.Edges, Edge{ID: runtimeQueryID(50000)})
	p = eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if !p.Truncated || p.ExaminedEdgeCount != 0 || p.TotalEdgeCount != EventsMaxExaminedEdges+1 {
		t.Fatal("edge-limit +1 not accounted", p)
	}
}

func TestEventsQueryAdmitsOneHundredThousandEdges(t *testing.T) {
	s := eventsQueryState(t)
	for len(s.Edges) < 100000 {
		s.Edges = append(s.Edges, Edge{ID: runtimeQueryID(10000 + len(s.Edges)), Kind: "derived_from", From: runtimeQueryID(1), To: runtimeQueryID(1)})
	}
	for _, view := range []string{"routes", "jobs", "service_calls"} {
		page := eventsQueryPage(t, s, EventsQueryInput{View: view})
		if !page.Complete || page.Truncated || page.ExaminedEdgeCount != 100000 || page.Limits.MaxExaminedEdges != 100000 {
			t.Fatalf("%s refused the requested budget: %+v", view, page)
		}
		if view == "routes" && len(page.Items) != 1 {
			t.Fatal("known route lost after raising admission")
		}
	}
	s.Edges = append(s.Edges, Edge{ID: runtimeQueryID(200001)})
	page := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	if page.Complete || page.ExaminedEdgeCount != 0 || page.TotalEdgeCount != 100001 || !slices.Contains(page.TruncationReasons, "edge_limit") {
		t.Fatal("100001 edges bypassed complete-scan admission")
	}
}

func TestEventsQueryPartialDispatchKeepsKnownFlow(t *testing.T) {
	s := eventsQueryState(t)
	s.Nodes[6].Attributes = runtimeQueryAttrs(t, map[string]any{"dispatchStatus": "partial", "dispatchReason": "Dynamic remainder", "analysisStatus": "partial", "gaps": []string{"Runtime handler remainder"}})
	runtimeQueryAddNode(t, s, 25, "unresolved_target", 1, map[string]any{"expectedKind": "handler", "reason": "Dynamic remainder"})
	runtimeQueryAddEdge(t, s, 107, "handles", 7, 25, nil)
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	b := p.Items[0].Boundary
	if b == nil || len(b.Dispatch) != 2 || len(b.Dispatch[0].FlowIDs) != 1 || b.Dispatch[1].UnresolvedTargetID != runtimeQueryID(25) {
		t.Fatal("partial dispatch erased explicit handler flow", p)
	}
}

func TestEventsQueryConfiguredScalarPreservesSourceShape(t *testing.T) {
	s := eventsQueryState(t)
	s.Edges[1].Attributes["group"] = runtimeQueryAttrs(t, map[string]any{"group": known("billing")})["group"]
	p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
	raw, err := json.Marshal(p.Items[0].Route.Group)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"status":"known"`) || !strings.Contains(string(raw), `"value":"billing"`) {
		t.Fatalf("configured scalar status/native value changed: %s", raw)
	}
}

func TestEventsQueryItemAdmissionExactAndPlusOne(t *testing.T) {
	for _, count := range []int{EventsMaxItems, EventsMaxItems + 1} {
		t.Run(map[int]string{EventsMaxItems: "exact", EventsMaxItems + 1: "plus_one"}[count], func(t *testing.T) {
			s := eventsQueryState(t)
			s.Edges = s.Edges[1:]
			for i := range count {
				runtimeQueryAddEdge(t, s, 10000+i, "emits", 4, 5, map[string]any{"channelId": runtimeQueryID(6), "deliveryStatus": "declared"})
			}
			p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
			if p.ConstructedItemCount != EventsMaxItems || p.Truncated != (count > EventsMaxItems) || p.Complete != (count == EventsMaxItems) {
				t.Fatal("exact item admission boundary", count, p)
			}
		})
	}
}

func TestEventsQueryAuxiliaryAdmissionStopsBeforeNestedConstruction(t *testing.T) {
	s := eventsQueryState(t)
	s.Edges = nil
	// Each job explicitly handles one shared handler with many owned flows. This
	// repeats configured dispatch expansion without inventing execution paths.
	for i := range 140 {
		runtimeQueryAddNode(t, s, 1000+i, "flow", 8, nil)
		runtimeQueryAddEdge(t, s, 2000+i, "contains", 8, 1000+i, nil)
	}
	for i := range 143 {
		runtimeQueryAddNode(t, s, 3000+i, "job", 1, map[string]any{"trigger": map[string]any{"kind": "manual"}, "dispatchStatus": "complete"})
		runtimeQueryAddEdge(t, s, 4000+i, "handles", 3000+i, 8, nil)
	}
	p := eventsQueryPage(t, s, EventsQueryInput{View: "jobs"})
	if p.AuxiliaryRecordCount != EventsMaxAuxiliaryRecords || !p.Truncated || p.Complete || !slices.Contains(p.TruncationReasons, "auxiliary_limit") {
		t.Fatal("nested dispatch construction unbounded", p)
	}
}

func TestEventsQueryScalarKnownAndUnknownNativeValues(t *testing.T) {
	for _, field := range []string{"expression", "timezone", "group", "address"} {
		for _, value := range []map[string]any{known("configured"), {"status": "unknown", "reason": "Dynamic configuration"}} {
			attrs := runtimeQueryAttrs(t, map[string]any{field: value})
			got := eventsScalar(attrs, field)
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if value["status"] == "known" {
				if got.Status != "known" || !strings.Contains(string(raw), `"value":"configured"`) {
					t.Fatal(field, got)
				}
			} else if got.Status != "unknown" || got.Reason != "Dynamic configuration" || len(got.Value) != 0 {
				t.Fatal(field, got)
			}
		}
	}
}

func TestEventsQueryMalformedCursorOrderingKey(t *testing.T) {
	s := eventsQueryState(t)
	runtimeQueryAddEdge(t, s, 108, "delivered_to", 6, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	first := eventsQueryPage(t, s, EventsQueryInput{View: "routes", Limit: 1})
	raw, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var cursor graphCursor
	if json.Unmarshal(raw, &cursor) != nil {
		t.Fatal("invalid fixture cursor")
	}
	cursor.After = "invalid-ordering-key"
	raw, err = json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectEvents(t.Context(), s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes", Limit: 1, Cursor: base64.RawURLEncoding.EncodeToString(raw)})
	f, ok := errors.AsType[*FaultError](err)
	if !ok || f.Status != 400 {
		t.Fatal("malformed ordering accepted", err)
	}
}
