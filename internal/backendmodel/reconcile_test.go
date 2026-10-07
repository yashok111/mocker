package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func commitStaged(t *testing.T, r *Repo, p *Project, s *ImportSession, version int64, key string) *ImportCommitResult {
	t.Helper()
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "ready" || v.CandidateHash == nil {
		t.Fatalf("preview: %+v", v)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func repeatInput(p *Project, sid string) BeginImportInput {
	in := firstImportFixture(p)
	// Decode through the wire contract so the initial red test compiles before the additive fields exist.
	b, _ := json.Marshal(in)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["mode"] = "reconcile"
	m["repositoryId"] = sid
	m["graphScope"] = map[string]any{"profile": GraphProfile, "status": "complete", "gaps": []string{}}
	b, _ = json.Marshal(m)
	_ = json.Unmarshal(b, &in)
	in.Inventory[0].KnownCount = 1
	in.Inventory[0].Denominator = new(int64(1))
	return in
}
func TestReconcileBeginIdentityStable(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "commit")
	p = &out.Project
	in := repeatInput(p, s.RepositoryID)
	in.IdempotencyKey = "repeat"
	next, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatalf("reconcile begin: %v", err)
	}
	if next.RepositoryID != s.RepositoryID || next.SnapshotID == s.SnapshotID {
		t.Fatalf("identity: %+v", next)
	}
	b2 := putFixture(t, r, p, next)
	if b.Identities[0].ID != b2.Identities[0].ID {
		t.Fatal("stable key allocated a new UUID")
	}
	legacy := firstImportFixture(p)
	legacy.IdempotencyKey = "legacy"
	_, err = r.BeginImport(t.Context(), p.ID, legacy)
	assertFault(t, err, "backend_reimport_unsupported")
}

func committedBase(t *testing.T) (*Repo, *Project, *ImportSession, *BatchReceipt) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "commit")
	return r, &out.Project, s, b
}
func beginRepeat(t *testing.T, r *Repo, p *Project, old *ImportSession) *ImportSession {
	t.Helper()
	in := repeatInput(p, old.RepositoryID)
	in.IdempotencyKey = "repeat"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func sendCommands(t *testing.T, r *Repo, p *Project, s *ImportSession, v int64, key string, commands ...ImportCommand) *BatchReceipt {
	t.Helper()
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, key, ImportBatchInput{ExpectedImportVersion: v, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestReconcileBeginCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*BeginImportInput)
	}{
		{"version", "backend_incompatible_provider", func(in *BeginImportInput) { in.Manifest.Provider.Version = "2" }},
		{"namespace", "backend_incompatible_provider", func(in *BeginImportInput) { in.Manifest.Provider.Namespace = "other" }},
		{"name", "backend_unsupported_scope", func(in *BeginImportInput) { in.Manifest.RepositoryName = "other" }},
		{"profiles", "backend_incompatible_provider", func(in *BeginImportInput) {
			in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, "extra")
		}},
		{"scope", "backend_unsupported_scope", func(in *BeginImportInput) { in.GraphScope.Status = "partial" }},
		{"initial", "backend_unsupported_scope", func(in *BeginImportInput) { in.Mode = "initial" }},
		{"invalid", "backend_import_invalid", func(in *BeginImportInput) { in.Mode = "invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, p, old, _ := committedBase(t)
			in := repeatInput(p, old.RepositoryID)
			in.IdempotencyKey = "bad"
			tc.change(&in)
			_, err := r.BeginImport(t.Context(), p.ID, in)
			assertFault(t, err, tc.code)
		})
	}
}
func TestIdentityMappingReservations(t *testing.T) {
	r, p, old, first := committedBase(t)
	s := beginRepeat(t, r, p, old)
	id := first.Identities[0].ID
	b := sendCommands(t, r, p, s, 1, "old", fixtureCommands(s)[0])
	original := b
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "remove", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "handler"}})
	mapping := ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "renamed", ExpectedID: id, Reason: "renamed handler", EvidenceKeys: []string{"proof"}}}
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "mapping", mapping)
	if b.Identities[0].ID != id {
		t.Fatal("mapping changed ID")
	}
	cs := fixtureCommands(s)
	cs[0].Node.ExternalKey = "renamed"
	cs[0].Node.Name = "New name"
	cs[1].Evidence.SubjectKey = "renamed"
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "new", cs...)
	if b.Identities[0].ID != id {
		t.Fatal("target changed ID")
	}
	replay := sendCommands(t, r, p, s, 1, "old", fixtureCommands(s)[0])
	if replay.Identities[0].ID != original.Identities[0].ID {
		t.Fatal("receipt changed")
	}
	var bindings int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_identity_bindings WHERE external_key='renamed'`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatal("pending alias published", err)
	}
}

// Review 2026-10-06, F84: a batch reads the staged decisions once and keeps
// that list in step with its own writes, so a decision staged earlier in the
// same batch must still block an upsert of the mapped source, and a decision
// removed earlier in the same batch must no longer block it.
func TestIdentityDecisionsTrackedWithinBatch(t *testing.T) {
	r, p, old, first := committedBase(t)
	s := beginRepeat(t, r, p, old)
	id := first.Identities[0].ID
	mapping := ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "renamed", ExpectedID: id, Reason: "renamed handler", EvidenceKeys: []string{"proof"}}}
	commands := []ImportCommand{mapping, fixtureCommands(s)[0]}
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "same-batch", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
	assertFault(t, err, "backend_identity_conflict")
	b := sendCommands(t, r, p, s, s.Version, "mapping", mapping)
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "unmap-and-upsert", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "renamed"}}, fixtureCommands(s)[0])
	if b.Identities[1].ID != id {
		t.Fatalf("upsert after removed mapping changed ID: %+v", b.Identities)
	}
}
func TestReconcileStaleRetainsBundle(t *testing.T) {
	r, p, old, first := committedBase(t)
	s := beginRepeat(t, r, p, old)
	out := commitStaged(t, r, p, s, s.Version, "repeat-commit")
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 || graph.Nodes[0].ID != first.Identities[0].ID || graph.Nodes[0].Freshness == nil || graph.Nodes[0].Freshness.Status != "stale" {
		t.Fatalf("retained nodes: %+v", graph.Nodes)
	}
	e, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{})
	if err != nil || len(e.Items) != 1 || e.Items[0].Source.SnapshotID != old.SnapshotID {
		t.Fatalf("historical evidence %+v %v", e, err)
	}
	c, err := r.RevisionCoverage(t.Context(), p.ID, out.Revision.ID)
	if err != nil || len(c.Snapshots) != 2 || c.StaleCounts.Nodes != 1 || c.Coverage.Status != "partial" {
		t.Fatalf("coverage %+v %v", c, err)
	}
}
func TestReconcileEvidenceOnlyBlocked(t *testing.T) {
	r, p, old, _ := committedBase(t)
	s := beginRepeat(t, r, p, old)
	b := sendCommands(t, r, p, s, 1, "evidence", fixtureCommands(s)[1])
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" {
		t.Fatalf("evidence only %+v %v", v, err)
	}
}
