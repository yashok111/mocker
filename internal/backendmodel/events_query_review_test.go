package backendmodel

import (
	"context"
	"slices"
	"testing"
)

// Counting cancellation checkpoints detects the review's excluded Cartesian
// join without timing assumptions; selected work stays linear in source records.
type eventsQueryWorkContext struct {
	context.Context
	checks int
}

func (c *eventsQueryWorkContext) Err() error { c.checks++; return c.Context.Err() }

func TestEventsQuerySelectiveHighFanoutWork(t *testing.T) {
	s := eventsQueryState(t)
	s.Edges = s.Edges[2:]
	runtimeQueryAddNode(t, s, 30, "consumer", 1, map[string]any{"dispatchStatus": "complete"})
	runtimeQueryAddNode(t, s, 31, "message", 1, nil)
	runtimeQueryAddNode(t, s, 32, "channel", 1, nil)
	runtimeQueryAddNode(t, s, 33, "flow_step", 2, map[string]any{"stepKind": "emit"})
	for i := range 1000 {
		runtimeQueryAddEdge(t, s, 1000+i, "emits", 4, 5, map[string]any{"channelId": runtimeQueryID(6), "deliveryStatus": "declared"})
		runtimeQueryAddEdge(t, s, 3000+i, "delivered_to", 6, 7, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	}
	for _, seed := range []struct {
		name      string
		id, items int
	}{{"consumer", 30, 0}, {"message", 31, 0}, {"channel", 32, 0}, {"producer", 33, 1}} {
		t.Run(seed.name, func(t *testing.T) {
			ctx := &eventsQueryWorkContext{Context: t.Context()}
			p, err := projectEvents(ctx, s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes", SeedNodeID: runtimeQueryID(seed.id)})
			if err != nil {
				t.Fatal(err)
			}
			if p.Truncated || !p.Complete || len(p.Items) != seed.items || p.ConstructedItemCount != seed.items {
				t.Fatalf("unrelated pairs entered selection: %+v", p)
			}
			bound := 20 * (len(s.Edges) + len(s.Nodes) + len(s.Evidence))
			if ctx.checks > bound {
				t.Fatalf("selective read expanded unrelated pairs: edges=%d items=%d checks=%d linear bound=%d", len(s.Edges), p.ConstructedItemCount, ctx.checks, bound)
			}
			t.Logf("edges=%d items=%d context checkpoints=%d bound=%d", len(s.Edges), p.ConstructedItemCount, ctx.checks, bound)
		})
	}
	// A selected subscription joins only its own bucket entries. Restricting the
	// delivery index must not manufacture missing-route boundaries or lose an
	// actual orphan of the same selected consumer.
	runtimeQueryAddEdge(t, s, 5000, "delivered_to", 6, 30, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	runtimeQueryAddEdge(t, s, 5001, "delivered_to", 32, 30, map[string]any{"messageId": runtimeQueryID(5), "deliveryStatus": "declared"})
	ctx := &eventsQueryWorkContext{Context: t.Context()}
	p, err := projectEvents(ctx, s, EventsQueryInput{RevisionID: s.Revision.ID, View: "routes", SeedNodeID: runtimeQueryID(30)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Truncated || !p.Complete || p.ConstructedItemCount != 1001 {
		t.Fatal("selected subscription/orphan enumeration changed", p)
	}
	orphan := eventsQueryRefs(p.Items[0])
	if orphan.EmitsEdgeID != "" || orphan.DeliveryEdgeID != runtimeQueryID(5001) || p.Items[0].Boundary == nil {
		t.Fatal("selected orphan hidden or invented producer", p.Items[0])
	}
	for _, item := range p.Items[1:] {
		refs := eventsQueryRefs(item)
		if refs.DeliveryEdgeID != runtimeQueryID(5000) || refs.ConsumerID != runtimeQueryID(30) || refs.EmitsEdgeID == "" {
			t.Fatal("unrelated delivery or missing-route boundary entered selected join", refs)
		}
	}
	if bound := 20 * (len(s.Edges) + len(s.Nodes) + len(s.Evidence)); ctx.checks > bound {
		t.Fatalf("selected consumer inspected excluded pairs: checks=%d bound=%d", ctx.checks, bound)
	}

}

func TestEventsQueryControlWitnessAlternateVerifiedPath(t *testing.T) {
	for _, firstProof := range []string{"stale", "unresolved", "inferred"} {
		t.Run(firstProof, func(t *testing.T) {
			s := eventsQueryState(t)
			s.Nodes[1].Attributes["entryStepId"] = runtimeQueryAttrs(t, map[string]any{"entryStepId": runtimeQueryID(20)})["entryStepId"]
			runtimeQueryAddNode(t, s, 20, "flow_step", 2, map[string]any{"stepKind": "transform"})
			runtimeQueryAddNode(t, s, 21, "flow_step", 2, map[string]any{"stepKind": "transform"})
			runtimeQueryAddEdge(t, s, 200, "next", 20, 4, nil)
			if firstProof == "stale" {
				s.Edges[len(s.Edges)-1].Freshness = &AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
			} else {
				s.Evidence[len(s.Evidence)-1].Status = firstProof
			}
			runtimeQueryAddEdge(t, s, 201, "next", 20, 21, nil)
			runtimeQueryAddEdge(t, s, 202, "next", 21, 4, nil)
			runtimeQueryAddEdge(t, s, 203, "next", 21, 20, nil) // Current cycle remains finite.
			p := eventsQueryPage(t, s, EventsQueryInput{View: "routes"})
			w := p.Items[0].Route.EmitContext.ControlWitness
			if w == nil {
				t.Fatal("earlier unverified branch suppressed available explicit control path")
			}
			if w.Status != "explicit" || !slices.Equal(w.EdgeIDs, []string{runtimeQueryID(201), runtimeQueryID(202)}) || slices.Contains(w.EvidenceIDs, runtimeQueryID(2200)) || !slices.Contains(w.NodeIDs, runtimeQueryID(21)) {
				t.Fatal("alternate control witness includes stale/unresolved proof", w)
			}
			if p.Truncated || !p.Complete {
				t.Fatal("alternate current path consumed a projection budget", p)
			}
		})
	}
}
