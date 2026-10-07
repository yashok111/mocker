package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func exploreFixture(t *testing.T) *EffectiveGraphSnapshot {
	t.Helper()
	s := runtimeQueryFixture(t)
	runtimeQueryAddNode(t, s, 500, "system", 0, nil)
	runtimeQueryAddNode(t, s, 501, "service", 500, map[string]any{"description": "Backend"})
	for i := range 33 {
		runtimeQueryAddNode(t, s, 600+i, "http_operation", 501, map[string]any{"path": "/api/v1/quizzes/" + strings.Repeat("x", i), "method": "GET", "tags": []string{"quiz", "platform"}, "summary": "Quiz"})
	}
	runtimeQueryAddNode(t, s, 700, "http_operation", 501, map[string]any{"path": "/api/v1/quizzes-other", "method": "GET"})
	return &EffectiveGraphSnapshot{Target: BackendReadTarget{RevisionID: s.Revision.ID}, State: *s, Pins: EffectiveGraphPins{TargetHash: strings.Repeat("a", 64)}}
}
func TestExploreBoundedCollections(t *testing.T) {
	g := exploreFixture(t)
	in := ExploreInput{Target: g.Target, Mode: "objects", Kind: "http_operation", Group: "prefix:/api/v1/quizzes", Limit: 20}
	p, err := projectExplore(g, in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 33 || len(p.Nodes) != 20 || p.NextCursor == "" {
		t.Fatalf("bounded membership: %+v", p)
	}
	in.Cursor = p.NextCursor
	second, err := projectExplore(g, in)
	if err != nil || len(second.Nodes) != 13 {
		t.Fatalf("second page: %+v %v", second, err)
	}
	in.Group = "prefix:/api/v1/quizzes-other"
	if _, err = projectExplore(g, in); err == nil {
		t.Fatal("cursor reused outside its filters")
	}
	tags, err := projectExplore(g, ExploreInput{Target: g.Target, Mode: "objects", Group: "tag:platform"})
	if err != nil || tags.Total != 33 {
		t.Fatalf("tag membership: %+v %v", tags, err)
	}
}
func TestExploreOverviewDoesNotExposeManifestOrInventEdges(t *testing.T) {
	g := exploreFixture(t)
	p, err := projectExplore(g, ExploreInput{Target: g.Target, Mode: "overview"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 3 || p.Counts["http_operation"] != 35 {
		t.Fatalf("overview %+v", p)
	}
	if len(p.Edges) != 0 {
		t.Fatal("invented dependencies")
	}
	b, err := json.Marshal(p)
	if err != nil || strings.Contains(string(b), "files") {
		t.Fatalf("manifest leaked: %s %v", b, err)
	}
	if _, err = projectExplore(g, ExploreInput{Target: g.Target, Mode: "children", ScopeID: runtimeQueryID(9999)}); err == nil {
		t.Fatal("missing scope broadened")
	}
}

func TestWorkbenchNewestProposalPage(t *testing.T) {
	r, base, first := changeFixture(t)
	newer, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Latest", BaseRevisionID: base.Revision.ID, IdempotencyKey: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.ListChangeProposals(t.Context(), base.Project.ID, ChangeProposalListInput{Order: "desc", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != newer.Proposal.ID {
		t.Fatalf("latest page %+v %v", page, err)
	}
	page, err = r.ListChangeProposals(t.Context(), base.Project.ID, ChangeProposalListInput{Order: "desc", Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != first.Proposal.ID {
		t.Fatalf("next page %+v %v", page, err)
	}
}

func TestExploreSourceUsesSameImmutablePins(t *testing.T) {
	r, _ := testRepo(t)
	project := createProject(t, r, "lean-explore")
	state := runtimeQueryFixture(t)
	state.Revision.ProjectID = project.ID
	runtimeQueryPersist(t, r, state)
	target := BackendReadTarget{RevisionID: state.Revision.ID}
	full, err := r.ResolveEffectiveGraph(t.Context(), project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.QueryExplore(t.Context(), project.ID, ExploreInput{Target: target, Mode: "objects", Kind: "http_operation"})
	if err != nil {
		t.Fatal(err)
	}
	if page.TargetHash != full.Pins.TargetHash || page.SemanticHash != full.Pins.EffectiveSemanticHash || page.Total != 1 {
		t.Fatalf("lean reader changed immutable identity: %+v vs %+v", page, full.Pins)
	}
}

func TestExploreComposedSourceUsesSameImmutablePins(t *testing.T) {
	r, base, _ := changeFixture(t)
	target := BackendReadTarget{RevisionID: base.Revision.ID}
	full, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.QueryExplore(t.Context(), base.Project.ID, ExploreInput{Target: target, Mode: "overview"})
	if err != nil {
		t.Fatal(err)
	}
	if page.TargetHash != full.Pins.TargetHash || page.SemanticHash != full.Pins.EffectiveSemanticHash {
		t.Fatalf("source6 pin drift: %s != %s", page.TargetHash, full.Pins.TargetHash)
	}
}

func TestWorkbenchNamedRelatedDiagrams(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "related-maps")
	state := runtimeQueryFixture(t)
	state.Revision.ProjectID = p.ID
	runtimeQueryPersist(t, r, state)
	document := diagramTestDocument(state.Revision.ID)
	document.Payload.Elements[0].Refs = []DiagramRef{{Kind: "record", RecordType: "node", ID: state.Nodes[0].ID}}
	created, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: document, IdempotencyKey: "related"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.ListDiagrams(t.Context(), p.ID, DiagramListInput{SubjectID: state.Nodes[0].ID, TargetHash: created.TargetHash, Limit: 20, Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != "Orders" || page.Items[0].Target.RevisionID != state.Revision.ID {
		t.Fatalf("named catalog: %+v", page)
	}
	missing, err := r.ListDiagrams(t.Context(), p.ID, DiagramListInput{SubjectID: state.Nodes[1].ID, TargetHash: created.TargetHash, Limit: 20})
	if err != nil || len(missing.Items) != 0 {
		t.Fatalf("invented membership %+v %v", missing, err)
	}
	wrong, err := r.ListDiagrams(t.Context(), p.ID, DiagramListInput{SubjectID: state.Nodes[0].ID, TargetHash: strings.Repeat("f", 64), Limit: 20})
	if err != nil || len(wrong.Items) != 0 {
		t.Fatalf("cross-target membership %+v %v", wrong, err)
	}
}

func TestWorkbenchComparisonKeepsBeforeEvidenceAndEdgeContext(t *testing.T) {
	g := exploreFixture(t)
	baseline := g.State
	baseline.Nodes = append([]Node{}, g.State.Nodes...)
	baseline.Edges = append([]Edge{}, g.State.Edges...)
	g.Source = &SourceGraphSnapshot{State: baseline}
	g.Target = BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: runtimeQueryID(9001), ProposalRevisionID: runtimeQueryID(9002)}}
	g.Pins.BaseRevisionID = baseline.Revision.ID
	g.State.Nodes[0].Name = "Updated operation"
	g.State.Edges[0].Kind = "calls"
	p, err := projectExploreComparison(g, ExploreInput{Target: g.Target, Mode: "comparison", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].RecordType != "edge" || p.Changes[0].Change != "changed" || p.NextCursor == "" {
		t.Fatalf("edge comparison %+v", p)
	}
	if len(p.Nodes) != 2 || len(p.BeforeNodes) != 2 || p.BeforeTarget.RevisionID != baseline.Revision.ID || p.BeforeEdges[0].Kind != "handles" || p.Edges[0].Kind != "calls" {
		t.Fatalf("lost exact before/context %+v", p)
	}
	next, err := projectExploreComparison(g, ExploreInput{Target: g.Target, Mode: "comparison", Limit: 1, Cursor: p.NextCursor})
	if err != nil || len(next.Changes) != 1 || next.Changes[0].Change != "changed" || next.BeforeNodes[0].Name == next.Nodes[0].Name {
		t.Fatalf("node before/after %+v %v", next, err)
	}
}

func TestWorkbenchNeighborhoodRepeatsAnchorAcrossAllEdgePages(t *testing.T) {
	g := exploreFixture(t)
	anchor := runtimeQueryID(700)
	for i := range 7 {
		runtimeQueryAddEdge(t, &g.State, 800+i, "calls", 700, 600+i, nil)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		p, err := projectExplore(g, ExploreInput{Target: g.Target, Mode: "neighborhood", ScopeID: anchor, Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Nodes) == 0 || p.Nodes[0].ID != anchor || len(p.Nodes) > 3 {
			t.Fatalf("anchor/bound lost %+v", p)
		}
		for _, e := range p.Edges {
			if seen[e.ID] {
				t.Fatal("duplicate edge")
			}
			seen[e.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 7 {
		t.Fatalf("lost relationships: %d", len(seen))
	}
}

func TestWorkbenchImportCatalogOmitsSourceManifests(t *testing.T) {
	r, out, session, _ := eventsOrdersCommitted(t, true)
	page, err := r.ImportSummaries(t.Context(), out.Project.ID, ListInput{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != session.ID || page.Items[0].RepositoryName != "orders-events-fixture" {
		t.Fatalf("wrong summary %+v", page)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "manifest") || strings.Contains(string(raw), "files") || len(raw) > 1024 {
		t.Fatalf("unbounded catalog: %s", raw)
	}
}

func TestWorkbenchLegacyOpenAPIDescriptionSuppliesReadableNamesAndTags(t *testing.T) {
	g := exploreFixture(t)
	n := &g.State.Nodes[0]
	n.Attributes = runtimeQueryAttrs(t, map[string]any{"method": "POST", "path": "/orders", "description": `{"summary":"Создание заказа","tags":["Orders","Public"]}`})
	p, err := projectExplore(g, ExploreInput{Target: g.Target, Mode: "objects", Search: "Создание заказа"})
	if err != nil || len(p.Nodes) != 1 || p.Nodes[0].Name != "Создание заказа" || p.Nodes[0].Description != "" {
		t.Fatalf("metadata not decoded: %+v %v", p, err)
	}
	tags, err := projectExplore(g, ExploreInput{Target: g.Target, Mode: "objects", Group: "tag:Public"})
	if err != nil || tags.Total != 1 {
		t.Fatalf("tag lost: %+v %v", tags, err)
	}
	if runtimeAttributeString(n.Attributes, "description") == "" {
		t.Fatal("display adapter mutated source record")
	}
}
