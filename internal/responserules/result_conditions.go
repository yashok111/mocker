package responserules

import (
	"context"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (c *ResultCondition) UnmarshalJSON(data []byte) error {
	data, err := compactBound(data, MaxGraphBytes)
	if err != nil {
		return err
	}
	leaves := 0
	v, err := decodeResultCondition(data, 1, &leaves)
	if err != nil {
		return err
	}
	*c = v
	return nil
}

// Groups decode raw children with one shared budget. Calling UnmarshalJSON on
// each child would reset the budget and admit arbitrarily wide/deep trees.
func decodeResultCondition(data []byte, level int, leaves *int) (ResultCondition, error) {
	var v ResultCondition
	if level > MaxResultConditionDepth {
		return v, invalid("", "условие результата превышает четыре уровня")
	}
	fields, err := decodeObjectFields(data)
	if err != nil {
		return v, err
	}
	_, all := fields["all"]
	_, anyGroup := fields["any"]
	if all || anyGroup {
		name := "all"
		if !all {
			name = "any"
		}
		var raw wireArray[jsonx.RawMessage]
		if err := decodeObject(data, required(name, &raw)); err != nil {
			return v, err
		}
		if len(raw) < 2 || len(raw) > MaxResultConditionLeaves {
			return v, invalid("/"+name, "группа требует от двух до 16 условий")
		}
		children := make([]ResultCondition, len(raw))
		for i, child := range raw {
			children[i], err = decodeResultCondition(child, level+1, leaves)
			if err != nil {
				return v, at(fmt.Sprintf("/%s/%d", name, i), err)
			}
		}
		if all {
			v.All = children
		} else {
			v.Any = children
		}
		return v, nil
	}
	*leaves++
	if *leaves > MaxResultConditionLeaves {
		return v, invalid("", "допустимо не более 16 листовых условий")
	}
	for _, name := range []string{"source", "valueFrom"} {
		if raw, present := fields[name]; present {
			if _, err := decodeObjectFields(raw); err != nil {
				return v, at("/"+name, err)
			}
		}
	}
	if err := decodeObject(data, required("source", &v.Source), required("op", &v.Op), optional("valueJSON", &v.ValueJSON), optional("valueFrom", &v.ValueFrom)); err != nil {
		return v, err
	}
	return v, checkResultConditionLeaf(v)
}

func checkResultCondition(c ResultCondition) error {
	leaves := 0
	return checkResultConditionTree(c, 1, &leaves)
}

func checkResultConditionTree(c ResultCondition, level int, leaves *int) error {
	if level > MaxResultConditionDepth {
		return invalid("", "условие результата превышает четыре уровня")
	}
	if c.All != nil || c.Any != nil {
		if c.All != nil && c.Any != nil || c.Source != (ValueRef{}) || c.Op != "" || c.ValueJSON != nil || c.ValueFrom != nil {
			return invalid("", "группа принимает только all или any")
		}
		name, children := "all", c.All
		if c.Any != nil {
			name, children = "any", c.Any
		}
		if len(children) < 2 || len(children) > MaxResultConditionLeaves {
			return invalid("/"+name, "группа требует от двух до 16 условий")
		}
		for i, child := range children {
			if err := checkResultConditionTree(child, level+1, leaves); err != nil {
				return at(fmt.Sprintf("/%s/%d", name, i), err)
			}
		}
		return nil
	}
	*leaves++
	if *leaves > MaxResultConditionLeaves {
		return invalid("", "допустимо не более 16 листовых условий")
	}
	return checkResultConditionLeaf(c)
}

func checkResultConditionLeaf(c ResultCondition) error {
	if c.Source.Source != "result" {
		return invalid("/source/source", "условие требует источник result")
	}
	if err := checkValueRef(c.Source); err != nil {
		return at("/source", err)
	}
	if c.ValueFrom != nil {
		if c.ValueFrom.Source != "result" {
			return invalid("/valueFrom/source", "условие требует источник result")
		}
		if err := checkValueRef(*c.ValueFrom); err != nil {
			return at("/valueFrom", err)
		}
	}
	switch c.Op {
	case "equals", "not_equals", "greater_than", "greater_or_equal", "less_than", "less_or_equal":
		if (c.ValueJSON == nil) == (c.ValueFrom == nil) {
			return invalid("/valueJSON", "сравнение требует ровно одно из valueJSON и valueFrom")
		}
		if c.ValueJSON != nil {
			value, err := resultConditionScalar(context.Background(), *c.ValueJSON)
			if err != nil {
				return at("/valueJSON", err)
			}
			if resultOrdering(c.Op) && resultScalarKind(value) != "number" {
				return invalid("/valueJSON", "числовое сравнение требует JSON-число")
			}
		}
	case "exists", "not_exists":
		if c.ValueJSON != nil || c.ValueFrom != nil {
			return invalid("/valueJSON", "проверка наличия не принимает valueJSON или valueFrom")
		}
	default:
		return invalid("/op", "неизвестный оператор условия результата")
	}
	return nil
}

func resultOrdering(op string) bool {
	return op == "greater_than" || op == "greater_or_equal" || op == "less_than" || op == "less_or_equal"
}

// Leaf traces retain their established wire fields. Group traces have no
// operand, so projecting them avoids inventing absent source/presence values.
func (t ResultConditionTrace) MarshalJSON() ([]byte, error) {
	if t.Op == "all" || t.Op == "any" {
		return jsonx.Marshal(struct {
			Op             string                 `json:"op"`
			Matched        bool                   `json:"matched"`
			Children       []ResultConditionTrace `json:"children"`
			ShortCircuited bool                   `json:"shortCircuited,omitzero"`
		}{t.Op, t.Matched, t.Children, t.ShortCircuited})
	}
	type plain ResultConditionTrace
	return jsonx.Marshal(plain(t))
}

func resultConditionScalar(ctx context.Context, raw string) (any, error) {
	value, err := decodeBody(ctx, raw)
	if err != nil {
		return nil, err
	}
	if resultScalarKind(value) == "" {
		return nil, invalid("", "требуется JSON-строка, число, boolean или null")
	}
	return value, nil
}

func resultScalarKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case jsonx.Number:
		return "number"
	case bool:
		return "boolean"
	default:
		return ""
	}
}

func evaluateResultCondition(ctx context.Context, c ResultCondition, results map[string]any) (bool, *ResultConditionTrace, error) {
	budget := resultPredicateBudget{remaining: MaxResultBytes}
	matched, trace, err := evaluateResultConditionTree(ctx, c, results, 1, &budget)
	if err != nil {
		return false, nil, err
	}
	encoded, err := jsonx.Marshal(trace)
	if err != nil {
		return false, nil, err
	}
	if len(encoded) > MaxResultBytes {
		return false, nil, invalid("", "результат превышает 512 КиБ")
	}
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	return matched, trace, nil
}

type resultPredicateBudget struct {
	leaves    int
	remaining int
}

func (b *resultPredicateBudget) jsonText(value any) (*string, error) {
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > b.remaining {
		return nil, invalid("", "результат превышает 512 КиБ")
	}
	b.remaining -= len(encoded)
	return new(string(encoded)), nil
}

func evaluateResultConditionTree(ctx context.Context, c ResultCondition, results map[string]any, level int, budget *resultPredicateBudget) (bool, *ResultConditionTrace, error) {
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	if level > MaxResultConditionDepth {
		return false, nil, invalid("", "условие результата превышает четыре уровня")
	}
	if c.All != nil || c.Any != nil {
		op, children := "all", c.All
		if c.Any != nil {
			op, children = "any", c.Any
		}
		trace := &ResultConditionTrace{Op: op, Children: []ResultConditionTrace{}, Matched: op == "all"}
		for i, child := range children {
			matched, childTrace, err := evaluateResultConditionTree(ctx, child, results, level+1, budget)
			if err != nil {
				if ctx.Err() != nil {
					return false, nil, ctx.Err()
				}
				return false, nil, at(fmt.Sprintf("/%s/%d", op, i), err)
			}
			trace.Children = append(trace.Children, *childTrace)
			trace.Matched = matched
			if op == "all" && !matched || op == "any" && matched {
				trace.ShortCircuited = i+1 < len(children)
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return false, nil, err
		}
		return trace.Matched, trace, nil
	}
	budget.leaves++
	if budget.leaves > MaxResultConditionLeaves {
		return false, nil, invalid("", "допустимо не более 16 листовых условий")
	}
	return evaluateResultConditionLeaf(ctx, c, results, budget)
}

func resultConditionOperand(ctx context.Context, ref ValueRef, results map[string]any, path string) (any, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	output, ok := results[ref.NodeID]
	if !ok {
		return nil, false, invalid(path+"/nodeId", fmt.Sprintf("результат узла %q для JSON Pointer %q отсутствует", ref.NodeID, ref.Pointer))
	}
	value, present := valuePointer(output, ref.Pointer)
	return value, present, nil
}

func evaluateResultConditionLeaf(ctx context.Context, c ResultCondition, results map[string]any, budget *resultPredicateBudget) (bool, *ResultConditionTrace, error) {
	actual, present, err := resultConditionOperand(ctx, c.Source, results, "/source")
	if err != nil {
		return false, nil, err
	}
	trace := &ResultConditionTrace{SourceNodeID: c.Source.NodeID, Pointer: c.Source.Pointer, Op: c.Op, Present: present}
	if present {
		trace.ActualJSON, err = budget.jsonText(actual)
		if err != nil {
			return false, nil, at("/source/pointer", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	switch c.Op {
	case "exists":
		trace.Matched = present
		return present, trace, nil
	case "not_exists":
		trace.Matched = !present
		return !present, trace, nil
	}
	if !present {
		return false, nil, invalid("/source/pointer", fmt.Sprintf("JSON Pointer %q результата узла %q отсутствует", c.Source.Pointer, c.Source.NodeID))
	}
	if resultScalarKind(actual) == "" {
		return false, nil, invalid("/source/pointer", fmt.Sprintf("JSON Pointer %q результата узла %q должен содержать скаляр или null", c.Source.Pointer, c.Source.NodeID))
	}
	var expected any
	expectedPath := "/valueJSON"
	if c.ValueFrom != nil {
		expectedPath = "/valueFrom/pointer"
		var found bool
		expected, found, err = resultConditionOperand(ctx, *c.ValueFrom, results, "/valueFrom")
		if err != nil {
			return false, nil, err
		}
		if !found {
			return false, nil, invalid(expectedPath, fmt.Sprintf("JSON Pointer %q результата узла %q отсутствует", c.ValueFrom.Pointer, c.ValueFrom.NodeID))
		}
		if resultScalarKind(expected) == "" {
			return false, nil, invalid(expectedPath, fmt.Sprintf("JSON Pointer %q результата узла %q должен содержать скаляр или null", c.ValueFrom.Pointer, c.ValueFrom.NodeID))
		}
		trace.ValueFrom = new(*c.ValueFrom)
		trace.ExpectedJSON, err = budget.jsonText(expected)
	} else {
		if c.ValueJSON == nil {
			return false, nil, invalid(expectedPath, "сравнение требует valueJSON или valueFrom")
		}
		expected, err = resultConditionScalar(ctx, *c.ValueJSON)
		trace.ExpectedJSON = new(*c.ValueJSON)
		budget.remaining -= len(*c.ValueJSON)
	}
	if err != nil || budget.remaining < 0 {
		if ctx.Err() != nil {
			return false, nil, ctx.Err()
		}
		if err == nil {
			err = invalid("", "результат превышает 512 КиБ")
		}
		return false, nil, at(expectedPath, err)
	}
	if resultOrdering(c.Op) {
		if resultScalarKind(actual) != "number" {
			return false, nil, invalid("/source/pointer", fmt.Sprintf("JSON Pointer %q результата узла %q должен содержать число", c.Source.Pointer, c.Source.NodeID))
		}
		if resultScalarKind(expected) != "number" {
			if c.ValueFrom != nil {
				return false, nil, invalid(expectedPath, fmt.Sprintf("JSON Pointer %q результата узла %q должен содержать число", c.ValueFrom.Pointer, c.ValueFrom.NodeID))
			}
			return false, nil, invalid(expectedPath, "числовое сравнение требует JSON-число")
		}
	}
	if actual != nil && expected != nil && resultScalarKind(actual) != resultScalarKind(expected) {
		return false, nil, invalid(expectedPath, fmt.Sprintf("тип JSON Pointer %q результата узла %q не совпадает с типом правого значения", c.Source.Pointer, c.Source.NodeID))
	}
	matched := jsonx.EqualValue(actual, expected)
	if c.Op == "not_equals" {
		matched = !matched
	} else if resultOrdering(c.Op) {
		order := jsonx.CompareNumbers(actual.(jsonx.Number), expected.(jsonx.Number))
		switch c.Op {
		case "greater_than":
			matched = order > 0
		case "greater_or_equal":
			matched = order >= 0
		case "less_than":
			matched = order < 0
		case "less_or_equal":
			matched = order <= 0
		}
	}
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	trace.Matched = matched
	return matched, trace, nil
}
