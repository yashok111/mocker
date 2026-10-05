package backendmodel

import (
	"testing"
	"uuid"
)

func TestChangeProposalFieldCriteriaValidateProposalSelectors(t *testing.T) {
	t.Parallel()
	r, _, d := changeFixture(t)
	source, err := r.ResolveSourceGraph(t.Context(), d.Proposal.ProjectID, d.Revision.BaseRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	identity := source.Identities[0]
	for _, tt := range []struct {
		name     string
		selector map[string]any
		expected any
	}{
		{"node cannot have edge display name", map[string]any{"kind": "edge_name"}, "label"},
		{"imported identity cannot use intent tag", map[string]any{"kind": "intent_identity", "recordType": "node", "id": identity.ID}, "key"},
		{"unknown source provider", map[string]any{"kind": "source_identity", "recordType": "node", "id": identity.ID, "repositoryId": identity.RepositoryID, "providerNamespace": "unknown"}, "key"},
		{"source expected key cannot be null", map[string]any{"kind": "source_identity", "recordType": "node", "id": identity.ID, "repositoryId": identity.RepositoryID, "providerNamespace": identity.ProviderNamespace}, nil},
		{"source expected key must be string", map[string]any{"kind": "source_identity", "recordType": "node", "id": identity.ID, "repositoryId": identity.RepositoryID, "providerNamespace": identity.ProviderNamespace}, 42},
		{"selector must address outer subject", map[string]any{"kind": "source_identity", "recordType": "node", "id": uuid.NewV7().String(), "repositoryId": identity.RepositoryID, "providerNamespace": identity.ProviderNamespace}, "key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "requirement", "kind": "field_equals", "required": true, "description": "Typed desired value", "recordType": "node", "id": identity.ID, "selector": tt.selector, "expected": map[string]any{"present": true, "value": tt.expected}}}})
			v, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}})
			if err != nil {
				t.Fatal(err)
			}
			if v.CandidateHash != nil {
				t.Fatal("ill-typed proposal property criterion accepted")
			}
		})
	}
	created := uuid.NewV7().String()
	create := changeCreateNode(t, created, "service", nil, map[string]any{})
	valid := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "unassigned", "kind": "field_equals", "required": true, "description": "Created identity is unassigned", "recordType": "node", "id": created, "selector": map[string]any{"kind": "intent_identity", "recordType": "node", "id": created}, "expected": map[string]any{"present": true, "value": nil}}}})
	_, _ = saveChange(t, r, d, "valid-intent-criterion", create, valid)
}
