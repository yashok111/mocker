package backendmodel

import (
	"encoding/json/v2"
	"log/slog"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestSource6SelectedPartitionMetadataRestartReplay(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "metadata")
	firstInput := source6Input(t, p)
	first, err := r.BeginImport(t.Context(), p.ID, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.SelectedPartition == nil || first.SelectedPartition.SnapshotID != first.SnapshotID {
		t.Fatalf("add_repository lacks incoming partition: %+v", first.SelectedPartition)
	}
	base := commitStaged(t, r, p, first, putFixture(t, r, p, first).AcceptedVersion, "base")
	p = &base.Project
	type pending struct {
		input   BeginImportInput
		session *ImportSession
		raw     []byte
	}
	requests := make([]pending, 0, 3)
	for _, kind := range []string{"reconcile", "migrate_provider", "add_provider"} {
		in := source6Input(t, p)
		in.IdempotencyKey = kind
		in.SourceScope = &SourceScope{Kind: kind, RepositoryID: first.RepositoryID}
		switch kind {
		case "reconcile":
			in.SourceScope.ProviderNamespace = first.Manifest.Provider.Namespace
		case "migrate_provider":
			in.Manifest.Provider.Namespace = "provider-b"
			in.SourceScope.FromProviderNamespace = first.Manifest.Provider.Namespace
			in.SourceScope.FromSnapshotID = first.SnapshotID
			in.SourceScope.Reason = "explicit migration"
		case "add_provider":
			in.Manifest.Provider.Namespace = "provider-c"
		}
		s, err := r.BeginImport(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if s.SelectedPartition == nil || s.SelectedPartition.RepositoryID != s.RepositoryID || s.SelectedPartition.SnapshotID != s.SnapshotID || s.SelectedPartition.ProviderNamespace != in.Manifest.Provider.Namespace {
			t.Fatalf("incoming selected partition mismatch for %s: %+v", kind, s.SelectedPartition)
		}
		wire, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(wire, &object); err != nil {
			t.Fatal(err)
		}
		if kind != "add_provider" {
			prior, ok := object["basePartition"].(map[string]any)
			if !ok || prior["snapshotId"] != first.SnapshotID {
				t.Fatalf("missing exact base partition for %s: %s", kind, wire)
			}
		}
		status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
		if err != nil || status.Session.SelectedPartition == nil || status.Session.SelectedPartition.SnapshotID != s.SnapshotID {
			t.Fatalf("status partition=%+v %v", status, err)
		}
		requests = append(requests, pending{in, s, wire})
	}
	last := requests[len(requests)-1]
	out := commitStaged(t, r, p, last.session, putFixture(t, r, p, last.session).AcceptedVersion, "advance")
	if out.Revision.ID == base.Revision.ID {
		t.Fatal("head did not advance")
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(reopened)
	for _, request := range requests {
		replayed, err := r.BeginImport(t.Context(), p.ID, request.input)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(replayed)
		if err != nil || string(wire) != string(request.raw) {
			t.Fatalf("begin replay after new head/restart differs: %s %v", wire, err)
		}
	}
}
