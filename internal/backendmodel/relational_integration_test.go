//go:build integration

package backendmodel

import (
	"encoding/json/v2"
	"log/slog"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func relationalPublishedCounts(t *testing.T, r *Repo, pid string) string {
	t.Helper()
	out := []int64{}
	for _, query := range []string{`SELECT count(*) FROM backend_revisions WHERE project_id=?`, `SELECT count(*) FROM backend_graph_records WHERE project_id=?`, `SELECT count(*) FROM backend_identity_bindings WHERE project_id=?`, `SELECT count(*) FROM backend_revision_sources s JOIN backend_revisions r ON r.id=s.revision_id WHERE r.project_id=?`, `SELECT count(*) FROM backend_command_receipts WHERE scope LIKE '%'||?||'%'`} {
		var count int64
		if err := r.db.R.QueryRowContext(t.Context(), query, pid).Scan(&count); err != nil {
			t.Fatal(err)
		}
		out = append(out, count)
	}
	b, _ := json.Marshal(out)
	return string(b)
}
func TestRelationalAtomicRollbackAndRestartReplay(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	begin := relationalFixtureInput(t, p, "postgresql", "v1")
	s, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	cs := relationalFixture(t, p, s, "postgresql", "v1")
	v, ids := stageRelational(t, r, p, s, cs, "fixture")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	before := relationalPublishedCounts(t, r, p.ID)
	if _, err = r.db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_relational_publish BEFORE UPDATE ON backend_projects BEGIN SELECT RAISE(ABORT,'forced after facets and identities'); END`); err != nil {
		t.Fatal(err)
	}
	commit := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"}
	if _, err = r.CommitImport(t.Context(), p.ID, s.ID, commit); err == nil {
		t.Fatal("forced SQL transaction failure committed")
	}
	if after := relationalPublishedCounts(t, r, p.ID); after != before {
		t.Fatalf("partial publication %s became %s", before, after)
	}
	head, err := r.Get(t.Context(), p.ID)
	if err != nil || head.CurrentRevisionID != p.CurrentRevisionID || head.Version != p.Version {
		t.Fatalf("head changed %+v %v", head, err)
	}
	empty, err := r.Revision(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil || empty.SchemaVersion != "1" {
		t.Fatal("schema/profile published early")
	}
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil || status.Session.State != "ready" {
		t.Fatal("rollback lost ready session", err)
	}
	if _, err = r.db.W.ExecContext(t.Context(), `DROP TRIGGER fail_relational_publish`); err != nil {
		t.Fatal(err)
	}
	first, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	if err = opened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(opened)
	replay, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("commit receipt changed after restart")
	}
	again, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil || again.ID != s.ID {
		t.Fatalf("begin replay after incompatible base/CAS %+v %v", again, err)
	}
	n, err := r.Node(t.Context(), p.ID, first.Revision.ID, ids["column:orders:total"])
	if err != nil || n.ID != ids["column:orders:total"] || n.Ownership.Profile != RelationalProfile {
		t.Fatalf("persisted facet/identity lost %+v %v", n, err)
	}
}
func TestRelationalCompetingCommitsSameBase(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	sessions := []*ImportSession{}
	previews := []*ImportPreview{}
	for i := range 2 {
		in := relationalFixtureInput(t, p, "sqlite", "v1")
		in.IdempotencyKey = []string{"begin-a", "begin-b"}[i]
		s, err := r.BeginImport(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := stageRelational(t, r, p, s, relationalFixture(t, p, s, "sqlite", "v1"), "batch")
		sessions = append(sessions, s)
		previews = append(previews, v)
	}
	start := make(chan struct{})
	type result struct {
		out   *ImportCommitResult
		err   error
		index int
	}
	responses := make(chan result, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			<-start
			v := previews[i]
			out, err := r.CommitImport(t.Context(), p.ID, sessions[i].ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"})
			responses <- result{out, err, i}
		})
	}
	close(start)
	wg.Wait()
	close(responses)
	success, conflict := 0, 0
	winner := -1
	for response := range responses {
		if response.err == nil {
			success++
			winner = response.index
		} else {
			assertFault(t, response.err, "backend_version_conflict")
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS outcomes %d/%d", success, conflict)
	}
	revisions, err := r.Revisions(t.Context(), p.ID, ListInput{})
	if err != nil || len(revisions.Items) != 2 {
		t.Fatalf("duplicate/partial revision %+v %v", revisions, err)
	}
	v := previews[winner]
	if _, err = r.CommitImport(t.Context(), p.ID, sessions[winner].ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"}); err != nil {
		t.Fatal("receipt must precede stale CAS", err)
	}
}
