package backendmodel

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
	"uuid"
)

func TestSourceReadFixComparisonCurrentness(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "review-currentness-only")
	initial, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	next := source6Input(t, &first.Project)
	next.IdempotencyKey = "review-reconcile"
	next.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: initial.RepositoryID, ProviderNamespace: initial.Manifest.Provider.Namespace}
	session, err := r.BeginImport(t.Context(), p.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: first.Revision.ID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	second, err := r.CommitImport(t.Context(), p.ID, session.ID, CommitImportInput{ExpectedVersion: first.Project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "review-second"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.ResolveSourceGraph(t.Context(), p.ID, first.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := r.ResolveSourceGraph(t.Context(), p.ID, second.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	a := before.Assertions[0]
	oldCurrent := sourceReadCurrentness(before, a)
	newCurrent := sourceReadCurrentness(after, a)
	if oldCurrent.Own.Status != "current" || newCurrent.Own.Status != "stale" {
		t.Fatalf("fixture currentness did not change: before=%+v after=%+v", oldCurrent, newCurrent)
	}
	if !bytes.Equal(before.rawAssertions[sourceAssertionKey(a)], after.rawAssertions[sourceAssertionKey(a)]) {
		t.Fatal("fixture raw claim changed")
	}
	comparison, err := r.CompareRevisions(t.Context(), p.ID, CompareRevisionsInput{FromRevisionID: first.Revision.ID, ToRevisionID: second.Revision.ID, ChangeKind: "freshness_changed"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("same raw claim; own currentness %s -> %s; comparison items=%d summary=%+v", oldCurrent.Own.Status, newCurrent.Own.Status, len(comparison.Items), comparison.Summary)
	if len(comparison.Items) == 0 {
		t.Fatal("source currentness changed but paged comparison emits no freshness change")
	}
}

func TestSourceReadFixWitnessSelectedProof(t *testing.T) {
	property := TypedSourcePropertySelector{Kind: "name"}
	selected := ProviderAssertion{RecordType: "node", RecordID: "id", AssertionHash: "selected", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "selected"}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "selected"}, EvidenceIDs: []string{"selected-proof"}, Freshness: AssertionFreshness{Status: "current"}}
	losing := selected
	losing.AssertionHash = "losing"
	losing.Owner.ProviderNamespace = "losing"
	losing.Payload.Name = "losing"
	losing.EvidenceIDs = []string{"losing-proof"}
	graph := &SourceGraphSnapshot{Assertions: []ProviderAssertion{selected, losing}, Selections: []SourceAssertionResolution{{RecordType: "node", ID: "id", Property: property, Select: SourceAssertionSelection{RepositoryID: "repo", ProviderNamespace: "selected", AssertionHash: "selected"}}}, RawEvidence: map[string]jsontext.Value{
		"selected-proof": jsontext.Value(`{"id":"selected-proof","status":"explicit","propertyPath":"/name"}`),
		"losing-proof":   jsontext.Value(`{"id":"losing-proof","status":"stale","propertyPath":"/name"}`),
	}}
	for _, a := range graph.Assertions {
		graph.Currentness = append(graph.Currentness, populateSourceFields(a, sourceCurrentness(a), false, true, ""))
	}
	n := Node{ID: "id", Kind: "system", Name: "selected", EvidenceIDs: []string{"selected-proof", "losing-proof"}}
	projection := eventsProjection{source: graph, nodes: map[string]Node{"id": n}, edges: map[string]Edge{}, evidence: map[string]Evidence{}, truncations: map[string]bool{}}
	witness := projection.witness([]string{"id"}, nil)
	t.Logf("selected name witness status=%s evidence=%v", witness.Status, witness.EvidenceIDs)
	if slices.Contains(witness.EvidenceIDs, "losing-proof") {
		t.Fatal("selected property witness advertises losing provider proof")
	}
}

func TestSourceReadFixLineageWire(t *testing.T) {
	chain := representationChain(t)
	graph := sourceReadFixtureSnapshot(t, &RevisionState{Revision: Revision{ProjectID: lineageQueryID(2), ID: lineageQueryID(1), SchemaVersion: ComposedSchemaVersion, SemanticHash: "source-semantic"}, Nodes: chain.Nodes, Edges: chain.Edges})
	page, err := projectSourceLineage(t.Context(), graph, LineageQueryInput{RevisionID: graph.State.Revision.ID, Seed: LineageValueRef{Kind: "representation_field", NodeID: "00000000-0000-4000-8000-000000000011"}, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 {
		t.Fatal("no returned mapping")
	}
	for name, encode := range map[string]func(any) ([]byte, error){"domain-v2": func(value any) ([]byte, error) { return json.Marshal(value) }, "http-v1": jsonv1.Marshal} {
		raw, err := encode(page.Items[0].Mapping)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s source marker nil=%v mapping=%s", name, page.Items[0].Mapping.Source == nil, raw)
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		if _, present := object["externalKey"]; present {
			t.Errorf("%s source6 lineage mapping exposes singular externalKey", name)
		}
		if page.Items[0].Mapping.Source == nil {
			t.Errorf("%s source6 lineage mapping lacks exact source claim/selection metadata", name)
		}
	}
}

func TestSourceReadFixDependencyIdentityAndSelectionDeltas(t *testing.T) {
	t.Parallel()
	property := TypedSourcePropertySelector{Kind: "name"}
	a := ProviderAssertion{RecordType: "node", RecordID: "shared", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "provider"}, ExternalKey: "old-key", AssertionHash: "intrinsic", Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "unchanged"}, Freshness: AssertionFreshness{Status: "current"}}
	before := &SourceGraphSnapshot{Assertions: []ProviderAssertion{a}, Currentness: []SourceClaimCurrentness{populateSourceFields(a, sourceCurrentness(a), false, true, "")}}
	after := &SourceGraphSnapshot{Assertions: []ProviderAssertion{a}, Currentness: []SourceClaimCurrentness{populateSourceFields(a, sourceCurrentness(a), false, true, "")}}
	after.Currentness[0].Dependency = AssertionFreshness{Status: "stale", Reasons: []string{"dependency_changed"}}
	for i := range after.Currentness[0].Fields {
		if after.Currentness[0].Fields[i].Property == property {
			after.Currentness[0].Fields[i].Dependency = after.Currentness[0].Dependency
		}
	}
	after.Assertions[0].ExternalKey = "new-key"
	after.Selections = []SourceAssertionResolution{{RecordType: "node", ID: a.RecordID, Property: property, ConflictHash: "conflict", Select: SourceAssertionSelection{RepositoryID: "repo", ProviderNamespace: "provider", AssertionHash: a.AssertionHash}}}
	delta := &RevisionDelta{}
	if err := appendSourceClaimDeltas(t.Context(), delta, before, after); err != nil {
		t.Fatal(err)
	}
	dependency, identity, selection := false, false, false
	for _, change := range delta.Changes {
		if change.Before == nil || change.After == nil || change.After.SourceClaim == nil {
			t.Fatalf("missing qualified sides: %+v", change)
		}
		if change.After.SourceClaim.Assertion.ProviderNamespace != "provider" || change.After.SourceClaim.Assertion.RepositoryID != "repo" {
			t.Fatal("qualified context lost")
		}
		dependency = dependency || slices.Contains(change.ChangeKinds, "freshness_changed")
		identity = identity || slices.Contains(change.ChangeKinds, "identity_mapped")
		if change.After.SourceClaim.Property != nil && *change.After.SourceClaim.Property == property && len(change.After.SourceClaim.Selections) == 1 {
			selection = true
		}
	}
	if !dependency || !identity || !selection || delta.Summary.FreshnessChanges == 0 || delta.Summary.IdentityMappings != 1 {
		t.Fatalf("source-only differences disappeared: %+v", delta)
	}
}

func TestSourceReadFixFlowNestedWireAndProof(t *testing.T) {
	t.Parallel()
	graph := sourceReadFixtureSnapshot(t, runtimeQueryFixture(t))
	for _, in := range []FlowQueryInput{{View: "entrypoints"}, {View: "steps", FlowID: runtimeQueryID(3)}, {View: "transitions", FlowID: runtimeQueryID(3)}} {
		in.RevisionID = graph.State.Revision.ID
		page, err := projectRuntimeFlowWithSource(t.Context(), &graph.State, graph, in)
		if err != nil {
			t.Fatal(err)
		}
		records := []any{}
		for _, entry := range page.EntryPointItems {
			records = append(records, entry.Operation)
		}
		for _, n := range page.StepItems {
			records = append(records, n)
		}
		for _, e := range page.TransitionItems {
			records = append(records, e)
		}
		if len(records) == 0 {
			t.Fatalf("empty %s fixture", in.View)
		}
		for _, record := range records {
			for _, encode := range []func(any) ([]byte, error){func(v any) ([]byte, error) { return json.Marshal(v) }, jsonv1.Marshal} {
				raw, err := encode(record)
				if err != nil {
					t.Fatal(err)
				}
				var object map[string]any
				if err := json.Unmarshal(raw, &object); err != nil {
					t.Fatal(err)
				}
				if _, present := object["externalKey"]; present {
					t.Fatalf("nested source singular wire: %s", raw)
				}
				if object["source"] == nil {
					t.Fatalf("nested source provenance missing: %s", raw)
				}
			}
		}
	}
}

func TestSourceReadFixDatabaseForeignKeyProofNavigation(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "source6-fk-proof")
	in := source6Input(t, p)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" || in.Inventory[i].Category == "datastores" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	session, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	graph := representationChain(t)
	const constraint = "00000000-0000-4000-8000-000000000070"
	const relationship = "b0000000-0000-4000-8000-000000000070"
	const table = "00000000-0000-4000-8000-000000000042"
	const fromColumn = "00000000-0000-4000-8000-000000000043"
	const toColumn = "00000000-0000-4000-8000-000000000044"
	graph.Nodes = append(graph.Nodes, Node{ID: constraint, Kind: "constraint", Name: "declared_fk", ParentID: new(table), Attributes: sourceContractAttrs(t, `{"facets":{"sql":{"dialect":"sqlite","analysisStatus":"complete","gaps":[],"constraintKind":"foreign_key","columnRefs":[{"localKey":"`+fromColumn+`"}],"expression":{"status":"known","value":null},"nativeDefinition":null,"deferrable":{"status":"known","value":false},"initiallyDeferred":{"status":"known","value":false}}}}`)})
	graph.Edges = append(graph.Edges, Edge{ID: "a0000000-0000-4000-8000-000000000070", Kind: "contains", From: table, To: constraint, Attributes: map[string]jsontext.Value{}}, Edge{ID: relationship, Kind: "references", From: constraint, To: table, Attributes: sourceContractAttrs(t, `{"facets":{"sql":{"sourceKind":"sql","evidenceKeys":["proof:`+relationship+`"],"dialect":"sqlite","analysisStatus":"complete","gaps":[],"columnPairs":[{"fromColumnRef":{"localKey":"`+fromColumn+`"},"toColumnRef":{"localKey":"`+toColumn+`"}}],"updateAction":{"status":"known","value":"no_action"},"deleteAction":{"status":"known","value":"no_action"},"matchType":{"status":"known","value":"simple"}}}}`)})
	commands := representationChainCommands(t, session, graph)
	batch := sendCommands(t, r, p, session, session.Version, "fk", commands...)
	committed := commitStaged(t, r, p, session, batch.AcceptedVersion, "fk-commit")
	source, err := r.ResolveSourceGraph(t.Context(), p.ID, committed.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, a := range source.Assertions {
		ids[a.ExternalKey] = a.RecordID
	}
	page, err := r.QueryDatabase(t.Context(), p.ID, DatabaseQueryInput{RevisionID: committed.Revision.ID, DatastoreID: ids["00000000-0000-4000-8000-000000000040"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.RelationshipItems) != 1 || len(page.RelationshipItems[0].EvidenceIDs) != 1 {
		t.Fatalf("selected FK proof navigation missing: %+v", page)
	}
	proof := page.RelationshipItems[0].EvidenceIDs[0]
	found := false
	for _, a := range source.Assertions {
		if a.RecordType == "edge" && a.RecordID == page.RelationshipItems[0].EdgeID {
			found = slices.Contains(a.EvidenceIDs, proof)
		}
	}
	if !found {
		t.Fatal("FK witness proof belongs to another claim")
	}
}

func TestSourceReadFixRealDependencyOnlyComparisonAndSelection(t *testing.T) {
	t.Parallel()
	f := sourceReviewSharedValue(t, "column", "different")
	session, batch := f.mapping(t, f.a)
	before := sourceReviewCommit(t, f.repo, f.project, session, batch.AcceptedVersion, "provider-a")
	f.project = &before.Project
	old, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, before.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var consumer ProviderAssertion
	for _, a := range old.Assertions {
		if a.Owner.ProviderNamespace == "provider-c" && a.Payload.Kind == "field_mapping" {
			consumer = a
		}
	}
	if consumer.RecordID == "" {
		t.Fatal("consumer absent")
	}
	in := source6Input(t, f.project)
	in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: f.repository}
	in.Manifest.Provider.Namespace = "provider-d"
	in.IdempotencyKey = "selection-for-compare"
	session, err = f.repo.BeginImport(t.Context(), f.project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := f.repo.PreviewImport(t.Context(), f.project.ID, session.ID, PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: f.project.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.repo.ImportChanges(t.Context(), f.project.ID, session.ID, ImportChangesInput{PreviewVersion: preview.Version, RecordType: "assertion_conflict"})
	if err != nil {
		t.Fatal(err)
	}
	choices := []ImportCommand{}
	for _, item := range page.Items {
		c := item.AssertionConflict
		for _, contender := range c.Contenders {
			if contender.Owner.ProviderNamespace == "provider-b" {
				choices = append(choices, ImportCommand{Op: "resolve_assertion", Resolution: &SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: c.RecordType, ID: c.ID, Property: c.Property, ConflictHash: c.ConflictHash, Select: SourceAssertionSelection{RepositoryID: contender.Owner.RepositoryID, ProviderNamespace: contender.Owner.ProviderNamespace, AssertionHash: contender.AssertionHash}, Reason: "source comparison exact selection"}})
			}
		}
	}
	if len(choices) == 0 {
		t.Fatal("no target conflicts")
	}
	chosen := sendCommands(t, f.repo, f.project, session, preview.Version, "choose-b", choices...)
	after := sourceReviewCommit(t, f.repo, f.project, session, chosen.AcceptedVersion, "provider-b")
	next, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, after.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(old.rawAssertions[sourceAssertionKey(consumer)], next.rawAssertions[sourceAssertionKey(consumer)]) {
		t.Fatal("dependency-only fixture rewrote claim")
	}
	comparison, err := f.repo.CompareRevisions(t.Context(), f.project.ID, CompareRevisionsInput{FromRevisionID: before.Revision.ID, ToRevisionID: after.Revision.ID, ChangeKind: "freshness_changed", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range comparison.Items {
		side := item.SourceClaimAfter
		if side != nil && side.Assertion.ExpectedID == consumer.RecordID && side.Assertion.ProviderNamespace == "provider-c" && side.Property == nil {
			found = true
			if side.Currentness.Own.Status != "current" || side.Currentness.Dependency.Status != "stale" {
				t.Fatalf("dependency currentness missing: %+v", side)
			}
		}
	}
	if !found {
		t.Logf("old=%+v new=%+v comparison=%+v", sourceReadCurrentness(old, consumer), sourceReadCurrentness(next, consumer), comparison)
		t.Fatal("dependency-only change hidden from filtered page")
	}
	modified, err := f.repo.CompareRevisions(t.Context(), f.project.ID, CompareRevisionsInput{FromRevisionID: before.Revision.ID, ToRevisionID: after.Revision.ID, ChangeKind: "modified", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, item := range modified.Items {
		if item.SourceClaimAfter != nil && item.SourceClaimAfter.Property != nil && len(item.SourceClaimAfter.Selections) > 0 {
			selected = true
		}
	}
	if !selected {
		t.Fatal("exact conflict selection change hidden")
	}
}
