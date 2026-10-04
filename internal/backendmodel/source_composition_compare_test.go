package backendmodel

import "testing"

func TestSource6ChangesSeparateRepositoryProviderPaths(t *testing.T) {
	t.Parallel()
	before := RevisionState{Revision: Revision{SchemaVersion: ComposedSchemaVersion}, Sources: []SourceSnapshot{{ID: "one", RepositoryID: "repo1", Role: "active_source", Provider: SourceProvider{Namespace: "provider"}, SnapshotManifest: SnapshotManifest{Files: []ManifestFile{{Path: "same.go", ContentHash: "old"}}}}, {ID: "two", RepositoryID: "repo2", Role: "active_source", Provider: SourceProvider{Namespace: "provider"}, SnapshotManifest: SnapshotManifest{Files: []ManifestFile{{Path: "same.go", ContentHash: "unchanged"}}}}}}
	after := before
	after.Sources = append([]SourceSnapshot{}, before.Sources...)
	after.Sources[0].Files = []ManifestFile{{Path: "same.go", ContentHash: "new"}}
	changes := sourceChanges(before, after)
	delta, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Changes) != 1 || delta.Changes[0].ID == "same.go" {
		t.Fatalf("unqualified compare identity: %+v", delta)
	}
	if len(changes) != 1 || changes[0].Before.ContentHash != "old" || changes[0].After.ContentHash != "new" {
		t.Fatalf("qualified file changes collapsed: %+v", changes)
	}
}
