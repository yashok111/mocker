package ordersprotocol_test

import (
	"encoding/json"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"os"
	"testing"
)

const run = "01900000-0000-7000-8000-000000000001"

func TestClosedEndpoints(t *testing.T) {
	for _, e := range []p.Endpoint{p.IdentityEndpoint, p.ResetEndpoint, p.FailureEndpoint, p.OrderEndpoint, p.JournalEndpoint} {
		if _, _, err := e.Route(run); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"../orders", run + "?x=1", "", "01900000-0000-7000-8000-00000000000A"} {
		if _, _, err := p.ResetEndpoint.Route(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if _, _, err := p.Endpoint("https://evil.test").Route(run); err == nil {
		t.Fatal("open endpoint")
	}
}
func TestStrictCodec(t *testing.T) {
	for _, raw := range []string{`{"runId":"x","runId":"y"}`, `{"url":"http://evil"}`, `null`, `{} {}`, `{"runId":null}`} {
		var v p.Fence
		if err := p.Decode([]byte(raw), &v, p.BodyLimit); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestManifestAndFixtureHashes(t *testing.T) {
	a := []p.SourceFile{{Path: "b.go", SHA256: p.HashBytes([]byte("b"))}, {Path: "a.go", SHA256: p.HashBytes([]byte("a"))}}
	b := []p.SourceFile{a[1], a[0]}
	h, err := p.SourceTreeHash(a)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := p.SourceTreeHash(b)
	if err != nil || h != h2 {
		t.Fatal("unstable manifest")
	}
	for _, path := range []string{"..", "../a", "/a", "a\\b", "a/../b"} {
		if _, err := p.SourceTreeHash([]p.SourceFile{{Path: path, SHA256: a[0].SHA256}}); err == nil {
			t.Fatal(path)
		}
	}
	if _, err := p.SourceTreeHash([]p.SourceFile{a[0], a[0]}); err == nil {
		t.Fatal("duplicate path")
	}
	if p.Fixture().PaymentScope != "mocked" || p.Fixture().PersistenceScope != "actual_fixture" {
		t.Fatal("scope")
	}
}
func TestAttemptVersusDelivery(t *testing.T) {
	f := p.Fence{RunID: run, StepID: run, RequestKey: run, Epoch: 1, IdentityHash: p.HashBytes([]byte("identity"))}
	a := p.OrderRequest{Fence: f, BusinessKey: run, Attempt: 1, Order: p.Fixture().Order}
	h, err := p.RequestHash(p.OrderEndpoint, a)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Attempt = 2
	b.StepID = "01900000-0000-7000-8000-000000000002"
	b.RequestKey = b.StepID
	h2, err := p.RequestHash(p.OrderEndpoint, b)
	if err != nil || h == h2 {
		t.Fatal("retry must differ")
	}
	h3, _ := p.RequestHash(p.OrderEndpoint, a)
	if h != h3 {
		t.Fatal("delivery not stable")
	}
}
func TestReceiptRecoveryRequiresExactWitness(t *testing.T) {
	j := validJournal()
	r := j.Receipts[0]
	f := r.Fence
	h := r.RequestHash
	if _, err := p.Reconcile(j, f, p.ResetEndpoint, h); err != nil {
		t.Fatal(err)
	}
	j.Receipts[0].RequestHash = p.HashBytes([]byte("other"))
	if _, err := p.Reconcile(j, f, p.ResetEndpoint, h); err == nil {
		t.Fatal("wrong receipt accepted")
	}
	j.Receipts = nil
	if _, err := p.Reconcile(j, f, p.ResetEndpoint, h); err == nil {
		t.Fatal("absence proves nothing")
	}
	j.Receipts = []p.Receipt{r}
	j.CurrentEpoch = 2
	if _, err := p.Reconcile(j, f, p.ResetEndpoint, h); err == nil {
		t.Fatal("stale epoch accepted")
	}
}

func TestIndependentExpectations(t *testing.T) {
	for _, variant := range []string{"buggy", "fixed"} {
		raw, err := os.ReadFile("testdata/" + variant + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Charges []struct {
				AmountMinor int64
				Scope       string
			}
			Orders     []any
			Events     []string
			Verdict    string
			Assertions map[string]bool
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		want := 1
		verdict := "succeeded"
		if variant == "buggy" {
			want = 2
			verdict = "failed"
		}
		if len(v.Charges) != want || len(v.Orders) != 1 || v.Verdict != verdict || v.Assertions["charges"] != (variant == "fixed") {
			t.Fatal("independent oracle changed")
		}
		for _, c := range v.Charges {
			if c.AmountMinor != 1000 || c.Scope != "mocked" {
				t.Fatal("payment scope")
			}
		}
	}
	raw, err := os.ReadFile("testdata/hash-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		FixtureWire string
		FixtureHash string
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	wire, _ := p.Encode(p.Fixture())
	if string(wire) != v.FixtureWire || p.FixtureHash() != v.FixtureHash {
		t.Fatal("wire/hash drift")
	}
}

func validJournal() p.Journal {
	ident := p.Identity{Protocol: p.Version, Service: "orders-reference", ServiceVersion: "1", TestOnly: true, IsolationID: run, InstanceID: run, Variant: "fixed", BuildHash: p.HashBytes([]byte("build")), SourceTreeHash: p.HashBytes([]byte("tree")), SourcePolicy: p.ManifestPolicy, FixtureHash: p.FixtureHash()}
	hash, _ := p.IdentityHash(ident)
	f := p.Fence{RunID: run, StepID: run, RequestKey: run, Epoch: 0, IdentityHash: hash}
	r := p.Receipt{Fence: f, Endpoint: p.ResetEndpoint, RequestHash: p.HashBytes([]byte("request")), HTTPStatus: 200, Outcome: "reset", ResultEpoch: 1, Sequence: 1}
	return p.Journal{Identity: ident, IdentityHash: hash, RunID: run, Epoch: 1, CurrentEpoch: 1, FixtureHash: p.FixtureHash(), Complete: true, HighWater: 1, Receipts: []p.Receipt{r}, Events: []p.Event{{Sequence: 1, Kind: "reset", StepID: run, RequestKey: run}}}
}
func TestJournalReceiptEventWitness(t *testing.T) {
	j := validJournal()
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
	j.Events = nil
	if err := j.Validate(); err == nil {
		t.Fatal("receipt without event")
	}
	j = validJournal()
	j.HighWater = 2
	j.Events = append([]p.Event{{Sequence: 2, Kind: "armed", StepID: run, RequestKey: run}}, j.Events...)
	if err := j.Validate(); err == nil {
		t.Fatal("out of order events")
	}
	j = validJournal()
	j.Events[0].StepID = "01900000-0000-7000-8000-000000000002"
	if err := j.Validate(); err == nil {
		t.Fatal("foreign event step")
	}
}
