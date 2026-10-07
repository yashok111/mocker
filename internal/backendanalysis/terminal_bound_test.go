package backendanalysis

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// The three halves of review 2026-10-06 cluster C1: a job-local deterministic
// fault at the terminal write must close that job, never stop the service, and
// startup recovery must be able to close any job the store already holds.

// About 100 KB serialized: past the 64 KiB manifest, inside the 2 MiB input.
func oversizedScope() Scope {
	ids := make([]ObjectAddress, 1000)
	for i := range ids {
		ids[i] = ObjectAddress{RecordType: "node", ID: fmt.Sprintf("node-%04d-%s", i, strings.Repeat("a", 64))}
	}
	return Scope{ChangedIDs: ids}
}

func TestAnalysisStartRefusesScopeBeyondManifestBound(t *testing.T) {
	r, _ := testRepo(t)
	s := NewService(r, nil, completeEngine{})
	in := StartInput{Kind: "diff", FromRevisionID: revisionID, Target: AnalysisTarget{RevisionID: revisionID}, Scope: oversizedScope(), Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: "start"}
	_, err := s.Start(t.Context(), projectID, in)
	requireStatus(t, err, 413)
	if !isFault(err, "backend_analysis_scope_limit") {
		t.Fatalf("want scope_limit, got %v", err)
	}
}

// A job admitted before the admission bound keeps its oversized scope in the
// immutable input. Recovery copies that scope into the prefix manifest; it
// answered manifest_limit and startup failed.
func TestAnalysisRecoveryClosesJobWithOversizedStoredScope(t *testing.T) {
	r, db := testRepo(t)
	in := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", ProjectID: projectID, Kind: "impact", From: backendmodel.BackendReadTarget{RevisionID: revisionID}, To: &backendmodel.BackendReadTarget{RevisionID: revisionID}, Scope: oversizedScope(), Limits: defaultLimits(), ObservationMode: "none"}
	raw, err := canonical(in)
	if err != nil {
		t.Fatal(err)
	}
	j, err := r.Start(t.Context(), PreparedStart{ProjectID: projectID, InputJSON: raw, InputHash: digest(raw), RequestHash: digest([]byte("legacy")), Key: "legacy", OutputReservation: 1 << 20}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(r, nil, completeEngine{})
	if err = s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatalf("recovery refused a stored job: %v", err)
	}
	current, err := r.Get(t.Context(), projectID, j.ID)
	if err != nil || current.Status != "interrupted" {
		t.Fatalf("job not closed: %+v %v", current, err)
	}
	var document string
	if err = db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_analysis_manifests_documents WHERE job_id=?`, j.ID).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if len(document) > maxManifestBytes || !strings.Contains(document, `"manifest_compacted"`) {
		t.Fatalf("prefix manifest not compacted: %d bytes", len(document))
	}
}

// oversizedEngine completes with a manifest no store can accept: the shape of a
// diagnostics run whose unknown-check gaps outgrew 64 KiB.
type oversizedEngine struct{}

func (oversizedEngine) Analyze(context.Context, *ImmutableInput, func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	gaps := make([]Diagnostic, 400)
	for i := range gaps {
		code := fmt.Sprintf("unused_table:%036d", i)
		gaps[i] = Diagnostic{ID: code, Code: code, Message: strings.Repeat("x", 200)}
	}
	return &TerminalSnapshot{Status: "completed", Snapshot: PreparedSnapshot{Manifest: ResultManifest{Verdict: "unknown", Gaps: gaps}}}, nil
}

func TestAnalysisUnpersistableResultFailsJobNotService(t *testing.T) {
	r, _ := testRepo(t)
	s := NewService(r, nil, oversizedEngine{})
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := mustStart(t, r, "first")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	waitTerminal := func(id string) *Job {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case err := <-done:
				t.Fatalf("service stopped over one job: %v", err)
			default:
			}
			j, err := r.Get(t.Context(), projectID, id)
			if err != nil {
				t.Fatal(err)
			}
			if j.Status != "queued" && j.Status != "running" {
				return j
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("job %s never reached a terminal state", id)
		return nil
	}
	j := waitTerminal(first.ID)
	if j.Status != "failed" || j.Diagnostic == nil || j.Diagnostic.Code != "result_unpersistable" {
		t.Fatalf("want failed/result_unpersistable, got %+v", j)
	}
	// The service is still serving: the next job is claimed and closed too.
	second := mustStart(t, r, "second")
	s.hint()
	if j = waitTerminal(second.ID); j.Status != "failed" {
		t.Fatalf("second job: %+v", j)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run reported a job-local fault: %v", err)
	}
}

// The storage-failure half keeps its behaviour: an error that is not a verdict
// about the job (a trigger aborting the insert) still stops the service, as
// TestAnalysisWorkerPersistenceFailureRefusesContinuedRun pins.
