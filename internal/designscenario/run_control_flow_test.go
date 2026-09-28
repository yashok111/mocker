package designscenario

import (
	"context"
	"testing"
)

func TestRunControlFlowOccurrences(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Fragments = []Fragment{{ID: "repeat", Kind: "loop", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Iterations: 2}}}
	initial := prepareTestRun(t, r)
	calls := 0
	result := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(""), nil }, nil)
	if result.Status != "passed" || calls != 3 {
		t.Fatalf("status=%s calls=%d", result.Status, calls)
	}
	if len(result.Steps) != 3 || result.Steps[0].Occurrence != 1 || result.Steps[1].Occurrence != 2 {
		t.Fatalf("steps=%+v", result.Steps)
	}
	if len(result.ControlFlow) != 2 || result.ControlFlow[0].Iterations[0].Iteration != 1 {
		t.Fatalf("flow=%+v", result.ControlFlow)
	}
}

func TestRunControlFlowBranchAndMissingVariable(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	value := "yes"
	r.Document.Fragments = []Fragment{{ID: "choice", Kind: "alt", FromMessageID: "login", ToMessageID: "profile", Branches: []FragmentBranch{{ID: "a", FromMessageID: "login", ToMessageID: "login", Execution: &BranchExecution{Condition: &ExecutionCondition{Variable: "flag", Operator: "equals", Value: &value}}}, {ID: "b", FromMessageID: "profile", ToMessageID: "profile", Execution: &BranchExecution{Otherwise: true}}}}}
	initial := prepareTestRun(t, r)
	result := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) {
		t.Fatal("unexpected dispatch")
		return StepResponse{}, nil
	}, nil)
	if result.Status != "failed" {
		t.Fatalf("status=%s", result.Status)
	}
	r.Document.Execution.Variables["flag"] = "no"
	initial = prepareTestRun(t, r)
	result = Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { return runResponse(""), nil }, nil)
	if result.Status != "passed" || len(result.Steps) != 2 || result.Steps[0].Status != "skipped" || result.Steps[1].Status != "passed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunControlFlowLoopRechecksExtraction(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Execution.Variables["again"] = "yes"
	r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "again", Pointer: "/again"}}
	yes := "yes"
	r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Iterations: 3, Condition: &ExecutionCondition{Variable: "again", Operator: "equals", Value: &yes}}}}
	initial := prepareTestRun(t, r)
	calls := 0
	result := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) {
		calls++
		if calls == 1 {
			return runResponse(`{"again":"yes"}`), nil
		}
		return runResponse(`{"again":"no"}`), nil
	}, nil)
	if result.Status != "passed" || calls != 3 || len(result.ControlFlow) != 3 || result.ControlFlow[2].Outcome != "skipped" {
		t.Fatalf("status=%s calls=%d flow=%+v", result.Status, calls, result.ControlFlow)
	}
	if len(result.Steps) != 4 || result.Steps[2].Status != "skipped" {
		t.Fatalf("steps=%+v", result.Steps)
	}
}

func TestRunControlFlowOccurrenceLimit(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Messages = r.Document.Messages[:1]
	for i := 1; i < 11; i++ {
		m := r.Document.Messages[0]
		m.ID = string(rune('a' + i))
		r.Document.Messages = append(r.Document.Messages, m)
	}
	r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: r.Document.Messages[10].ID, Execution: &FragmentExecution{Iterations: 100}}}
	initial := prepareTestRun(t, r)
	calls := 0
	result := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(""), nil }, nil)
	if result.Status != "failed" || calls != 1000 {
		t.Fatalf("status=%s calls=%d", result.Status, calls)
	}
}

func TestRunControlFlowCancelledMidLoop(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Iterations: 10}}}
	initial := prepareTestRun(t, r)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	result := Run(ctx, r, initial, func(context.Context, StepRequest) (StepResponse, error) {
		calls++
		cancel()
		return runResponse(""), nil
	}, nil)
	if result.Status != "cancelled" || calls != 1 {
		t.Fatalf("status=%s calls=%d", result.Status, calls)
	}
}

func TestRunControlFlowSkippedOptCanRunOnNextIteration(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "show", Pointer: "/show"}}
	yes := "yes"
	r.Document.Fragments = []Fragment{
		{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "profile", Execution: &FragmentExecution{Iterations: 2}},
		{ID: "opt", Kind: "opt", FromMessageID: "profile", ToMessageID: "profile", ParentFragmentID: "loop", Execution: &FragmentExecution{Condition: &ExecutionCondition{Variable: "show", Operator: "equals", Value: &yes}}},
	}
	initial := prepareTestRun(t, r)
	sources, profiles := 0, 0
	result := Run(t.Context(), r, initial, func(_ context.Context, request StepRequest) (StepResponse, error) {
		if request.MessageID == "login" {
			sources++
			if sources == 1 {
				return runResponse(`{"show":"no"}`), nil
			}
			return runResponse(`{"show":"yes"}`), nil
		}
		profiles++
		return runResponse(""), nil
	}, nil)
	if result.Status != "passed" || profiles != 1 || len(result.Steps) != 4 || result.Steps[1].Status != "skipped" || result.Steps[3].Status != "passed" {
		t.Fatalf("profiles=%d steps=%+v status=%s", profiles, result.Steps, result.Status)
	}
}

func TestRunControlFlowSkippedAltBranchCanRunOnNextIteration(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "choice", Pointer: "/choice"}}
	alternate := r.Document.Messages[1]
	alternate.ID = "other"
	r.Document.Messages = append(r.Document.Messages, alternate)
	yes := "yes"
	r.Document.Fragments = []Fragment{
		{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "other", Execution: &FragmentExecution{Iterations: 2}},
		{ID: "alt", Kind: "alt", FromMessageID: "profile", ToMessageID: "other", ParentFragmentID: "loop", Branches: []FragmentBranch{{ID: "yes", FromMessageID: "profile", ToMessageID: "profile", Execution: &BranchExecution{Condition: &ExecutionCondition{Variable: "choice", Operator: "equals", Value: &yes}}}, {ID: "other", FromMessageID: "other", ToMessageID: "other", Execution: &BranchExecution{Otherwise: true}}}},
	}
	initial := prepareTestRun(t, r)
	sources, profiles, others := 0, 0, 0
	result := Run(t.Context(), r, initial, func(_ context.Context, request StepRequest) (StepResponse, error) {
		switch request.MessageID {
		case "login":
			sources++
			if sources == 1 {
				return runResponse(`{"choice":"no"}`), nil
			}
			return runResponse(`{"choice":"yes"}`), nil
		case "profile":
			profiles++
		case "other":
			others++
		}
		return runResponse(""), nil
	}, nil)
	if result.Status != "passed" || profiles != 1 || others != 1 || len(result.Steps) != 6 || result.Steps[1].Status != "skipped" || result.Steps[4].Status != "passed" {
		t.Fatalf("profiles=%d others=%d steps=%+v status=%s", profiles, others, result.Steps, result.Status)
	}
}
