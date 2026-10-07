package backendreplay

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

// Review 2026-10-06, F192 (the replay sibling of F4): a writer held past the
// 5 s persist window made finalize answer "begin: context deadline exceeded",
// and the worker turned it into the service's failure -- the server stopped,
// the verdict was lost and recovery fenced the target. The wait is retried.
func TestReplayBusyWriterAtFinalizeDoesNotFailService(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	s.persistTimeout = 30 * time.Millisecond
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	// Revoked consent ends the run before any dispatch, straight at finalize.
	if err = s.Revoke(t.Context(), pid, "actor", RevokeInput{Profile: in.Profile, IdempotencyKey: newReplayID()}); err != nil {
		t.Fatal(err)
	}
	claimed, actor, err := s.claim(t.Context())
	if err != nil || claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claim %v", err)
	}
	held, released := make(chan struct{}), make(chan error, 1)
	go func() {
		released <- s.repo.db.Write(context.Background(), func(*sql.Tx) error {
			close(held)
			time.Sleep(400 * time.Millisecond)
			return nil
		})
	}()
	<-held
	if err = s.execute(t.Context(), actor, claimed); err != nil {
		t.Fatalf("busy writer reported as a service failure: %v", err)
	}
	if err = <-released; err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(t.Context(), pid, run.ID)
	if err != nil || current.Status != "unverified" {
		t.Fatalf("want unverified, got %+v %v", current, err)
	}
}

// A report over the 4 MiB limit is a verdict about this run, not about the
// store: it is closed unverified on a bounded report instead of stopping the
// service (F192).
func TestReplayOversizedReportClosesRunUnverified(t *testing.T) {
	s, pid, in, _ := replayServiceFixture(t)
	run, err := s.Start(t.Context(), pid, "actor", in)
	if err != nil {
		t.Fatal(err)
	}
	claimed, _, err := s.claim(t.Context())
	if err != nil || claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claim %v", err)
	}
	report := initialReport(claimed.Input, claimed.Provenance)
	report.Status = "succeeded"
	report.Reason = strings.Repeat("x", p.ReportLimit)
	if err = s.repo.finalize(t.Context(), claimed, report); err == nil {
		t.Fatal("the store accepted an oversized report; the test no longer covers the limit")
	}
	if err = s.finalize(t.Context(), claimed, report); err != nil {
		t.Fatalf("oversized report reported as a service failure: %v", err)
	}
	current, err := s.Get(t.Context(), pid, run.ID)
	if err != nil || current.Status != "unverified" || current.Report == nil || !strings.Contains(current.Report.Reason, "report limit") {
		t.Fatalf("want unverified with the limit named, got %+v %v", current, err)
	}
}
