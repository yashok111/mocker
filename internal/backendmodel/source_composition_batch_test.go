package backendmodel

import (
	"encoding/json/v2"
	"testing"
	"uuid"
)

func source6SharedInput(t *testing.T, r *Repo, p *Project, repository string) (BeginImportInput, BaseAssertionRef) {
	t.Helper()
	in := source6Input(t, p)
	in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: repository}
	in.Manifest.Provider.Namespace = "provider-b"
	in.IdempotencyKey = "provider-b"
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	return in, sourceAssertionRef(graph.Assertions[0])
}

func TestSource6BatchClaimBeforeUpsert(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "shared")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, a.RepositoryID)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	claim := ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same source object", EvidenceKeys: []string{"proof"}}}
	commands := append([]ImportCommand{claim}, fixtureCommands(s)...)
	hash, _ := ImportBatchHash(commands)
	batchInput := ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: commands}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "claims", batchInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Identities) != 2 || len(b.DecisionIDs) != 1 || b.Identities[0].ID != target.ExpectedID {
		t.Fatalf("wrong ordered receipt: %+v", b)
	}
	replayed, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "claims", batchInput)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(b)
	right, _ := json.Marshal(replayed)
	if string(left) != string(right) {
		t.Fatal("batch replay differs")
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "ready" {
		t.Fatalf("shared preview=%+v %v", v, err)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "shared-commit"})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Assertions) != 2 || len(graph.State.Nodes) != 1 || len(graph.RawEvidence) != 2 || len(graph.Identities) != 2 {
		t.Fatalf("claims/projection/proof lost: %+v", graph)
	}
	if graph.State.Nodes[0].ExternalKey != "" || graph.State.Nodes[0].Ownership != nil || graph.State.Nodes[0].Freshness != nil {
		t.Fatal("arbitrary singular identity leaked")
	}
	if graph.Assertions[0].EvidenceIDs[0] == graph.Assertions[1].EvidenceIDs[0] {
		t.Fatal("shared proof identity")
	}
}

func TestSource6BatchRejectsReverseOrder(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "reverse")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, a.RepositoryID)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	commands = append(commands, ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same object", EvidenceKeys: []string{"proof"}}})
	hash, _ := ImportBatchHash(commands)
	if _, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "reverse", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: commands}); err == nil {
		t.Fatal("reverse decision replaced acknowledged ID")
	}
	for _, table := range []string{"backend_import_records", "backend_import_identities", "backend_import_source_decisions", "backend_import_batches"} {
		var n int
		if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table+` WHERE session_id=?`, s.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial batch rows %s=%d %v", table, n, err)
		}
	}
}

func TestSource6FieldConflicts(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "conflict")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, a.RepositoryID)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	commands[0].Node.Name = "Other name"
	commands = append([]ImportCommand{{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same object", EvidenceKeys: []string{"proof"}}}}, commands...)
	b := sendCommands(t, r, p, s, 1, "conflict", commands...)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" {
		t.Fatalf("conflict preview=%+v err=%v", v, err)
	}
	loaded, err := loadSession(t.Context(), r.db.R, p.ID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := prepareComposedGraph(t.Context(), r.db.R, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Conflicts) != 1 || g.Conflicts[0].Property.Kind != "name" {
		t.Fatalf("wrong typed conflicts: %+v", g.Conflicts)
	}
	conflict := g.Conflicts[0]
	page, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "assertion_conflict"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("needs_resolution conflict page: %+v %v", page, err)
	}
	choice := conflict.Contenders[1]
	resolution := SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: "node", ID: target.ExpectedID, Property: conflict.Property, ConflictHash: conflict.ConflictHash, Select: SourceAssertionSelection{RepositoryID: choice.Owner.RepositoryID, ProviderNamespace: choice.Owner.ProviderNamespace, AssertionHash: choice.AssertionHash}, Reason: "explicit choice"}
	b = sendCommands(t, r, p, s, v.Version, "resolve", ImportCommand{Op: "resolve_assertion", Resolution: &resolution})
	v, err = r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "ready" {
		t.Fatalf("resolved preview=%+v err=%v", v, err)
	}
	changed := fixtureCommands(s)
	changed[0].Node.Name = "Changed after selection"
	b = sendCommands(t, r, p, s, v.Version, "changed-after-selection", changed...)
	v, err = r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" {
		t.Fatalf("stale staged selection must expose new resolvable conflict: %+v %v", v, err)
	}
	page, err = r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "assertion_conflict"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("updated conflict page=%+v %v", page, err)
	}
	updated := page.Items[0].AssertionConflict
	resolution.DecisionID = uuid.NewV7().String()
	resolution.ConflictHash = updated.ConflictHash
	choice = updated.Contenders[1]
	resolution.Select = SourceAssertionSelection{RepositoryID: choice.Owner.RepositoryID, ProviderNamespace: choice.Owner.ProviderNamespace, AssertionHash: choice.AssertionHash}
	b = sendCommands(t, r, p, s, v.Version, "replace-resolution", ImportCommand{Op: "resolve_assertion", Resolution: &resolution})
	v, err = r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "ready" {
		t.Fatalf("superseded resolution: %+v %v", v, err)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "resolve-commit"})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Selections) != 1 || len(graph.Assertions) != 2 || graph.State.Revision.Coverage.Status != "partial" {
		t.Fatal("selection hid disagreement")
	}
}

func TestSource6BatchDistinctScopedValues(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "distinct-values")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	node := &ImportNode{ExternalKey: "mapping", Kind: "field_mapping", Name: "computed value", Attributes: runtimeAttrs(t, map[string]any{"sources": []any{map[string]any{"kind": "column", "nodeRef": ImportRecordRef{LocalKey: "left"}, "facetKey": "sql"}, map[string]any{"kind": "column", "nodeRef": ImportRecordRef{LocalKey: "right"}, "facetKey": "sql"}}, "destination": map[string]any{"kind": "api_field", "nodeRef": ImportRecordRef{LocalKey: "destination"}}, "transform": LineageTransform{Kind: "compute", Description: "combine values"}, "analysisStatus": "complete", "gaps": []string{}}), EvidenceKeys: []string{"proof"}}
	commands := []ImportCommand{{Op: "upsert_node", Node: node}}
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "values", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: commands}); err != nil {
		t.Fatalf("distinct scoped value addresses collapsed during shape validation: %v", err)
	}
}
