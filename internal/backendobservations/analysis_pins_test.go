package backendobservations

import (
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestResolveAnalysisPinsExactAndStable(t *testing.T) {
	db := testkit.NewDB(t)
	project, err := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "pins", IdempotencyKey: "pins"})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(db)
	c := testContext()
	v, err := repo.Import(t.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "pins", BatchID: "a", IdempotencyKey: "a", Records: []Record{testSpan()}})
	if err != nil {
		t.Fatal(err)
	}
	graph := &bm.EffectiveGraphSnapshot{Pins: bm.EffectiveGraphPins{TargetHash: strings.Repeat("a", 64), BaseSemanticHash: strings.Repeat("b", 64)}}
	svc := &Service{Repo: repo, Graphs: correlationGraphs{graph: graph}}
	corr, err := svc.Correlate(t.Context(), project.ID, v.SetID, CorrelateInput{Observation: *v, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: CorrelationPolicy, IdempotencyKey: "correlate"})
	if err != nil {
		t.Fatal(err)
	}
	pin := AnalysisPin{ObservationSetID: v.SetID, Version: v.Version, ContentHash: v.ContentHash, CorrelationVersion: corr.Version, CorrelationHash: corr.ContentHash, Side: "before"}
	request := PinRequest{Pins: []AnalysisPin{pin}, Targets: map[string]SourcePin{"before": {RevisionID: project.CurrentRevisionID, SemanticHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash}}, MaxRecords: 100, MaxBytes: 1 << 20}
	got, err := svc.ResolveAnalysisPins(t.Context(), project.ID, request)
	if err != nil || len(got.Sets) != 1 || len(got.Sets[0].Records) != 1 {
		t.Fatal(got, err)
	}
	request.RequireCompatible = true
	if _, err = svc.ResolveAnalysisPins(t.Context(), project.ID, request); err == nil {
		t.Fatal("unknown build confirmed")
	}
	request.RequireCompatible = false
	for _, mutate := range []func(*AnalysisPin){func(p *AnalysisPin) { p.Version++ }, func(p *AnalysisPin) { p.ContentHash = strings.Repeat("f", 64) }, func(p *AnalysisPin) { p.CorrelationHash = strings.Repeat("f", 64) }, func(p *AnalysisPin) { p.Side = "after" }} {
		bad := request
		bad.Pins = []AnalysisPin{pin}
		mutate(&bad.Pins[0])
		if _, err = svc.ResolveAnalysisPins(t.Context(), project.ID, bad); err == nil {
			t.Fatal("wrong pin accepted", bad)
		}
	}
	next := testSpan()
	next.ID = "new-record"
	next.SpanID = "2222222222222222"
	if _, err = repo.Import(t.Context(), project.ID, ImportInput{Mode: "append", SetID: v.SetID, ExpectedVersion: 1, BatchID: "b", IdempotencyKey: "b", Records: []Record{next}}); err != nil {
		t.Fatal(err)
	}
	old, err := svc.ResolveAnalysisPins(t.Context(), project.ID, request)
	if err != nil || len(old.Sets[0].Records) != 1 {
		t.Fatal("old pins changed", err)
	}
	request.MaxRecords = 0
	if _, err = svc.ResolveAnalysisPins(t.Context(), project.ID, request); err == nil {
		t.Fatal("unbounded resolution")
	}
}
