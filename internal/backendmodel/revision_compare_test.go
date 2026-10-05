package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"slices"
	"testing"
)

func TestCompareStableIdentityAndSemanticFacets(t *testing.T) {
	before := RevisionState{Nodes: []Node{{ID: "one", ExternalKey: "old", Kind: "handler", Name: "Handle", Attributes: map[string]jsontext.Value{}}}}
	after := RevisionState{Nodes: []Node{{ID: "one", ExternalKey: "new", Kind: "handler", Name: "Renamed", Attributes: map[string]jsontext.Value{}}}}
	delta, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Summary.Nodes.Modified != 1 || delta.Summary.Nodes.Added != 0 || delta.Summary.Nodes.Removed != 0 || delta.Summary.IdentityMappings != 1 {
		t.Fatalf("mapped rename summary: %+v", delta.Summary)
	}
	if len(delta.Changes) != 2 || !slices.Contains(delta.Changes[1].ChangeKinds, "modified") {
		t.Fatalf("mapped rename changes: %+v", delta.Changes)
	}
	after.Nodes[0].Name = before.Nodes[0].Name
	delta, err = CompareRevisionStates(t.Context(), before, after)
	if err != nil || delta.Summary.Nodes.Modified != 0 || delta.Summary.IdentityMappings != 1 {
		t.Fatalf("key-only change %+v %v", delta, err)
	}
}

func TestCompareNullAbsentFreshnessAndEvidence(t *testing.T) {
	before := RevisionState{Nodes: []Node{{ID: "one", ExternalKey: "handler", Kind: "handler", Name: "Handle", Attributes: map[string]jsontext.Value{}, Freshness: &AssertionFreshness{Status: "current", ConfirmedSnapshotID: "first", Reasons: []string{}}}}, Evidence: []Evidence{{ID: "proof", ExternalKey: "proof", SubjectID: "one", Method: "ast", Status: "explicit", Source: EvidenceSource{SnapshotID: "first", File: "main.go", ContentHash: fixtureHash}}}}
	after := RevisionState{Nodes: []Node{{ID: "one", ExternalKey: "handler", Kind: "handler", Name: "Handle", Attributes: map[string]jsontext.Value{"description": jsontext.Value("null")}, Freshness: &AssertionFreshness{Status: "stale", ConfirmedSnapshotID: "first", Reasons: []string{"not_reobserved"}}}}, Evidence: []Evidence{{ID: "proof", ExternalKey: "proof", SubjectID: "one", Method: "ast", Status: "explicit", Source: EvidenceSource{SnapshotID: "second", File: "main.go", ContentHash: fixtureHash}}}}
	delta, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Summary.Nodes.Modified != 1 || delta.Summary.Evidence.Modified != 1 || delta.Summary.FreshnessChanges != 1 {
		t.Fatalf("facets %+v", delta.Summary)
	}
	var node RecordDelta
	for _, change := range delta.Changes {
		if change.RecordType == "node" {
			node = change
		}
	}
	if !slices.Contains(node.ChangedPaths, "/attributes/description") || !slices.Contains(node.ChangeKinds, "freshness_changed") {
		t.Fatalf("null/absence and freshness lost: %+v", node)
	}
	after.Nodes[0].Attributes = map[string]jsontext.Value{}
	delta, err = CompareRevisionStates(t.Context(), before, after)
	if err != nil || delta.Summary.Nodes.Modified != 0 || delta.Summary.FreshnessChanges != 1 {
		t.Fatalf("freshness-only counted as structure: %+v %v", delta, err)
	}
}

func TestCompareDeterminismDeletionAndCancellation(t *testing.T) {
	before := RevisionState{Nodes: []Node{{ID: "b", Name: "B", Attributes: map[string]jsontext.Value{}}, {ID: "a", Name: "A", Attributes: map[string]jsontext.Value{}}}}
	after := RevisionState{Nodes: []Node{before.Nodes[1]}}
	delta, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil || len(delta.Changes) != 1 || delta.Changes[0].Before.ID != "b" || delta.Changes[0].After != nil || delta.Summary.Nodes.Removed != 1 {
		t.Fatalf("delete %+v %v", delta, err)
	}
	slices.Reverse(before.Nodes)
	again, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil || len(again.Changes) != 1 || again.Changes[0].ID != delta.Changes[0].ID {
		t.Fatalf("order changed %+v %v", again, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = CompareRevisionStates(ctx, before, after); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	empty, err := CompareRevisionStates(t.Context(), before, before)
	if err != nil || len(empty.Changes) != 0 {
		t.Fatalf("same revision differs %+v %v", empty, err)
	}
}

func TestComparePinnedDatabasePagesAndHeadChanges(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "compare-project")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.CandidateHash == nil {
		t.Fatalf("preview %+v %v", v, err)
	}
	committed, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "compare-commit"})
	if err != nil {
		t.Fatal(err)
	}
	in := CompareRevisionsInput{FromRevisionID: p.CurrentRevisionID, ToRevisionID: committed.Revision.ID, Limit: 1}
	first, err := r.CompareRevisions(t.Context(), p.ID, in)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" || first.Summary.Nodes.Added != 1 {
		t.Fatalf("first comparison %+v %v", first, err)
	}
	pageInput := in
	pageInput.Cursor = first.NextCursor
	second, err := r.CompareRevisions(t.Context(), p.ID, pageInput)
	if err != nil || second.ComparisonHash != first.ComparisonHash || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second comparison %+v %v", second, err)
	}
	foreign := pageInput
	foreign.FromRevisionID = committed.Revision.ID
	if _, err = r.CompareRevisions(t.Context(), p.ID, foreign); err == nil {
		t.Fatal("cursor accepted different revision pair")
	}
	filtered := in
	filtered.RecordType = "node"
	nodes, err := r.CompareRevisions(t.Context(), p.ID, filtered)
	if err != nil || nodes.ComparisonHash != first.ComparisonHash || len(nodes.Items) != 1 || nodes.Items[0].RecordType != "node" {
		t.Fatalf("filtered %+v %v", nodes, err)
	}
	begin := firstImportFixture(&committed.Project)
	begin.Mode = "reconcile"
	begin.RepositoryID = new(s.RepositoryID)
	begin.IdempotencyKey = "compare-new-head"
	begin.GraphScope = &GraphScope{Profile: GraphProfile, Status: "partial", Gaps: []string{"fixture omitted"}}
	begin.Inventory[0].KnownCount = 1
	begin.Inventory[0].Denominator = new(int64(1))
	next, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, next.ID, PreviewImportInput{ExpectedImportVersion: next.Version, BaseRevisionID: committed.Revision.ID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("new preview %+v %v", preview, err)
	}
	newHead, err := r.CommitImport(t.Context(), p.ID, next.ID, CommitImportInput{ExpectedVersion: committed.Project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "compare-new-commit"})
	if err != nil {
		t.Fatal(err)
	}
	if newHead.Revision.ID == committed.Revision.ID {
		t.Fatal("head did not advance")
	}
	again, err := r.CompareRevisions(t.Context(), p.ID, in)
	if err != nil || again.ComparisonHash != first.ComparisonHash || again.To.RevisionID != committed.Revision.ID {
		t.Fatalf("pinned result moved: %+v %v", again, err)
	}
	same, err := r.CompareRevisions(t.Context(), p.ID, CompareRevisionsInput{FromRevisionID: committed.Revision.ID, ToRevisionID: committed.Revision.ID})
	if err != nil || len(same.Items) != 0 {
		t.Fatalf("same revision %+v %v", same, err)
	}
}

func TestCompareUnknownSourceAbsenceIsNotDeletion(t *testing.T) {
	before := RevisionState{Sources: []SourceSnapshot{{ID: "first", Role: "primary", SnapshotManifest: SnapshotManifest{Consistency: "verified", Files: []ManifestFile{{Path: "main.go", ContentHash: fixtureHash, AnalysisStatus: "analyzed"}}}}}, Inventory: []InventoryItem{{Category: "files", Status: "complete", KnownCount: 1, Denominator: new(int64(1))}}}
	after := RevisionState{Sources: []SourceSnapshot{{ID: "second", Role: "primary", SnapshotManifest: SnapshotManifest{Consistency: "unverified", Files: []ManifestFile{}}}}, Inventory: []InventoryItem{{Category: "files", Status: "partial"}}}
	delta, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil || len(delta.Changes) != 1 || slices.Contains(delta.Changes[0].ChangeKinds, "removed") {
		t.Fatalf("unknown absence called deletion %+v %v", delta, err)
	}
	after.Sources[0].Consistency = "verified"
	after.Inventory[0].Status = "complete"
	after.Inventory[0].Denominator = new(int64(0))
	delta, err = CompareRevisionStates(t.Context(), before, after)
	if err != nil || len(delta.Changes) != 1 || !slices.Contains(delta.Changes[0].ChangeKinds, "removed") {
		t.Fatalf("complete absence %+v %v", delta, err)
	}
}
