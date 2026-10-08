package backendmodel

import (
	"reflect"
	"testing"
)

// The existing authored proposal layer is the small-transfer localization path:
// it pins a source baseline and carries name deltas, rather than reimporting proof.
func TestPresentationRenameKeepsSourceNativeDataAndTopology(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveFiveRelationalFixture(t)
	target := BackendReadTarget{RevisionID: base.Revision.ID}
	before, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	id := before.State.Nodes[0].ID
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Presentation", BaseRevisionID: base.Revision.ID, IdempotencyKey: "presentation"})
	if err != nil {
		t.Fatal(err)
	}
	command := changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": id, "name": "Понятное название"})
	command.Reason = "Authored Russian presentation; original source and native text retained"
	next, _ := saveChange(t, r, draft, "presentation-label", command)
	desired, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(desired.State.Nodes) != len(before.State.Nodes) || len(desired.State.Edges) != len(before.State.Edges) {
		t.Fatal("presentation changed topology")
	}
	for i, n := range before.State.Nodes {
		got := desired.State.Nodes[i]
		if n.ID != got.ID || n.Kind != got.Kind || !reflect.DeepEqual(n.ParentID, got.ParentID) || !reflect.DeepEqual(n.Attributes, got.Attributes) {
			t.Fatal("presentation changed native data or source identity")
		}
		if n.ID == id && got.Name != "Понятное название" {
			t.Fatal("authored label absent")
		}
	}
	after, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.State, after.State) || !reflect.DeepEqual(before.Pins, after.Pins) {
		t.Fatal("source history was rewritten by presentation")
	}
	if len(desired.State.Evidence) != len(before.State.Evidence) {
		t.Fatal("retained proof lost")
	}
}
