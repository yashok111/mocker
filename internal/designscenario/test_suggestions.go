package designscenario

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

const (
	maxSuggestionCandidates = 256
	maxSuggestedTests       = 20
	maxSuggestionBytes      = 1 << 20
)

type TestTarget struct {
	FragmentID string `json:"fragmentId"`
	BranchID   string `json:"branchId,omitempty"`
	Outcome    string `json:"outcome"`
}
type SuggestedTest struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Variables ExecutionValues `json:"variables"`
	Targets   []TestTarget    `json:"targets"`
}
type UnresolvedTestTarget struct {
	Target TestTarget `json:"target"`
	Code   string     `json:"code"`
	Reason string     `json:"reason"`
}
type TestSuggestions struct {
	RevisionID        int64                  `json:"revisionId"`
	RunCount          int                    `json:"runCount"`
	SampleLimit       int                    `json:"sampleLimit"`
	Cases             []SuggestedTest        `json:"cases"`
	Unresolved        []UnresolvedTestTarget `json:"unresolved"`
	Truncated         bool                   `json:"truncated"`
	CheckedCandidates int                    `json:"checkedCandidates"`
}

// SuggestTests proposes inputs, never observations. Only real runs change coverage.
// String equivalence classes are sufficient for the current condition language;
// the bounded search is deliberately not a reachability proof.
func SuggestTests(ctx context.Context, revision Revision, coverage Coverage) (TestSuggestions, error) {
	out := TestSuggestions{RevisionID: revision.ID, RunCount: coverage.RunCount, SampleLimit: coverage.SampleLimit, Cases: []SuggestedTest{}, Unresolved: []UnresolvedTestTarget{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if coverage.RevisionID != revision.ID {
		return out, fmt.Errorf("%w: покрытие относится к другой ревизии", ErrInvalid)
	}
	if _, err := PrepareRun(revision, "suggestions", "", "ui", nil); err != nil {
		return out, err
	}
	pending := map[TestTarget]bool{}
	var ordered []TestTarget
	for _, p := range coverage.Paths {
		if p.Hits == 0 {
			target := TestTarget{p.FragmentID, p.BranchID, p.Outcome}
			if !pending[target] {
				ordered = append(ordered, target)
				pending[target] = true
			}
		}
	}
	if len(pending) == 0 {
		return out, nil
	}
	defaults := ExecutionValues{}
	if revision.Document.Execution != nil {
		maps.Copy(defaults, revision.Document.Execution.Variables)
	}
	names, choices := suggestionChoices(revision.Document, defaults)
	candidate := maps.Clone(defaults)
	for i, name := range names {
		setSuggestionValue(candidate, name, choices[i][0])
	}
	seen := map[string]bool{}
	bytesUsed := 0
	dynamic, limited := false, false
	check := func() bool {
		if ctx.Err() != nil || len(pending) == 0 {
			return false
		}
		// Key only contains condition inputs; saved defaults remain unchanged.
		signature := make([]int, len(names))
		for i, name := range names {
			value, exists := candidate[name]
			signature[i] = slices.Index(choices[i], suggestionValue{value, exists})
		}
		raw, _ := jsonx.Marshal(signature)
		key := string(raw)
		if seen[key] {
			return true
		}
		if out.CheckedCandidates >= maxSuggestionCandidates {
			out.Truncated = true
			return false
		}
		seen[key] = true
		out.CheckedCandidates++
		if validateRunVariables(candidate) != nil {
			return true
		}
		trace, reason := predictTestFlow(ctx, revision.Document, candidate)
		dynamic = dynamic || errors.Is(reason, errSuggestionResponse)
		limited = limited || errors.Is(reason, errSuggestionLimit)
		if reason != nil && !errors.Is(reason, errSuggestionResponse) {
			return true
		}
		targets := []TestTarget{}
		for _, target := range ordered {
			if pending[target] && trace[target] {
				targets = append(targets, target)
			}
		}
		if len(targets) == 0 {
			return true
		}
		variables := ExecutionValues{}
		for name, value := range candidate {
			if previous, ok := defaults[name]; !ok || previous != value {
				variables[name] = value
			}
		}
		c := SuggestedTest{ID: fmt.Sprintf("branch-test-%d", len(out.Cases)+1), Name: suggestedTestName(revision.Document, targets), Variables: variables, Targets: targets}
		raw, _ = jsonx.Marshal(c)
		if len(out.Cases) >= maxSuggestedTests || bytesUsed+len(raw) > maxSuggestionBytes {
			out.Truncated = true
			return false
		}
		bytesUsed += len(raw)
		out.Cases = append(out.Cases, c)
		for _, target := range targets {
			delete(pending, target)
		}
		return len(pending) > 0
	}
	// Start with a complete input and single-variable changes, so unrelated
	// conditions cannot consume the entire Cartesian-product budget first.
	keepGoing := check()
	for i, name := range names {
		if !keepGoing {
			break
		}
		for _, value := range choices[i][1:] {
			setSuggestionValue(candidate, name, value)
			if !check() {
				keepGoing = false
				break
			}
		}
		setSuggestionValue(candidate, name, choices[i][0])
	}
	var combinations func(int) bool
	combinations = func(index int) bool {
		if index == len(names) {
			return check()
		}
		for _, value := range choices[index] {
			setSuggestionValue(candidate, names[index], value)
			if !combinations(index + 1) {
				return false
			}
		}
		return true
	}
	if keepGoing {
		combinations(0)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Truncated = out.Truncated || limited
	for _, target := range ordered {
		if !pending[target] {
			continue
		}
		code, reason := "no_input_found", "Не удалось подобрать начальные переменные. Проверьте порядок и совместимость условий. Переменную из настроек нельзя удалить переопределением запуска."
		if dynamic {
			code, reason = "response_dependent", "Подбор остановился на условии, зависящем от ответа HTTP. Настройте ответ мока или извлечение переменной и повторите проверку."
		}
		if out.Truncated {
			code, reason = "search_limit", "Достигнут предел подбора. Набор для этой ветки не найден; это не означает, что ветка недостижима."
		}
		out.Unresolved = append(out.Unresolved, UnresolvedTestTarget{target, code, reason})
	}
	return out, nil
}

type suggestionValue struct {
	Value  string
	Exists bool
}

func setSuggestionValue(values ExecutionValues, name string, value suggestionValue) {
	if value.Exists {
		values[name] = value.Value
	} else {
		delete(values, name)
	}
}
func suggestionChoices(document Document, defaults ExecutionValues) ([]string, [][]suggestionValue) {
	literals := map[string][]string{}
	add := func(c *ExecutionCondition) {
		if c == nil {
			return
		}
		if _, ok := literals[c.Variable]; !ok {
			literals[c.Variable] = nil
		}
		if c.Value != nil && !slices.Contains(literals[c.Variable], *c.Value) {
			literals[c.Variable] = append(literals[c.Variable], *c.Value)
		}
	}
	for _, f := range document.Fragments {
		if f.Execution != nil {
			add(f.Execution.Condition)
		}
		for _, b := range f.Branches {
			if b.Execution != nil {
				add(b.Execution.Condition)
			}
		}
	}
	names := slices.Sorted(maps.Keys(literals))
	choices := make([][]suggestionValue, len(names))
	for i, name := range names {
		values := literals[name]
		other := "test"
		for slices.Contains(values, other) {
			other += "_"
		}
		appendValue := func(v suggestionValue) {
			if !slices.Contains(choices[i], v) {
				choices[i] = append(choices[i], v)
			}
		}
		if value, ok := defaults[name]; ok {
			appendValue(suggestionValue{value, true})
		}
		for _, value := range values {
			appendValue(suggestionValue{value, true})
		}
		appendValue(suggestionValue{other, true})
		if _, ok := defaults[name]; !ok {
			appendValue(suggestionValue{})
		}
	}
	return names, choices
}

var (
	errSuggestionResponse = errors.New("condition depends on HTTP response")
	errSuggestionLimit    = errors.New("prediction exceeds execution limit")
)

// Prediction shares condition evaluation with the runner. Successful extraction
// establishes existence, but its value stays unknown; response examples are not
// evidence of the value that an actual run will receive.
type testFlowPrediction struct {
	ctx               context.Context
	document          Document
	variables         ExecutionValues
	unknown           map[string]bool
	positions         map[string]int
	children          map[flowScope]map[int]Fragment
	targets           map[TestTarget]bool
	visits, decisions int
}

func predictTestFlow(ctx context.Context, document Document, variables ExecutionValues) (map[TestTarget]bool, error) {
	p := testFlowPrediction{ctx: ctx, document: document, variables: maps.Clone(variables), unknown: map[string]bool{}, positions: map[string]int{}, children: map[flowScope]map[int]Fragment{}, targets: map[TestTarget]bool{}}
	for i, m := range document.Messages {
		p.positions[m.ID] = i
	}
	for _, f := range document.Fragments {
		key := flowScope{f.ParentFragmentID, f.ParentBranchID}
		if p.children[key] == nil {
			p.children[key] = map[int]Fragment{}
		}
		p.children[key][p.positions[f.FromMessageID]] = f
	}
	err := p.scope(0, len(document.Messages)-1, flowScope{})
	return p.targets, err
}
func (p *testFlowPrediction) condition(c *ExecutionCondition) (bool, error) {
	if p.unknown[c.Variable] && (c.Operator == "equals" || c.Operator == "not_equals") {
		return false, errSuggestionResponse
	}
	return evaluateCondition(c, p.variables)
}
func (p *testFlowPrediction) record(f, b, outcome string) error {
	if p.decisions >= maxRunDecisions {
		return errSuggestionLimit
	}
	p.decisions++
	p.targets[TestTarget{f, b, outcome}] = true
	return nil
}
func (p *testFlowPrediction) scope(start, end int, key flowScope) error {
	for pos := start; pos <= end; {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if f, ok := p.children[key][pos]; ok {
			if err := p.fragment(f); err != nil {
				return err
			}
			pos = p.positions[f.ToMessageID] + 1
			continue
		}
		if p.visits >= maxRunOccurrences {
			return errSuggestionLimit
		}
		p.visits++
		m := p.document.Messages[pos]
		if m.Kind == "request" && m.Operation != nil && m.Execution != nil && m.Execution.Enabled {
			for _, e := range m.Execution.Extract {
				p.variables[e.Name] = ""
				p.unknown[e.Name] = true
			}
		}
		pos++
	}
	return nil
}
func (p *testFlowPrediction) skip(start, end int) error {
	p.visits += end - start + 1
	if p.visits > maxRunOccurrences {
		return errSuggestionLimit
	}
	return nil
}
func (p *testFlowPrediction) fragment(f Fragment) error {
	start, end := p.positions[f.FromMessageID], p.positions[f.ToMessageID]
	switch f.Kind {
	case "alt":
		choice := -1
		for i, b := range f.Branches {
			if b.Execution.Otherwise {
				choice = i
				break
			}
			match, err := p.condition(b.Execution.Condition)
			if err != nil {
				return err
			}
			if match {
				choice = i
				break
			}
		}
		// The runner records decisions for all branches before visiting their bodies.
		if p.decisions+len(f.Branches) > maxRunDecisions {
			return errSuggestionLimit
		}
		p.decisions += len(f.Branches)
		if choice >= 0 {
			p.targets[TestTarget{f.ID, f.Branches[choice].ID, "taken"}] = true
		}
		for i, b := range f.Branches {
			first, last := p.positions[b.FromMessageID], p.positions[b.ToMessageID]
			if i == choice {
				if err := p.scope(first, last, flowScope{f.ID, b.ID}); err != nil {
					return err
				}
			} else if err := p.skip(first, last); err != nil {
				return err
			}
		}
	case "opt", "loop":
		iterations := 1
		if f.Kind == "loop" {
			iterations = f.Execution.Iterations
		}
		for range iterations {
			if err := p.ctx.Err(); err != nil {
				return err
			}
			match := true
			if f.Execution.Condition != nil {
				var err error
				match, err = p.condition(f.Execution.Condition)
				if err != nil {
					return err
				}
			}
			outcome := "taken"
			if !match {
				outcome = "skipped"
			}
			if err := p.record(f.ID, "", outcome); err != nil {
				return err
			}
			if !match {
				return p.skip(start, end)
			}
			if err := p.scope(start, end, flowScope{f.ID, ""}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Names describe the first newly covered path; the full target list remains in
// the preview. Keep the suffix when truncating to the runner's 200-rune limit.
func suggestedTestName(document Document, targets []TestTarget) string {
	target := targets[0]
	name := "Проверка ветки"
	for i, fragment := range document.Fragments {
		if fragment.ID != target.FragmentID {
			continue
		}
		label := strings.Join(strings.Fields(fragment.Label), " ")
		if label == "" {
			kind := "условие"
			if fragment.Kind == "loop" {
				kind = "цикл"
			}
			if fragment.Kind == "alt" {
				kind = "выбор"
			}
			label = fmt.Sprintf("%s %d", kind, i+1)
		}
		if fragment.Kind == "alt" {
			for _, branch := range fragment.Branches {
				if branch.ID != target.BranchID {
					continue
				}
				name = strings.Join(strings.Fields(branch.Label), " ")
				if name == "" || strings.EqualFold(name, "иначе") || strings.EqualFold(name, "else") || strings.EqualFold(name, "otherwise") {
					if branch.Execution.Otherwise {
						name = "Иначе: " + label
					} else {
						condition := branch.Execution.Condition
						switch condition.Operator {
						case "equals":
							name = fmt.Sprintf("%s = «%s»", condition.Variable, *condition.Value)
						case "not_equals":
							name = fmt.Sprintf("%s ≠ «%s»", condition.Variable, *condition.Value)
						case "exists":
							name = "Есть переменная " + condition.Variable
						case "not_exists":
							name = "Нет переменной " + condition.Variable
						}
					}
				}
				break
			}
		} else {
			action := "Выполнить"
			if target.Outcome == "skipped" {
				action = "Пропустить"
			}
			name = action + " «" + label + "»"
		}
		break
	}
	name = strings.Join(strings.Fields(name), " ")
	suffix := ""
	if len(targets) > 1 {
		suffix = fmt.Sprintf(" · ещё веток: %d", len(targets)-1)
	}
	limit := 200 - len([]rune(suffix))
	runes := []rune(name)
	if len(runes) > limit {
		name = string(runes[:limit-1]) + "…"
	}
	return name + suffix
}
