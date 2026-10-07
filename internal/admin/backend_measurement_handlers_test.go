package admin

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	a "github.com/yashok111/mocker/internal/backendanalysis"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
)

func TestBackendMeasurementPublicJobAndFrozenResult(t *testing.T) {
	s := loopbackTestServer(t, nil)
	repo := a.NewRepo(s.db)
	obs := &o.Service{Repo: o.NewRepo(s.db), Graphs: s.backendRepo}
	svc := a.NewService(repo, s.backendRepo, a.NewEngine(s.backendRepo, nil))
	svc.SetObservations(obs)
	s.SetBackendAnalysis(svc, repo)
	s.SetBackendObservations(obs)
	project, err := s.backendRepo.Create(t.Context(), bm.CreateInput{Name: "measurement", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.backendRepo.ResolveEffectiveGraph(t.Context(), project.ID, bm.BackendReadTarget{RevisionID: project.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	c := o.ObservationContext{Producer: o.Producer{ID: "test", SchemaVersion: "test-v1", AdapterVersion: "test-v1"}, Source: o.Source{Status: "unknown", ServiceID: "service", Reason: "fixture has no imported build"}, Environment: o.Environment{ID: "local", Kind: "test"}, Window: o.Window{Start: "2026-10-06T00:00:00Z", End: "2026-10-06T00:00:01Z"}, ConfigurationHash: strings.Repeat("a", 64), Input: o.InputContext{Description: "one request"}, Sampling: o.Sampling{Kind: "unknown", Reason: "test"}, Instrumentation: o.Instrumentation{SQL: "complete", ExternalCalls: "unknown", Retries: "unknown", Bytes: "unknown", Latency: "complete", Limitations: []string{}}}
	rec := o.Record{Type: "span", ID: "root", ExecutionID: "run", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16), StartTimeUnixNano: "1791244800000000000", EndTimeUnixNano: "1791244800000000100", Kind: "server", Category: "http", Status: "ok", Attrs: &o.Attributes{}}
	ver, err := obs.Repo.Import(t.Context(), project.ID, o.ImportInput{Mode: "create", Context: &c, Name: "observed", BatchID: "a", IdempotencyKey: "a", Records: []o.Record{rec}})
	if err != nil {
		t.Fatal(err)
	}
	corr, err := obs.Correlate(t.Context(), project.ID, ver.SetID, o.CorrelateInput{Observation: *ver, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: o.CorrelationPolicy, Overrides: []o.Override{}, IdempotencyKey: "correlate"})
	if err != nil {
		t.Fatal(err)
	}
	input := a.StartMeasurementInput{Kind: "scenario_measurement", BeforeRevisionID: project.CurrentRevisionID, ObservationPins: []o.AnalysisPin{{ObservationSetID: ver.SetID, Version: 1, ContentHash: ver.ContentHash, CorrelationVersion: 1, CorrelationHash: corr.ContentHash, Side: "before"}}, Measurements: []a.MeasurementInput{{Side: "before", ExecutionIDs: []string{"run"}, RootSpanIDs: []string{rec.SpanID}, Basis: a.MetricBasis{Kind: "spans"}, Policy: a.MeasurementPolicy}}, IdempotencyKey: "measure"}
	if err = svc.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	base := "/api/backend-projects/" + project.ID + "/analyses"
	status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base, raw)
	if err != nil || status != 202 {
		t.Fatal(status, string(body), err)
	}
	var job a.Job
	if err = json.Unmarshal(body, &job); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := repo.Get(t.Context(), project.ID, job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status == "completed" {
			job = *j
			break
		}
		if j.Status == "failed" || time.Now().After(deadline) {
			t.Fatal(j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	resultPath := fmt.Sprintf("%s/%s/results?section=checks&resultVersion=%d", base, job.ID, *job.ResultVersion)
	status, old, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", resultPath, nil)
	if err != nil || status != 200 {
		t.Fatal(status, string(old), err)
	}
	validateBackendImportResponse(t, "GET", "/api/backend-projects/{id}/analyses/{aid}/results", old)
	if !strings.Contains(string(old), `"p95":"100"`) {
		t.Fatal(string(old))
	}
	status, detail, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", base+"/"+job.ID, nil)
	if err != nil || status != 200 {
		t.Fatal(status, string(detail), err)
	}
	validateBackendImportResponse(t, "GET", "/api/backend-projects/{id}/analyses/{aid}", detail)
	rec.ID = "later"
	rec.SpanID = strings.Repeat("3", 16)
	if _, err = obs.Repo.Import(t.Context(), project.ID, o.ImportInput{Mode: "append", SetID: ver.SetID, ExpectedVersion: 1, BatchID: "b", IdempotencyKey: "b", Records: []o.Record{rec}}); err != nil {
		t.Fatal(err)
	}
	_, again, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", resultPath, nil)
	if err != nil || string(again) != string(old) {
		t.Fatal("saved result changed", err)
	}
}
