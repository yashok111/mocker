package designscenario

import (
	"context"
	"errors"
	"fmt"
)

const (
	maxRunOccurrences = 1000
	maxRunDecisions   = 10000
)

type flowScope struct{ parent, branch string }
type flowRunner struct {
	e                 *runEngine
	ctx               context.Context
	execute           StepExecutor
	positions         map[string]int
	children          map[flowScope][]Fragment
	occurrences       map[string]int
	visits, decisions int
	order             []int
	ordered           map[int]bool
	initialSteps      []StepResult
}

func (e *runEngine) runControlFlow(ctx context.Context, execute StepExecutor) RunReport {
	doc := e.report.Document
	f := flowRunner{e: e, ctx: ctx, execute: execute, positions: map[string]int{}, children: map[flowScope][]Fragment{}, occurrences: map[string]int{}, ordered: map[int]bool{}, initialSteps: append([]StepResult(nil), e.report.Steps...)}
	for i, m := range doc.Messages {
		f.positions[m.ID] = i
	}
	for _, fragment := range doc.Fragments {
		key := flowScope{fragment.ParentFragmentID, fragment.ParentBranchID}
		f.children[key] = append(f.children[key], fragment)
	}
	if err := f.scope(0, len(doc.Messages)-1, flowScope{}, nil); err != nil {
		status := "failed"
		if ctx.Err() != nil {
			status = "cancelled"
			err = errors.New(runCancellationReason(ctx))
		}
		f.reorder()
		return e.finish(status, err.Error())
	}
	f.reorder()
	if ctx.Err() != nil {
		return e.finish("cancelled", runCancellationReason(ctx))
	}
	return e.finish("passed", "")
}

func (f *flowRunner) scope(start, end int, key flowScope, path []LoopIteration) error {
	children := f.children[key]
	for pos := start; pos <= end; {
		if err := f.ctx.Err(); err != nil {
			return err
		}
		var child *Fragment
		for i := range children {
			if f.positions[children[i].FromMessageID] == pos {
				child = &children[i]
				break
			}
		}
		if child == nil {
			if err := f.visit(pos, path); err != nil {
				return err
			}
			pos++
			continue
		}
		last := f.positions[child.ToMessageID]
		if err := f.fragment(*child, path); err != nil {
			return err
		}
		pos = last + 1
	}
	return nil
}

func (f *flowRunner) fragment(fragment Fragment, path []LoopIteration) error {
	start, end := f.positions[fragment.FromMessageID], f.positions[fragment.ToMessageID]
	switch fragment.Kind {
	case "alt":
		return f.altFragment(fragment, path)
	case "opt":
		match, err := evaluateCondition(fragment.Execution.Condition, f.e.report.Variables)
		if err != nil {
			return err
		}
		result := ControlFlowResult{FragmentID: fragment.ID, Outcome: "skipped", Iterations: path, Reason: "Условие не выполнено."}
		if match {
			result.Outcome = "taken"
			result.Reason = ""
		}
		if err := f.record(result); err != nil {
			return err
		}
		if match {
			return f.scope(start, end, flowScope{fragment.ID, ""}, path)
		}
		return f.skip(start, end, "Условие opt не выполнено.", path)
	case "loop":
		return f.loopFragment(fragment, start, end, path)
	}
	return nil
}

// altFragment records a decision for every branch before running the chosen
// one and skipping the rest, as coverage expects.
func (f *flowRunner) altFragment(fragment Fragment, path []LoopIteration) error {
	choice := -1
	for i, b := range fragment.Branches {
		if b.Execution.Otherwise {
			if choice < 0 {
				choice = i
			}
			break
		}
		match, err := evaluateCondition(b.Execution.Condition, f.e.report.Variables)
		if err != nil {
			return err
		}
		if choice < 0 && match {
			choice = i
			break
		}
	}
	for i, b := range fragment.Branches {
		result := ControlFlowResult{FragmentID: fragment.ID, BranchID: b.ID, Outcome: "skipped", Iterations: path}
		if i == choice {
			result.Outcome = "taken"
		} else {
			result.Reason = "Условие ветки не выполнено."
		}
		if err := f.record(result); err != nil {
			return err
		}
	}
	for i, b := range fragment.Branches {
		first, last := f.positions[b.FromMessageID], f.positions[b.ToMessageID]
		if i == choice {
			if err := f.scope(first, last, flowScope{fragment.ID, b.ID}, path); err != nil {
				return err
			}
		} else {
			if err := f.skip(first, last, "Ветка не выбрана.", path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *flowRunner) loopFragment(fragment Fragment, start, end int, path []LoopIteration) error {
	for iteration := 1; iteration <= fragment.Execution.Iterations; iteration++ {
		if err := f.ctx.Err(); err != nil {
			return err
		}
		nextPath := append(append([]LoopIteration(nil), path...), LoopIteration{FragmentID: fragment.ID, Iteration: iteration})
		if fragment.Execution.Condition != nil {
			match, err := evaluateCondition(fragment.Execution.Condition, f.e.report.Variables)
			if err != nil {
				return err
			}
			if !match {
				if err := f.record(ControlFlowResult{FragmentID: fragment.ID, Outcome: "skipped", Iterations: nextPath, Reason: "Условие цикла не выполнено."}); err != nil {
					return err
				}
				return f.skip(start, end, "Условие цикла не выполнено.", nextPath)
			}
		}
		if err := f.record(ControlFlowResult{FragmentID: fragment.ID, Outcome: "taken", Iterations: nextPath}); err != nil {
			return err
		}
		if err := f.scope(start, end, flowScope{fragment.ID, ""}, nextPath); err != nil {
			return err
		}
	}
	return nil
}

func evaluateCondition(condition *ExecutionCondition, variables ExecutionValues) (bool, error) {
	value, exists := variables[condition.Variable]
	switch condition.Operator {
	case "exists":
		return exists, nil
	case "not_exists":
		return !exists, nil
	case "equals", "not_equals":
		if !exists {
			return false, fmt.Errorf("переменная %q для условия отсутствует", condition.Variable)
		}
		if condition.Operator == "equals" {
			return value == *condition.Value, nil
		}
		return value != *condition.Value, nil
	}
	return false, errors.New("неизвестный оператор условия")
}

func (f *flowRunner) record(result ControlFlowResult) error {
	if f.decisions >= maxRunDecisions {
		return errors.New("превышен предел 10000 решений управления")
	}
	f.decisions++
	if err := f.e.retain(result, 1); err != nil {
		return err
	}
	f.e.report.ControlFlow = append(f.e.report.ControlFlow, result)
	return f.e.publish()
}

func (f *flowRunner) skip(start, end int, reason string, path []LoopIteration) error {
	for i := start; i <= end; i++ {
		if f.visits >= maxRunOccurrences {
			return errors.New("превышен предел 1000 посещений сообщений")
		}
		f.visits++
		message := f.e.report.Document.Messages[i]
		occurrence := f.occurrences[message.ID] + 1
		f.occurrences[message.ID] = occurrence
		index := i
		if occurrence > 1 {
			f.e.report.Steps = append(f.e.report.Steps, StepResult{MessageID: message.ID, Status: "skipped", Reason: reason, Assertions: []AssertionResult{}, Occurrence: occurrence, Iterations: append([]LoopIteration(nil), path...)})
			index = len(f.e.report.Steps) - 1
		} else {
			step := &f.e.report.Steps[i]
			step.Status = "skipped"
			step.Reason = reason
			step.Occurrence = 1
			step.Iterations = append([]LoopIteration(nil), path...)
		}
		f.addOrder(index)
	}
	return nil
}

func (f *flowRunner) addOrder(index int) {
	if !f.ordered[index] {
		f.ordered[index] = true
		f.order = append(f.order, index)
	}
}
func (f *flowRunner) reorder() {
	for i := range f.e.report.Steps {
		f.addOrder(i)
	}
	ordered := make([]StepResult, 0, len(f.order))
	for _, i := range f.order {
		ordered = append(ordered, f.e.report.Steps[i])
	}
	f.e.report.Steps = ordered
}

func (f *flowRunner) visit(messageIndex int, path []LoopIteration) error {
	message := f.e.report.Document.Messages[messageIndex]
	occurrence := f.occurrences[message.ID] + 1
	f.occurrences[message.ID] = occurrence
	if f.visits >= maxRunOccurrences {
		return errors.New("превышен предел 1000 посещений сообщений")
	}
	f.visits++
	index := messageIndex
	if occurrence > 1 {
		step := StepResult{MessageID: message.ID, Status: "pending", Assertions: []AssertionResult{}, Occurrence: occurrence, Iterations: path}
		if f.initialSteps[messageIndex].Status == "skipped" {
			step.Status = "skipped"
			step.Reason = f.initialSteps[messageIndex].Reason
		}
		f.e.report.Steps = append(f.e.report.Steps, step)
		index = len(f.e.report.Steps) - 1
	} else {
		f.e.report.Steps[index].Occurrence = 1
		f.e.report.Steps[index].Iterations = append([]LoopIteration(nil), path...)
	}
	f.addOrder(index)
	step := &f.e.report.Steps[index]
	if step.Status == "skipped" {
		return f.e.publish()
	}
	step.Status = "running"
	if err := f.e.publish(); err != nil {
		return f.fail(index, err)
	}
	if err := f.e.executeStep(f.ctx, index, message, f.execute); err != nil {
		return f.fail(index, err)
	}
	f.e.report.Steps[index].Status = "passed"
	if err := f.e.publish(); err != nil {
		return f.fail(index, err)
	}
	return nil
}

func (f *flowRunner) fail(index int, err error) error {
	status := "failed"
	if f.ctx.Err() != nil {
		status = "cancelled"
	}
	f.e.report.Steps[index].Status = status
	f.e.report.Steps[index].Reason = boundedRunReason(err.Error())
	return err
}
