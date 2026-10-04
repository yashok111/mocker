package backendmodel

import (
	"encoding/json/v2"
	"testing"
	"uuid"
)

func TestChangeProposalTwoProvidersRetainIndependentIntendedKeys(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "qualified")
	firstSession, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, firstSession.RepositoryID)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	claim := ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same record", EvidenceKeys: []string{"proof"}}}
	commands := append([]ImportCommand{claim}, fixtureCommands(s)...)
	b := sendCommands(t, r, p, s, 1, "shared", commands...)
	base := commitStaged(t, r, p, s, b.AcceptedVersion, "shared-commit")
	d, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Qualified desired", BaseRevisionID: base.Revision.ID, IdempotencyKey: "qualified"})
	if err != nil {
		t.Fatal(err)
	}
	initial := d
	source, err := r.ResolveSourceGraph(t.Context(), p.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Identities) != 2 || source.Identities[0].ExternalKey != source.Identities[1].ExternalKey {
		t.Fatal("fixture must share literal key across providers")
	}
	a, bIdentity := source.Identities[0], source.Identities[1]
	before := immutableBytes(t, r)
	c := changeMapCommand(t, "map_identity", map[string]any{"target": map[string]any{"kind": "source_identity", "source": a}, "expectedExternalKey": a.ExternalKey, "newExternalKey": "desired-a"})
	d, _ = saveChange(t, r, d, "rename-a", c)
	for _, identity := range changeReadSnapshot(t, r, d).Identities {
		if identity.Target.Source == nil {
			continue
		}
		if identity.Target.Source.ProviderNamespace == a.ProviderNamespace && *identity.ExternalKey != "desired-a" {
			t.Fatal("selected provider unchanged")
		}
		if identity.Target.Source.ProviderNamespace == bIdentity.ProviderNamespace && *identity.ExternalKey != bIdentity.ExternalKey {
			t.Fatal("other provider key overwritten")
		}
	}
	historical, err := r.GetChangeProposal(t.Context(), p.ID, d.Proposal.ID, GetChangeProposalInput{ProposalRevisionID: initial.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(initial.Revision)
	now, _ := json.Marshal(historical.Revision)
	if string(old) != string(now) {
		t.Fatal("historical draft changed")
	}
	for key, value := range before {
		if immutableBytes(t, r)[key] != value {
			t.Fatalf("source bytes changed %s", key)
		}
	}
}
