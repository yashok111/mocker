package backendmodel

import "testing"

func TestWorkspaceFiltersServiceAndUnknown(t *testing.T) {
	service := "01900000-0000-7000-8000-000000000001"
	module := "01900000-0000-7000-8000-000000000002"
	node := "01900000-0000-7000-8000-000000000003"
	graph := &EffectiveGraphSnapshot{State: RevisionState{Nodes: []Node{{ID: service, Kind: "service"}, {ID: module, Kind: "module", ParentID: &service}, {ID: node, Kind: "symbol", ParentID: &module}, {ID: "other", Kind: "symbol"}}}}
	for _, tc := range []struct {
		id   string
		want bool
	}{{node, true}, {service, true}, {"other", false}} {
		got, e := workspaceMatches(t.Context(), graph, GraphQueryInput{ServiceID: service}, "node", tc.id)
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	got, e := workspaceMatches(t.Context(), graph, GraphQueryInput{Certainty: "explicit"}, "node", node)
	if e != nil || got {
		t.Fatal("unknown source promoted", got, e)
	}
	if e := validateEffectiveGraphSelectors(GraphQueryInput{RecordType: "nodes", ID: node, ServiceID: service}); e == nil {
		t.Fatal("mixed exact id and filter accepted")
	}
	if e := validateEffectiveGraphSelectors(GraphQueryInput{RecordType: "nodes", Certainty: "confirmed"}); e == nil {
		t.Fatal("unsupported certainty accepted")
	}
}
