package designscenario

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func suggestionRevision() Revision {
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Fragments = []Fragment{{ID: "choice", Kind: "alt", FromMessageID: "login", ToMessageID: "profile", Branches: []FragmentBranch{
		{ID: "yes", FromMessageID: "login", ToMessageID: "login", Execution: &BranchExecution{Condition: suggestionCondition("flag", "equals", "yes")}},
		{ID: "other", FromMessageID: "profile", ToMessageID: "profile", Execution: &BranchExecution{Otherwise: true}},
	}}}
	return r
}
func suggestionCondition(variable, operator, value string) *ExecutionCondition {
	c := &ExecutionCondition{Variable: variable, Operator: operator}
	if operator == "equals" || operator == "not_equals" {
		c.Value = &value
	}
	return c
}
func checkSuggestedRuns(t *testing.T, r Revision, got TestSuggestions) {
	t.Helper()
	for _, c := range got.Cases {
		initial, err := PrepareRun(t.Context(), r, c.ID, c.Name, "mcp", c.Variables)
		if err != nil {
			t.Fatal(err)
		}
		run := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { return runResponse(`{"flag":"yes"}`), nil }, nil)
		if run.Status != "passed" {
			t.Fatalf("case %s failed: %s", c.ID, run.Reason)
		}
		for _, target := range c.Targets {
			found := false
			for _, actual := range run.ControlFlow {
				if target.FragmentID == actual.FragmentID && target.BranchID == actual.BranchID && target.Outcome == actual.Outcome {
					found = true
				}
			}
			if !found {
				t.Fatalf("predicted target %+v absent from real trace %+v", target, run.ControlFlow)
			}
		}
	}
}
func TestSuggestTestsProducesRunnableVariantsWithoutMutation(t *testing.T) {
	r := suggestionRevision()
	before, _ := cloneDocument(r.Document)
	cov := BuildCoverage(r, nil)
	got, err := SuggestTests(t.Context(), r, cov)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cases) != 2 || len(got.Unresolved) != 0 || got.RevisionID != r.ID {
		t.Fatalf("suggestions=%+v", got)
	}
	checkSuggestedRuns(t, r, got)
	again, err := SuggestTests(t.Context(), r, cov)
	if err != nil || !reflect.DeepEqual(got, again) || !reflect.DeepEqual(before, r.Document) {
		t.Fatal("non deterministic or mutated input")
	}
	cov.Paths[0].Hits = 1
	got, err = SuggestTests(t.Context(), r, cov)
	if err != nil || len(got.Cases) != 1 || got.Cases[0].Targets[0].BranchID != "other" {
		t.Fatalf("covered branch suggested: %+v %v", got, err)
	}
	cov.Paths[1].Hits = 1
	got, err = SuggestTests(t.Context(), r, cov)
	if err != nil || len(got.Cases) != 0 || len(got.Unresolved) != 0 {
		t.Fatalf("all covered: %+v %v", got, err)
	}
}
func TestSuggestTestsNestedLoopAndOpt(t *testing.T) {
	r := suggestionRevision()
	r.Document.Fragments = append(r.Document.Fragments,
		Fragment{ID: "repeat", Kind: "loop", FromMessageID: "login", ToMessageID: "login", ParentFragmentID: "choice", ParentBranchID: "yes", Execution: &FragmentExecution{Iterations: 2, Condition: suggestionCondition("again", "equals", "yes")}},
		Fragment{ID: "optional", Kind: "opt", FromMessageID: "login", ToMessageID: "login", ParentFragmentID: "repeat", Execution: &FragmentExecution{Condition: suggestionCondition("notify", "exists", "")}})
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Unresolved) != 0 {
		t.Fatalf("nested: %+v %v", got, err)
	}
	covered := map[string]bool{}
	for _, c := range got.Cases {
		for _, target := range c.Targets {
			covered[fmt.Sprint(target)] = true
		}
	}
	if len(covered) != 6 {
		t.Fatalf("expected six targets, got %+v", got)
	}
	checkSuggestedRuns(t, r, got)
}
func TestSuggestTestsFirstMatchingBranchAndContradictoryNesting(t *testing.T) {
	r := suggestionRevision()
	r.Document.Fragments[0].Branches[1].Execution = &BranchExecution{Condition: suggestionCondition("flag", "equals", "yes")}
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Unresolved) != 1 || got.Unresolved[0].Target.BranchID != "other" {
		t.Fatalf("shadowed: %+v %v", got, err)
	}
	checkSuggestedRuns(t, r, got)
	r = suggestionRevision()
	r.Document.Fragments = append(r.Document.Fragments, Fragment{ID: "impossible", Kind: "opt", FromMessageID: "login", ToMessageID: "login", ParentFragmentID: "choice", ParentBranchID: "yes", Execution: &FragmentExecution{Condition: suggestionCondition("flag", "not_equals", "yes")}})
	got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Unresolved) != 1 || got.Unresolved[0].Target.Outcome != "taken" {
		t.Fatalf("contradiction: %+v %v", got, err)
	}
	checkSuggestedRuns(t, r, got)
}
func TestSuggestTestsExtractionUncertaintyAndDisabledSource(t *testing.T) {
	r := suggestionRevision()
	r.Document.Fragments = []Fragment{{ID: "opt", Kind: "opt", FromMessageID: "profile", ToMessageID: "profile", Execution: &FragmentExecution{Condition: suggestionCondition("flag", "equals", "yes")}}}
	r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "flag", Pointer: "/flag"}}
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Cases) != 0 || len(got.Unresolved) != 2 || got.Unresolved[0].Code != "response_dependent" {
		t.Fatalf("unknown response: %+v %v", got, err)
	}
	r.Document.Messages[0].Execution.Enabled = false
	got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Unresolved) != 0 {
		t.Fatalf("disabled source: %+v %v", got, err)
	}
	checkSuggestedRuns(t, r, got)
}
func TestSuggestTestsMissingIsNotEmptyAndCannotRemoveDefault(t *testing.T) {
	for _, operator := range []string{"exists", "not_exists"} {
		r := suggestionRevision()
		r.Document.Fragments = []Fragment{{ID: "opt", Kind: "opt", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Condition: suggestionCondition("flag", operator, "")}}}
		got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
		if err != nil || len(got.Cases) != 2 || len(got.Unresolved) != 0 {
			t.Fatalf("missing: %+v %v", got, err)
		}
		checkSuggestedRuns(t, r, got)
		r.Document.Execution.Variables["flag"] = ""
		got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
		if err != nil || len(got.Cases) != 1 || len(got.Unresolved) != 1 {
			t.Fatalf("default: %+v %v", got, err)
		}
		checkSuggestedRuns(t, r, got)
	}
}
func TestSuggestTestsRejectsStaleCoverageInvalidFormsAndCancellation(t *testing.T) {
	r := suggestionRevision()
	cov := BuildCoverage(r, nil)
	cov.RevisionID++
	if _, err := SuggestTests(t.Context(), r, cov); err == nil {
		t.Fatal("stale coverage accepted")
	}
	r.FormDrafts = map[string]string{"all": "{\"pending\":true}"}
	if _, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil)); err == nil {
		t.Fatal("unfinished forms accepted")
	}
	r.FormDrafts = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := SuggestTests(ctx, r, BuildCoverage(r, nil)); err == nil {
		t.Fatal("cancelled analysis continued")
	}
}

func TestSuggestTestsBoundsSearchAndOutput(t *testing.T) {
	r := suggestionRevision()
	r.Document.Messages = nil
	r.Document.Fragments = nil
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("m%d", i)
		r.Document.Messages = append(r.Document.Messages, Message{ID: id, Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "login"}})
		r.Document.Fragments = append(r.Document.Fragments, Fragment{ID: fmt.Sprintf("f%d", i), Kind: "opt", FromMessageID: id, ToMessageID: id, Execution: &FragmentExecution{Condition: suggestionCondition(fmt.Sprintf("v%d", i), "equals", "yes")}})
	}
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || !got.Truncated || len(got.Cases) > 20 || len(got.Unresolved) == 0 || got.CheckedCandidates > 256 {
		t.Fatalf("case bound: %+v %v", got, err)
	}
	// Make one nested path contradictory so enumeration cannot stop early.
	r = suggestionRevision()
	for i := 0; i < 9; i++ {
		previous := "choice"
		branch := "yes"
		if i > 0 {
			previous = fmt.Sprintf("f%d", i-1)
			branch = ""
		}
		r.Document.Fragments = append(r.Document.Fragments, Fragment{ID: fmt.Sprintf("f%d", i), Kind: "opt", FromMessageID: "login", ToMessageID: "login", ParentFragmentID: previous, ParentBranchID: branch, Execution: &FragmentExecution{Condition: suggestionCondition(fmt.Sprintf("v%d", i), "exists", "")}})
	}
	r.Document.Fragments = append(r.Document.Fragments, Fragment{ID: "impossible", Kind: "opt", FromMessageID: "login", ToMessageID: "login", ParentFragmentID: "f8", Execution: &FragmentExecution{Condition: suggestionCondition("flag", "not_equals", "yes")}})
	got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || !got.Truncated || got.CheckedCandidates != 256 {
		t.Fatalf("search bound: %+v %v", got, err)
	}
}

func TestSuggestTestsLoopExtractionDoesNotReuseInitialValue(t *testing.T) {
	r := suggestionRevision()
	r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "profile", Execution: &FragmentExecution{Iterations: 2, Condition: suggestionCondition("flag", "equals", "yes")}}}
	r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "flag", Pointer: "/flag"}}
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Cases) != 2 || len(got.Unresolved) != 0 {
		t.Fatalf("loop: %+v %v", got, err)
	}
	checkSuggestedRuns(t, r, got)
	// Existence is known after successful extraction even though the value is not.
	r.Document.Fragments = []Fragment{{ID: "opt", Kind: "opt", FromMessageID: "profile", ToMessageID: "profile", Execution: &FragmentExecution{Condition: suggestionCondition("flag", "exists", "")}}}
	got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Cases) != 1 || len(got.Unresolved) != 1 || got.Cases[0].Targets[0].Outcome != "taken" {
		t.Fatalf("extracted existence: %+v %v", got, err)
	}
	checkSuggestedRuns(t, r, got)
}

func TestSuggestTestsRejectsUnrunnableOperation(t *testing.T) {
	r := suggestionRevision()
	r.Document.Messages[0].Operation.OperationKey = "missing"
	if _, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil)); err == nil {
		t.Fatal("suggested run with broken operation")
	}
}

func TestSuggestTestsDoesNotProposeKnownExecutionLimitFailure(t *testing.T) {
	r := suggestionRevision()
	r.Document.Fragments = []Fragment{
		{ID: "outer", Kind: "loop", FromMessageID: "login", ToMessageID: "profile", Execution: &FragmentExecution{Iterations: 100}},
		{ID: "inner", Kind: "loop", FromMessageID: "login", ToMessageID: "profile", ParentFragmentID: "outer", Execution: &FragmentExecution{Iterations: 100}},
	}
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || len(got.Cases) != 0 || len(got.Unresolved) != 2 || !got.Truncated {
		t.Fatalf("known failing run proposed: %+v %v", got, err)
	}
}

func TestSuggestedTestNamesDescribeBranches(t *testing.T) {
	r := suggestionRevision()
	r.Document.Fragments[0].Label = "Результат оплаты"
	r.Document.Fragments[0].Branches[0].Label = "Оплата одобрена"
	r.Document.Fragments[0].Branches[1].Label = "Оплата отклонена"
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cases[0].Name != "Оплата одобрена" || got.Cases[1].Name != "Оплата отклонена" {
		t.Fatalf("names: %+v", got.Cases)
	}
	checkSuggestedRuns(t, r, got)
	for _, kind := range []string{"opt", "loop"} {
		r.Document.Fragments = []Fragment{{ID: "f", Kind: kind, Label: "Уведомление клиента", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Condition: suggestionCondition("notify", "equals", "yes")}}}
		if kind == "loop" {
			r.Document.Fragments[0].Execution.Iterations = 2
		}
		got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got.Cases[0].Name != "Выполнить «Уведомление клиента»" || got.Cases[1].Name != "Пропустить «Уведомление клиента»" {
			t.Fatalf("%s names: %+v", kind, got.Cases)
		}
	}
}

func TestSuggestedTestNamesRemainUsefulWithoutLabelsAndWithinRunLimit(t *testing.T) {
	r := suggestionRevision()
	got, err := SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cases[0].Name != "flag = «yes»" || got.Cases[1].Name != "Иначе: выбор 1" {
		t.Fatalf("fallback names: %+v", got.Cases)
	}
	r.Document.Fragments[0].Branches[0].Label = "  Оплата\n одобрена  "
	r.Document.Fragments = append(r.Document.Fragments, Fragment{ID: "notify", Kind: "opt", Label: "Уведомление", FromMessageID: "login", ToMessageID: "login", ParentFragmentID: "choice", ParentBranchID: "yes", Execution: &FragmentExecution{Condition: suggestionCondition("notify", "exists", "")}})
	got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil || got.Cases[0].Name != "Оплата одобрена · ещё веток: 1" {
		t.Fatalf("multiple: %+v %v", got, err)
	}
	r.Document.Fragments[0].Branches[0].Label = strings.Repeat("Я🌍", 150)
	got, err = SuggestTests(t.Context(), r, BuildCoverage(r, nil))
	if err != nil {
		t.Fatal(err)
	}
	name := got.Cases[0].Name
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 200 || !strings.HasSuffix(name, "… · ещё веток: 1") {
		t.Fatalf("long name: %s", name)
	}
	checkSuggestedRuns(t, r, got)
}
