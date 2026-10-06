package backendobservations

import (
	"context"
	"encoding/json/v2"
	"os"
	"strings"
	"testing"
)

func TestObservationAdaptersPrivacy(t *testing.T) {
	c := testContext()
	in := AdaptInput{Adapter: "otlp-traces-json-v1", Context: c, Data: `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"11111111111111111111111111111111","spanId":"2222222222222222","startTimeUnixNano":"1791244800000000001","endTimeUnixNano":"1791244800000000002","kind":2,"name":"SECRET_SENTINEL","attributes":[{"key":"db.statement","value":{"stringValue":"SECRET_SENTINEL"}}],"links":[{"traceId":"33333333333333333333333333333333","spanId":"4444444444444444","attributes":[{"key":"secret","value":{"stringValue":"SECRET_SENTINEL"}}]}]}]}]}]}`}
	out, e := Adapt(context.Background(), "", in, nil)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "SECRET_SENTINEL") {
		t.Fatal("secret leaked")
	}
	if len(out.Batches) != 1 || (*out.Batches[0].Records[0].Links)[0].Relation != "association" {
		t.Fatal(out)
	}
	in.Adapter = "junit-summary-v1"
	in.Data = `<!DOCTYPE testsuite [<!ENTITY x SYSTEM "file:///etc/passwd">]><testsuite/>`
	if _, e = Adapt(t.Context(), "", in, nil); e == nil {
		t.Fatal("DTD accepted")
	}
	in.Data = `<testsuite name="SECRET_SENTINEL"><testcase name="SECRET_SENTINEL" time="0.000000001"><skipped/></testcase></testsuite>`
	out, e = Adapt(t.Context(), "", in, nil)
	if e != nil {
		t.Fatal(e)
	}
	if out.Batches[0].Records[0].Outcome != "skipped" || out.Batches[0].Records[0].DurationNs != "1" {
		t.Fatal(out)
	}
	b, _ = json.Marshal(out)
	if strings.Contains(string(b), "SECRET_SENTINEL") {
		t.Fatal("JUnit secret leaked")
	}
}

func TestObservationSpanLinkPublicFixture(t *testing.T) {
	raw, e := os.ReadFile("testdata/otlp-cross-trace-links.json")
	if e != nil {
		t.Fatal(e)
	}
	out, e := Adapt(t.Context(), "", AdaptInput{Adapter: "otlp-traces-json-v1", Context: testContext(), Data: string(raw)}, nil)
	if e != nil || len(out.Batches) != 2 {
		t.Fatal(out, e)
	}
	if out.Batches[0].Context.Source.ServiceID == out.Batches[1].Context.Source.ServiceID || (*out.Batches[0].Records[0].Links)[0].Relation != "association" {
		t.Fatal("resource identity or generic association lost")
	}
}

func TestObservationAdapterRawLimitExcludesEnvelopeEscaping(t *testing.T) {
	in := AdaptInput{Adapter: "otlp-traces-json-v1", Context: testContext(), Data: strings.Repeat(`"`, AdapterBytes-1024)}
	raw, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) <= AdapterBytes {
		t.Fatal("fixture did not exercise escaped envelope")
	}
	var decoded AdaptInput
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal("legal decoded data rejected due to escaping", e)
	}
	in.Data = strings.Repeat("x", AdapterBytes+1)
	raw, _ = json.Marshal(in)
	if json.Unmarshal(raw, &decoded) == nil {
		t.Fatal("oversized decoded input admitted")
	}
}
