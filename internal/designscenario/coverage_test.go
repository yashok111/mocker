package designscenario

import "testing"

func TestBuildCoverageRevisionAndZeroHits(t *testing.T) {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Fragments = []Fragment{{ID: "opt", Kind: "opt", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Condition: &ExecutionCondition{Variable: "flag", Operator: "exists"}}}}
	report := RunReport{RunSummary: RunSummary{ScenarioID: r.ScenarioID, RevisionID: r.ID, Status: "passed"}, Steps: []StepResult{{MessageID: "login", Status: "passed"}, {MessageID: "profile", Status: "skipped"}}, ControlFlow: []ControlFlowResult{{FragmentID: "opt", Outcome: "taken"}}}
	other := report
	other.RevisionID++
	active := report
	active.Status = "running"
	got := BuildCoverage(r, []RunReport{other, active, report})
	if got.RunCount != 1 || got.SampleLimit != 50 || len(got.Messages) != 2 || got.Messages[0].Attempted != 1 || got.Messages[1].Skipped != 1 || len(got.Paths) != 2 || got.Paths[0].Hits != 1 || got.Paths[1].Hits != 0 {
		t.Fatalf("coverage=%+v", got)
	}
}
