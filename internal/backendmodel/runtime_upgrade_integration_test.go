package backendmodel

import (
	"encoding/json/v2"
	"log/slog"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func runtimeRelationalCommands(t *testing.T, p *Project, s *ImportSession, dialect string) []ImportCommand {
	t.Helper()
	cs := append(relationalFixture(t, p, s, dialect, "v1"), runtimeCommands(t, s)...)
	query := relationalCommand(cs, "query").Node
	query.Attributes["dialect"] = relationalRaw(t, dialect)
	e := &ImportEdge{ExternalKey: "query-read", Kind: "reads", FromKey: "query", ToKey: "column:orders:user_id", Attributes: runtimeAttrs(t, map[string]any{"accessMode": "read", "datastoreKey": "database:orders", "facetKey": "sql", "columnScope": "listed"}), EvidenceKeys: []string{"proof:query-read"}}
	cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: e})
	proof := *relationalCommand(cs, "proof:query-call").Evidence
	proof.ExternalKey, proof.SubjectKey = "proof:query-read", "query-read"
	cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	return cs
}

func runtimeRelationalFixture(t *testing.T, r *Repo, dialect string) (*ImportCommitResult, map[string]string) {
	t.Helper()
	p := createProject(t, r, "runtime-relational")
	in := runtimeProfileInput(relationalFixtureInput(t, p, dialect, "v1"), false)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cs := runtimeRelationalCommands(t, p, s, dialect)
	v, ids := stageRelational(t, r, p, s, cs, "runtime-relational")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "runtime-relational-commit")
	if err != nil {
		t.Fatal(err)
	}
	return out, ids
}

func TestRuntimeRelationalProposalEligibility(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := testRepo(t)
			out, ids := runtimeRelationalFixture(t, r, dialect)
			in := DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "tables"}
			page, err := r.QueryDatabase(t.Context(), out.Project.ID, in)
			if err != nil || len(page.TableItems) == 0 {
				t.Fatalf("source3 database %+v %v", page, err)
			}
			before := proposalSourceBytes(t, r)
			detail, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "source3-proposal"))
			if err != nil {
				t.Fatal(err)
			}
			apply := proposalApplyInput(t, r, detail, "source3-apply", proposalNullable(ids, "required", false))
			applied, err := r.ApplyProposal(t.Context(), out.Project.ID, detail.Proposal.ID, apply)
			if err != nil {
				t.Fatal(err)
			}
			graph, err := r.QueryGraph(t.Context(), out.Project.ID, GraphQueryInput{Proposal: &ProposalReadTarget{ProposalID: detail.Proposal.ID, ProposalRevisionID: applied.Revision.ID}, RecordType: "nodes", Kind: "flow"})
			if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].ID != ids["flow"] {
				t.Fatalf("flow inherited %+v %v", graph, err)
			}
			flow, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["flow"])
			if err != nil {
				t.Fatal(err)
			}
			want, _ := canonicalJSON(flow.Attributes)
			got, _ := canonicalJSON(graph.Nodes[0].Attributes)
			if string(want) != string(got) {
				t.Fatal("DB proposal changed inherited flow attrs")
			}
			invalid, err := r.PreviewProposal(t.Context(), out.Project.ID, detail.Proposal.ID, PreviewProposalInput{ExpectedVersion: applied.Proposal.Version, DraftRevisionID: applied.Revision.ID, Commands: []ProposalCommand{{Type: "alter_column", CommandID: "flow-edit", Reason: "invalid flow edit", ColumnID: ids["flow"], Nullable: new(false)}}})
			if err == nil && (invalid.CandidateHash != nil || len(invalid.Diagnostics) == 0) {
				t.Fatal("DB proposal accepted flow target")
			}
			assertProposalSourceBytes(t, r, before)
		})
	}
}

func TestRuntimeUpgradePreservesOldDocumentsAndReceipts(t *testing.T) {
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	p := &out.Project
	createProject(t, r, "old-schema1")
	proposalIn := proposalCreateInput(out, ids, "old-proposal")
	proposal, err := r.CreateProposal(t.Context(), p.ID, proposalIn)
	if err != nil {
		t.Fatal(err)
	}
	var oldSessionID string
	if err := db.R.QueryRowContext(t.Context(), `SELECT id FROM backend_import_sessions WHERE project_id=? AND state='committed'`, p.ID).Scan(&oldSessionID); err != nil {
		t.Fatal(err)
	}
	old, err := loadSession(t.Context(), db.R, p.ID, oldSessionID)
	if err != nil {
		t.Fatal(err)
	}
	openIn := relationalReconcileInput(t, p, old.RepositoryID, "sqlite", "v1", "old-pending")
	pending, err := r.BeginImport(t.Context(), p.ID, openIn)
	if err != nil {
		t.Fatal(err)
	}
	cs := relationalFixture(t, p, pending, "sqlite", "v1")
	hash, err := ImportBatchHash(cs)
	if err != nil {
		t.Fatal(err)
	}
	batchIn := ImportBatchInput{ExpectedImportVersion: pending.Version, PayloadHash: hash, Commands: cs}
	batch, err := r.PutImportBatch(t.Context(), p.ID, pending.ID, "pending-batch", batchIn)
	if err != nil {
		t.Fatal(err)
	}
	before := proposalSourceBytes(t, r)
	proposalRaw, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeProfileInput(relationalReconcileInput(t, p, old.RepositoryID, "sqlite", "v1", "runtime-extension"), true)
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"Only provider extension; assertions retain prior source proof"}
	for i := range in.Inventory {
		if in.Inventory[i].Category == "datastores" {
			in.Inventory[i].Status = "partial"
			in.Inventory[i].KnownCount = 0
			in.Inventory[i].Denominator = nil
			in.Inventory[i].Gaps = []string{"Datastore not reobserved"}
		}
	}
	extension, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	next := commitStaged(t, r, p, extension, extension.Version, "runtime-extension-commit")
	if next.Revision.SchemaVersion != RuntimeSchemaVersion {
		t.Fatal("extension didn't select schema3")
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
	version, err := reopened.SchemaVersion(t.Context())
	if err != nil || version != 22 {
		t.Fatalf("unexpected migration %d %v", version, err)
	}
	after := proposalSourceBytes(t, r)
	for _, table := range []string{"revisions", "graph", "sources", "receipts"} {
		for _, row := range before[table] {
			if !slices.Contains(after[table], row) {
				t.Fatalf("immutable %s row rewritten", table)
			}
		}
	}
	replay, err := r.BeginImport(t.Context(), p.ID, openIn)
	if err != nil || replay.ID != pending.ID {
		t.Fatalf("old begin replay %+v %v", replay, err)
	}
	var receipt string
	if err := reopened.R.QueryRowContext(t.Context(), `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, "import:"+p.ID+":begin", openIn.IdempotencyKey).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	assertReceiptBytes(t, replay, receipt)
	b, err := r.PutImportBatch(t.Context(), p.ID, pending.ID, "pending-batch", batchIn)
	if err != nil || !slices.Equal(batch.Identities, b.Identities) {
		t.Fatalf("old batch replay %+v %v", b, err)
	}
	if _, err := r.AbortImport(t.Context(), p.ID, pending.ID, AbortImportInput{ExpectedImportVersion: batch.AcceptedVersion, IdempotencyKey: "abort-old-pending"}); err != nil {
		t.Fatal(err)
	}
	replayedProposal, err := r.CreateProposal(t.Context(), p.ID, proposalIn)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(replayedProposal)
	if err != nil || string(raw) != string(proposalRaw) {
		t.Fatal("old source2 proposal receipt changed")
	}
	down := relationalReconcileInput(t, &next.Project, old.RepositoryID, "sqlite", "v1", "downgrade")
	_, err = r.BeginImport(t.Context(), p.ID, down)
	assertFault(t, err, "backend_unsupported_scope")
}
