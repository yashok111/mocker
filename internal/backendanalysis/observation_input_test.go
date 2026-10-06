package backendanalysis

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
	"testing"
)

type frozenResolver struct {
	data  o.PinnedObservations
	calls int
}

func (r *frozenResolver) ResolveAnalysisPins(_ context.Context, _ string, in o.PinRequest) (*o.PinnedObservations, error) {
	r.calls++
	return &r.data, nil
}
func TestMeasurementJobFrozenInputAndLegacyNoneBytes(t *testing.T) {
	repo, db := testRepo(t)
	graphs := bm.NewRepo(db)
	project, e := graphs.Create(t.Context(), bm.CreateInput{Name: "metrics", IdempotencyKey: "metrics"})
	if e != nil {
		t.Fatal(e)
	}
	svc := NewService(repo, graphs, NewEngine(graphs, nil))
	resolver := &frozenResolver{data: metricData()}
	svc.SetObservations(resolver)
	pin := o.AnalysisPin{ObservationSetID: project.ID, Version: 1, ContentHash: digest([]byte("a")), CorrelationVersion: 1, CorrelationHash: digest([]byte("b")), Side: "before"}
	resolver.data.Sets[0].Pin = pin
	input := StartInput{Kind: "scenario_measurement", Measurement: &StartMeasurementInput{Kind: "scenario_measurement", BeforeRevisionID: project.CurrentRevisionID, ObservationPins: []o.AnalysisPin{pin}, Measurements: []MeasurementInput{measureInput()}, Limits: defaultLimits(), IdempotencyKey: "start"}}
	job, e := svc.Start(t.Context(), project.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	frozen, e := repo.Input(t.Context(), project.ID, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	terminal, e := NewEngine(graphs, nil).Analyze(t.Context(), frozen, nil)
	if e != nil || len(terminal.Snapshot.Chunks) != 1 {
		t.Fatal(terminal, e)
	}
	before, _ := canonical(terminal)
	resolver.data.Sets[0].Records[0].EndTimeUnixNano = "999"
	again, e := NewEngine(graphs, nil).Analyze(t.Context(), frozen, nil)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := canonical(again)
	if !bytes.Equal(before, after) {
		t.Fatal("saved report changed")
	}
	if _, e = svc.Start(t.Context(), project.ID, input); e != nil || resolver.calls != 1 {
		t.Fatal("receipt re-resolved pins", e)
	}
	raw := []byte(`{"kind":"impact","fromRevisionId":"` + project.CurrentRevisionID + `","target":{"revisionId":"` + project.CurrentRevisionID + `"},"scope":{},"limits":{},"idempotencyKey":"none"}`)
	var omitted StartInput
	if e = json.Unmarshal(raw, &omitted); e != nil {
		t.Fatal(e)
	}
	explicit := omitted
	explicit.ObservationMode = "none"
	x, _ := canonical(omitted)
	y, _ := canonical(explicit)
	if !bytes.Equal(x, y) {
		t.Fatal("omitted none changed input hash")
	}
	legacy := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", ObservationMode: "none"}
	x, _ = canonical(legacy)
	if bytes.Contains(x, []byte("Observations")) || bytes.Contains(x, []byte("impactObservations")) {
		t.Fatal("legacy fields added")
	}
}
func TestObservedImpactRejectsDesiredAfter(t *testing.T) {
	repo, db := testRepo(t)
	graphs := bm.NewRepo(db)
	svc := NewService(repo, graphs, nil)
	// The real resolver rejects an after pin before reading its nonexistent set:
	// no after source target is supplied for a desired proposal.
	svc.SetObservations(&o.Service{Repo: o.NewRepo(db)})
	pin := o.AnalysisPin{ObservationSetID: "01900000-0000-7000-8000-000000000001", Version: 1, ContentHash: digest(nil), CorrelationVersion: 1, CorrelationHash: digest(nil), Side: "after"}
	raw, _ := canonical(pin)
	in := StartInput{ObservationMode: "pinned", ObservationPins: []jsontext.Value{raw}, Limits: defaultLimits()}
	before := &bm.EffectiveGraphSnapshot{Target: bm.BackendReadTarget{RevisionID: pin.ObservationSetID}, Pins: bm.EffectiveGraphPins{BaseSemanticHash: digest(nil), TargetHash: digest(nil)}}
	after := &bm.EffectiveGraphSnapshot{}
	if e := svc.resolveImpact(t.Context(), pin.ObservationSetID, in, &ImmutableInput{}, before, after); e == nil {
		t.Fatal("desired after measured")
	}

}

func TestObservedImpactRejectsConflictingMappings(t *testing.T) {
	data := metricData()
	ref := bm.DiagramRef{Kind: "record", RecordType: "node", ID: "a"}
	data.Sets[0].Correlation.Rows = []o.CorrelationRow{{RecordID: "root", Outcome: "explicit", Selected: &ref}}
	other := data.Sets[0]
	different := ref
	different.ID = "b"
	other.Correlation.Rows = []o.CorrelationRow{{RecordID: "root", Outcome: "inferred", Selected: &different}}
	data.Sets = append(data.Sets, other)
	input := engineInput()
	input.ImpactObservations = &data
	if err := newReport(input).addObservedImpact(t.Context()); err == nil {
		t.Fatal("conflicting correlation silently selected")
	}
}
