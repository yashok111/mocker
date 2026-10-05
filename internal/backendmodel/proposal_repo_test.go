package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

func proposalCreateInput(out *ImportCommitResult, ids map[string]string, key string) CreateProposalInput {
	return CreateProposalInput{Name: "Require customer", BaseRevisionID: out.Revision.ID,
		RepositoryID: out.Project.Repositories[0].ID, DatastoreID: ids["database:orders"], FacetKey: "sql", IdempotencyKey: key}
}

// Capture raw persisted bytes, including old receipts and provider identities.
func proposalSourceBytes(t *testing.T, r *Repo) map[string][]string {
	t.Helper()
	queries := map[string]string{
		"project":      `SELECT id,name,version,current_revision_id,created_at,updated_at FROM backend_projects ORDER BY id`,
		"revisions":    `SELECT id,project_id,document FROM backend_revisions ORDER BY id`,
		"graph":        `SELECT revision_id,record_type,id,document FROM backend_graph_records ORDER BY revision_id,record_type,id`,
		"sources":      `SELECT revision_id,document FROM backend_revision_sources ORDER BY revision_id`,
		"identity":     `SELECT project_id,repository_id,provider_namespace,record_type,external_key,id,state,revision_id FROM backend_identity_bindings ORDER BY project_id,repository_id,provider_namespace,record_type,external_key`,
		"reservations": `SELECT session_id,record_type,external_key,id FROM backend_import_identities ORDER BY session_id,record_type,external_key`,
		"receipts":     `SELECT scope,key,request_hash,response FROM backend_command_receipts WHERE scope NOT LIKE 'proposal-%' ORDER BY scope,key`,
	}
	result := map[string][]string{}
	for name, query := range queries {
		rows, err := r.db.R.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]string, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			b, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			result[name] = append(result[name], string(b))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return result
}

func assertProposalSourceBytes(t *testing.T, r *Repo, before map[string][]string) {
	t.Helper()
	after := proposalSourceBytes(t, r)
	for name, values := range before {
		if !slices.Equal(values, after[name]) {
			t.Fatalf("proposal changed source %s", name)
		}
	}
}

func TestProposalCreateIsolationAndReplay(t *testing.T) {
	t.Parallel()
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := testRepo(t)
			out, ids := commitRelationalFixture(t, r, dialect, "v1")
			before := proposalSourceBytes(t, r)
			in := proposalCreateInput(out, ids, "create-proposal")
			created, err := r.CreateProposal(t.Context(), out.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			p, rev := created.Proposal, created.Revision
			if !ValidID(p.ID) || p.Version != 1 || p.Status != "draft" || p.Name != "Require customer" || p.BaseRevisionID != out.Revision.ID || p.BaseSemanticHash != out.Revision.SemanticHash || p.DatastoreID != ids["database:orders"] || p.FacetKey != "sql" || p.RepositoryID != out.Project.Repositories[0].ID {
				t.Fatalf("incorrect proposal binding: %+v", p)
			}
			if !ValidID(rev.ID) || rev.ProposalID != p.ID || rev.ParentRevisionID != nil || rev.DocumentVersion != "proposal-relational-v1" || p.DraftRevisionID != rev.ID || p.DraftHash != rev.SemanticHash || len(rev.SemanticHash) != 64 || len(rev.Commands) != 0 || len(rev.Overlays) != 0 || len(rev.Criteria) != 0 || !slices.Equal(rev.SourceSnapshotIDs, out.Revision.SourceSnapshotIDs) || rev.BaseRevisionID != out.Revision.ID || rev.BaseSemanticHash != out.Revision.SemanticHash {
				t.Fatalf("incorrect empty revision: %+v", rev)
			}
			if created.BaseOutdated || created.CurrentSourceRevisionID != out.Revision.ID || created.CurrentSourceSemanticHash != out.Revision.SemanticHash || len(created.History) != 1 || created.NextCursor != "" || created.LastApplyReceipt != nil {
				t.Fatalf("incorrect detail: %+v", created)
			}
			assertProposalSourceBytes(t, r, before)
			first, err := json.Marshal(created)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := r.CreateProposal(t.Context(), out.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			second, err := json.Marshal(replay)
			if err != nil || string(first) != string(second) {
				t.Fatalf("replay changed: %s / %s: %v", first, second, err)
			}
			var count int
			if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_proposals`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate proposal: %d %v", count, err)
			}
			in.Name = "Other intent"
			_, err = r.CreateProposal(t.Context(), out.Project.ID, in)
			assertFault(t, err, "backend_idempotency_conflict")
			got, err := r.GetProposal(t.Context(), out.Project.ID, p.ID, GetProposalInput{})
			if err != nil || got.Revision.ID != rev.ID || got.Proposal.Version != 1 {
				t.Fatalf("read: %+v %v", got, err)
			}
		})
	}
}

func TestProposalCreateHistoricalBase(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	in := proposalCreateInput(out, ids, "historical")
	latest, _ := databaseCommitV2(t, r, out, ids, "postgresql")
	if _, err := r.Apply(t.Context(), out.Project.ID, renameInput(latest.Project.Version, "rename-after-import", "Renamed")); err != nil {
		t.Fatal(err)
	}
	before := proposalSourceBytes(t, r)
	got, err := r.CreateProposal(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.BaseOutdated || got.Proposal.BaseRevisionID != out.Revision.ID || got.CurrentSourceRevisionID != latest.Revision.ID || got.CurrentSourceSemanticHash != latest.Revision.SemanticHash || got.Proposal.Version != 1 {
		t.Fatalf("lost historical binding: %+v", got)
	}
	assertProposalSourceBytes(t, r, before)
	page, err := r.ListProposals(t.Context(), out.Project.ID, ProposalListInput{BaseRevisionID: out.Revision.ID, Status: "draft"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != got.Proposal.ID {
		t.Fatalf("list: %+v %v", page, err)
	}
	page, err = r.ListProposals(t.Context(), out.Project.ID, ProposalListInput{BaseRevisionID: latest.Revision.ID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("base filter leaked: %+v %v", page, err)
	}
}

func TestProposalCreateForeignOrSchema1Base(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	other := createProject(t, r, "other")
	base := proposalCreateInput(out, ids, "invalid")
	for _, tc := range []struct {
		name, pid, code string
		input           CreateProposalInput
	}{
		{"foreign-project", other.ID, "backend_not_found", base},
		{"schema1", other.ID, "backend_relational_unavailable", CreateProposalInput{Name: "Change", BaseRevisionID: other.CurrentRevisionID, RepositoryID: base.RepositoryID, DatastoreID: base.DatastoreID, FacetKey: "sql", IdempotencyKey: "schema1"}},
		{"foreign-repository", out.Project.ID, "backend_not_found", CreateProposalInput{Name: "Change", BaseRevisionID: base.BaseRevisionID, RepositoryID: uuid.NewV7().String(), DatastoreID: base.DatastoreID, FacetKey: "sql", IdempotencyKey: "repository"}},
		{"non-store", out.Project.ID, "backend_relational_unavailable", CreateProposalInput{Name: "Change", BaseRevisionID: base.BaseRevisionID, RepositoryID: base.RepositoryID, DatastoreID: ids["table:orders"], FacetKey: "sql", IdempotencyKey: "table"}},
		{"missing-facet", out.Project.ID, "backend_relational_unavailable", CreateProposalInput{Name: "Change", BaseRevisionID: base.BaseRevisionID, RepositoryID: base.RepositoryID, DatastoreID: base.DatastoreID, FacetKey: "missing", IdempotencyKey: "facet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.CreateProposal(t.Context(), tc.pid, tc.input)
			assertFault(t, err, tc.code)
		})
	}
	created, err := r.CreateProposal(t.Context(), out.Project.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.GetProposal(t.Context(), other.ID, created.Proposal.ID, GetProposalInput{})
	assertFault(t, err, "backend_not_found")
	_, err = r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{ProposalRevisionID: out.Revision.ID})
	assertFault(t, err, "backend_not_found")
}

func TestProposalCreateRollback(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	before := proposalSourceBytes(t, r)
	for _, table := range []string{"backend_proposal_revisions", "backend_command_receipts"} {
		t.Run(table, func(t *testing.T) {
			trigger := fmt.Sprintf(`CREATE TRIGGER fail_proposal BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT, 'test insert fault'); END`, table)
			if _, err := db.W.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			_, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "rollback"))
			if err == nil {
				t.Fatal("fault accepted")
			}
			for _, query := range []string{`SELECT count(*) FROM backend_proposals`, `SELECT count(*) FROM backend_proposal_revisions`, `SELECT count(*) FROM backend_command_receipts WHERE scope LIKE 'proposal-%'`} {
				var n int
				if err := db.R.QueryRowContext(t.Context(), query).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial transaction: %d %v", n, err)
				}
			}
			if _, err := db.W.ExecContext(t.Context(), `DROP TRIGGER fail_proposal`); err != nil {
				t.Fatal(err)
			}
			assertProposalSourceBytes(t, r, before)
		})
	}
	if _, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "rollback")); err != nil {
		t.Fatal(err)
	}
}

func TestProposalCreationConcurrentKey(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	in := proposalCreateInput(out, ids, "concurrent")
	results := make(chan string, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, err := r.CreateProposal(t.Context(), out.Project.ID, in)
			if err != nil {
				errs <- err
				return
			}
			b, err := json.Marshal(got)
			if err != nil {
				errs <- err
				return
			}
			results <- string(b)
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first string
	for result := range results {
		if first == "" {
			first = result
		}
		if result != first {
			t.Fatal("concurrent replay differs")
		}
	}
	for _, table := range []string{"backend_proposals", "backend_proposal_revisions"} {
		var n int
		if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s duplicate: %d %v", table, n, err)
		}
	}
}

func TestProposalCreateReplayAfterRestart(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	in := proposalCreateInput(out, ids, "restart")
	created, err := r.CreateProposal(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := db.R.QueryRowContext(t.Context(), `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, "proposal-create:"+out.Project.ID, in.IdempotencyKey).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	latest, _ := databaseCommitV2(t, r, out, ids, "postgresql")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(reopened)
	replay, err := r.CreateProposal(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(replay)
	if err != nil || string(b) != saved || replay.BaseOutdated {
		t.Fatalf("receipt bytes recomputed: %s / %s %v", saved, b, err)
	}
	read, err := r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{})
	if err != nil || !read.BaseOutdated || read.CurrentSourceRevisionID != latest.Revision.ID {
		t.Fatalf("derived current read: %+v %v", read, err)
	}
}

func TestProposalHistoryCursorPin(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	created, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "history"))
	if err != nil {
		t.Fatal(err)
	}
	// Seed a later saved draft directly: Task1 has no apply operation yet.
	appendDraft := func() string {
		t.Helper()
		rev := created.Revision
		rev.ID = uuid.NewV7().String()
		rev.ParentRevisionID = new(created.Proposal.DraftRevisionID)
		doc, err := json.Marshal(rev)
		if err != nil {
			t.Fatal(err)
		}
		err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_proposal_revisions(id,proposal_id,parent_revision_id,document) VALUES(?,?,?,?)`, rev.ID, rev.ProposalID, rev.ParentRevisionID, string(doc)); err != nil {
				return err
			}
			_, err := tx.ExecContext(t.Context(), `UPDATE backend_proposals SET version=version+1,draft_revision_id=?,draft_hash=? WHERE id=?`, rev.ID, rev.SemanticHash, rev.ProposalID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return rev.ID
	}
	secondID := appendDraft()
	first, err := r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{Limit: 1})
	if err != nil || first.Revision.ID != secondID || len(first.History) != 1 || first.NextCursor == "" {
		t.Fatalf("history first: %+v %v", first, err)
	}
	second, err := r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.History) != 1 || second.History[0].ID == first.History[0].ID || second.NextCursor != "" {
		t.Fatalf("history next: %+v %v", second, err)
	}
	_, err = r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{ProposalRevisionID: created.Revision.ID, Limit: 1, Cursor: first.NextCursor})
	assertFault(t, err, "backend_invalid")
	appendDraft()
	_, err = r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{Limit: 1, Cursor: first.NextCursor})
	assertFault(t, err, "backend_invalid")
	old, err := r.GetProposal(t.Context(), out.Project.ID, created.Proposal.ID, GetProposalInput{ProposalRevisionID: created.Revision.ID})
	if err != nil || old.Revision.ID != created.Revision.ID {
		t.Fatalf("historical draft lost: %+v %v", old, err)
	}
}

func TestProposalListCursorAndLimits(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	for i := range 3 {
		if _, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, fmt.Sprintf("list-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	in := ProposalListInput{Limit: 1}
	for {
		page, err := r.ListProposals(t.Context(), out.Project.ID, in)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("list: %+v %v", page, err)
		}
		seen = append(seen, page.Items[0].ID)
		if page.NextCursor == "" {
			break
		}
		_, err = r.ListProposals(t.Context(), out.Project.ID, ProposalListInput{Status: "draft", Cursor: page.NextCursor})
		assertFault(t, err, "backend_invalid")
		in.Cursor = page.NextCursor
	}
	if len(seen) != 3 || !slices.IsSorted(seen) || len(slices.Compact(seen)) != 3 {
		t.Fatalf("unstable list: %v", seen)
	}
	for _, input := range []ProposalListInput{{Limit: -1}, {Limit: 501}, {BaseRevisionID: "null"}, {Cursor: "invalid"}} {
		_, err := r.ListProposals(t.Context(), out.Project.ID, input)
		assertFault(t, err, "backend_invalid")
	}
	_, err := r.ListProposals(t.Context(), out.Project.ID, ProposalListInput{Status: "ready"})
	assertFault(t, err, "backend_unsupported_scope")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.CreateProposal(cancelled, out.Project.ID, proposalCreateInput(out, ids, "cancelled")); err == nil {
		t.Fatal("cancelled create succeeded")
	}
}

func TestProposalCreateStrictInput(t *testing.T) {
	valid := `{"name":"Change","baseRevisionId":"a","repositoryId":"b","datastoreId":"c","facetKey":"sql","idempotencyKey":"k"}`
	for _, raw := range []string{
		`null`, `{}`, `{"name":"Change"}`, valid[:len(valid)-1] + `,"name":"duplicate"}`,
		valid[:len(valid)-1] + `,"status":"ready"}`, valid[:len(valid)-1] + `,"expectedVersion":1}`,
		`{"name":null,"baseRevisionId":"a","repositoryId":"b","datastoreId":"c","facetKey":"sql","idempotencyKey":"k"}`,
		`{"name":true,"baseRevisionId":"a","repositoryId":"b","datastoreId":"c","facetKey":"sql","idempotencyKey":"k"}`,
	} {
		var in CreateProposalInput
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("accepted malformed input: %s", raw)
		}
	}
	var in CreateProposalInput
	if err := json.Unmarshal([]byte(valid), &in); err != nil {
		t.Fatal(err)
	}
}

func TestProposalCreateB11StoreUpgrade(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			// These are the unchanged, committed B1.1 migrations, stopped at 15.
			// Import via the source APIs before the proposal tables exist.
			db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "b11.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			entries, err := os.ReadDir("../store/migrations")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() >= "0016" {
					continue
				}
				b, err := os.ReadFile(filepath.Join("../store/migrations", entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.W.ExecContext(t.Context(), string(b)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.W.ExecContext(t.Context(), "PRAGMA user_version=15"); err != nil {
				t.Fatal(err)
			}
			r := NewRepo(db)
			out, ids := commitRelationalFixture(t, r, dialect, "v1")
			createProject(t, r, "schema1-project")
			before := proposalSourceBytes(t, r)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(t.Context(), db.Path())
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(t.Context(), slog.Default()); err != nil {
				t.Fatal(err)
			}
			version, err := db.SchemaVersion(t.Context())
			if err != nil || version != 22 {
				t.Fatalf("upgrade: %d %v", version, err)
			}
			r = NewRepo(db)
			assertProposalSourceBytes(t, r, before)
			if _, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "after-upgrade")); err != nil {
				t.Fatal(err)
			}
			assertProposalSourceBytes(t, r, before)
			if err := db.Migrate(t.Context(), slog.Default()); err != nil {
				t.Fatal(err)
			}
			assertProposalSourceBytes(t, r, before)
		})
	}
}

func TestProposalCreateDatabaseOwnershipAndImmutability(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	one, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "two"))
	if err != nil {
		t.Fatal(err)
	}
	other := createProject(t, r, "foreign-owner")
	for _, tc := range []struct {
		query string
		args  []any
	}{
		{`UPDATE backend_proposals SET draft_revision_id=? WHERE id=?`, []any{two.Revision.ID, one.Proposal.ID}},
		{`UPDATE backend_proposals SET project_id=? WHERE id=?`, []any{other.ID, one.Proposal.ID}},
		{`UPDATE backend_proposals SET status='ready' WHERE id=?`, []any{one.Proposal.ID}},
		{`UPDATE backend_proposals SET facet_key='orm' WHERE id=?`, []any{one.Proposal.ID}},
		{`UPDATE backend_proposals SET base_semantic_hash='changed' WHERE id=?`, []any{one.Proposal.ID}},
		{`UPDATE backend_proposal_revisions SET document='{}' WHERE id=?`, []any{one.Revision.ID}},
		{`DELETE FROM backend_proposal_revisions WHERE id=?`, []any{one.Revision.ID}},
		{`DELETE FROM backend_revisions WHERE id=?`, []any{out.Revision.ID}},
		{`DELETE FROM backend_repositories WHERE id=?`, []any{one.Proposal.RepositoryID}},
		{`INSERT INTO backend_proposal_revisions(id,proposal_id,parent_revision_id,document) VALUES(?,?,?,?)`, []any{uuid.NewV7().String(), one.Proposal.ID, two.Revision.ID, `{}`}},
	} {
		if _, err := db.W.ExecContext(t.Context(), tc.query, tc.args...); err == nil {
			t.Fatalf("accepted broken ownership/immutable write: %s", tc.query)
		}
	}
	got, err := r.GetProposal(t.Context(), out.Project.ID, one.Proposal.ID, GetProposalInput{})
	if err != nil || got.Revision.ID != one.Revision.ID || got.Proposal.Status != "draft" || got.Proposal.ProjectID != out.Project.ID {
		t.Fatalf("failed writes changed proposal: %+v %v", got, err)
	}
}

func TestProposalCreateReceiptPrecedesCompatibilityAndLimits(t *testing.T) {
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	in := proposalCreateInput(out, ids, "receipt-first")
	created, err := r.CreateProposal(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a later compatibility refusal. A saved response must win even
	// if its source can no longer pass the current creation validation.
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_revisions SET document=json_set(document,'$.schemaVersion','1') WHERE id=?`, out.Revision.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := r.CreateProposal(t.Context(), out.Project.ID, in)
	if err != nil || replay.Proposal.ID != created.Proposal.ID {
		t.Fatalf("receipt lost to compatibility: %+v %v", replay, err)
	}
	in.IdempotencyKey = "new-incompatible"
	_, err = r.CreateProposal(t.Context(), out.Project.ID, in)
	assertFault(t, err, "backend_relational_unavailable")
	in.IdempotencyKey = "receipt-first"
	in.Name = "Changed payload"
	_, err = r.CreateProposal(t.Context(), out.Project.ID, in)
	assertFault(t, err, "backend_idempotency_conflict")
}
