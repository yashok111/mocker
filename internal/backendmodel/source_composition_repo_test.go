package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func commitSource6Fixture(t *testing.T, r *Repo, p *Project, in BeginImportInput) (*ImportSession, *ImportCommitResult) {
	t.Helper()
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "ready" {
		t.Fatalf("source6 preview: %+v", v)
	}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	return s, out
}

func TestSource6WholeComposition(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "source6")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	if first.Revision.SchemaVersion != "6" {
		t.Fatalf("schema=%s", first.Revision.SchemaVersion)
	}
	before := immutableBytes(t, r)
	in := source6Input(t, &first.Project)
	in.IdempotencyKey = "second"
	in.Manifest.RepositoryName = "other"
	b, second := commitSource6Fixture(t, r, &first.Project, in)
	if a.RepositoryID == b.RepositoryID {
		t.Fatal("repository identity collapsed")
	}
	var count int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_assertions WHERE revision_id=?`, second.Revision.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("claims=%d err=%v", count, err)
	}
	var raw string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_sources WHERE revision_id=?`, second.Revision.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var coverage map[string]any
	if err := json.Unmarshal([]byte(raw), &coverage); err != nil {
		t.Fatal(err)
	}
	if coverage["viewSchemaVersion"] != "6" {
		t.Fatalf("missing source6 context: %s", raw)
	}
	for key, value := range before {
		if immutableBytes(t, r)[key] != value {
			t.Fatalf("historical row rewritten: %s", key)
		}
	}
}
