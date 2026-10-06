package backendobservations

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestObservationSpanLinkHash(t *testing.T) {
	a := testSpan()
	h, err := RecordHash(a)
	if err != nil {
		t.Fatal(err)
	}
	empty := []SpanLink{}
	a.Links = &empty
	h2, err := RecordHash(a)
	if err != nil || h == h2 {
		t.Fatal("missing and empty links collapsed", err)
	}
}
func TestObservationSpanLinkLimits(t *testing.T) {
	c := testContext()
	a := testSpan()
	links := make([]SpanLink, 33)
	for i := range links {
		links[i] = SpanLink{TraceID: strings.Repeat("2", 32), SpanID: strings.Repeat("3", 16), Relation: "association"}
	}
	a.Links = &links
	if ValidateRecord(c, a) == nil {
		t.Fatal("accepted overflow")
	}
	links = links[:1]
	links[0].TraceID = a.TraceID
	links[0].SpanID = a.SpanID
	if ValidateRecord(c, a) == nil {
		t.Fatal("accepted self link")
	}
	links[0].TraceID = strings.Repeat("2", 32)
	links[0].Relation = "follows_from"
	if ValidateRecord(c, a) == nil {
		t.Fatal("accepted forged causal link")
	}
}
func TestObservationStrictAndLossless(t *testing.T) {
	c := testContext()
	a := testSpan()
	if err := ValidateRecord(c, a); err != nil {
		t.Fatal(err)
	}
	a.EndTimeUnixNano = "9223372036854775808"
	if ValidateRecord(c, a) == nil {
		t.Fatal("accepted int64 overflow")
	}
	a = testSpan()
	b, _ := json.Marshal(a)
	b = []byte(strings.Replace(string(b), `"id":`, `"unknown":1,"id":`, 1))
	if json.Unmarshal(b, &a) == nil {
		t.Fatal("accepted unknown member")
	}
}
func testSpan() Record {
	return Record{Type: "span", ID: "span-1", ExecutionID: "run-1", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16), StartTimeUnixNano: "1791244800000000001", EndTimeUnixNano: "1791244800000000002", Kind: "server", Category: "http", Status: "ok", Attrs: &Attributes{}}
}
func testContext() ObservationContext {
	return ObservationContext{Producer: Producer{"fixture", "v1", "v1"}, Source: Source{Status: "unknown", ServiceID: "orders", Reason: "build absent"}, Environment: Environment{"test", "test"}, Window: Window{"2026-10-06T00:00:00Z", "2026-10-07T00:00:00Z"}, ConfigurationHash: strings.Repeat("a", 64), Input: InputContext{Description: "fixture"}, Sampling: Sampling{Kind: "all", Reason: "all"}, Instrumentation: Instrumentation{SQL: "unknown", ExternalCalls: "unknown", Retries: "unknown", Bytes: "unknown", Latency: "complete", Limitations: []string{}}}
}

func TestObservationRejectsEmptyVariantExtras(t *testing.T) {
	var source Source
	if json.Unmarshal([]byte(`{"status":"unknown","serviceId":"orders","reason":"unknown build","repositoryId":""}`), &source) == nil {
		t.Fatal("empty known-source member admitted on unknown arm")
	}
	var scenario Scenario
	if json.Unmarshal([]byte(`{"kind":"backend_replay","packageId":"x","version":1,"hash":"x","id":""}`), &scenario) == nil {
		t.Fatal("empty design member admitted on replay arm")
	}
	c := testContext()
	in := ImportInput{Mode: "create", Context: &c, Name: "fixture", BatchID: "b", IdempotencyKey: "k", Records: []Record{testSpan()}}
	raw, _ := json.Marshal(in)
	raw = []byte(strings.Replace(string(raw), `"mode":"create"`, `"mode":"create","setId":""`, 1))
	if json.Unmarshal(raw, &in) == nil {
		t.Fatal("empty append member admitted on create arm")
	}
}
