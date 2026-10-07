package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

// Review 2026-10-06, F61 (the set half, decided by the owner): a set that
// restates exactly the current pin and bindings previewed as applicable with
// only "unchanged" diff rows, and Apply wrote a new revision identical to its
// base and bumped the project version. It is now an idempotent no-op: Apply
// answers with the current head and version, and a replay of the key returns
// the same bytes.
func TestArtifactPinSetRestatingCurrentPinIsNoop(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, "first"})
	if err != nil {
		t.Fatal(err)
	}
	again := in
	again.BaseRevisionID, again.ExpectedVersion = first.Revision.ID, first.Project.Version
	restated, err := s.Preview(t.Context(), base.Project.ID, again)
	if err != nil {
		t.Fatal(err)
	}
	if !restated.CanApply || restated.CandidateHash == "" {
		t.Fatalf("restated pin is not applicable: %+v", restated.Diagnostics)
	}
	apply := ApplyArtifactPinsInput{again.BaseRevisionID, again.ExpectedVersion, again.Commands, restated.CandidateHash, "second"}
	second, err := s.Apply(t.Context(), base.Project.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision.ID != first.Revision.ID || second.Project.Version != first.Project.Version || second.Project.CurrentRevisionID != first.Revision.ID {
		t.Fatalf("restated pin wrote revision %s version %d (head %s version %d)", second.Revision.ID, second.Project.Version, first.Revision.ID, first.Project.Version)
	}
	replay, err := s.Apply(t.Context(), base.Project.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(second)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("no-op replay changed bytes")
	}
	current, err := s.repo.Get(t.Context(), base.Project.ID)
	if err != nil || current.Version != first.Project.Version {
		t.Fatalf("project moved: %+v %v", current, err)
	}
}

// Review 2026-10-06, F44 (the v3 half): the dangling-binding check walked the
// legacy artifact context only. A draft on a namespaced (v3) context kept its
// bindings in ArtifactContextV3.Groups, so remove_node there still left an
// API or editor binding on a missing node that Preview admitted.
func TestChangeArtifactBindingSourcesCoverNamespacedGroups(t *testing.T) {
	t.Parallel()
	live := ChangeCreatedRecord{ChangeRecordRef: ChangeRecordRef{RecordType: "node", ID: "live"}, Payload: SourceAssertionPayload{Kind: "http_operation"}}
	for name, group := range map[string]ArtifactNamespaceGroup{
		"api":    {APIBindings: []APIArtifactBinding{{SourceNodeID: "gone", SourceKind: "http_operation"}}},
		"editor": {EditorBindings: []EditorBinding{{SourceNodeIDs: []string{"live", "gone"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := &changeEvaluation{records: map[string]ChangeCreatedRecord{"live": live}}
			e.revision.ArtifactContextV3 = &ArtifactContextV3{Groups: []ArtifactNamespaceGroup{{}, group}}
			if err := validateChangeArtifactBindingSources(e); err == nil {
				t.Fatal("namespaced binding on a removed node was admitted")
			}
		})
	}
	e := &changeEvaluation{records: map[string]ChangeCreatedRecord{"live": live}}
	e.revision.ArtifactContextV3 = &ArtifactContextV3{Groups: []ArtifactNamespaceGroup{{
		APIBindings:    []APIArtifactBinding{{SourceNodeID: "live", SourceKind: "http_operation"}},
		EditorBindings: []EditorBinding{{SourceNodeIDs: []string{"live"}}},
	}}}
	if err := validateChangeArtifactBindingSources(e); err != nil {
		t.Fatalf("live namespaced bindings refused: %v", err)
	}
}
