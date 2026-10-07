package backendreplay

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"github.com/yashok111/mocker/internal/testkit"
)

func replayServiceFixture(t *testing.T) (*Service, string, StartInput, *engineTransport) {
	t.Helper()
	db := testkit.NewDB(t)
	graphs := backendmodel.NewRepo(db)
	project, err := graphs.Create(t.Context(), backendmodel.CreateInput{Name: "Replay", IdempotencyKey: "replay-project"})
	if err != nil {
		t.Fatal(err)
	}
	_, provenance, journal, live := engineFixture(t, "fixed")
	transport := &engineTransport{journal: journal, live: live, lost: -1}
	s := NewService(NewRepo(db), graphs, []Target{{TargetInfo: TargetInfo{ID: "test", Version: 1, IsolationID: live.Identity.IsolationID}, Transport: transport, ConfigFingerprint: "fixture"}})
	s.ActorAllowed = func(context.Context, string) (bool, error) { return true, nil }
	profile, err := s.Connect(t.Context(), project.ID, "actor", ConnectInput{ConfiguredTargetID: "test", ExpectedIdentityHash: live.IdentityHash, AllowReset: true, IdempotencyKey: newReplayID()})
	if err != nil {
		t.Fatal(err)
	}
	target := backendmodel.BackendReadTarget{RevisionID: project.CurrentRevisionID}
	graph, err := graphs.ResolveEffectiveGraph(t.Context(), project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	pkg := Template()
	pkg.Profile = profile.Pin
	pkg.Target = target
	pkg.TargetHash = graph.Pins.TargetHash
	saved, err := s.SavePackage(t.Context(), project.ID, "actor", SavePackageInput{ID: newReplayID(), Package: pkg, Provenance: provenance, IdempotencyKey: newReplayID()})
	if err != nil {
		t.Fatal(err)
	}
	if transport.mutations != 0 {
		t.Fatal("connect/save mutated target")
	}
	in := StartInput{Package: saved.Pin, Profile: profile.Pin, ExpectedIdentityHash: profile.IdentityHash, ResetAuthorizationID: profile.Authorization.ID, ResetAuthorizationVersion: 1, IdempotencyKey: newReplayID()}
	return s, project.ID, in, transport
}
func TestReplayAdmissionReceiptLeaseAndQueuedCancellation(t *testing.T) {
	s, pid, in, transport := replayServiceFixture(t)
	first, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil || !reflect.DeepEqual(first, repeated) {
		t.Fatalf("receipt changed: %v", err)
	}
	changed := in
	changed.ExpectedIdentityHash = p.HashBytes([]byte("different"))
	if _, err = s.Start(t.Context(), pid, "actor", changed); err == nil {
		t.Fatal("key conflict accepted")
	}
	second := in
	second.IdempotencyKey = newReplayID()
	if _, err = s.Start(t.Context(), pid, "actor", second); err == nil {
		t.Fatal("concurrent target lease accepted")
	}
	cancelled, err := s.Cancel(t.Context(), pid, "actor", first.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel: %v", err)
	}
	repeated, err = s.Start(t.Context(), pid, "actor", in)
	if err != nil || !reflect.DeepEqual(first, repeated) {
		t.Fatal("terminal changed original start receipt")
	}
	if _, err = s.Start(t.Context(), pid, "actor", second); err != nil {
		t.Fatal("queued cancel did not release target", err)
	}
	if transport.mutations != 0 {
		t.Fatal("admission dispatched work")
	}
	if _, err = s.Get(t.Context(), newReplayID(), first.ID); err == nil {
		t.Fatal("cross project read accepted")
	}
}
func TestReplayRestartInterruptsWithoutDispatchAndRequiresAcknowledgment(t *testing.T) {
	s, pid, in, transport := replayServiceFixture(t)
	first, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	// A run that persisted a step may have dispatched it, so its lease stays
	// uncertain; one that never did is released instead (review 2026-10-06,
	// F6, TestReplayRestartReleasesNeverDispatchedLease).
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("claim %v", err)
	}
	persistFirstStep(t, s, actor, claimed)
	if err = s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(t.Context(), pid, first.ID)
	if err != nil || current.Status != "interrupted" || current.Report == nil || current.Report.Status != "interrupted" {
		t.Fatalf("recover: %v", err)
	}
	next := in
	next.IdempotencyKey = newReplayID()
	if _, err = s.Start(t.Context(), pid, "actor", next); err == nil {
		t.Fatal("uncertain lease silently released")
	}
	next.AcknowledgedPreviousRunID = first.ID
	if _, err = s.Start(t.Context(), pid, "actor", next); err != nil {
		t.Fatal(err)
	}
	if transport.mutations != 0 {
		t.Fatal("restart dispatched work")
	}
}
func TestReplayRevocationStopsNextIntentAndTerminalIsImmutable(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed.ID != run.ID || actor != "actor" {
		t.Fatalf("claim: %v", err)
	}
	f := run.Input.Requests[0]
	request := p.ResetRequest{Fence: f, Authorization: run.Input.Profile.Authorization, FixtureHash: p.FixtureHash()}
	hash, _ := p.RequestHash(p.ResetEndpoint, request)
	raw, _ := p.Encode(request)
	if err = s.repo.beforeMutation(t.Context(), pid, actor, run.Input, p.ResetEndpoint, f, hash, raw); err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke(t.Context(), pid, actor, RevokeInput{Profile: in.Profile, IdempotencyKey: newReplayID()}); err != nil {
		t.Fatal(err)
	}
	f = run.Input.Requests[1]
	f.Epoch = 1
	if err = s.repo.beforeMutation(t.Context(), pid, actor, run.Input, p.FailureEndpoint, f, p.HashBytes([]byte("arm")), []byte(`{}`)); err == nil {
		t.Fatal("revoked consent dispatched next step")
	}
	if _, err = s.Cancel(t.Context(), pid, actor, run.ID); err != nil {
		t.Fatal(err)
	}
	late := initialReport(run.Input, run.Provenance)
	late.Status = "succeeded"
	if err = s.repo.finalize(t.Context(), run, late); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(t.Context(), pid, run.ID)
	if err != nil || current.Status != "cancelled" || current.Report.Status != "cancelled" {
		t.Fatal("late result rewrote cancellation", err)
	}
	var n int
	if err = s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_replay_evidence WHERE run_id=? AND kind='late_terminal'`, run.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("late evidence absent", err)
	}
	if _, err = s.Reconcile(t.Context(), pid, actor, run.ID); err == nil {
		t.Fatal("foreign journal released uncertain lease")
	}
	if err = s.repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_replay_runs SET status='succeeded' WHERE id=?`, run.ID)
		return err
	}); err == nil {
		t.Fatal("terminal SQL protection missing")
	}
}
func TestReplaySavedPackageVersionAndProvenance(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	saved, err := s.GetPackage(t.Context(), pid, in.Package.ID, in.Package.Version)
	if err != nil {
		t.Fatal(err)
	}
	update := SavePackageInput{ID: saved.Pin.ID, ExpectedVersion: 1, Package: saved.Package, Provenance: saved.Provenance, IdempotencyKey: newReplayID()}
	update.Provenance.SourceFiles = nil
	if _, err = s.SavePackage(t.Context(), pid, "actor", update); err == nil {
		t.Fatal("missing source tree admitted")
	}
	update.Provenance = saved.Provenance
	update.ExpectedVersion = 0
	if _, err = s.SavePackage(t.Context(), pid, "actor", update); err == nil {
		t.Fatal("stale package version accepted")
	}
	update.ExpectedVersion = 1
	v2, err := s.SavePackage(t.Context(), pid, "actor", update)
	if err != nil || v2.Pin.Version != 2 {
		t.Fatalf("version save: %v", err)
	}
	original, err := s.GetPackage(t.Context(), pid, saved.Pin.ID, 1)
	if err != nil || !reflect.DeepEqual(saved, original) {
		t.Fatal("original package changed", err)
	}
}

func TestReplayWorkerRevokedConsentHasNoEffectsAndDrains(t *testing.T) {
	s, pid, in, transport := replayServiceFixture(t)
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Revoke(t.Context(), pid, "actor", RevokeInput{Profile: in.Profile, IdempotencyKey: newReplayID()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	if err = s.WaitRunning(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := s.Get(t.Context(), pid, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "unverified" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not stop revoked run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if transport.mutations != 0 || s.Running() {
		t.Fatal("revoked work dispatched or worker did not drain")
	}
}

func TestReplayRegistryAliasCannotBypassUncertainLease(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	if _, err := s.Start(t.Context(), pid, "actor", in); err != nil {
		t.Fatal(err)
	}
	renamed := s.targets["test"]
	renamed.ID = "renamed"
	if err := s.repo.registerConfigs(t.Context(), []Target{renamed}); err == nil {
		t.Fatal("renaming configured target bypassed active isolation lease")
	}
}
