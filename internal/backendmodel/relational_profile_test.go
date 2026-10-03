package backendmodel

import (
	"encoding/json/v2"
	"log/slog"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestRelationalProfileEmptyStoreReopen(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	rev, err := r.Revision(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil || rev.SchemaVersion != SchemaVersion {
		t.Fatalf("empty schema1: %+v, %v", rev, err)
	}
	before, err := json.Marshal(rev)
	if err != nil {
		t.Fatal(err)
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
	rev, err = r.Revision(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(rev)
	if err != nil || string(before) != string(after) {
		t.Fatalf("empty revision changed on reopen: %s, %v", after, err)
	}
	var schema int
	if err := reopened.R.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&schema); err != nil || schema != 19 {
		t.Fatalf("schema17 compatibility: %d, %v", schema, err)
	}
	in := relationalInput(firstImportFixture(p), false)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	v := previewFixture(t, r, p, s)
	out, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil || out.Revision.SchemaVersion != RelationalSchemaVersion {
		t.Fatalf("empty reopened extension: %+v, %v", out, err)
	}
}

func relationalInput(in BeginImportInput, extension bool) BeginImportInput {
	in.Profile = RelationalProfile
	in.Manifest.Provider.Profiles = []string{RelationalProfile, GraphProfile}
	if in.GraphScope != nil {
		in.GraphScope.Profile = RelationalProfile
	}
	if extension {
		in.ProfileExtension = &ImportProfileExtension{FromProfile: GraphProfile, ToProfile: RelationalProfile}
	}
	return in
}

func TestRelationalProfileInitialAndDefault(t *testing.T) {
	for _, tc := range []struct{ name, profile string }{{"omitted foundation", ""}, {"relational initial", RelationalProfile}} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			in := firstImportFixture(p)
			wantProfile, wantSchema := GraphProfile, SchemaVersion
			if tc.profile != "" {
				in = relationalInput(in, false)
				wantProfile, wantSchema = RelationalProfile, RelationalSchemaVersion
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if s.Profile != wantProfile || s.ProfileExtension != nil {
				t.Fatalf("session selection: %+v", s)
			}
			v := previewFixture(t, r, p, s)
			if v.ModelSchemaVersion != wantSchema || v.ProfileExtension != nil {
				t.Fatalf("preview selection: %+v", v)
			}
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil || status.Session.Profile != wantProfile || status.Preview.ModelSchemaVersion != wantSchema {
				t.Fatalf("saved selection: %+v, %v", status, err)
			}
			out, err := commitFixture(t, r, p, s, v, "commit")
			if err != nil || out.Revision.SchemaVersion != wantSchema || out.Revision.SemanticHash != *v.CandidateHash {
				t.Fatalf("commit selection: %+v, %v", out, err)
			}
			graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
			if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Kind != "handler" || graph.Nodes[0].Ownership.Profile != GraphProfile {
				t.Fatalf("foundation assertion: %+v, %v", graph, err)
			}
		})
	}
	if SchemaVersion != "1" || !slices.Equal(SupportedModelSchemaVersions(), []string{"1", "2", "3", "4", "5"}) {
		t.Fatal("foundation schema constant or supported versions changed")
	}
	if !slices.Equal(SupportedNodeKindsForProfile(GraphProfile), SupportedNodeKinds()) || !slices.Equal(SupportedEdgeKindsForProfile(GraphProfile), SupportedEdgeKinds()) {
		t.Fatal("foundation kind contract changed")
	}
	if !slices.Contains(SupportedNodeKindsForProfile(RelationalProfile), "column") || !slices.Contains(SupportedEdgeKindsForProfile(RelationalProfile), "references") {
		t.Fatal("relational profile kinds missing")
	}
}

func extensionFixture(t *testing.T, r *Repo, p *Project, old *ImportSession) (*Project, *ImportSession) {
	t.Helper()
	in := relationalInput(repeatInput(p, old.RepositoryID), true)
	in.IdempotencyKey = "extension"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "extension-commit")
	return &out.Project, s
}

func profilePublishedState(t *testing.T, r *Repo, p *Project) string {
	t.Helper()
	state, err := loadSourceState(t.Context(), r.db.R, p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var bindings, sessions int64
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_identity_bindings WHERE project_id=?`, p.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_import_sessions WHERE project_id=?`, p.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	project, err := r.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalJSON([]any{project, state, bindings, sessions})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRelationalProfileTransition(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		relational bool
		change     func(*BeginImportInput)
	}{
		{name: "explicit extension"},
		{name: "same relational profile", relational: true, change: func(in *BeginImportInput) { in.ProfileExtension = nil }},
		{name: "missing extension", code: "backend_unsupported_scope", change: func(in *BeginImportInput) { in.ProfileExtension = nil }},
		{name: "repeated extension", relational: true, code: "backend_unsupported_scope"},
		{name: "implicit extra profile", code: "backend_incompatible_provider", change: func(in *BeginImportInput) {
			in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, "extra")
		}},
		{name: "provider namespace", code: "backend_incompatible_provider", change: func(in *BeginImportInput) { in.Manifest.Provider.Namespace = "other" }},
		{name: "provider version", code: "backend_incompatible_provider", change: func(in *BeginImportInput) { in.Manifest.Provider.Version = "2" }},
		{name: "provider method", code: "backend_incompatible_provider", change: func(in *BeginImportInput) { in.Manifest.Provider.Method = "agent" }},
		{name: "provider name", code: "backend_incompatible_provider", change: func(in *BeginImportInput) { in.Manifest.Provider.Name = "other" }},
		{name: "downgrade", relational: true, code: "backend_unsupported_scope", change: func(in *BeginImportInput) {
			in.Profile = GraphProfile
			in.GraphScope.Profile = GraphProfile
			in.ProfileExtension = nil
		}},
		{name: "sourced initial", code: "backend_reimport_unsupported", change: func(in *BeginImportInput) {
			in.Mode = "initial"
			in.RepositoryID = nil
			in.GraphScope = nil
			in.ProfileExtension = nil
		}},
		{name: "foreign repository", code: "backend_unsupported_scope", change: func(in *BeginImportInput) { in.RepositoryID = new("11111111-1111-4111-8111-111111111111") }},
		{name: "scope profile mismatch", code: "backend_unsupported_scope", change: func(in *BeginImportInput) { in.GraphScope.Profile = GraphProfile }},
		{name: "invalid extension", code: "backend_unsupported_scope", change: func(in *BeginImportInput) { in.ProfileExtension.FromProfile = RelationalProfile }},
		{name: "unsupported profile", code: "backend_unsupported_scope", change: func(in *BeginImportInput) { in.Profile = "future"; in.GraphScope.Profile = "future" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, p, old, first := committedBase(t)
			if tc.relational {
				p, old = extensionFixture(t, r, p, old)
			}
			before := profilePublishedState(t, r, p)
			in := relationalInput(repeatInput(p, old.RepositoryID), true)
			in.IdempotencyKey = "case"
			if tc.change != nil {
				tc.change(&in)
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if tc.code != "" {
				assertFault(t, err, tc.code)
				if after := profilePublishedState(t, r, p); after != before {
					t.Fatal("invalid transition mutated published state or sessions")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b := putFixture(t, r, p, s)
			if b.Identities[0].ID != first.Identities[0].ID {
				t.Fatal("extension changed foundation UUID")
			}
			v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err != nil || v.ModelSchemaVersion != RelationalSchemaVersion || (v.ProfileExtension != nil) != (in.ProfileExtension != nil) {
				t.Fatalf("extension preview: %+v, %v", v, err)
			}
			out, err := commitFixture(t, r, p, s, v, "case-commit")
			if err != nil || out.Revision.SchemaVersion != RelationalSchemaVersion {
				t.Fatalf("extension commit: %+v, %v", out, err)
			}
			g, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
			if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Ownership.Profile != GraphProfile || g.Nodes[0].ID != first.Identities[0].ID {
				t.Fatalf("foundation ownership or UUID changed: %+v, %v", g, err)
			}
		})
	}
}

func TestRelationalProfileInitialRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*BeginImportInput)
	}{
		{"missing foundation declaration", "backend_incompatible_provider", func(in *BeginImportInput) { in.Manifest.Provider.Profiles = []string{RelationalProfile} }},
		{"missing relational declaration", "backend_incompatible_provider", func(in *BeginImportInput) { in.Manifest.Provider.Profiles = []string{GraphProfile} }},
		{"extra declaration", "backend_incompatible_provider", func(in *BeginImportInput) {
			in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, "extra")
		}},
		{"initial extension", "backend_unsupported_scope", func(in *BeginImportInput) {
			in.ProfileExtension = &ImportProfileExtension{FromProfile: GraphProfile, ToProfile: RelationalProfile}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			before := profilePublishedState(t, r, p)
			in := relationalInput(firstImportFixture(p), false)
			tc.change(&in)
			_, err := r.BeginImport(t.Context(), p.ID, in)
			assertFault(t, err, tc.code)
			if profilePublishedState(t, r, p) != before {
				t.Fatal("refused initial profile published state")
			}
		})
	}
}

func TestRelationalProfileExtensionPublishesOnlyOnCommit(t *testing.T) {
	for _, action := range []string{"abort", "invalid batch", "rollback", "CAS conflict"} {
		t.Run(action, func(t *testing.T) {
			r, p, old, first := committedBase(t)
			in := relationalInput(repeatInput(p, old.RepositoryID), true)
			in.IdempotencyKey = "extension"
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			v := previewFixture(t, r, p, s)
			if action == "CAS conflict" {
				competing := repeatInput(p, old.RepositoryID)
				competing.IdempotencyKey = "competing"
				other, err := r.BeginImport(t.Context(), p.ID, competing)
				if err != nil {
					t.Fatal(err)
				}
				out := commitStaged(t, r, p, other, other.Version, "competing-commit")
				_, err = commitFixture(t, r, p, s, v, "extension-commit")
				assertFault(t, err, "backend_version_conflict")
				p = &out.Project
			} else if action == "abort" {
				_, err = r.AbortImport(t.Context(), p.ID, s.ID, AbortImportInput{ExpectedImportVersion: v.Version, IdempotencyKey: "abort"})
				if err != nil {
					t.Fatal(err)
				}
			} else if action == "invalid batch" {
				cs := fixtureCommands(s)
				cs[1].Evidence.Source.SnapshotID = old.SnapshotID
				h, err := ImportBatchHash(cs)
				if err != nil {
					t.Fatal(err)
				}
				_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "bad", ImportBatchInput{ExpectedImportVersion: v.Version, PayloadHash: h, Commands: cs})
				assertFault(t, err, "backend_import_invalid")
			} else {
				if _, err := r.db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_profile_publish BEFORE UPDATE ON backend_projects BEGIN SELECT RAISE(ABORT,'forced late publication failure'); END`); err != nil {
					t.Fatal(err)
				}
				if _, err := commitFixture(t, r, p, s, v, "extension-commit"); err == nil {
					t.Fatal("forced late rollback committed")
				}
				if _, err := r.db.W.ExecContext(t.Context(), `DROP TRIGGER fail_profile_publish`); err != nil {
					t.Fatal(err)
				}
			}
			current, err := r.Get(t.Context(), p.ID)
			if err != nil || current.CurrentRevisionID != p.CurrentRevisionID || current.Version != p.Version {
				t.Fatalf("failed extension changed head: %+v, %v", current, err)
			}
			state, err := loadRevisionState(t.Context(), r.db.R, p.ID, p.CurrentRevisionID)
			if err != nil || state.Revision.SchemaVersion != SchemaVersion || !slices.Equal(primarySource(*state).Provider.Profiles, []string{GraphProfile}) || state.Nodes[0].ID != first.Identities[0].ID {
				t.Fatalf("extension published early: %+v, %v", state, err)
			}
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil || status.Session.Profile != RelationalProfile || status.Session.ProfileExtension == nil {
				t.Fatalf("extension decision lost: %+v, %v", status, err)
			}
			if action != "abort" && status.Session.State != "ready" {
				t.Fatalf("failed extension changed session: %+v", status.Session)
			}
			if action == "rollback" {
				out, err := commitFixture(t, r, p, s, v, "extension-commit")
				if err != nil || out.Revision.SchemaVersion != RelationalSchemaVersion {
					t.Fatalf("rollback retry: %+v, %v", out, err)
				}
			}
			if action == "CAS conflict" {
				v, err = r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: v.Version, BaseRevisionID: p.CurrentRevisionID})
				if err != nil {
					t.Fatal(err)
				}
				out, err := commitFixture(t, r, p, s, v, "extension-commit")
				if err != nil || out.Revision.SchemaVersion != RelationalSchemaVersion {
					t.Fatalf("explicit new-base retry: %+v, %v", out, err)
				}
			}
		})
	}
}

func TestRelationalProfileB04ReceiptsAfterExtensionRestart(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	begin := firstImportFixture(p)
	s, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	commit := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	p = &out.Project
	abortBegin := repeatInput(p, s.RepositoryID)
	abortBegin.IdempotencyKey = "abort-begin"
	as, err := r.BeginImport(t.Context(), p.ID, abortBegin)
	if err != nil {
		t.Fatal(err)
	}
	abort := AbortImportInput{ExpectedImportVersion: as.Version, IdempotencyKey: "abort"}
	if _, err := r.AbortImport(t.Context(), p.ID, as.ID, abort); err != nil {
		t.Fatal(err)
	}
	// B0.4 has mode/graphScope, but no profile or extension, in receipts and saved sessions.
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_command_receipts SET response=json_remove(response,'$.profile','$.profileExtension') WHERE scope LIKE ?`, "import:"+p.ID+":%"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_import_sessions SET document=json_remove(document,'$.profile','$.profileExtension') WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_import_previews SET document=json_remove(document,'$.modelSchemaVersion','$.profileExtension') WHERE session_id=?`, s.ID); err != nil {
		t.Fatal(err)
	}
	var beginReceipt, abortReceipt, oldRevision, oldSource string
	for _, query := range []struct {
		sql  string
		args []any
		dest *string
	}{
		{`SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, []any{"import:" + p.ID + ":begin", begin.IdempotencyKey}, &beginReceipt},
		{`SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, []any{"import:" + p.ID + ":" + as.ID + ":abort", abort.IdempotencyKey}, &abortReceipt},
		{`SELECT document FROM backend_revisions WHERE id=?`, []any{out.Revision.ID}, &oldRevision},
		{`SELECT document FROM backend_revision_sources WHERE revision_id=?`, []any{out.Revision.ID}, &oldSource},
	} {
		if err := db.R.QueryRowContext(t.Context(), query.sql, query.args...).Scan(query.dest); err != nil {
			t.Fatal(err)
		}
	}
	p, _ = extensionFixture(t, r, p, s)
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
	replay, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	assertWireDocument(t, replay, beginReceipt)
	assertReceiptBytes(t, replay, beginReceipt)
	replay, err = r.AbortImport(t.Context(), p.ID, as.ID, abort)
	if err != nil {
		t.Fatal(err)
	}
	assertWireDocument(t, replay, abortReceipt)
	assertReceiptBytes(t, replay, abortReceipt)
	replayedCommit, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil || replayedCommit.Revision.SemanticHash != out.Revision.SemanticHash {
		t.Fatalf("commit replay: %+v, %v", replayedCommit, err)
	}
	replayedBatch := putFixture(t, r, &out.Project, s)
	if !slices.Equal(replayedBatch.Identities, b.Identities) {
		t.Fatal("batch identities changed")
	}
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil || status.Session.Profile != GraphProfile || status.Preview.ModelSchemaVersion != SchemaVersion {
		t.Fatalf("old session default: %+v, %v", status, err)
	}
	page, err := r.Imports(t.Context(), p.ID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == s.ID && item.Profile != GraphProfile {
			t.Fatalf("old list session default: %+v", item)
		}
	}
	var afterRevision, afterSource string
	if err := reopened.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revisions WHERE id=?`, out.Revision.ID).Scan(&afterRevision); err != nil {
		t.Fatal(err)
	}
	if err := reopened.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_sources WHERE revision_id=?`, out.Revision.ID).Scan(&afterSource); err != nil {
		t.Fatal(err)
	}
	if afterRevision != oldRevision || afterSource != oldSource {
		t.Fatal("schema1 revision/source bytes changed")
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(beginReceipt), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["mode"] == nil || fields["profile"] != nil {
		t.Fatal("test does not exercise B0.4 receipt shape")
	}
}

func assertReceiptBytes(t *testing.T, got any, expected string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != expected {
		t.Fatalf("receipt bytes changed\n got: %s\nwant: %s", b, expected)
	}
}
