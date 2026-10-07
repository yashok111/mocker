package backendmodel

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func source6ArtifactBaseline(t *testing.T, r *Repo, source *ImportCommitResult) *ImportCommitResult {
	t.Helper()
	in := eventsProfileInput(lineageOrdersInput(t, &source.Project), true)
	in.Mode = "reconcile"
	in.RepositoryID = new(source.Project.Repositories[0].ID)
	in.GraphScope = &GraphScope{Profile: EventsProfile, Status: "partial", Gaps: []string{"Fixture extension"}}
	in.IdempotencyKey = "artifact-source5"
	session, err := r.BeginImport(t.Context(), source.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := stageRelational(t, r, &source.Project, session, lineageOrdersCommands(t, session), "artifact-source5")
	if preview.State != "ready" {
		t.Fatalf("source5 extension: %+v", preview)
	}
	base, err := commitFixture(t, r, &source.Project, session, preview, "artifact-source5-commit")
	if err != nil {
		t.Fatal(err)
	}
	next := source6Input(t, &base.Project)
	next.Manifest.RepositoryName = "artifact-second-repository"
	next.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	next.IdempotencyKey = "artifact-source6"
	_, out := commitSource6Fixture(t, r, &base.Project, next)
	graph, err := r.ResolveSourceGraph(t.Context(), source.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.SourceVector.Partitions) != 2 || len(graph.LegacyProofBases) == 0 || len(graph.Assertions) < 2 {
		t.Fatal("fixture requires two partitions and genuine legacy proof")
	}
	return out
}

func source6ArtifactResolvedProvider(t *testing.T, r *Repo, base *ImportCommitResult) *ImportCommitResult {
	t.Helper()
	graph, err := r.ResolveSourceGraph(t.Context(), base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var target BaseAssertionRef
	for _, claim := range graph.Assertions {
		if claim.ExternalKey == "handler" && claim.Owner.RepositoryID != base.Project.Repositories[0].ID {
			target = sourceAssertionRef(claim)
		}
	}
	if target.ExpectedID == "" {
		t.Fatal("missing exact fixture handler claim")
	}
	in := source6Input(t, &base.Project)
	in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: target.RepositoryID}
	in.Manifest.RepositoryName = "artifact-second-repository"
	in.Manifest.Provider.Namespace = "artifact-second-provider"
	in.IdempotencyKey = "artifact-second-provider"
	session, err := r.BeginImport(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(session)
	commands[0].Node.Name = "Another independently asserted name"
	commands = append([]ImportCommand{{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{
		DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target,
		Reason: "Known shared handler", EvidenceKeys: []string{"proof"},
	}}}, commands...)
	batch := sendCommands(t, r, &base.Project, session, 1, "artifact-conflict", commands...)
	preview, err := r.PreviewImport(t.Context(), base.Project.ID, session.ID, PreviewImportInput{
		ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: base.Revision.ID,
	})
	if err != nil || preview.State != "needs_resolution" {
		t.Fatalf("expected name conflict: %+v %v", preview, err)
	}
	page, err := r.ImportChanges(t.Context(), base.Project.ID, session.ID, ImportChangesInput{
		PreviewVersion: preview.Version, RecordType: "assertion_conflict",
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("expected exact conflict detail: %+v %v", page, err)
	}
	conflict := page.Items[0].AssertionConflict
	choice := conflict.Contenders[0]
	resolution := SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: "node", ID: target.ExpectedID,
		Property: conflict.Property, ConflictHash: conflict.ConflictHash, Reason: "Explicit existing claim selection",
		Select: SourceAssertionSelection{RepositoryID: choice.Owner.RepositoryID, ProviderNamespace: choice.Owner.ProviderNamespace, AssertionHash: choice.AssertionHash}}
	batch = sendCommands(t, r, &base.Project, session, preview.Version, "artifact-resolution", ImportCommand{Op: "resolve_assertion", Resolution: &resolution})
	out := commitStaged(t, r, &base.Project, session, batch.AcceptedVersion, "artifact-resolved")
	graph, err = r.ResolveSourceGraph(t.Context(), base.Project.ID, out.Revision.ID)
	if err != nil || len(graph.Selections) != 1 || len(graph.Assertions) < 3 {
		t.Fatalf("fixture lost selection or losing claim: %v", err)
	}
	return out
}

func source6ArtifactDocuments(t *testing.T, r *Repo, rid string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range []string{
		"backend_graph_records", "backend_revision_sources", "backend_revision_decisions",
		"backend_revision_assertions", "backend_revision_assertion_resolutions", "backend_revision_legacy_proof_bases",
	} {
		out[table] = source6ArtifactTableDocuments(t, r, table, rid)
	}
	return out
}

func source6ArtifactTableDocuments(t *testing.T, r *Repo, table, rid string) []string {
	t.Helper()
	// Store27 (48dce80, B6.3): the raw bytes live behind the owner's _documents
	// view; ORDER BY still sorts the exact stored bytes, never a re-encoding.
	rows, err := r.db.R.QueryContext(t.Context(), "SELECT document FROM "+table+"_documents WHERE revision_id=? ORDER BY document", rid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var document string
		if err := rows.Scan(&document); err != nil {
			t.Fatal(err)
		}
		out = append(out, document)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSource6ArtifactPinRawCopyClearAndReplay(t *testing.T) {
	t.Parallel()
	s, old, ids, scenario := artifactServiceFixture(t)
	base := source6ArtifactBaseline(t, s.repo, old)
	base = source6ArtifactResolvedProvider(t, s.repo, base)
	before := source6ArtifactDocuments(t, s.repo, base.Revision.ID)
	owners := artifactOwnerRows(t, s.repo)
	pinned, request := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, scenario), "source6-scenario-pin")
	if got := source6ArtifactDocuments(t, s.repo, pinned.Revision.ID); !reflect.DeepEqual(before, got) {
		t.Fatal("pin-only successor did not copy every raw source document")
	}
	graph, err := s.repo.ResolveSourceGraph(t.Context(), base.Project.ID, pinned.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if graph.State.ArtifactContext.SourceContentHash != graph.SourceContentHash {
		t.Fatal("artifact pin used a graph-only source hash, dropping provider claims")
	}
	rebuilt, err := source6SemanticHash(graph)
	if err != nil || rebuilt != pinned.Revision.SemanticHash {
		t.Fatalf("source6 semantic hash does not rebuild after pin: %s %v", rebuilt, err)
	}
	remove := PreviewArtifactPinsInput{BaseRevisionID: pinned.Revision.ID, ExpectedVersion: pinned.Project.Version,
		Commands: []ArtifactPinCommand{{Type: "remove_artifact_pin", Artifact: artifactKey(pinned.Revision.ArtifactPins[0]), Reason: "Remove explicit association"}}}
	unavailable := NewArtifactService(s.repo, nil, nil)
	cleared, _ := applyArtifactTest(t, unavailable, base.Project.ID, remove, "source6-scenario-clear")
	if cleared.Revision.SemanticHash != base.Revision.SemanticHash || len(cleared.Revision.ArtifactPins) != 0 {
		t.Fatal("clear lost exact source-only semantic anchor")
	}
	if got := source6ArtifactDocuments(t, s.repo, cleared.Revision.ID); !reflect.DeepEqual(before, got) {
		t.Fatal("clear changed raw source context")
	}
	replay, err := unavailable.Apply(t.Context(), base.Project.ID, request)
	if err != nil || !bytes.Equal(pinned.ReceiptBytes(), replay.ReceiptBytes()) {
		t.Fatalf("exact replay required available owners/current CAS: %v", err)
	}
	if !bytes.Equal(owners, artifactOwnerRows(t, s.repo)) {
		t.Fatal("pin/clear changed artifact owner documents")
	}
	path := s.repo.db.Path()
	if err := s.repo.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := NewRepo(reopened)
	if got := source6ArtifactDocuments(t, restarted, pinned.Revision.ID); !reflect.DeepEqual(before, got) {
		t.Fatal("historical source documents changed after restart")
	}
	replay, err = NewArtifactService(restarted, nil, nil).Apply(t.Context(), base.Project.ID, request)
	if err != nil || !bytes.Equal(pinned.ReceiptBytes(), replay.ReceiptBytes()) {
		t.Fatalf("exact receipt changed after restart: %v", err)
	}
	graph, err = restarted.ResolveSourceGraph(t.Context(), base.Project.ID, pinned.Revision.ID)
	if err != nil || len(graph.LegacyProofBases) == 0 || len(graph.Selections) != 1 {
		t.Fatalf("historical proof/selection did not survive restart: %v", err)
	}
}

func TestSource6ArtifactCopyFailureRollsBackAllRows(t *testing.T) {
	for _, table := range []string{"backend_revision_assertions", "backend_revision_assertion_resolutions", "backend_revision_legacy_proof_bases"} {
		t.Run(table, func(t *testing.T) {
			s, old, ids, scenario := artifactServiceFixture(t)
			base := source6ArtifactBaseline(t, s.repo, old)
			base = source6ArtifactResolvedProvider(t, s.repo, base)
			in := scenarioSet(base, ids, scenario)
			preview, err := s.Preview(t.Context(), base.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			before := source6ArtifactRowCounts(t, s.repo)
			if _, err := s.repo.db.W.ExecContext(t.Context(), "CREATE TRIGGER reject_source_copy BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT,'injected source copy failure'); END"); err != nil {
				t.Fatal(err)
			}
			_, err = s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, preview.CandidateHash, "copy-failure"})
			if err == nil {
				t.Fatal("injected copy failure committed")
			}
			project, err := s.repo.Get(t.Context(), base.Project.ID)
			if err != nil || project.CurrentRevisionID != base.Revision.ID || project.Version != base.Project.Version {
				t.Fatalf("failed copy advanced project: %v", err)
			}
			if !reflect.DeepEqual(before, source6ArtifactRowCounts(t, s.repo)) {
				t.Fatal("failed copy left revision, claims, context or receipt rows")
			}
		})
	}
}

func source6ArtifactRowCounts(t *testing.T, r *Repo) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{"backend_revisions", "backend_graph_records", "backend_revision_sources",
		"backend_revision_decisions", "backend_revision_assertions", "backend_revision_assertion_resolutions",
		"backend_revision_legacy_proof_bases", "backend_revision_api_artifacts", "backend_command_receipts"} {
		var count int
		if err := r.db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		out[table] = count
	}
	return out
}

func TestSource6ArtifactRawBaselineCAS(t *testing.T) {
	t.Parallel()
	s, old, ids, scenario := artifactServiceFixture(t)
	base := source6ArtifactBaseline(t, s.repo, old)
	in := scenarioSet(base, ids, scenario)
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	s.scenarios = artifactMutatingScenarioReader{s.scenarios, func() {
		// Simulate a storage fault while the owner is read, after the source RO
		// snapshot closes but before the pin transaction starts. Store27
		// (48dce80, B6.3) seals the payload in an immutable blob, so the raw
		// bytes change only through the Store26 fixture rebuild + production
		// migration; the CAS still sees different raw bytes for the base.
		if err := testkit.EditLegacyBackendFixture(t.Context(), s.repo.db, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(t.Context(), "DROP TRIGGER backend_assertions_update"); err != nil {
				return err
			}
			_, err := tx.ExecContext(t.Context(), "UPDATE backend_revision_assertions SET document=char(10)||document WHERE revision_id=?", base.Revision.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}}
	_, err = s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, preview.CandidateHash, "source6-raw-cas"})
	assertFault(t, err, "backend_artifact_pins_base_conflict")
	project, err := s.repo.Get(t.Context(), base.Project.ID)
	if err != nil || project.CurrentRevisionID != base.Revision.ID || project.Version != base.Project.Version {
		t.Fatalf("changed raw source was published: %v", err)
	}
}

func TestSource6ArtifactLegacyAPIWrapperCopiesClaims(t *testing.T) {
	t.Parallel()
	s, old, ids, api := apiPinFixture(t)
	base := source6ArtifactBaseline(t, s.repo, old)
	before := source6ArtifactDocuments(t, s.repo, base.Revision.ID)
	pinned, request := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "source6-api-pin")
	if got := source6ArtifactDocuments(t, s.repo, pinned.Revision.ID); !reflect.DeepEqual(before, got) {
		t.Fatal("API pin lost raw source documents")
	}
	graph, err := s.repo.ResolveSourceGraph(t.Context(), base.Project.ID, pinned.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := source6SemanticHash(graph)
	if err != nil || rebuilt != pinned.Revision.SemanticHash {
		t.Fatalf("API wrapper semantic hash: %s %v", rebuilt, err)
	}
	remove := PreviewAPIPinsInput{BaseRevisionID: pinned.Revision.ID, ExpectedVersion: pinned.Project.Version,
		Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: strconv.FormatInt(api.Design.ID, 10), Reason: "Detach"}}}
	unavailable := NewAPIArtifactService(s.repo, nil)
	cleared, _ := applyPinTest(t, unavailable, base.Project.ID, remove, "source6-api-clear")
	if cleared.Revision.SemanticHash != base.Revision.SemanticHash {
		t.Fatal("API clear lost source-only anchor")
	}
	replay, err := unavailable.Apply(t.Context(), base.Project.ID, request)
	if err != nil || !bytes.Equal(pinned.ReceiptBytes(), replay.ReceiptBytes()) {
		t.Fatalf("API exact replay: %v", err)
	}
}
