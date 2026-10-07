package backendreplay

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"time"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

// Provenance pins reproducible build inputs, independently of an imported artifact.
type Provenance struct {
	SourceFiles []p.SourceFile    `json:"sourceFiles"`
	Build       p.BuildDescriptor `json:"build"`
}

func (v Provenance) Validate(identity p.Identity) error {
	source, err := p.SourceTreeHash(v.SourceFiles)
	if err != nil {
		return err
	}
	build, err := p.BuildHash(v.Build)
	if err != nil {
		return err
	}
	if source != identity.SourceTreeHash || source != v.Build.SourceTreeHash || build != identity.BuildHash || v.Build.Variant != identity.Variant || v.Build.ServiceVersion != identity.ServiceVersion {
		return fmt.Errorf("source or build provenance mismatch")
	}
	return nil
}

type Hooks struct {
	// BeforeMutation must durably persist the exact request and recheck authorization.
	BeforeMutation func(context.Context, p.Endpoint, p.Fence, string, []byte) error
	Evidence       func(context.Context, string, []byte) error
}
type Engine struct{ Transport p.Transport }

type AssertionResult struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Expected int    `json:"expected"`
	Actual   int    `json:"actual"`
	Passed   bool   `json:"passed"`
	Scope    string `json:"scope"`
}
type Report struct {
	RunID          string            `json:"runId"`
	Status         string            `json:"status"`
	Reason         string            `json:"reason,omitempty"`
	CheckerVersion string            `json:"checkerVersion"`
	Package        Pin               `json:"package"`
	Profile        Pin               `json:"profile"`
	Identity       p.Identity        `json:"identity"`
	Provenance     Provenance        `json:"provenance"`
	Receipts       []p.Receipt       `json:"receipts"`
	Assertions     []AssertionResult `json:"assertions"`
	Bindings       []BindingEvidence `json:"bindings"`
}

func initialReport(in RunInput, provenance Provenance) Report {
	r := Report{RunID: in.RunID, Status: "unverified", CheckerVersion: CheckerVersion, Package: in.Start.Package, Profile: in.Start.Profile, Identity: in.Profile.Identity, Provenance: provenance, Receipts: []p.Receipt{}, Assertions: []AssertionResult{}, Bindings: []BindingEvidence{}}
	for _, b := range in.Package.DiagramBindings {
		r.Bindings = append(r.Bindings, BindingEvidence{Package: in.Start.Package, Binding: b, Status: "unverified", AssertionIDs: slices.Clone(b.AssertionIDs)})
	}
	return r
}
func validateRun(in RunInput, provenance Provenance) error {
	if err := in.Start.Validate(); err != nil {
		return err
	}
	if err := in.Profile.Validate(); err != nil {
		return err
	}
	hash, err := PackageHash(in.Package)
	if err != nil {
		return err
	}
	a := in.Profile.Authorization
	if hash != in.Start.Package.ContentHash || in.Start.Profile != in.Profile.Pin || in.Package.Profile != in.Profile.Pin || in.Start.ExpectedIdentityHash != in.Profile.IdentityHash || in.Start.ResetAuthorizationID != a.ID || in.Start.ResetAuthorizationVersion != a.Version || !p.ValidID(in.RunID) || !p.ValidID(in.BusinessKey) || len(in.Requests) != 4 {
		return fmt.Errorf("run pins mismatch")
	}
	keys := map[string]bool{}
	for i, f := range in.Requests {
		if !p.ValidID(f.RequestKey) || keys[f.RequestKey] || f.RunID != in.RunID || f.StepID != in.Package.Steps[i].ID || f.IdentityHash != in.Profile.IdentityHash {
			return fmt.Errorf("invalid request fence")
		}
		keys[f.RequestKey] = true
	}
	return provenance.Validate(in.Profile.Identity)
}

// Execute never repeats an ambiguous mutation. Only a positive journal witness
// permits progression. Request identities originate in the durable run input.
func (e Engine) Execute(ctx context.Context, in RunInput, provenance Provenance, hooks Hooks) (report Report, err error) {
	report = initialReport(in, provenance)
	defer func() {
		if err != nil {
			report.Status = "unverified"
			report.Reason = err.Error()
		}
	}()
	if err = validateRun(in, provenance); err != nil {
		return
	}
	if e.Transport == nil || hooks.BeforeMutation == nil || hooks.Evidence == nil {
		err = fmt.Errorf("transport and durable hooks required")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ExecutionSeconds)*time.Second)
	defer cancel()
	live, err := e.identity(ctx, in, hooks)
	if err != nil {
		return report, err
	}
	if live.Epoch == math.MaxInt64 {
		return report, fmt.Errorf("epoch exhausted")
	}
	in.Requests = slices.Clone(in.Requests)
	for i := range in.Requests {
		in.Requests[i].Epoch = live.Epoch
		if i > 0 {
			in.Requests[i].Epoch++
		}
	}
	for i := range 4 {
		var receipt p.Receipt
		if receipt, err = e.dispatch(ctx, in, hooks, i); err != nil {
			return
		}
		report.Receipts = append(report.Receipts, receipt)
	}
	journal, err := e.journal(ctx, in, hooks)
	if err != nil {
		return report, err
	}
	for _, receipt := range report.Receipts {
		index := slices.IndexFunc(journal.Receipts, func(r p.Receipt) bool { return r.RequestKey == receipt.RequestKey })
		if index < 0 || journal.Receipts[index] != receipt {
			return report, fmt.Errorf("journal changed an observed receipt")
		}
	}
	live, err = e.identity(ctx, in, hooks)
	if err != nil {
		return report, err
	}
	checked, checkErr := Check(in, provenance, journal, live)
	if checkErr != nil {
		// Preserve the already verified dispatch witnesses even if the final
		// journal cannot establish a complete verdict.
		checked.Receipts = report.Receipts
	}
	return checked, checkErr
}

// dispatch sends mutation i once, after its durable BeforeMutation hook, and
// returns its verified receipt. A lost or incomplete answer is recovered from
// the journal, never by sending the mutation again.
func (e Engine) dispatch(ctx context.Context, in RunInput, hooks Hooks, i int) (p.Receipt, error) {
	endpoint, request := mutation(in, i)
	hash, err := p.RequestHash(endpoint, request)
	if err != nil {
		return p.Receipt{}, err
	}
	raw, err := p.Encode(request)
	if err != nil {
		return p.Receipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return p.Receipt{}, err
	}
	if err = hooks.BeforeMutation(ctx, endpoint, in.Requests[i], hash, raw); err != nil {
		return p.Receipt{}, err
	}
	if err = ctx.Err(); err != nil {
		return p.Receipt{}, err
	}
	var response p.Response[p.Receipt]
	switch request := request.(type) {
	case p.ResetRequest:
		response, err = e.Transport.Reset(ctx, request)
	case p.FailureRequest:
		response, err = e.Transport.Arm(ctx, request)
	case p.OrderRequest:
		response, err = e.Transport.Order(ctx, request)
	}
	if evidenceErr := hooks.recordResponse(ctx, string(endpoint), response); evidenceErr != nil {
		return p.Receipt{}, evidenceErr
	}
	if response.ProtocolError != nil {
		return p.Receipt{}, fmt.Errorf("mutation rejected: %s", response.ProtocolError.Code)
	}
	var receipt p.Receipt
	if err != nil || !response.Complete || response.Payload == nil {
		receipt, err = e.recover(ctx, in, hooks, i, endpoint, hash)
		if err != nil {
			return p.Receipt{}, err
		}
	} else {
		receipt = *response.Payload
		if response.HTTPStatus != receipt.HTTPStatus {
			return p.Receipt{}, fmt.Errorf("receipt HTTP status mismatch")
		}
	}
	if err = validateReceipt(in, i, receipt, hash); err != nil {
		return p.Receipt{}, err
	}
	return receipt, nil
}
func mutation(in RunInput, i int) (p.Endpoint, any) {
	f := in.Requests[i]
	switch i {
	case 0:
		return p.ResetEndpoint, p.ResetRequest{Fence: f, Authorization: in.Profile.Authorization, FixtureHash: in.Package.FixtureHash}
	case 1:
		return p.FailureEndpoint, p.FailureRequest{Fence: f, Point: p.FailurePoint, Count: 1}
	default:
		return p.OrderEndpoint, p.OrderRequest{Fence: f, BusinessKey: in.BusinessKey, Attempt: i - 1, Order: p.Fixture().Order}
	}
}
func validateReceipt(in RunInput, i int, r p.Receipt, hash string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	endpoint, _ := mutation(in, i)
	if r.Fence != in.Requests[i] || r.Endpoint != endpoint || r.RequestHash != hash {
		return fmt.Errorf("receipt does not witness dispatched request")
	}
	return checkStepOutcome(in, i, r)
}

// checkStepOutcome holds each step of the closed program to the outcome and
// counters it must report.
func checkStepOutcome(in RunInput, i int, r p.Receipt) error {
	switch i {
	case 0:
		if r.Counters != (p.Counters{}) {
			return fmt.Errorf("reset not empty")
		}
	case 1:
		if r.Counters != (p.Counters{}) {
			return fmt.Errorf("arm changed counters")
		}
	case 2:
		if r.BusinessKey != in.BusinessKey || r.Attempt != 1 || r.Outcome != "persistence_failed" || r.Counters != (p.Counters{Charges: 1, Attempts: 1, Triggers: 1}) {
			return fmt.Errorf("first failure not witnessed")
		}
	case 3:
		if r.BusinessKey != in.BusinessKey || r.Attempt != 2 || r.Outcome != "persisted" || r.Counters.Orders != 1 || r.Counters.Attempts != 2 || r.Counters.Triggers != 1 || r.Counters.Charges < 1 {
			return fmt.Errorf("retry not witnessed")
		}
	}
	return nil
}
func (h Hooks) recordResponse[T any](ctx context.Context, kind string, r p.Response[T]) error {
	if len(r.Body) > p.JournalLimit {
		return fmt.Errorf("evidence exceeds limit")
	}
	raw, err := p.Encode(struct {
		HTTPStatus int    `json:"httpStatus"`
		Complete   bool   `json:"complete"`
		Body       []byte `json:"body"`
	}{r.HTTPStatus, r.Complete, r.Body})
	if err != nil {
		return err
	}
	return h.Evidence(ctx, kind, raw)
}
func (e Engine) identity(ctx context.Context, in RunInput, h Hooks) (p.IdentityResponse, error) {
	r, err := e.Transport.Identity(ctx)
	if saveErr := h.recordResponse(ctx, "identity", r); saveErr != nil {
		return p.IdentityResponse{}, saveErr
	}
	if err != nil {
		return p.IdentityResponse{}, err
	}
	if !r.Complete || r.HTTPStatus != 200 || r.Payload == nil || r.ProtocolError != nil {
		return p.IdentityResponse{}, fmt.Errorf("identity unavailable")
	}
	v := *r.Payload
	if err = v.Validate(); err != nil {
		return v, err
	}
	if v.IdentityHash != in.Profile.IdentityHash || !reflect.DeepEqual(v.Identity, in.Profile.Identity) {
		return v, fmt.Errorf("live identity changed")
	}
	return v, nil
}
func (e Engine) journal(ctx context.Context, in RunInput, h Hooks) (p.Journal, error) {
	r, err := e.Transport.Journal(ctx, in.RunID)
	if saveErr := h.recordResponse(ctx, "journal", r); saveErr != nil {
		return p.Journal{}, saveErr
	}
	if err != nil {
		return p.Journal{}, err
	}
	if !r.Complete || r.HTTPStatus != 200 || r.Payload == nil || r.ProtocolError != nil {
		return p.Journal{}, fmt.Errorf("journal unavailable")
	}
	return *r.Payload, nil
}
func (e Engine) recover(ctx context.Context, in RunInput, h Hooks, i int, endpoint p.Endpoint, hash string) (p.Receipt, error) {
	live, err := e.identity(ctx, in, h)
	if err != nil {
		return p.Receipt{}, err
	}
	epoch := in.Requests[0].Epoch + 1
	if live.Epoch != epoch {
		return p.Receipt{}, fmt.Errorf("recovery epoch changed")
	}
	j, err := e.journal(ctx, in, h)
	if err != nil {
		return p.Receipt{}, err
	}
	if !reflect.DeepEqual(j.Identity, live.Identity) {
		return p.Receipt{}, fmt.Errorf("journal identity changed")
	}
	return p.Reconcile(j, in.Requests[i], endpoint, hash)
}
