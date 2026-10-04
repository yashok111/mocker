package backendmodel

import (
	"testing"
	"uuid"
)

func TestChangeProposalHistoricalMigrationRequiresOwnedAncestor(t *testing.T) {
	r, d, ids := changeRelationalFixture(t)
	migrationID := uuid.NewV7().String()
	facet := changeDesiredFacet(map[string]any{"order": known(1), "parentIds": []string{}, "definition": "opaque migration text", "derivationStatus": "complete", "changes": []any{map[string]any{"target": map[string]any{"kind": "historical", "revisionId": uuid.NewV7().String(), "objectId": uuid.NewV7().String()}, "operation": "drop", "description": "Removed historical table"}}})
	commands := []ChangeProposalCommand{changeCreateNode(t, migrationID, "migration", new(ids["database"]), changeFacets(facet)), changeContains(t, ids["database"], migrationID)}
	preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands})
	if err != nil {
		return
	}
	if preview.CandidateHash != nil {
		t.Fatal("unknown/foreign historical migration pin admitted")
	}
}
