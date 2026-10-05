package backendmodel

import "testing"

func TestSource6RenamePreservesStableKind(t *testing.T) {
	for _, kind := range []string{"handler", "symbol"} {
		t.Run(kind, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "kind")
			old, base := commitSource6Fixture(t, r, p, source6Input(t, p))
			p = &base.Project
			graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			id := graph.Assertions[0].RecordID
			in := source6Input(t, p)
			in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: old.RepositoryID, ProviderNamespace: old.Manifest.Provider.Namespace}
			in.IdempotencyKey = "rename"
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			b := sendCommands(t, r, p, s, 1, "map", ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "renamed", ExpectedID: id, Reason: "rename stable identity", EvidenceKeys: []string{"proof"}}})
			commands := fixtureCommands(s)
			commands[0].Node.ExternalKey = "renamed"
			commands[0].Node.Kind = kind
			commands[1].Evidence.SubjectKey = "renamed"
			b = sendCommands(t, r, p, s, b.AcceptedVersion, "subject", commands...)
			preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if kind != "handler" {
				if err == nil && preview.State == "ready" {
					t.Fatal("map_identity changed stable UUID kind")
				}
				current, readErr := r.Get(t.Context(), p.ID)
				if readErr != nil || current.CurrentRevisionID != base.Revision.ID {
					t.Fatal("invalid rename published a revision")
				}
				return
			}
			if err != nil || preview.State != "ready" {
				t.Fatalf("same-kind rename rejected: %+v %v", preview, err)
			}
			out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "rename"})
			if err != nil {
				t.Fatal(err)
			}
			next, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
			if err != nil || len(next.Assertions) != 1 || next.Assertions[0].RecordID != id || next.Assertions[0].ExternalKey != "renamed" || next.Assertions[0].Payload.Kind != "handler" {
				t.Fatalf("rename lost identity: %+v %v", next, err)
			}
		})
	}
}

func TestSource6RenameRejectsSourceAndTargetUpserts(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "two-keys")
	old, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	in := source6Input(t, p)
	in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: old.RepositoryID, ProviderNamespace: old.Manifest.Provider.Namespace}
	in.IdempotencyKey = "two-keys"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, 1, "map", ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "renamed", ExpectedID: graph.Assertions[0].RecordID, Reason: "rename", EvidenceKeys: []string{"proof"}}})
	commands := fixtureCommands(s)
	copyNode := *commands[0].Node
	copyNode.ExternalKey = "renamed"
	commands = append(commands, ImportCommand{Op: "upsert_node", Node: &copyNode})
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "both", commands...)
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err == nil && preview.State == "ready" {
		t.Fatal("mapped source and target both survived under one UUID")
	}
}
