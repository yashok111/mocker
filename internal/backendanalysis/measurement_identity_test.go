package backendanalysis

import (
	"slices"
	"strings"
	"testing"

	o "github.com/yashok111/mocker/internal/backendobservations"
)

// Review 2026-10-06, F152: a sample names the caller's execution id, not the
// internal identity-JSON grouping key.
func TestScenarioMeasurementSampleCarriesRawExecutionID(t *testing.T) {
	got, err := MeasureScenario(t.Context(), measureInput(), metricData())
	if err != nil {
		t.Fatal(err)
	}
	samples := got.Metrics["latency_ns"].Samples
	if len(samples) != 1 || samples[0].ExecutionID != "run" {
		t.Fatalf("sample execution ids %+v", samples)
	}
}

// Review 2026-10-06, F153: a passed test keeps the execution known whatever
// the record-id order of an unknown-status root span in the same execution.
func TestScenarioMeasurementClassificationIndependentOfRecordOrder(t *testing.T) {
	for _, ids := range [][2]string{{"a-test", "z-root"}, {"z-test", "a-root"}} {
		data := metricData()
		root := data.Sets[0].Records[0]
		root.ID = ids[1]
		root.Status = "unknown"
		test := o.Record{Type: "test", ID: ids[0], ExecutionID: "run", Outcome: "passed", DurationNs: "5"}
		data.Sets[0].Records = []o.Record{root, test}
		got, err := MeasureScenario(t.Context(), measureInput(), data)
		if err != nil {
			t.Fatal(err)
		}
		if got.SampledExecutions != 1 || got.UnknownExecutions != 0 {
			t.Fatalf("ids %v: sampled=%d unknown=%d", ids, got.SampledExecutions, got.UnknownExecutions)
		}
	}
}

// Review 2026-10-06, F37: execution and record ids containing "/" never
// collide into one observation key.
func TestScenarioMeasurementSlashIDsDoNotCollide(t *testing.T) {
	data := metricData()
	data.Sets[0].Records = []o.Record{
		{Type: "measurement", ID: "b/c", ExecutionID: "a", Metric: "sql_count", Value: "1", Unit: "count", Basis: "provider", Scope: "business"},
		{Type: "measurement", ID: "c", ExecutionID: "a/b", Metric: "sql_count", Value: "2", Unit: "count", Basis: "provider", Scope: "business"},
	}
	in := measureInput()
	in.ExecutionIDs = []string{"a", "a/b"}
	in.RootSpanIDs = nil
	in.Basis = MetricBasis{Kind: "measurements", Basis: "provider", Scope: "business"}
	if _, err := MeasureScenario(t.Context(), in, data); err != nil {
		t.Fatalf("distinct records collided: %v", err)
	}
}

// Review 2026-10-06, F156: totals over different sample sets are not
// subtracted into a delta.
func TestScenarioComparisonWithholdsDeltaForUnequalSamples(t *testing.T) {
	metric := func(total string, samples int) ScenarioMeasurements {
		m := MeasuredMetric{Unit: "count", Value: &total, SampleCount: samples, Samples: []MetricSample{}, Limitations: []string{}}
		return ScenarioMeasurements{Metrics: map[string]MeasuredMetric{"sql_count": m}}
	}
	got := CompareMeasurements(metric("30", 3), metric("50", 5))
	if got.Deltas["sql_count"] != nil {
		t.Fatalf("delta over unequal samples: %s", *got.Deltas["sql_count"])
	}
	if !slices.ContainsFunc(got.Limitations, func(s string) bool { return strings.HasPrefix(s, "sql_count: unequal sample sets") }) {
		t.Fatalf("limitation missing: %v", got.Limitations)
	}
	got = CompareMeasurements(metric("30", 3), metric("36", 3))
	if got.Deltas["sql_count"] == nil || *got.Deltas["sql_count"] != "6" {
		t.Fatal("equal sample sets lost their delta")
	}
}
