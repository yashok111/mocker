package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func TestEventsNestedReferencesAndEndpoints(t *testing.T) {
	attrs := runtimeAttrs(t, map[string]any{"channelKey": "same", "deliveryStatus": "declared"})
	out, err := resolveEventsAttributes("emits", attrs, true, func(typ, key, path string) string {
		if typ != "node" || key != "same" {
			t.Fatal(typ, key)
		}
		return "11111111-1111-4111-8111-111111111111"
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtimeString(out["channelId"]) != "11111111-1111-4111-8111-111111111111" || out["channelKey"] != nil {
		t.Fatal(out)
	}
	refs, err := sourceAttributeReferences("emits", out, true, true)
	if err != nil || len(refs) != 1 || refs[0].Kind != "channel" {
		t.Fatal(refs, err)
	}
	for _, tc := range []struct {
		edge, from, to string
		valid          bool
	}{
		{"emits", "flow_step", "message", true}, {"emits", "consumer", "message", false},
		{"delivered_to", "channel", "consumer", true}, {"retries", "consumer", "channel", true},
		{"handles", "job", "handler", true}, {"handles", "consumer", "symbol", false},
		{"calls", "flow_step", "http_operation", true},
	} {
		from := Node{Kind: tc.from, Attributes: map[string]jsontext.Value{"stepKind": jsontext.Value(`"emit"`)}}
		if tc.edge == "calls" {
			from.Attributes["stepKind"] = jsontext.Value(`"call"`)
		}
		got, _ := eventsEndpoints(Edge{Kind: tc.edge}, from, Node{Kind: tc.to})
		if got != tc.valid {
			t.Fatal(tc)
		}
	}
}
func TestEventsGraphRejectsHiddenDispatchAndMissingProof(t *testing.T) {
	s := &ImportSession{Profile: "events-service-v1", RepositoryID: "repo"}
	g := &graphCandidate{Nodes: []Node{{ID: "consumer", Kind: "consumer", ParentID: new("service"), Attributes: eventNodeAttrs(t, "consumer")}, {ID: "service", Kind: "service"}}, Edges: []Edge{{ID: "contains", Kind: "contains", From: "service", To: "consumer"}}}
	var d []ImportDiagnostic
	if err := validateEventsGraph(t.Context(), s, g, &d); err != nil {
		t.Fatal(err)
	}
	if len(d) < 2 {
		t.Fatal("missing parent/proof/dispatch checks", d)
	}
}
func TestEventsReferenceRecordIdentity(t *testing.T) {
	refs := []relationalReference{{Kind: "message", ID: "same"}, {Kind: "emits", ID: "same", RecordType: "edge"}, {Kind: "evidence", ID: "proof"}, {Kind: "message", ID: "historic", HistoricalRevisionID: "old"}}
	if !activeRecordReferenceTo(refs, "node", "same") || !activeRecordReferenceTo(refs, "edge", "same") {
		t.Fatal("record references were conflated")
	}
	if activeRecordReferenceTo(refs, "edge", "proof") || activeRecordReferenceTo(refs, "node", "historic") {
		t.Fatal("proof/history became an active dependency")
	}
	if activeRecordReferenceTo(refs[:1], "edge", "same") {
		t.Fatal("legacy node ref protects unrelated edge")
	}
}
func TestEventsDispatchHandlerCapAndRemainder(t *testing.T) {
	for _, tc := range []struct {
		name             string
		known, remainder int
		complete, valid  bool
	}{
		{"fifty complete", 50, 0, true, true}, {"fifty one complete", 51, 0, true, false}, {"fifty partial", 50, 1, false, true}, {"two remainders", 1, 2, false, false}, {"missing remainder", 1, 0, false, false}, {"complete hides remainder", 1, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attrs := eventNodeAttrs(t, "consumer")
			if !tc.complete {
				attrs["analysisStatus"] = relationalRaw(t, "partial")
				attrs["gaps"] = relationalRaw(t, []string{"Unknown dispatcher"})
				attrs["dispatchStatus"] = relationalRaw(t, "partial")
				attrs["dispatchReason"] = relationalRaw(t, "Unknown dispatcher")
			}
			g := &graphCandidate{Nodes: []Node{{ID: "consumer", Kind: "consumer", Attributes: attrs}}}
			for i := range tc.known {
				key := runtimeQueryID(i + 1)
				g.Nodes = append(g.Nodes, Node{ID: key, Kind: "handler"})
				g.Edges = append(g.Edges, Edge{ID: runtimeQueryID(i + 100), Kind: "handles", From: "consumer", To: key})
			}
			for i := range tc.remainder {
				key := runtimeQueryID(i + 1000)
				g.Nodes = append(g.Nodes, Node{ID: key, Kind: "unresolved_target", Attributes: runtimeAttrs(t, map[string]any{"expectedKind": "handler"})})
				g.Edges = append(g.Edges, Edge{ID: runtimeQueryID(i + 1100), Kind: "handles", From: "consumer", To: key})
			}
			var d []ImportDiagnostic
			if err := validateEventsGraph(t.Context(), &ImportSession{RepositoryID: "repo"}, g, &d); err != nil {
				t.Fatal(err)
			}
			bad := false
			for _, item := range d {
				if item.Path == "nodes/consumer/attributes/dispatchStatus" {
					bad = true
				}
			}
			if bad == tc.valid {
				t.Fatal(tc.name, d)
			}
		})
	}
}
func TestEventsFieldCapAndSelectorIdentity(t *testing.T) {
	for _, count := range []int{500, 501} {
		t.Run(string(rune('a'+count-500)), func(t *testing.T) {
			g := &graphCandidate{Nodes: []Node{{ID: "message", Kind: "message", Attributes: eventNodeAttrs(t, "message")}}}
			for i := range count {
				a := eventNodeAttrs(t, "event_field")
				a["path"] = relationalRaw(t, []any{map[string]any{"property": runtimeQueryID(i + 1)}})
				g.Nodes = append(g.Nodes, Node{ID: runtimeQueryID(i + 1000), Kind: "event_field", ParentID: new("message"), Attributes: a})
			}
			var d []ImportDiagnostic
			err := validateEventsGraph(t.Context(), &ImportSession{}, g, &d)
			if (err != nil) != (count == 501) {
				t.Fatal(count, err)
			}
		})
	}
}
func TestEventsUpgradeReclassifiesEmitOwnership(t *testing.T) {
	old := &AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "provider", Profile: RuntimeProfile}
	session := &ImportSession{Profile: EventsProfile, RepositoryID: "repo"}
	next := relationalOwnership("flow_step", eventNodeAttrs(t, "flow_step"), false, session, old)
	if next.Profile != EventsProfile || old.Profile != RuntimeProfile {
		t.Fatal("reclassification mutated old owner", old, next)
	}
	inherited := relationalOwnership("field_mapping", lineageMappingAttrs(t), false, session, &AssertionOwnership{Profile: LineageProfile})
	if inherited.Profile != LineageProfile {
		t.Fatal("ordinary lineage mapping owner changed")
	}
	g := &graphCandidate{Nodes: []Node{{ID: "emit", Kind: "flow_step", Attributes: eventNodeAttrs(t, "flow_step")}, {ID: "flow", Kind: "flow"}}, Edges: []Edge{{ID: "contains", Kind: "contains", From: "flow", To: "emit"}}, Evidence: []Evidence{{ID: "proof", SubjectID: "contains"}}}
	base := &RevisionState{Edges: []Edge{{ID: "contains", Ownership: old}}}
	assignRelationalOwnership(base, g, session)
	if g.Edges[0].Ownership.Profile != EventsProfile || g.Evidence[0].Ownership.Profile != EventsProfile || old.Profile != RuntimeProfile {
		t.Fatal("edge/proof ownership drift")
	}
}
