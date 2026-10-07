package backendreplay

import (
	"context"
	"testing"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

// persistedTransport is the fixture transport behind the service's own
// BeforeMutation hook: the hook persists the step row, so the dispatch
// counter it checks is advanced here. afterJournal runs on the identity read
// that follows the journal, the engine's last remote call.
type persistedTransport struct {
	*engineTransport
	afterJournal func()
}

func (t *persistedTransport) Reset(ctx context.Context, r p.ResetRequest) (p.Response[p.Receipt], error) {
	t.authorized++
	return t.engineTransport.Reset(ctx, r)
}
func (t *persistedTransport) Arm(ctx context.Context, r p.FailureRequest) (p.Response[p.Receipt], error) {
	t.authorized++
	return t.engineTransport.Arm(ctx, r)
}
func (t *persistedTransport) Order(ctx context.Context, r p.OrderRequest) (p.Response[p.Receipt], error) {
	t.authorized++
	return t.engineTransport.Order(ctx, r)
}
func (t *persistedTransport) Identity(ctx context.Context) (p.Response[p.IdentityResponse], error) {
	if t.mutations == 4 && t.afterJournal != nil {
		t.afterJournal()
	}
	return t.engineTransport.Identity(ctx)
}

// journalFor re-keys the engineFixture journal onto a run the service
// created: same receipts, events and counters, under this run's fences,
// request hashes and business key, so the engine reaches a full verdict.
func journalFor(t *testing.T, in RunInput, j p.Journal) p.Journal {
	t.Helper()
	out := j
	out.RunID = in.RunID
	out.Receipts = nil
	fences := make([]p.Fence, 4)
	for i, r := range j.Receipts {
		f := in.Requests[i]
		f.Epoch = r.Epoch
		fences[i] = f
		run := in
		run.Requests = append([]p.Fence(nil), in.Requests...)
		run.Requests[i] = f
		endpoint, request := mutation(run, i)
		hash, err := p.RequestHash(endpoint, request)
		if err != nil {
			t.Fatal(err)
		}
		r.Fence, r.RequestHash = f, hash
		if r.BusinessKey != "" {
			r.BusinessKey = in.BusinessKey
		}
		out.Receipts = append(out.Receipts, r)
	}
	out.Events = nil
	for _, e := range j.Events {
		for i, r := range j.Receipts {
			if r.RequestKey == e.RequestKey {
				e.RequestKey, e.StepID = fences[i].RequestKey, fences[i].StepID
			}
		}
		if e.BusinessKey != "" {
			e.BusinessKey = in.BusinessKey
		}
		out.Events = append(out.Events, e)
	}
	out.Orders = append([]p.OrderRecord(nil), j.Orders...)
	for i := range out.Orders {
		out.Orders[i].BusinessKey = in.BusinessKey
	}
	out.Charges = append([]p.ChargeRecord(nil), j.Charges...)
	for i := range out.Charges {
		out.Charges[i].BusinessKey = in.BusinessKey
	}
	return out
}

func withTransport(s *Service, transport *persistedTransport) {
	target := s.targets["test"]
	target.Transport = transport
	s.targets["test"] = target
}

func leaseState(t *testing.T, s *Service, runID string) string {
	t.Helper()
	var state string
	err := s.repo.db.R.QueryRowContext(t.Context(), `SELECT COALESCE((SELECT state FROM backend_replay_target_leases WHERE run_id=?),'released')`, runID).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// review 2026-10-06, F7/F129: a shutdown that began AFTER the engine returned
// a complete verdict rewrote it to "interrupted", and with steps on record
// finalize then fenced the target as uncertain. A finished run keeps its
// verdict and releases its lease.
func TestReplayShutdownAfterVerdictKeepsIt(t *testing.T) {
	s, pid, in, inner := replayServiceFixture(t)
	app, stop := context.WithCancel(t.Context())
	defer stop()
	withTransport(s, &persistedTransport{engineTransport: inner, afterJournal: stop})
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claim %v", err)
	}
	inner.journal = journalFor(t, claimed.Input, inner.journal)
	if err = s.execute(app, actor, claimed); err != nil {
		t.Fatal(err)
	}
	if app.Err() == nil {
		current, _ := s.Get(t.Context(), pid, run.ID)
		t.Fatalf("the shutdown was never triggered; the test no longer covers it: %s", current.Report.Reason)
	}
	current, err := s.Get(t.Context(), pid, run.ID)
	if err != nil || current.Status != "succeeded" {
		t.Fatalf("verdict lost to shutdown: %+v %v", current, err)
	}
	if state := leaseState(t, s, run.ID); state != "released" {
		t.Fatalf("lease %s after a complete verdict", state)
	}
}

// review 2026-10-06, F126: the engine names the cause of an unverified run
// (here the journal no longer witnessing an observed receipt), and the worker
// replaced it with a fixed message that even blamed authorization.
func TestReplayWorkerKeepsEngineReason(t *testing.T) {
	s, pid, in, inner := replayServiceFixture(t)
	inner.missing = true
	withTransport(s, &persistedTransport{engineTransport: inner})
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("claim %v", err)
	}
	inner.journal = journalFor(t, claimed.Input, inner.journal)
	if err = s.execute(t.Context(), actor, claimed); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(t.Context(), pid, run.ID)
	if err != nil || current.Status != "unverified" || current.Report == nil || current.Report.Reason != "journal changed an observed receipt" {
		t.Fatalf("engine reason lost: %+v %v", current.Report, err)
	}
}

// review 2026-10-06, F6/F128: restart recovery marked EVERY interrupted run's
// lease uncertain, so a run that never wrote a step row (never dispatched a
// mutation) still fenced its target until acknowledged. finalize already
// releases such a lease; recovery now applies the same rule, and keeps the
// fence for a run that did persist a step.
func TestReplayRestartReleasesNeverDispatchedLease(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	queued, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if current, err := s.Get(t.Context(), pid, queued.ID); err != nil || current.Status != "interrupted" {
		t.Fatalf("recover: %+v %v", current, err)
	}
	if state := leaseState(t, s, queued.ID); state != "released" {
		t.Fatalf("never-dispatched run left lease %s", state)
	}
	next := in
	next.IdempotencyKey = newReplayID()
	if _, err = s.Start(t.Context(), pid, "actor", next); err != nil {
		t.Fatalf("target still fenced by a run that sent nothing: %v", err)
	}
}

func TestReplayRestartKeepsDispatchedLeaseUncertain(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("claim %v", err)
	}
	persistFirstStep(t, s, actor, claimed)
	if err = s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := leaseState(t, s, run.ID); state != "uncertain" {
		t.Fatalf("dispatched run's lease %s, want uncertain", state)
	}
}

// review 2026-10-06, F6: Cancel of a RUNNING run fenced its target even when
// no step row existed; beforeMutation re-reads the status in its own write,
// so once Cancel commits nothing can be dispatched any more.
func TestReplayCancelRunningReleasesNeverDispatchedLease(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, _, err := s.claim(t.Context()); err != nil || claimed == nil {
		t.Fatalf("claim %v", err)
	}
	if _, err = s.Cancel(t.Context(), pid, "actor", run.ID); err != nil {
		t.Fatal(err)
	}
	if state := leaseState(t, s, run.ID); state != "released" {
		t.Fatalf("cancelled never-dispatched run left lease %s", state)
	}
}

func TestReplayCancelRunningKeepsDispatchedLeaseUncertain(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("claim %v", err)
	}
	persistFirstStep(t, s, actor, claimed)
	if _, err = s.Cancel(t.Context(), pid, "actor", run.ID); err != nil {
		t.Fatal(err)
	}
	if state := leaseState(t, s, run.ID); state != "uncertain" {
		t.Fatalf("dispatched run's lease %s, want uncertain", state)
	}
}

func persistFirstStep(t *testing.T, s *Service, actor string, run *Run) {
	t.Helper()
	endpoint, request := mutation(run.Input, 0)
	hash, err := p.RequestHash(endpoint, request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.repo.beforeMutation(t.Context(), run.ProjectID, actor, run.Input, endpoint, run.Input.Requests[0], hash, raw); err != nil {
		t.Fatal(err)
	}
}
