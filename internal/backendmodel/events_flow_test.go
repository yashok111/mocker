package backendmodel

import (
	"encoding/json/jsontext"
	"slices"
	"testing"
)

func eventsRuntimeState(t *testing.T) *RevisionState {
	s := runtimeQueryFixture(t)
	s.Revision.SchemaVersion = "5"
	s.Sources[0].Provider.Profiles = append(s.Sources[0].Provider.Profiles, LineageProfile, "events-service-v1")
	return s
}
func TestEventsFlowEntrypointsAndRemoteWitness(t *testing.T) {
	for _, kind := range []string{"consumer", "job"} {
		t.Run(kind, func(t *testing.T) {
			s := eventsRuntimeState(t)
			s.Nodes[0].Kind = kind
			p := runtimeQueryPage(t, s, FlowQueryInput{View: "entrypoints"})
			if len(p.EntryPointItems) != 1 || len(p.EntryPointItems[0].FlowIDs) != 1 {
				t.Fatal("event entrypoint missing", p)
			}
			// Exact explicit call points to a remote operation; its handles edge is part of the witness.
			s.Nodes[3].Attributes["stepKind"] = jsontext.Value(`"call"`)
			runtimeQueryAddNode(t, s, 20, "http_operation", 0, nil)
			runtimeQueryAddEdge(t, s, 120, "calls", 4, 20, nil)
			runtimeQueryAddEdge(t, s, 121, "handles", 20, 2, nil)
			s.Edges = slices.DeleteFunc(s.Edges, func(e Edge) bool { return e.ID == runtimeQueryID(110) })
			// Remote target reuses this pinned handler flow; a cycle must terminate and still expose the access.
			runtimeQueryAddEdge(t, s, 122, "next", 4, 5, nil)
			p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
			if len(p.AccessItems) != 1 {
				t.Fatal("consumer/job access missing", p)
			}
			// Distinct remote flow reaches a second query access only through the explicit operation.
			runtimeQueryAddNode(t, s, 21, "handler", 0, nil)
			runtimeQueryAddNode(t, s, 22, "flow", 21, map[string]any{"entryStepId": runtimeQueryID(23)})
			runtimeQueryAddNode(t, s, 23, "flow_step", 22, nil)
			s.Edges = slices.DeleteFunc(s.Edges, func(e Edge) bool { return e.ID == runtimeQueryID(121) })
			runtimeQueryAddEdge(t, s, 121, "handles", 20, 21, nil)
			runtimeQueryAddEdge(t, s, 123, "contains", 21, 22, nil)
			runtimeQueryAddEdge(t, s, 124, "contains", 22, 23, nil)
			runtimeQueryAddEdge(t, s, 125, "calls", 23, 9, nil)
			p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
			var remote *FlowAccessItem
			for i := range p.AccessItems {
				if p.AccessItems[i].AccessEdgeID == runtimeQueryID(115) {
					remote = &p.AccessItems[i]
				}
			}
			want := make([]string, 0, 9)
			for _, id := range []int{100, 101, 102, 120, 121, 123, 124, 125, 115} {
				want = append(want, runtimeQueryID(id))
			}
			if remote == nil || !slices.Equal(remote.PathEdgeIDs, want) {
				t.Fatal("remote exact witness missing", p.AccessItems)
			}
		})
	}
}
func TestEventsFlowPolicySeparatesCursors(t *testing.T) {
	s := runtimeQueryFixture(t)
	s.Revision.SchemaVersion = "4"
	s.Sources[0].Provider.Profiles = append(s.Sources[0].Provider.Profiles, LineageProfile)
	old := runtimeQueryPage(t, s, FlowQueryInput{View: "steps", FlowID: runtimeQueryID(3), Limit: 1})
	s.Revision.SchemaVersion = "5"
	s.Sources[0].Provider.Profiles = append(s.Sources[0].Provider.Profiles, "events-service-v1")
	_, err := projectRuntimeFlow(t.Context(), s, FlowQueryInput{RevisionID: s.Revision.ID, View: "steps", FlowID: runtimeQueryID(3), Cursor: old.NextCursor})
	if err == nil {
		t.Fatal("source4 cursor entered source5 traversal")
	}
}
func TestEventsFlowEmitTransitionAndExternalBoundary(t *testing.T) {
	s := eventsRuntimeState(t)
	s.Nodes[3].Attributes["stepKind"] = jsontext.Value(`"emit"`)
	runtimeQueryAddNode(t, s, 30, "message", 0, nil)
	runtimeQueryAddEdge(t, s, 130, "emits", 4, 30, map[string]any{"channelId": runtimeQueryID(31), "deliveryStatus": "declared"})
	p := runtimeQueryPage(t, s, FlowQueryInput{View: "transitions", FlowID: runtimeQueryID(3)})
	if !slices.ContainsFunc(p.TransitionItems, func(e Edge) bool { return e.ID == runtimeQueryID(130) }) {
		t.Fatal("emit source navigation missing")
	}
	runtimeQueryAddNode(t, s, 32, "external_system", 0, nil)
	runtimeQueryAddEdge(t, s, 131, "calls", 4, 32, nil)
	p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if !slices.ContainsFunc(p.Limitations, func(s string) bool { return s == "Unknown source reachability boundary "+runtimeQueryID(131) }) {
		t.Fatal("external boundary concealed")
	}
}
