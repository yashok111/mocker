package backendobservations

import (
	"strings"
	"testing"
)

// Review 2026-10-06, F159: big.Rat.SetString accepts 0x/0b/0o prefixes,
// underscores and p exponents; probabilities and JUnit times are plain
// decimal strings only.
func TestObservationDecimalStringsRejectNonDecimalForms(t *testing.T) {
	for _, v := range []string{"0x.8p0", "0b1", "0o1", "1_0", "0x1p-1"} {
		c := testContext()
		c.Sampling = Sampling{Kind: "head", Probability: new(v), Reason: "head"}
		if ValidateContext(c) == nil {
			t.Fatalf("probability %q accepted", v)
		}
	}
	for _, v := range []string{"0.5", "1", ".5"} {
		c := testContext()
		c.Sampling = Sampling{Kind: "head", Probability: new(v), Reason: "head"}
		if e := ValidateContext(c); e != nil {
			t.Fatalf("probability %q rejected: %v", v, e)
		}
	}
	for _, v := range []string{"0x10", "0x1p3", "1_000", "0b1"} {
		in := AdaptInput{Adapter: "junit-summary-v1", Context: testContext(), Data: `<testsuite><testcase time="` + v + `"/></testsuite>`}
		if _, e := Adapt(t.Context(), "", in, nil); e == nil {
			t.Fatalf("junit time %q accepted", v)
		}
	}
	in := AdaptInput{Adapter: "junit-summary-v1", Context: testContext(), Data: `<testsuite><testcase time="1.5"/></testsuite>`}
	out, e := Adapt(t.Context(), "", in, nil)
	if e != nil || out.Batches[0].Records[0].DurationNs != "1500000000" {
		t.Fatal(out, e)
	}
}

// Review 2026-10-06, F160: duplicate and self links are dropped (and counted)
// by the OTLP adapter instead of failing the whole upload with a bare 422.
func TestObservationOTelDropsDuplicateAndSelfLinks(t *testing.T) {
	trace, span, other := strings.Repeat("1", 32), strings.Repeat("2", 16), strings.Repeat("3", 16)
	link := `{"traceId":"` + trace + `","spanId":"` + other + `"}`
	self := `{"traceId":"` + trace + `","spanId":"` + span + `"}`
	data := `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"` + trace + `","spanId":"` + span + `","startTimeUnixNano":"1791244800000000001","endTimeUnixNano":"1791244800000000002","kind":2,"links":[` + link + `,` + link + `,` + self + `]}]}]}]}`
	out, e := Adapt(t.Context(), "", AdaptInput{Adapter: "otlp-traces-json-v1", Context: testContext(), Data: data}, nil)
	if e != nil {
		t.Fatalf("one duplicate link rejected the upload: %v", e)
	}
	links := *out.Batches[0].Records[0].Links
	if len(links) != 1 || links[0].SpanID != other || out.Excluded["links.duplicate/self"] != 2 {
		t.Fatalf("links %+v excluded %v", links, out.Excluded)
	}
}

// Review 2026-10-06, F161: a measurement's unit must match its metric.
func TestObservationMeasurementUnitMatchesMetric(t *testing.T) {
	base := Record{Type: "measurement", ID: "m", ExecutionID: "run", Timestamp: "1791244800000000001", Value: "1", Basis: "provider", Scope: "business"}
	for _, tc := range []struct {
		metric, unit string
		ok           bool
	}{{"sql_count", "count", true}, {"sql_count", "bytes", false}, {"request_bytes", "bytes", true}, {"request_bytes", "count", false}, {"response_bytes", "count", false}, {"retry_count", "bytes", false}} {
		r := base
		r.Metric, r.Unit = tc.metric, tc.unit
		if got := ValidateRecord(testContext(), r) == nil; got != tc.ok {
			t.Fatalf("%s/%s accepted=%v", tc.metric, tc.unit, got)
		}
	}
}
