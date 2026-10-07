package backendobservations

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestDiagramCorrelationUnknownAndAmbiguity(t *testing.T) {
	ref := bm.DiagramRef{Kind: "record", RecordType: "node", ID: "01900000-0000-7000-8000-000000000001"}
	row := CorrelationRow{RecordID: "x", Outcome: "inferred", Candidates: []bm.DiagramRef{ref, ref}, Reasons: []string{}}
	scope := &bm.DiagramScope{Selectors: []bm.DiagramScopeSelector{{Kind: "semantic", ID: ref.ID}}, SourceRefs: []bm.DiagramRef{ref}}
	out := mapDiagramRow(row, false, scope, map[string][]bm.DiagramRef{ref.ID: {ref}}, false)
	if out.Basis != "unresolved" || len(out.Selectors) != 0 {
		t.Fatal("unknown build acquired mapping")
	}
	row.Selected = &ref
	out = mapDiagramRow(row, true, scope, map[string][]bm.DiagramRef{ref.ID: {ref}}, false)
	if out.Basis != "unresolved" {
		t.Fatal("ambiguous source mapped")
	}
	row.Candidates = []bm.DiagramRef{ref}
	out = mapDiagramRow(row, true, scope, map[string][]bm.DiagramRef{ref.ID: {ref}}, false)
	if out.Basis != "source_mapping" || len(out.Selectors) != 1 {
		t.Fatal(out)
	}
	out = mapDiagramRow(row, true, scope, map[string][]bm.DiagramRef{ref.ID: {ref}}, true)
	if out.Basis != "unresolved" {
		t.Fatal("endpoint implied transition")
	}
}

type correlationGraphs struct {
	graph   *bm.EffectiveGraphSnapshot
	scope   *bm.DiagramScope
	diagram *bm.DiagramVersion
}

func (f correlationGraphs) ResolveEffectiveGraph(context.Context, string, bm.BackendReadTarget) (*bm.EffectiveGraphSnapshot, error) {
	return f.graph, nil
}
func (f correlationGraphs) ResolveDiagramScope(context.Context, string, bm.DiagramScopeInput) (*bm.DiagramScope, error) {
	return f.scope, nil
}
func (f correlationGraphs) GetDiagram(context.Context, string, bm.DiagramPin) (*bm.DiagramVersion, error) {
	return f.diagram, nil
}
func TestObservationCorrelationImmutablePins(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "correlation", IdempotencyKey: "correlation"})
	if e != nil {
		t.Fatal(e)
	}
	repo := NewRepo(db)
	c := testContext()
	first, e := repo.Import(t.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "unknown", BatchID: "a", IdempotencyKey: "a", Records: []Record{testSpan()}})
	if e != nil {
		t.Fatal(e)
	}
	graph := &bm.EffectiveGraphSnapshot{Pins: bm.EffectiveGraphPins{TargetHash: strings.Repeat("a", 64), BaseSemanticHash: strings.Repeat("b", 64)}}
	service := &Service{Repo: repo, Graphs: correlationGraphs{graph: graph}}
	in := CorrelateInput{Observation: *first, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: CorrelationPolicy, Overrides: []Override{}, IdempotencyKey: "correlation-a"}
	old, e := service.Correlate(t.Context(), project.ID, first.SetID, in)
	if e != nil {
		t.Fatal(e)
	}
	if old.SourceCompatible || old.Rows[0].Outcome != "unresolved" {
		t.Fatal("unknown was mapped")
	}
	bad := in
	bad.SourceHash = strings.Repeat("c", 64)
	bad.IdempotencyKey = "mismatch"
	if _, e = service.Correlate(t.Context(), project.ID, first.SetID, bad); e == nil {
		t.Fatal("stale source pin accepted")
	}
	next := in
	next.IdempotencyKey = "correlation-b"
	next.ExpectedCorrelationVersion = 1
	if _, e = service.Correlate(t.Context(), project.ID, first.SetID, next); e != nil {
		t.Fatal(e)
	}
	again, e := service.Correlate(t.Context(), project.ID, first.SetID, in)
	if e != nil || again.ContentHash != old.ContentHash || again.Version != 1 {
		t.Fatal("receipt changed after advancement", e)
	}
	read, e := repo.CorrelationPage(t.Context(), project.ID, first.SetID, 1, 1, "")
	if e != nil || read.Input.Observation != *first || read.ContentHash != old.ContentHash {
		t.Fatal(read, e)
	}
}

func TestDiagramCorrelationAmbiguousElements(t *testing.T) {
	ref := bm.DiagramRef{Kind: "record", RecordType: "node", ID: "endpoint"}
	row := CorrelationRow{RecordID: "span", Outcome: "explicit", Candidates: []bm.DiagramRef{ref}, Selected: &ref}
	scope := &bm.DiagramScope{Selectors: []bm.DiagramScopeSelector{{Kind: "semantic", ID: "step-a"}, {Kind: "semantic", ID: "step-b"}}}
	got := mapDiagramRow(row, true, scope, map[string][]bm.DiagramRef{"step-a": {ref}, "step-b": {ref}}, false)
	if got.Basis != "unresolved" || len(got.Selectors) != 0 {
		t.Fatal("one source endpoint selected two diagram steps", got)
	}
}

func TestObservationCandidatesBoundedDeterministic(t *testing.T) {
	a := CorrelationRow{Candidates: []bm.DiagramRef{}, Reasons: []string{}}
	b := a
	for i := 99; i >= 0; i-- {
		addCandidate(&a, bm.DiagramRef{Kind: "record", RecordType: "node", ID: fmt.Sprintf("%03d", i)})
		if len(a.Candidates) > 20 {
			t.Fatal("unbounded allocation")
		}
	}
	for i := 0; i < 100; i++ {
		addCandidate(&b, bm.DiagramRef{Kind: "record", RecordType: "node", ID: fmt.Sprintf("%03d", i)})
	}
	if !reflect.DeepEqual(a, b) || len(a.Reasons) != 1 || a.Reasons[0] != "candidate_limit" {
		t.Fatal(a, b)
	}
}

// Review 2026-10-06, F164: the quota now sums byte_length from the blob store
// instead of reading every document through the _documents views. The two
// must agree byte for byte, or the 512 MiB project cap silently moves.
func TestObservationProjectBytesMatchesStoredDocuments(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "bytes", IdempotencyKey: "bytes"})
	if e != nil {
		t.Fatal(e)
	}
	repo := NewRepo(db)
	c := testContext()
	first, e := repo.Import(t.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "bytes", BatchID: "a", IdempotencyKey: "a", Records: []Record{testSpan()}})
	if e != nil {
		t.Fatal(e)
	}
	graph := &bm.EffectiveGraphSnapshot{Pins: bm.EffectiveGraphPins{TargetHash: strings.Repeat("a", 64), BaseSemanticHash: strings.Repeat("b", 64)}}
	service := &Service{Repo: repo, Graphs: correlationGraphs{graph: graph}}
	in := CorrelateInput{Observation: *first, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: CorrelationPolicy, Overrides: []Override{}, IdempotencyKey: "correlation"}
	if _, e = service.Correlate(t.Context(), project.ID, first.SetID, in); e != nil {
		t.Fatal(e)
	}
	var viaViews, documents int64
	e = db.R.QueryRowContext(t.Context(), `SELECT COALESCE((SELECT sum(logical_bytes) FROM backend_observation_sets WHERE project_id=?1),0)+COALESCE((SELECT sum(length(CAST(document AS BLOB))) FROM backend_observation_correlations_documents WHERE project_id=?1),0)+COALESCE((SELECT sum(length(CAST(document AS BLOB))) FROM backend_observation_versions_documents WHERE project_id=?1),0), (SELECT count(*) FROM backend_observation_correlations_documents WHERE project_id=?1)+(SELECT count(*) FROM backend_observation_versions_documents WHERE project_id=?1)`, project.ID).Scan(&viaViews, &documents)
	if e != nil {
		t.Fatal(e)
	}
	got, e := projectBytes(t.Context(), db.R, project.ID)
	if e != nil {
		t.Fatal(e)
	}
	if documents < 2 || got != viaViews {
		t.Fatalf("projectBytes = %d, views = %d over %d documents", got, viaViews, documents)
	}
}
