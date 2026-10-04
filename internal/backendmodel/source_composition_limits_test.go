package backendmodel

import (
	"fmt"
	"testing"
)

func commitEmptySource6(t *testing.T, r *Repo, p *Project, in BeginImportInput) (*ImportSession, *ImportCommitResult) {
	t.Helper()
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: s.Version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("empty partition: %+v %v", preview, err)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	return s, out
}

func TestSource6RepositoryAndProviderLimitsIncludeEmptyPartitions(t *testing.T) {
	for _, mode := range []string{"repositories", "providers"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, mode)
			maximum := MaxSourceRepositories
			if mode == "providers" {
				maximum = MaxSourceProviders
			}
			repository := ""
			for i := range maximum {
				in := source6Input(t, p)
				in.IdempotencyKey = fmt.Sprintf("partition-%d", i)
				if mode == "repositories" {
					in.Manifest.RepositoryName = fmt.Sprintf("repository-%d", i)
				} else if i > 0 {
					in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: repository}
					in.Manifest.Provider.Namespace = fmt.Sprintf("provider-%d", i)
				}
				s, out := commitEmptySource6(t, r, p, in)
				repository = s.RepositoryID
				p = &out.Project
			}
			graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
			if err != nil || len(graph.SourceVector.Partitions) != maximum || len(graph.SourceVector.Snapshots) != maximum || len(graph.Assertions) != 0 {
				t.Fatalf("empty active source vector lost: %+v %v", graph, err)
			}
			in := source6Input(t, p)
			in.IdempotencyKey = "overflow"
			if mode == "repositories" {
				in.Manifest.RepositoryName = "overflow"
			} else {
				in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: repository}
				in.Manifest.Provider.Namespace = "overflow"
			}
			_, err = r.BeginImport(t.Context(), p.ID, in)
			assertFault(t, err, "backend_import_limit")
		})
	}
}

func TestSource6CommitProjectCASAndReceiptReplay(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "cas")
	sessions := make([]*ImportSession, 0, 2)
	previews := make([]*ImportPreview, 0, 2)
	for _, name := range []string{"a", "b"} {
		in := source6Input(t, p)
		in.Manifest.RepositoryName = name
		in.IdempotencyKey = name
		s, err := r.BeginImport(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		b := putFixture(t, r, p, s)
		v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
		previews = append(previews, v)
	}
	in := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: previews[0].Version, CandidateHash: *previews[0].CandidateHash, IdempotencyKey: "winner"}
	winner, err := r.CommitImport(t.Context(), p.ID, sessions[0].ID, in)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.CommitImport(t.Context(), p.ID, sessions[1].ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: previews[1].Version, CandidateHash: *previews[1].CandidateHash, IdempotencyKey: "loser"})
	assertFault(t, err, "backend_version_conflict")
	next := source6Input(t, &winner.Project)
	next.Manifest.RepositoryName = "newer"
	next.IdempotencyKey = "newer"
	commitEmptySource6(t, r, &winner.Project, next)
	replayed, err := r.CommitImport(t.Context(), p.ID, sessions[0].ID, in)
	if err != nil || replayed.Revision.ID != winner.Revision.ID {
		t.Fatalf("winner receipt failed after newer head: %+v %v", replayed, err)
	}
}
