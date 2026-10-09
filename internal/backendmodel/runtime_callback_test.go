package backendmodel

import (
	"slices"
	"testing"
)

func TestCallbackArgumentIsSourceProvenanceNotInvocation(t *testing.T) {
	attrs := runtimeQueryAttrs(t, map[string]any{"argumentPosition": 0, "invocationKnowledge": "unknown", "reason": "External transaction manager"})
	if err := validateRuntimeAttributes("callback_argument", attrs, true, false); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{RuntimeProfile, EventsProfile, ComposedProfile} {
		if !slices.Contains(SupportedEdgeKindsForProfile(profile), "callback_argument") {
			t.Fatalf("callback not negotiated for %s", profile)
		}
	}
	from := Node{Kind: "flow_step", Attributes: runtimeQueryAttrs(t, map[string]any{"stepKind": "call"})}
	if valid, applies := runtimeEndpoints(Edge{Kind: "callback_argument"}, from, Node{Kind: "symbol"}); !valid || !applies {
		t.Fatal("literal callback endpoint rejected")
	}
	if valid, _ := runtimeEndpoints(Edge{Kind: "callback_argument"}, from, Node{Kind: "table"}); valid {
		t.Fatal("non-callable argument accepted")
	}
	for _, value := range []any{nil, -1, 1.5, "0"} {
		bad := runtimeQueryAttrs(t, map[string]any{"argumentPosition": value, "invocationKnowledge": "unknown", "reason": "Unknown invoker"})
		if validateRuntimeAttributes("callback_argument", bad, true, false) == nil {
			t.Fatal("invalid argument position accepted")
		}
	}
	s := runtimeQueryFixture(t)
	// The callback reuses a known body with a write absent from the ordinary
	// entrypoint path. Argument provenance must never make that write reachable.
	runtimeQueryAddNode(t, s, 30, "flow_step", 3, map[string]any{"stepKind": "call", "dispatchStatus": "unknown", "dispatchReason": "External invoker"})
	runtimeQueryAddNode(t, s, 31, "symbol", 0, nil)
	runtimeQueryAddNode(t, s, 32, "flow", 31, map[string]any{"entryStepId": runtimeQueryID(33)})
	runtimeQueryAddNode(t, s, 33, "flow_step", 32, map[string]any{"stepKind": "query"})
	runtimeQueryAddEdge(t, s, 130, "next", 4, 30, nil)
	runtimeQueryAddEdge(t, s, 131, "callback_argument", 30, 31, map[string]any{"argumentPosition": 0, "invocationKnowledge": "unknown", "reason": "External invoker"})
	runtimeQueryAddEdge(t, s, 132, "contains", 31, 32, nil)
	runtimeQueryAddEdge(t, s, 133, "contains", 32, 33, nil)
	runtimeQueryAddEdge(t, s, 134, "calls", 33, 9, nil)
	page := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	for _, access := range page.AccessItems {
		if slices.Contains(access.PathEdgeIDs, runtimeQueryID(131)) || slices.Contains(access.PathNodeIDs, runtimeQueryID(33)) {
			t.Fatal("argument provenance became an execution path")
		}
	}
	body := runtimeQueryPage(t, s, FlowQueryInput{View: "steps", FlowID: runtimeQueryID(32)})
	if len(body.StepItems) != 1 || body.StepItems[0].ID != runtimeQueryID(33) {
		t.Fatal("lexical callback body is not inspectable")
	}
}
