package backendanalysis

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"
)

type faultEngine struct{ err error }

func (e faultEngine) Analyze(context.Context, *ImmutableInput, func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	return nil, e.err
}

// Review 2026-10-06, F150: every engine failure was stored as code "failed"
// with "Analysis did not complete", so a stale-input conflict (start a new
// analysis) read the same as a bug or an overload (retry).
func TestAnalysisFailedJobKeepsEngineFaultReason(t *testing.T) {
	r, _ := testRepo(t)
	s := NewService(r, nil, faultEngine{err: fault(409, "input_conflict", "Saved pins drifted")})
	if err := s.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	j := mustStart(t, r, "start")
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	deadline := time.Now().Add(5 * time.Second)
	var current *Job
	for {
		var err error
		if current, err = r.Get(t.Context(), projectID, j.ID); err != nil {
			t.Fatal(err)
		}
		if current.Status != "queued" && current.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	d := current.Diagnostic
	if current.Status != "failed" || d == nil || d.Code != "backend_analysis_input_conflict" || d.Message != "Saved pins drifted" {
		t.Fatalf("engine reason lost: %+v %+v", current, d)
	}
}

// Review 2026-10-06, F155: resultBytes below the 128 KiB terminal headroom
// decoded fine and failed only after the graphs were resolved, with a 400 that
// named no field. It is refused at decode, naming the field and its range.
func TestLimitsRefuseResultBytesBelowTerminalHeadroom(t *testing.T) {
	var l Limits
	err := json.Unmarshal([]byte(`{"resultBytes":1000}`), &l)
	requireStatus(t, err, 400)
	if err = json.Unmarshal([]byte(`{"resultBytes":131072}`), &l); err != nil || l.ResultBytes != terminalHeadroom {
		t.Fatalf("headroom itself refused: %v", err)
	}
}
