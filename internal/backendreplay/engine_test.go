package backendreplay

import (
	"context"
	"fmt"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func TestEngineMissingProvenanceStopsBeforeDispatch(t *testing.T) {
	report, err := (Engine{}).Execute(t.Context(), RunInput{}, Provenance{}, Hooks{})
	if err == nil || report.Status != "unverified" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
func TestProvenanceRequiresExplicitSourceTree(t *testing.T) {
	if err := (Provenance{}).Validate(p.Identity{}); err == nil {
		t.Fatal("missing source accepted")
	}
}

func engineFixture(t *testing.T, variant string) (RunInput, Provenance, p.Journal, p.IdentityResponse) {
	t.Helper()
	key := func(n int) string { return fmt.Sprintf("01900000-0000-7000-8000-%012d", n) }
	provenance := Provenance{SourceFiles: []p.SourceFile{{Path: "cmd/orders-reference/main.go", SHA256: p.HashBytes([]byte(variant))}}}
	source, _ := p.SourceTreeHash(provenance.SourceFiles)
	provenance.Build = p.BuildDescriptor{ServiceVersion: "1", Variant: variant, SourceTreeHash: source, Toolchain: "go1.27", GOOS: "linux", GOARCH: "amd64", BuildFlags: []string{}}
	build, _ := p.BuildHash(provenance.Build)
	identity := p.Identity{Protocol: p.Version, Service: "orders-reference", ServiceVersion: "1", TestOnly: true, IsolationID: key(1), InstanceID: key(2), Variant: variant, BuildHash: build, SourceTreeHash: source, SourcePolicy: p.ManifestPolicy, FixtureHash: p.FixtureHash()}
	ih, _ := p.IdentityHash(identity)
	profile := Profile{Pin: Pin{ID: key(3), Version: 1, ContentHash: p.HashBytes([]byte("profile" + variant))}, TargetID: "test", ConfigVersion: 1, Identity: identity, IdentityHash: ih, Authorization: p.ResetAuthorization{ID: key(4), Version: 1, TargetID: "test", ConfigVersion: 1, IdentityHash: ih, IsolationID: key(1), AllowReset: true}}
	pkg := Template()
	pkg.Profile = profile.Pin
	pkg.Target = backendmodel.BackendReadTarget{RevisionID: key(5)}
	pkg.TargetHash = p.HashBytes([]byte("target"))
	pkg.DiagramBindings = []DiagramBinding{{Diagram: backendmodel.DiagramPin{ID: key(8), Version: 1, ContentHash: p.HashBytes([]byte("diagram"))}, ElementID: key(9), StepID: pkg.Steps[4].ID, AssertionIDs: []string{pkg.Assertions[1].ID}}}
	ph, _ := PackageHash(pkg)
	in := RunInput{RunID: key(6), BusinessKey: key(7), Profile: profile, Package: pkg, Start: StartInput{Package: Pin{ID: key(10), Version: 1, ContentHash: ph}, Profile: profile.Pin, ExpectedIdentityHash: ih, ResetAuthorizationID: key(4), ResetAuthorizationVersion: 1, IdempotencyKey: key(11)}}
	j := p.Journal{Identity: identity, IdentityHash: ih, RunID: in.RunID, Epoch: 1, CurrentEpoch: 1, FixtureHash: p.FixtureHash(), Complete: true, HighWater: 8, PendingKeys: []string{}}
	counters := []p.Counters{{}, {}, {Charges: 1, Attempts: 1, Triggers: 1}, {Orders: 1, Charges: 1, Attempts: 2, Triggers: 1}}
	charge2 := key(30)
	payment := "payment_reused"
	if variant == "buggy" {
		counters[3].Charges = 2
		charge2 = key(31)
		payment = "payment_charged"
	}
	for i := range 4 {
		epoch := int64(1)
		if i == 0 {
			epoch = 0
		}
		f := p.Fence{RunID: in.RunID, StepID: pkg.Steps[i].ID, RequestKey: key(20 + i), Epoch: epoch, IdentityHash: ih}
		in.Requests = append(in.Requests, f)
		var request any
		endpoint := p.OrderEndpoint
		switch i {
		case 0:
			endpoint = p.ResetEndpoint
			request = p.ResetRequest{Fence: f, Authorization: profile.Authorization, FixtureHash: p.FixtureHash()}
		case 1:
			endpoint = p.FailureEndpoint
			request = p.FailureRequest{Fence: f, Point: p.FailurePoint, Count: 1}
		default:
			request = p.OrderRequest{Fence: f, BusinessKey: in.BusinessKey, Attempt: i - 1, Order: p.Fixture().Order}
		}
		hash, _ := p.RequestHash(endpoint, request)
		receipt := p.Receipt{Fence: f, Endpoint: endpoint, RequestHash: hash, HTTPStatus: []int{200, 200, 503, 201}[i], Outcome: []string{"reset", "armed", "persistence_failed", "persisted"}[i], ResultEpoch: 1, Counters: counters[i], Sequence: []int64{1, 2, 5, 8}[i]}
		if i >= 2 {
			receipt.BusinessKey = in.BusinessKey
			receipt.Attempt = i - 1
			receipt.ChargeID = key(30)
		}
		if i == 3 {
			receipt.ChargeID = charge2
			receipt.OrderID = key(40)
		}
		j.Receipts = append(j.Receipts, receipt)
	}
	for n, kind := range []string{"reset", "armed", "attempt", "payment_charged", "failure_triggered", "attempt", payment, "order_persisted"} {
		owner := []int{0, 1, 2, 2, 2, 3, 3, 3}[n]
		f := in.Requests[owner]
		event := p.Event{Sequence: int64(n + 1), Kind: kind, StepID: f.StepID, RequestKey: f.RequestKey}
		if owner >= 2 {
			event.BusinessKey = in.BusinessKey
		}
		if n == 3 {
			event.ObjectID = key(30)
			event.AmountMinor = 1000
		}
		if n == 6 {
			event.ObjectID = charge2
			event.AmountMinor = 1000
		}
		if n == 7 {
			event.ObjectID = key(40)
		}
		j.Events = append(j.Events, event)
	}
	j.Orders = []p.OrderRecord{{ID: key(40), BusinessKey: in.BusinessKey, Order: p.Fixture().Order}}
	j.Charges = []p.ChargeRecord{{ID: key(30), BusinessKey: in.BusinessKey, AmountMinor: 1000, Currency: "USD", Scope: "mocked"}}
	if variant == "buggy" {
		j.Charges = append(j.Charges, p.ChargeRecord{ID: charge2, BusinessKey: in.BusinessKey, AmountMinor: 1000, Currency: "USD", Scope: "mocked"})
	}
	return in, provenance, j, p.IdentityResponse{Identity: identity, IdentityHash: ih, Epoch: 1}
}
func TestCheckerIndependentRecords(t *testing.T) {
	for _, variant := range []string{"buggy", "fixed"} {
		t.Run(variant, func(t *testing.T) {
			in, provenance, j, live := engineFixture(t, variant)
			report, err := Check(in, provenance, j, live)
			if err != nil {
				t.Fatal(err)
			}
			status := "succeeded"
			binding := "mocked"
			if variant == "buggy" {
				status = "failed"
				binding = "failed"
			}
			if report.Status != status || report.Bindings[0].Status != binding {
				t.Fatalf("unexpected report %+v", report)
			}
			if len(report.Assertions) != 4 || report.Assertions[1].Scope != "mocked" {
				t.Fatal("missing scoped assertions")
			}
		})
	}
}
func TestCheckerRejectsIncompleteOrContradictoryEvidence(t *testing.T) {
	mutations := map[string]func(*p.Journal, *p.IdentityResponse){
		"pending":             func(j *p.Journal, _ *p.IdentityResponse) { j.Complete = false },
		"epoch":               func(_ *p.Journal, l *p.IdentityResponse) { l.Epoch++ },
		"missing event":       func(j *p.Journal, _ *p.IdentityResponse) { j.Events = append(j.Events[:3], j.Events[4:]...) },
		"unlinked charge":     func(j *p.Journal, _ *p.IdentityResponse) { j.Charges[0].ID = "01900000-0000-7000-8000-000000000099" },
		"invented counters":   func(j *p.Journal, _ *p.IdentityResponse) { j.Receipts[3].Counters.Charges = 2 },
		"wrong first outcome": func(j *p.Journal, _ *p.IdentityResponse) { j.Receipts[2].Outcome = "persisted" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in, provenance, j, live := engineFixture(t, "fixed")
			mutate(&j, &live)
			r, err := Check(in, provenance, j, live)
			if err == nil || r.Status != "unverified" {
				t.Fatalf("accepted: %+v %v", r, err)
			}
		})
	}
}

type engineTransport struct {
	journal    p.Journal
	live       p.IdentityResponse
	lost       int
	missing    bool
	mutations  int
	authorized int
	evidence   int
}

func response[T any](v T, status int) p.Response[T] {
	raw, _ := p.Encode(v)
	return p.Response[T]{HTTPStatus: status, Body: raw, Complete: true, Payload: &v}
}
func (s *engineTransport) Identity(context.Context) (p.Response[p.IdentityResponse], error) {
	v := s.live
	if s.mutations == 0 {
		v.Epoch = 0
	}
	return response(v, 200), nil
}
func (s *engineTransport) dispatch() (p.Response[p.Receipt], error) {
	if s.authorized != s.mutations+1 {
		return p.Response[p.Receipt]{}, fmt.Errorf("dispatch before persistence")
	}
	i := s.mutations
	s.mutations++
	if s.lost == i {
		return p.Response[p.Receipt]{}, fmt.Errorf("lost reply")
	}
	return response(s.journal.Receipts[i], s.journal.Receipts[i].HTTPStatus), nil
}
func (s *engineTransport) Reset(context.Context, p.ResetRequest) (p.Response[p.Receipt], error) {
	return s.dispatch()
}
func (s *engineTransport) Arm(context.Context, p.FailureRequest) (p.Response[p.Receipt], error) {
	return s.dispatch()
}
func (s *engineTransport) Order(context.Context, p.OrderRequest) (p.Response[p.Receipt], error) {
	return s.dispatch()
}
func (s *engineTransport) Journal(context.Context, string) (p.Response[p.Journal], error) {
	j := s.journal
	if s.missing {
		j.Receipts = nil
	}
	return response(j, 200), nil
}
func (s *engineTransport) hooks() Hooks {
	return Hooks{BeforeMutation: func(_ context.Context, _ p.Endpoint, _ p.Fence, _ string, raw []byte) error {
		if len(raw) == 0 {
			return fmt.Errorf("empty request")
		}
		s.authorized++
		return nil
	}, Evidence: func(context.Context, string, []byte) error { s.evidence++; return nil }}
}
func TestEnginePositiveRecoveryNeverRedispatches(t *testing.T) {
	for _, lost := range []int{-1, 0, 1, 2, 3} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			in, provenance, j, live := engineFixture(t, "fixed")
			transport := &engineTransport{journal: j, live: live, lost: lost}
			r, err := (Engine{Transport: transport}).Execute(t.Context(), in, provenance, transport.hooks())
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "succeeded" || transport.mutations != 4 || transport.evidence < 7 {
				t.Fatalf("unexpected %+v", r)
			}
		})
	}
}
func TestEngineAbsentWitnessAndRevocationStop(t *testing.T) {
	in, provenance, j, live := engineFixture(t, "fixed")
	transport := &engineTransport{journal: j, live: live, lost: 0, missing: true}
	r, err := (Engine{Transport: transport}).Execute(t.Context(), in, provenance, transport.hooks())
	if err == nil || r.Status != "unverified" || transport.mutations != 1 {
		t.Fatal("absent witness continued")
	}
	transport = &engineTransport{journal: j, live: live, lost: -1}
	hooks := transport.hooks()
	hooks.BeforeMutation = func(context.Context, p.Endpoint, p.Fence, string, []byte) error { return fmt.Errorf("revoked") }
	_, err = (Engine{Transport: transport}).Execute(t.Context(), in, provenance, hooks)
	if err == nil || transport.mutations != 0 {
		t.Fatal("revoked request dispatched")
	}
}
func TestRegressionRetainsBothPins(t *testing.T) {
	left, lp, lj, ll := engineFixture(t, "buggy")
	right, rp, rj, rl := engineFixture(t, "fixed")
	a, err := Check(left, lp, lj, ll)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Check(right, rp, rj, rl)
	if err != nil {
		t.Fatal(err)
	}
	compared, err := Compare(left, a, right, b)
	if err != nil {
		t.Fatal(err)
	}
	if !compared.Fixed || compared.Left.Identity.BuildHash == compared.Right.Identity.BuildHash || compared.Left.Package.ContentHash == compared.Right.Package.ContentHash {
		t.Fatal("pins collapsed")
	}
	right.Package.ExcludedIDs = []string{"different"}
	hash, _ := PackageHash(right.Package)
	right.Start.Package.ContentHash = hash
	b.Package = right.Start.Package
	if _, err = Compare(left, a, right, b); err == nil {
		t.Fatal("unrelated programs compared")
	}
}

func TestEngineProvenanceAndEvidenceFailureStop(t *testing.T) {
	in, provenance, j, live := engineFixture(t, "fixed")
	transport := &engineTransport{journal: j, live: live, lost: -1}
	provenance.SourceFiles[0].SHA256 = p.HashBytes([]byte("different source"))
	_, err := (Engine{Transport: transport}).Execute(t.Context(), in, provenance, transport.hooks())
	if err == nil || transport.mutations != 0 || transport.evidence != 0 {
		t.Fatal("invalid provenance reached transport")
	}
	in, provenance, j, live = engineFixture(t, "fixed")
	transport = &engineTransport{journal: j, live: live, lost: -1}
	hooks := transport.hooks()
	hooks.Evidence = func(context.Context, string, []byte) error { return fmt.Errorf("storage unavailable") }
	_, err = (Engine{Transport: transport}).Execute(t.Context(), in, provenance, hooks)
	if err == nil || transport.mutations != 0 {
		t.Fatal("failed evidence persistence allowed mutation")
	}
}

func TestReportIncludesVerifiedReceipts(t *testing.T) {
	in, provenance, journal, live := engineFixture(t, "fixed")
	report, err := Check(in, provenance, journal, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Receipts) != 4 {
		t.Fatalf("receipt count=%d", len(report.Receipts))
	}
	for i, outcome := range []string{"reset", "armed", "persistence_failed", "persisted"} {
		if report.Receipts[i] != journal.Receipts[i] || report.Receipts[i].Outcome != outcome {
			t.Fatalf("receipt %d=%+v", i, report.Receipts[i])
		}
	}
	if report.Receipts[0].Counters != (p.Counters{}) || report.Receipts[2].HTTPStatus != 503 || report.Receipts[2].Counters.Triggers != 1 {
		t.Fatal("reset/trigger failure evidence omitted")
	}
}

func TestReportRetainsOnlyVerifiedReceiptPrefix(t *testing.T) {
	for _, lost := range []int{0, 2} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			in, provenance, journal, live := engineFixture(t, "fixed")
			transport := &engineTransport{journal: journal, live: live, lost: lost, missing: true}
			report, err := (Engine{Transport: transport}).Execute(t.Context(), in, provenance, transport.hooks())
			if err == nil || report.Status != "unverified" || len(report.Receipts) != lost || report.Receipts == nil {
				t.Fatalf("fabricated or absent receipt prefix: %+v %v", report, err)
			}
			for i, receipt := range report.Receipts {
				if receipt != journal.Receipts[i] {
					t.Fatalf("changed verified receipt %d", i)
				}
			}
		})
	}
	in, provenance, journal, live := engineFixture(t, "fixed")
	journal.Receipts[2].RequestHash = p.HashBytes([]byte("wrong request"))
	report, err := Check(in, provenance, journal, live)
	if err == nil || report.Status != "unverified" || len(report.Receipts) != 2 {
		t.Fatalf("checker prefix: %+v %v", report, err)
	}
}
