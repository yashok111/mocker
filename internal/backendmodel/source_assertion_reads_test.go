package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func TestSourceAssertionsPinsFiltersAndDetachedPages(t *testing.T) {
	t.Parallel()
	graph := &SourceGraphSnapshot{State: RevisionState{Revision: Revision{ProjectID: "project", ID: "revision"}}, SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}, Assertions: []ProviderAssertion{
		{RecordType: "node", RecordID: "b", Owner: AssertionOwnership{RepositoryID: "r2", ProviderNamespace: "p"}, AssertionHash: "h2", Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "B"}},
		{RecordType: "node", RecordID: "a", Owner: AssertionOwnership{RepositoryID: "r1", ProviderNamespace: "p"}, AssertionHash: "h1", Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "A", Attributes: map[string]jsontext.Value{"description": jsontext.Value(`"original"`)}}},
	}}
	pin := SourceReadPin{ProjectID: "project", BaseRevisionID: "revision", TargetHash: "target", EffectiveSemanticHash: "semantic"}
	pin.SourceVectorHash, _ = requestDigest(graph.SourceVector)
	first, err := QuerySourceAssertions(t.Context(), graph, pin, SourceAssertionsQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Assertion.RecordID != "a" || first.NextCursor == "" {
		t.Fatalf("bad first page: %+v", first)
	}
	second, err := QuerySourceAssertions(t.Context(), graph, pin, SourceAssertionsQuery{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].Assertion.RecordID != "b" {
		t.Fatalf("bad second: %+v %v", second, err)
	}
	wrong := pin
	wrong.SourceVectorHash = "wrong"
	if _, err := QuerySourceAssertions(t.Context(), graph, wrong, SourceAssertionsQuery{}); err == nil {
		t.Fatal("accepted wrong vector pin")
	}
	changed := pin
	changed.TargetHash = "new"
	if _, err := QuerySourceAssertions(t.Context(), graph, changed, SourceAssertionsQuery{Cursor: first.NextCursor}); err == nil {
		t.Fatal("accepted changed target")
	}
	if _, err := QuerySourceAssertions(t.Context(), graph, pin, SourceAssertionsQuery{RepositoryID: "r1", Cursor: first.NextCursor}); err == nil {
		t.Fatal("accepted changed filter")
	}
	filtered, err := QuerySourceAssertions(t.Context(), graph, pin, SourceAssertionsQuery{RecordType: "node", ID: "b", RepositoryID: "r2", ProviderNamespace: "p"})
	if err != nil || len(filtered.Items) != 1 {
		t.Fatalf("filter: %+v %v", filtered, err)
	}
	first.Items[0].Assertion.Payload.Attributes["description"][1] = 'x'
	if string(graph.Assertions[1].Payload.Attributes["description"]) != `"original"` {
		t.Fatal("read page aliases source payload bytes")
	}
	for _, bad := range []SourceAssertionsQuery{{Limit: -1}, {Limit: 501}, {RecordType: "evidence"}, {Cursor: "!!!!"}} {
		if _, err := QuerySourceAssertions(t.Context(), graph, pin, bad); err == nil {
			t.Fatalf("invalid query admitted: %+v", bad)
		}
	}
	first.Items[0].Assertion.Payload.Name = "changed"
	if graph.Assertions[1].Payload.Name != "A" {
		t.Fatal("read mutated source")
	}
	if _, err := QuerySourceAssertions(t.Context(), &SourceGraphSnapshot{}, pin, SourceAssertionsQuery{}); err == nil {
		t.Fatal("legacy assertion scope admitted")
	}
}
