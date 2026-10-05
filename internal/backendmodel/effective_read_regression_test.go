package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"reflect"
	"testing"
)

func TestEffectiveFixEffectiveNativeIdentityOriginsRoundTrip(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "review-source-origin")
	_, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	page, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: base.Revision.ID, RecordType: "nodes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Identities) == 0 {
		t.Fatal("fixture has no qualified identity")
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decoded GraphPage
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("native source6 graph cannot decode its own strict identity origin: %v; selector=%+v", err, page.Identities[0].Origin.Selector)
	}
}
func TestEffectiveFixEffectiveRenamedEdgePublicProjection(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveRepresentationFixture(t)
	source, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	edge := source.State.Edges[0]
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Rename edge", BaseRevisionID: base.Revision.ID, IdempotencyKey: "review-edge"})
	if err != nil {
		t.Fatal(err)
	}
	const name = "review-only-desired-edge-name"
	next, _ := saveChange(t, r, draft, "review-edge-rename", changeMapCommand(t, "rename", map[string]any{"recordType": "edge", "id": edge.ID, "name": name}))
	target := &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: target})
	if err != nil || graph.EdgeNames[edge.ID] != name {
		t.Fatalf("fixture lost rename: %+v %v", graph, err)
	}
	page, err := r.QueryGraph(t.Context(), base.Project.ID, GraphQueryInput{ChangeProposal: target, RecordType: "edges", ID: edge.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(name)) {
		t.Fatalf("persisted desired edge name is absent from the exact public graph response: %s", raw)
	}
}
func TestEffectiveFixEffectiveCoverageRetainsReconciliationMetadata(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "review-coverage")
	first, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	input := source6Input(t, &base.Project)
	input.IdempotencyKey = "review-partial"
	input.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: first.RepositoryID, ProviderNamespace: first.Manifest.Provider.Namespace}
	input.ScopeStatus = &SourceScopeStatus{Status: "partial", Gaps: []string{"Review fixture not reobserved"}}
	session, err := r.BeginImport(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: base.Revision.ID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("preview %+v %v", preview, err)
	}
	next, err := r.CommitImport(t.Context(), p.ID, session.ID, CommitImportInput{ExpectedVersion: base.Project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "review-partial-commit"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := r.RevisionCoverage(t.Context(), p.ID, next.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.ReconciliationGaps) == 0 {
		t.Fatal("fixture did not produce reconciliation gaps")
	}
	public, err := r.ReadCoverage(t.Context(), p.ID, BackendReadTarget{RevisionID: next.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	fullDraft, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Native coverage", BaseRevisionID: next.Revision.ID, IdempotencyKey: "native-coverage-full"})
	if err != nil {
		t.Fatal(err)
	}
	full, err := r.ReadCoverage(t.Context(), p.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: fullDraft.Proposal.ID, ProposalRevisionID: fullDraft.Revision.ID}})
	if err != nil || !reflect.DeepEqual(full.ReconciliationGaps, stored.ReconciliationGaps) || full.StaleCounts != stored.StaleCounts {
		t.Fatalf("native full coverage erased metadata: %+v %v", full, err)
	}
	if !reflect.DeepEqual(public.ReconciliationGaps, stored.ReconciliationGaps) || public.StaleCounts != stored.StaleCounts {
		t.Fatalf("effective coverage erased persisted metadata: stored gaps=%v counts=%+v; public gaps=%v counts=%+v", stored.ReconciliationGaps, stored.StaleCounts, public.ReconciliationGaps, public.StaleCounts)
	}
}
func TestEffectiveFixSavedV2RejectsUnsupportedSourceFlow(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "review-source1-view")
	if _, err := r.QueryFlow(t.Context(), p.ID, FlowQueryInput{RevisionID: p.CurrentRevisionID, View: "entrypoints"}); err == nil {
		t.Fatal("fixture source1 unexpectedly supports Flow")
	}
	saved, err := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Unsupported flow", Target: BackendReadTarget{RevisionID: p.CurrentRevisionID}, State: savedV2FlowState(), IdempotencyKey: "review-source1-save"})
	if err == nil {
		t.Fatalf("saved a Flow view whose exact source rejects Flow reads: %+v", saved)
	}
}
func TestEffectiveFixEffectiveUnchangedSourceFiveKeepsSelectedFacetProof(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "review-desired-fk")
	input := relationalFixtureInput(t, p, "postgresql", "v1")
	input.Profile = EventsProfile
	input.Manifest.Provider.Profiles = []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile}
	session, err := r.BeginImport(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	preview, ids := stageRelational(t, r, p, session, relationalFixture(t, p, session, "postgresql", "v1"), "review-source5")
	if preview.State != "ready" {
		t.Fatalf("fixture %+v", preview.Diagnostics)
	}
	base, err := commitFixture(t, r, p, session, preview, "review-source5-commit")
	if err != nil {
		t.Fatal(err)
	}
	baselinePage, err := r.QueryDatabase(t.Context(), p.ID, DatabaseQueryInput{RevisionID: base.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range baselinePage.RelationshipItems {
		if item.ConstraintID == ids["constraint:orders:user_fk"] {
			t.Logf("source5 baseline relationship status=%s basis=%v", item.Status, item.TargetCardinality.Basis)
		}
	}
	draft, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Desired nullable", BaseRevisionID: base.Revision.ID, IdempotencyKey: "review-fk"})
	if err != nil {
		t.Fatal(err)
	}
	emptyPage, err := r.QueryDatabase(t.Context(), p.ID, DatabaseQueryInput{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range emptyPage.RelationshipItems {
		if item.ConstraintID == ids["constraint:orders:user_fk"] {
			t.Logf("unchanged full source5 status=%s basis=%v", item.Status, item.TargetCardinality.Basis)
			graph, err := r.ResolveEffectiveGraph(t.Context(), p.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}})
			if err != nil {
				t.Fatal(err)
			}
			for _, origin := range graph.Origins {
				if origin.SubjectID == item.EdgeID && origin.Selector.Source != nil {
					t.Logf("FK origin selector=%+v evidence=%v freshness=%+v", *origin.Selector.Source, origin.EvidenceIDs, origin.Freshness)
				}
			}
			for _, before := range baselinePage.RelationshipItems {
				if before.EdgeID == item.EdgeID && (len(item.EvidenceIDs) == 0 || !reflect.DeepEqual(before.TargetCardinality.Min, item.TargetCardinality.Min) || !reflect.DeepEqual(before.TargetCardinality.Max, item.TargetCardinality.Max)) {
					t.Fatalf("unchanged full source5 lost selected facet proof/cardinality: baseline=%s full=%s cardinality before=%+v after=%+v", before.Status, item.Status, before.TargetCardinality, item.TargetCardinality)
				}
			}
		}
	}
}

func effectiveFiveRelationalFixture(t *testing.T) (*Repo, *ImportCommitResult, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "source5-proof-matrix")
	input := relationalFixtureInput(t, p, "postgresql", "v1")
	input.Profile = EventsProfile
	input.Manifest.Provider.Profiles = []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile}
	session, err := r.BeginImport(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	preview, ids := stageRelational(t, r, p, session, relationalFixture(t, p, session, "postgresql", "v1"), "source5")
	base, err := commitFixture(t, r, p, session, preview, "source5-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, base, ids
}
func TestEffectiveFixSelectedFacetParityAndNullableIntent(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Facet parity", BaseRevisionID: base.Revision.ID, IdempotencyKey: "parity"})
	if err != nil {
		t.Fatal(err)
	}
	target := &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}
	for _, facet := range []string{"sql", "orm"} {
		in := DatabaseQueryInput{RevisionID: base.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: facet, RecordType: "relationships"}
		baseline, err := r.QueryDatabase(t.Context(), base.Project.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		in.RevisionID = ""
		in.ChangeProposal = target
		full, err := r.QueryDatabase(t.Context(), base.Project.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(baseline.RelationshipItems, full.RelationshipItems) {
			t.Fatalf("%s selected proof/cardinality parity lost: before=%+v after=%+v", facet, baseline.RelationshipItems, full.RelationshipItems)
		}
	}
	column := ids["column:orders:user_id"]
	next, _ := saveChange(t, r, draft, "nullable", changeMapCommand(t, "alter_column", map[string]any{"columnId": column, "facetKey": "sql", "change": map[string]any{"group": "nullable", "nullable": map[string]any{"status": "known", "value": true}}}))
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := effectivePropertyProof(graph, "node", column, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "nullable"})
	if err != nil || proof.status != "desired" || len(proof.evidenceIDs) != 0 {
		t.Fatalf("nullable intent gained baseline proof: %+v %v", proof, err)
	}
	page, err := r.QueryDatabase(t.Context(), base.Project.ID, DatabaseQueryInput{ChangeProposal: graph.Target.ChangeProposal, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.RelationshipItems {
		if item.EdgeID != ids["references:orders:user_fk"] {
			continue
		}
		if item.Status != "desired" || item.RuntimeStatus != "unverified" || item.TargetCardinality.Min == nil || *item.TargetCardinality.Min != 0 {
			t.Fatalf("desired cardinality mislabeled: %+v", item)
		}
		for _, basis := range item.TargetCardinality.Basis {
			if bytes.Contains([]byte(basis), []byte("Selected source column "+column+" nullable")) {
				t.Fatalf("changed nullable attributed imported proof: %s", basis)
			}
		}
	}
}

func TestEffectiveFixForeignKeyIntentDoesNotReuseSelectedProof(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Desired FK", BaseRevisionID: base.Revision.ID, IdempotencyKey: "fk-intent"})
	if err != nil {
		t.Fatal(err)
	}
	definition := changeDesiredFacet(map[string]any{"constraintKind": "foreign_key", "columnIds": []string{ids["column:orders:tenant_id"], ids["column:orders:user_id"]}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false), "reference": map[string]any{"id": ids["references:orders:user_fk"], "targetTableId": ids["table:users"], "columnPairs": []any{map[string]any{"fromColumnId": ids["column:orders:tenant_id"], "toColumnId": ids["column:users:tenant_id"]}, map[string]any{"fromColumnId": ids["column:orders:user_id"], "toColumnId": ids["column:users:id"]}}, "updateAction": known("restrict"), "deleteAction": known("restrict"), "matchType": known("simple")}})
	next, _ := saveChange(t, r, draft, "desired-fk", changeMapCommand(t, "alter_constraint", map[string]any{"action": "update", "constraintId": ids["constraint:orders:user_fk"], "facetKey": "sql", "definition": definition}))
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := effectivePropertyProof(graph, "edge", ids["references:orders:user_fk"], TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "updateAction"})
	if err != nil || sql.status != "desired" || len(sql.evidenceIDs) != 0 {
		t.Fatalf("changed FK gained old proof: %+v %v", sql, err)
	}
	orm, err := effectivePropertyProof(graph, "edge", ids["references:orders:user_fk"], TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "orm", Group: "updateAction"})
	if err != nil || orm.status == "desired" || len(orm.evidenceIDs) == 0 {
		t.Fatalf("neighbor facet was overwritten: %+v %v", orm, err)
	}
	for _, eid := range orm.evidenceIDs {
		for _, proof := range graph.Source.State.Evidence {
			if proof.ID == eid && proof.PropertyPath != nil && *proof.PropertyPath != "/attributes/facets/orm" {
				t.Fatalf("borrowed adjacent facet proof: %s", *proof.PropertyPath)
			}
		}
	}
	page, err := r.QueryDatabase(t.Context(), base.Project.ID, DatabaseQueryInput{ChangeProposal: graph.Target.ChangeProposal, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.RelationshipItems {
		if item.EdgeID == ids["references:orders:user_fk"] && (item.Status != "desired" || len(item.EvidenceIDs) != 0 || item.RuntimeStatus != "unverified") {
			t.Fatalf("desired FK mislabeled imported: %+v", item)
		}
	}
}

func TestEffectiveFixEdgeNamePagingAndHistoricalTargets(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveRepresentationFixture(t)
	source, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Names", BaseRevisionID: base.Revision.ID, IdempotencyKey: "names"})
	if err != nil {
		t.Fatal(err)
	}
	edge := source.State.Edges[0]
	next, _ := saveChange(t, r, draft, "first-name", changeMapCommand(t, "rename", map[string]any{"recordType": "edge", "id": edge.ID, "name": "Original desired name"}))
	_, _ = saveChange(t, r, next, "later-name", changeMapCommand(t, "rename", map[string]any{"recordType": "edge", "id": edge.ID, "name": "Later desired name"}))
	in := GraphQueryInput{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}, RecordType: "edges", Limit: 1}
	count := 0
	for {
		page, err := r.QueryGraph(t.Context(), base.Project.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range page.EdgeNames {
			if len(page.Edges) != 1 || page.Edges[0].ID != name.ID || name.Name != "Original desired name" {
				t.Fatalf("page name drift: %+v", page)
			}
			count++
		}
		if page.NextCursor == "" {
			break
		}
		in.Cursor = page.NextCursor
	}
	if count != 1 {
		t.Fatalf("name sidecar count %d", count)
	}
	nodes, err := r.QueryGraph(t.Context(), base.Project.ID, GraphQueryInput{ChangeProposal: in.ChangeProposal, RecordType: "nodes"})
	if err != nil || len(nodes.EdgeNames) != 0 {
		t.Fatalf("edge names escaped edge page: %+v %v", nodes, err)
	}
}

func TestEffectiveFixSavedV2ProfileAdmissionMatrix(t *testing.T) {
	profiles := []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile}
	for version, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "saved-profile")
			datastore, repository := "", ""
			if version > 0 {
				input := relationalFixtureInput(t, p, "postgresql", "v1")
				input.Profile = profile
				input.Manifest.Provider.Profiles = profiles[:version+1]
				session, err := r.BeginImport(t.Context(), p.ID, input)
				if err != nil {
					t.Fatal(err)
				}
				preview, ids := stageRelational(t, r, p, session, relationalFixture(t, p, session, "postgresql", "v1"), "matrix")
				base, err := commitFixture(t, r, p, session, preview, "matrix-commit")
				if err != nil {
					t.Fatal(err)
				}
				p = &base.Project
				datastore, repository = ids["database:orders"], session.RepositoryID
			}
			_, readErr := r.QueryFlow(t.Context(), p.ID, FlowQueryInput{RevisionID: p.CurrentRevisionID, View: "entrypoints"})
			_, saveErr := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Flow", Target: BackendReadTarget{RevisionID: p.CurrentRevisionID}, State: savedV2FlowState(), IdempotencyKey: "flow"})
			if (readErr == nil) != (saveErr == nil) || (version < 2 && saveErr == nil) {
				t.Fatalf("source%d read/save admission differ: %v %v", version+1, readErr, saveErr)
			}
			if version > 0 {
				state := SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: datastore, FacetKey: "sql"}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
				if _, err := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Database", Target: BackendReadTarget{RevisionID: p.CurrentRevisionID}, State: state, IdempotencyKey: "db"}); err != nil {
					t.Fatal(err)
				}
				legacy, err := r.CreateProposal(t.Context(), p.ID, CreateProposalInput{Name: "Legacy", BaseRevisionID: p.CurrentRevisionID, RepositoryID: repository, DatastoreID: datastore, FacetKey: "sql", IdempotencyKey: "legacy"})
				if err != nil {
					t.Fatal(err)
				}
				target := BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: legacy.Proposal.ID, ProposalRevisionID: legacy.Revision.ID}}
				if _, err := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Legacy db", Target: target, State: state, IdempotencyKey: "legacy-db"}); err != nil {
					t.Fatal(err)
				}
				if _, err := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Legacy flow", Target: target, State: savedV2FlowState(), IdempotencyKey: "legacy-flow"}); err == nil {
					t.Fatal("legacy Flow admitted")
				}
			}
			if version < 4 {
				return
			}
			full, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Full", BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "full"})
			if err != nil {
				t.Fatal(err)
			}
			target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: full.Proposal.ID, ProposalRevisionID: full.Revision.ID}}
			if _, err := r.QueryFlow(t.Context(), p.ID, FlowQueryInput{ChangeProposal: target.ChangeProposal, View: "entrypoints"}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Full flow", Target: target, State: savedV2FlowState(), IdempotencyKey: "full-flow"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	r, base, _ := effectiveRepresentationFixture(t)
	if _, err := r.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Native6", Target: BackendReadTarget{RevisionID: base.Revision.ID}, State: savedV2FlowState(), IdempotencyKey: "native6"}); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveFixFullCoveragePreservesNonzeroStoredStaleCounts(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "coverage-stale-source5")
	in := firstImportFixture(p)
	in.Profile = EventsProfile
	in.Manifest.Provider.Profiles = []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile}
	first, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, first)
	base := commitStaged(t, r, p, first, b.AcceptedVersion, "baseline")
	repeat := repeatInput(&base.Project, first.RepositoryID)
	repeat.IdempotencyKey = "repeat-stale"
	repeat.Profile = EventsProfile
	repeat.Manifest.Provider.Profiles = in.Manifest.Provider.Profiles
	repeat.GraphScope.Profile = EventsProfile
	second, err := r.BeginImport(t.Context(), base.Project.ID, repeat)
	if err != nil {
		t.Fatal(err)
	}
	stale := commitStaged(t, r, &base.Project, second, second.Version, "stale")
	stored, err := r.RevisionCoverage(t.Context(), p.ID, stale.Revision.ID)
	if err != nil || stored.StaleCounts.Nodes == 0 {
		t.Fatalf("no genuine stale counts: %+v %v", stored, err)
	}
	draft, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Stale full", BaseRevisionID: stale.Revision.ID, IdempotencyKey: "stale-full"})
	if err != nil {
		t.Fatal(err)
	}
	full, err := r.ReadCoverage(t.Context(), p.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if full.StaleCounts != stored.StaleCounts || !reflect.DeepEqual(full.ReconciliationGaps, stored.ReconciliationGaps) || !reflect.DeepEqual(full.Inventory, stored.Inventory) || !reflect.DeepEqual(full.Snapshots, stored.Snapshots) {
		t.Fatalf("full coverage drift: stored=%+v full=%+v", stored, full)
	}
}
