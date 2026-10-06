package backendanalysis

import (
	o "github.com/yashok111/mocker/internal/backendobservations"
	"slices"
	"strconv"
	"testing"
)

func metricData() o.PinnedObservations {
	c := o.ObservationContext{Producer: o.Producer{ID: "producer"}, Source: o.Source{Status: "known", BuildID: "b", ServiceID: "s", RepositoryID: "repo", SourceFilesHash: "hash"}, Instrumentation: o.Instrumentation{SQL: "complete", Latency: "complete", ExternalCalls: "complete", Retries: "partial", Bytes: "unknown"}, Sampling: o.Sampling{Kind: "tail"}}
	return o.PinnedObservations{Sets: []o.PinnedSet{{Pin: o.AnalysisPin{Side: "before"}, Version: o.Version{Context: c}, Records: []o.Record{
		{Type: "span", ID: "root", ExecutionID: "run", TraceID: "t", SpanID: "root", StartTimeUnixNano: "10", EndTimeUnixNano: "110", Kind: "server", Category: "http", Status: "ok", Attrs: &o.Attributes{}},
		{Type: "span", ID: "sql", ExecutionID: "run", TraceID: "t", SpanID: "sql", ParentSpanID: "root", StartTimeUnixNano: "20", EndTimeUnixNano: "100", Kind: "client", Category: "sql", Status: "ok", Attrs: &o.Attributes{}},
		{Type: "span", ID: "sql-server", ExecutionID: "run", TraceID: "t", SpanID: "server", ParentSpanID: "sql", StartTimeUnixNano: "20", EndTimeUnixNano: "100", Kind: "server", Category: "sql", Status: "ok", Attrs: &o.Attributes{}},
		{Type: "measurement", ID: "count", ExecutionID: "run", Metric: "sql_count", Value: "51", Timestamp: "2026-10-06T00:00:00Z", Unit: "count", Basis: "provider", Scope: "business"},
	}}}}
}
func measureInput() MeasurementInput {
	return MeasurementInput{Side: "before", ExecutionIDs: []string{"run"}, RootSpanIDs: []string{"root"}, Basis: MetricBasis{Kind: "spans"}, Policy: MeasurementPolicy}
}
func TestScenarioMeasurementIndependentOracles(t *testing.T) {
	data := metricData()
	data.Sets = append(data.Sets, data.Sets[0])
	got, e := MeasureScenario(t.Context(), measureInput(), data)
	if e != nil {
		t.Fatal(e)
	}
	if *got.Metrics["sql_count"].Value != "1" || *got.Metrics["latency_ns"].P95 != "100" {
		t.Fatal(got)
	}
	if got.Metrics["response_bytes"].Value != nil || got.Metrics["retry_count"].Value != nil {
		t.Fatal("missing became zero")
	}
	if got.PopulationRate != nil {
		t.Fatal("biased sample extrapolated")
	}
	in := measureInput()
	in.Basis = MetricBasis{Kind: "measurements", Basis: "provider", Scope: "business"}
	got, e = MeasureScenario(t.Context(), in, data)
	if e != nil || *got.Metrics["sql_count"].Value != "51" {
		t.Fatal(got, e)
	}
}
func TestScenarioMeasurementNearestRankMissingOverflow(t *testing.T) {
	if got := nearestRank([]int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}, 95); got != 100 {
		t.Fatal(got)
	}
	data := metricData()
	data.Sets[0].Records[0].ParentSpanID = "absent"
	got, e := MeasureScenario(t.Context(), measureInput(), data)
	if e != nil || got.Metrics["latency_ns"].Value != nil {
		t.Fatal("fragment became root", got, e)
	}
	data = metricData()
	data.Sets[0].Records = append(data.Sets[0].Records, o.Record{Type: "measurement", ID: "overflow", ExecutionID: "run", Metric: "sql_count", Value: "9223372036854775807", Basis: "provider", Scope: "business"})
	in := measureInput()
	in.Basis = MetricBasis{Kind: "measurements", Basis: "provider", Scope: "business"}
	got, e = MeasureScenario(t.Context(), in, data)
	if e != nil || got.Metrics["sql_count"].Value != nil || !slices.Contains(got.Metrics["sql_count"].Limitations, "overflow") {
		t.Fatal(got, e)
	}
}
func TestScenarioMeasurementConflictingDuplicates(t *testing.T) {
	data := metricData()
	other := data.Sets[0]
	other.Records = slices.Clone(other.Records)
	other.Records[0].EndTimeUnixNano = "111"
	data.Sets = append(data.Sets, other)
	if _, e := MeasureScenario(t.Context(), measureInput(), data); e == nil {
		t.Fatal("conflict coalesced")
	}
}

func TestScenarioMeasurementUnequalRawSetsAndConditions(t *testing.T) {
	data := metricData()
	in := measureInput()
	in.ExecutionIDs = []string{"a", "b", "c"}
	root := data.Sets[0].Records[0]
	data.Sets[0].Records = nil
	for i, id := range []string{"a", "b"} {
		r := root
		r.ExecutionID = id
		r.TraceID = id
		r.EndTimeUnixNano = strconv.Itoa(11 + i)
		data.Sets[0].Records = append(data.Sets[0].Records, r)
	}
	last := data.Sets[0]
	r := root
	r.ExecutionID = "c"
	r.TraceID = "c"
	r.EndTimeUnixNano = "110"
	last.Records = []o.Record{r}
	data.Sets = append(data.Sets, last)
	before, err := MeasureScenario(t.Context(), in, data)
	if err != nil {
		t.Fatal(err)
	}
	if *before.Metrics["latency_ns"].P95 != "100" || len(before.Metrics["latency_ns"].Samples) != 3 {
		t.Fatal("averaged or lost raw samples", before)
	}
	after := *before
	after.Conditions = slices.Clone(before.Conditions)
	after.Conditions[0].Context.Environment.ID = "other"
	compared := CompareMeasurements(*before, after)
	if !slices.Contains(compared.ConditionDifferences, "environment") || compared.Before.Conditions[0].Context.Environment.ID == compared.After.Conditions[0].Context.Environment.ID {
		t.Fatal("conditions lost")
	}
}
