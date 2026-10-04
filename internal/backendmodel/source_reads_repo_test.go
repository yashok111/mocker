package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"testing"
)

func TestSource6AssertionsAndLineageFromImmutableRepository(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "source-read-chain")
	in := source6Input(t, p)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" || in.Inventory[i].Category == "datastores" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := representationChainCommands(t, s, representationChain(t))
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "chain", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	commit, err := r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ResolveSourceGraph(t.Context(), p.ID, commit.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, a := range snapshot.Assertions {
		ids[a.ExternalKey] = a.RecordID
	}
	before := immutableBytes(t, r)
	seed := LineageValueRef{Kind: "column", NodeID: ids["00000000-0000-4000-8000-000000000043"], FacetKey: "sql"}
	page, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: commit.Revision.ID, Seed: seed, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || len(page.Items[0].ExpandedValues) != 1 || page.Items[0].ExpandedValues[0].NodeID != ids["00000000-0000-4000-8000-000000000003"] || page.Items[3].Expansion != "boundary" {
		raw, _ := json.Marshal(page)
		t.Fatalf("chain traversal: %s", raw)
	}
	if page.Items[0].Status != "explicit" || page.Items[0].RequiresReview {
		t.Fatalf("fresh own proof not read: %+v", page.Items[0])
	}
	reverse, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: commit.Revision.ID, Seed: LineageValueRef{Kind: "representation_field", NodeID: ids["00000000-0000-4000-8000-000000000003"]}, Direction: "reverse"})
	if err != nil || len(reverse.Items) != 1 || len(reverse.Items[0].ExpandedValues) != 2 || reverse.Items[0].ExpandedValues[0].NodeID != ids["00000000-0000-4000-8000-000000000043"] || reverse.Items[0].ExpandedValues[1].NodeID != ids["00000000-0000-4000-8000-000000000044"] {
		t.Fatalf("ordered two-input hyperedge: %+v %v", reverse, err)
	}
	unmapped, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: commit.Revision.ID, Seed: LineageValueRef{Kind: "representation_field", NodeID: ids["00000000-0000-4000-8000-000000000022"]}, Direction: "forward"})
	if err != nil || len(unmapped.Items) != 0 || len(unmapped.Limitations) == 0 {
		t.Fatalf("same-name unmapped value inferred lineage: %+v %v", unmapped, err)
	}
	pin := SourceReadPin{ProjectID: p.ID, BaseRevisionID: commit.Revision.ID, TargetHash: "target", EffectiveSemanticHash: commit.Revision.SemanticHash}
	pin.SourceVectorHash, _ = requestDigest(snapshot.SourceVector)
	assertions, err := QuerySourceAssertions(t.Context(), snapshot, pin, SourceAssertionsQuery{ID: ids["00000000-0000-4000-8000-000000000050"]})
	if err != nil || len(assertions.Items) != 1 {
		t.Fatalf("assertions: %+v %v", assertions, err)
	}
	events, err := r.QueryEvents(t.Context(), p.ID, EventsQueryInput{RevisionID: commit.Revision.ID, View: "service_calls"})
	if err != nil || len(events.Items) != 0 || events.Policy != "events-source6-query-v1" {
		t.Fatalf("source6 event scope: %+v %v", events, err)
	}
	flow, err := r.QueryFlow(t.Context(), p.ID, FlowQueryInput{RevisionID: commit.Revision.ID, View: "entrypoints"})
	if err != nil || len(flow.EntryPointItems) != 1 {
		t.Fatalf("source6 flow entrypoints: %+v %v", flow, err)
	}
	database, err := r.QueryDatabase(t.Context(), p.ID, DatabaseQueryInput{RevisionID: commit.Revision.ID, DatastoreID: ids["00000000-0000-4000-8000-000000000040"], FacetKey: "sql", RecordType: "tables"})
	if err != nil || len(database.TableItems) != 1 || database.FacetStatus != "current" {
		t.Fatalf("source6 database: %+v %v", database, err)
	}
	graphPage, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: commit.Revision.ID, RecordType: "nodes", Kind: "representation_field"})
	if err != nil || len(graphPage.Nodes) != 4 || graphPage.Source == nil || graphPage.Nodes[0].Source == nil {
		t.Fatalf("source graph projection: %+v %v", graphPage, err)
	}
	graphJSON, err := json.Marshal(graphPage)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(graphJSON, []byte(`"sourceVector"`)) != 1 || len(graphPage.Source.AssertionRefs) != 0 || graphPage.Nodes[0].Source.SourceVector != nil {
		t.Fatalf("page multiplied full source provenance: %s", graphJSON)
	}
	listed, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: commit.Revision.ID, RecordType: "nodes", Kind: "representation_field", ParentID: ids["00000000-0000-4000-8000-000000000010"]})
	if err != nil || len(listed.Nodes) != 1 || listed.Nodes[0].ID != ids["00000000-0000-4000-8000-000000000011"] {
		t.Fatalf("exact representation owner list: %+v %v", listed, err)
	}
	inspected, err := r.Node(t.Context(), p.ID, commit.Revision.ID, listed.Nodes[0].ID)
	if err != nil || inspected.Source == nil || len(inspected.Source.AssertionRefs) != 1 || inspected.Ownership != nil || inspected.Freshness != nil || inspected.ExternalKey != "" {
		t.Fatalf("representation inspection source proof: %+v %v", inspected, err)
	}
	stableSeed := LineageValueRef{Kind: "representation_field", NodeID: inspected.ID}
	represented, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: commit.Revision.ID, Seed: stableSeed, Direction: "forward"})
	if err != nil || len(represented.Items) != 2 || represented.Items[1].Expansion != "boundary" {
		t.Fatalf("stable representation seed: %+v %v", represented, err)
	}
	proofPage, err := r.Evidence(t.Context(), p.ID, commit.Revision.ID, EvidenceQueryInput{SubjectID: ids["00000000-0000-4000-8000-000000000050"]})
	if err != nil || proofPage.Source == nil || len(proofPage.Source.AssertionRefs) != 1 {
		t.Fatalf("proof claim navigation: %+v %v", proofPage, err)
	}
	firstPage, err := QuerySourceAssertions(t.Context(), snapshot, pin, SourceAssertionsQuery{Limit: 1})
	if err != nil || firstPage.NextCursor == "" {
		t.Fatalf("bounded assertion page: %+v %v", firstPage, err)
	}
	for key, raw := range before {
		if immutableBytes(t, r)[key] != raw {
			t.Fatalf("read mutated %s", key)
		}
	}
}

func TestSource6HistoricalAssertionCursorSurvivesLaterHead(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "historical-source-pages")
	_, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	next := source6Input(t, &first.Project)
	next.Manifest.RepositoryName = "second"
	next.IdempotencyKey = "second"
	_, second := commitSource6Fixture(t, r, &first.Project, next)
	snapshot, err := r.ResolveSourceGraph(t.Context(), p.ID, second.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	vectorHash, _ := requestDigest(snapshot.SourceVector)
	pin := SourceReadPin{ProjectID: p.ID, BaseRevisionID: second.Revision.ID, TargetHash: second.Revision.SemanticHash, EffectiveSemanticHash: second.Revision.SemanticHash, SourceVectorHash: vectorHash}
	page, err := QuerySourceAssertions(t.Context(), snapshot, pin, SourceAssertionsQuery{Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page: %+v %v", page, err)
	}
	next = source6Input(t, &second.Project)
	next.Manifest.RepositoryName = "third"
	next.IdempotencyKey = "third"
	_, third := commitSource6Fixture(t, r, &second.Project, next)
	historical, err := r.ResolveSourceGraph(t.Context(), p.ID, second.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	continuation, err := QuerySourceAssertions(t.Context(), historical, pin, SourceAssertionsQuery{Cursor: page.NextCursor})
	if err != nil || len(continuation.Items) != 1 {
		t.Fatalf("historical continuation: %+v %v", continuation, err)
	}
	comparison, err := r.CompareRevisions(t.Context(), p.ID, CompareRevisionsInput{FromRevisionID: second.Revision.ID, ToRevisionID: third.Revision.ID})
	if err != nil || comparison.SourceBefore == nil || comparison.SourceAfter == nil || len(comparison.SourceBefore.SourceVector.Partitions) != 2 || len(comparison.SourceAfter.SourceVector.Partitions) != 3 {
		t.Fatalf("comparison provenance: %+v %v", comparison, err)
	}
	if historical.SourceContentHash != snapshot.SourceContentHash {
		t.Fatal("old source content changed")
	}
}
