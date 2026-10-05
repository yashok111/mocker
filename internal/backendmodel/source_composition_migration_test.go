package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func TestSource6MigrationAddsNamespaceAndPreservesOld(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "migration")
	a, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	before := immutableBytes(t, r)
	p = &base.Project
	in := source6Input(t, p)
	in.IdempotencyKey = "migration"
	in.SourceScope = &SourceScope{Kind: "migrate_provider", RepositoryID: a.RepositoryID, FromProviderNamespace: a.Manifest.Provider.Namespace, FromSnapshotID: a.SnapshotID, Reason: "new analysis provider"}
	in.Manifest.Provider.Namespace = "provider-v2"
	in.Manifest.Provider.Name = "new-provider"
	in.Manifest.Provider.Version = "2"
	in.Manifest.Provider.Method = "manual"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	commands[1].Evidence.Method = "manual"
	b := sendCommands(t, r, p, s, 1, "migration", commands...)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "migration")
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.SourceVector.Partitions) != 2 || len(graph.State.Nodes) != 2 {
		t.Fatal("migration silently retired or merged old provider")
	}
	var state string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT state FROM backend_identity_bindings WHERE project_id=? AND repository_id=? AND provider_namespace=? AND record_type='node'`, p.ID, a.RepositoryID, a.Manifest.Provider.Namespace).Scan(&state); err != nil || state != "active" {
		t.Fatalf("old namespace state=%s %v", state, err)
	}
	var raw string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_decisions WHERE revision_id=?`, out.Revision.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var decisions map[string]any
	if err := json.Unmarshal([]byte(raw), &decisions); err != nil {
		t.Fatal(err)
	}
	if decisions["documentVersion"] != "source-decisions-v1" || decisions["migration"] == nil {
		t.Fatalf("missing durable migration decision: %s", raw)
	}
	after := immutableBytes(t, r)
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("old source row changed: %s", key)
		}
	}
}
