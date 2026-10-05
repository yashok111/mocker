package backendmodel

import (
	"testing"
	"uuid"
)

func TestSource6LastClaimDeletion(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "delete")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, a.RepositoryID)
	b, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := append([]ImportCommand{{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "shared object", EvidenceKeys: []string{"proof"}}}}, fixtureCommands(b)...)
	batch := sendCommands(t, r, p, b, 1, "share", commands...)
	shared := commitStaged(t, r, p, b, batch.AcceptedVersion, "share")
	p = &shared.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range graph.Assertions {
		if claim.Owner.ProviderNamespace == "provider-b" {
			target = sourceAssertionRef(claim)
		}
	}
	third := source6Input(t, p)
	third.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: a.RepositoryID}
	third.Manifest.Provider.Namespace = "provider-c"
	third.IdempotencyKey = "caller"
	c, err := r.BeginImport(t.Context(), p.ID, third)
	if err != nil {
		t.Fatal(err)
	}
	commands = fixtureCommands(c)
	commands[0].Node.ExternalKey = "caller"
	commands[1].Evidence.SubjectKey = "caller"
	edgeProof := *commands[1].Evidence
	edgeProof.ExternalKey = "edge-proof"
	edgeProof.SubjectType = "edge"
	edgeProof.SubjectKey = "calls"
	commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "calls", Kind: "calls", FromRef: &ImportRecordRef{LocalKey: "caller"}, ToRef: &ImportRecordRef{Base: &target}, Attributes: commands[0].Node.Attributes, EvidenceKeys: []string{"edge-proof"}}}, ImportCommand{Op: "upsert_evidence", Evidence: &edgeProof})
	batch = sendCommands(t, r, p, c, 1, "caller", commands...)
	withCaller := commitStaged(t, r, p, c, batch.AcceptedVersion, "caller")
	p = &withCaller.Project
	for _, provider := range []string{"fixture", "provider-b"} {
		next := source6Input(t, p)
		next.Manifest.Provider.Namespace = provider
		next.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: a.RepositoryID, ProviderNamespace: provider}
		next.IdempotencyKey = "delete-" + provider
		s, err := r.BeginImport(t.Context(), p.ID, next)
		if err != nil {
			t.Fatal(err)
		}
		batch := sendCommands(t, r, p, s, 1, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: target.ExpectedID, Reason: "explicit deletion"}})
		preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
		if err != nil {
			t.Fatal(err)
		}
		if provider == "provider-b" {
			if preview.State != "needs_resolution" {
				t.Fatal("last claim deleted despite live foreign-provider caller")
			}
			continue
		}
		if preview.State != "ready" {
			t.Fatalf("another provider should preserve record: %+v", preview)
		}
		out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "delete-a"})
		if err != nil {
			t.Fatal(err)
		}
		p = &out.Project
		after, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(SourceIdentities(after, "node", target.ExpectedID)) != 1 || len(after.SourceVector.Partitions) != 3 {
			t.Fatal("owner deletion removed surviving identity or empty active partition")
		}
	}
}
