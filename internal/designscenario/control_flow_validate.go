package designscenario

import (
	"fmt"
	"unicode/utf8"
)

func validateControlFlow(document Document, complete bool) []Diagnostic {
	var out []Diagnostic
	add := func(p, m string) { out = append(out, Diagnostic{Pointer: p, Message: m, Severity: "error"}) }
	for i, f := range document.Fragments {
		p := fmt.Sprintf("/fragments/%d", i)
		if f.Kind == "alt" {
			if f.Execution != nil {
				add(p+"/execution", "alt execution belongs to branches")
			}
			otherwise := false
			for j, b := range f.Branches {
				bp := fmt.Sprintf("%s/branches/%d/execution", p, j)
				if b.Execution == nil {
					if complete {
						add(bp, "branch condition or otherwise is required")
					}
					continue
				}
				e := b.Execution
				if e.Otherwise {
					if otherwise || j != len(f.Branches)-1 {
						add(bp+"/otherwise", "otherwise must occur once and last")
					}
					otherwise = true
				}
				if e.Otherwise == (e.Condition != nil) {
					add(bp, "branch requires exactly one condition or otherwise")
				}
				if e.Condition != nil {
					validateCondition(e.Condition, bp+"/condition", add)
				}
			}
		} else {
			if f.Execution == nil {
				if complete {
					add(p+"/execution", "fragment execution is required")
				}
				continue
			}
			if f.Kind == "opt" {
				if f.Execution.Iterations != 0 {
					add(p+"/execution/iterations", "only loop accepts iterations")
				}
				if f.Execution.Condition == nil {
					add(p+"/execution/condition", "opt condition is required")
				}
			}
			if f.Kind == "loop" && (f.Execution.Iterations < 1 || f.Execution.Iterations > 100) {
				add(p+"/execution/iterations", "iterations must be between 1 and 100")
			}
			if f.Execution.Condition != nil {
				validateCondition(f.Execution.Condition, p+"/execution/condition", add)
			}
		}
	}
	return out
}
func validateCondition(c *ExecutionCondition, p string, add func(string, string)) {
	if !executionVariableName.MatchString(c.Variable) {
		add(p+"/variable", "invalid variable name")
	}
	switch c.Operator {
	case "equals", "not_equals":
		if c.Value == nil {
			add(p+"/value", "value is required")
		} else if utf8.RuneCountInString(*c.Value) > maxText {
			add(p+"/value", "value is too long")
		}
	case "exists", "not_exists":
		if c.Value != nil {
			add(p+"/value", "value is not allowed")
		}
	default:
		add(p+"/operator", "unknown operator")
	}
}
