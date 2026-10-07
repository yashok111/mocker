package responserules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

const ExecutionExtension = "x-mocker-response-rules-execution"

// CheckResponse applies the shared structural and header/media safety checks
// again at the serving boundary without parsing or re-encoding the static body.
func CheckResponse(response Response) error { return checkResponse(context.Background(), response) }

// DecodeExecution admits the separate explicit execution copies. Decode owns
// the returned graph data, even when the caller supplied typed Go structures.
func DecodeExecution(root map[string]any) (Envelope, error) {
	authoringShape := map[string]any{}
	if value, ok := root[ExecutionExtension]; ok {
		authoringShape[Extension] = value
	}
	env, err := Decode(authoringShape)
	if err != nil {
		return env, executionError(err)
	}
	for i, rule := range env.Rules {
		if rule.Examples != nil {
			return Envelope{}, invalid(fmt.Sprintf("/%s/rules/%d/examples", ExecutionExtension, i), "Примеры доступны только в авторском правиле")
		}
	}
	return env, nil
}

func executionError(err error) error {
	if field, ok := errors.AsType[*FieldError](err); ok {
		return invalid(strings.Replace(field.Pointer, "/"+Extension, "/"+ExecutionExtension, 1), field.Message)
	}
	return err
}

// CompileExecution validates all applied copies against this immutable API and
// builds isolated programs keyed by the same exact operation identity as routing.
func CompileExecution(ctx context.Context, root map[string]any) (map[string]*Program, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env, err := DecodeExecution(root)
	if err != nil {
		return nil, err
	}
	programs := make(map[string]*Program, len(env.Rules))
	for i, rule := range env.Rules {
		validation, err := Validate(ctx, env, rule.ID, root)
		if err != nil {
			return nil, executionError(err)
		}
		if !validation.Valid {
			for _, d := range validation.Diagnostics {
				if d.Severity == "error" {
					return nil, executionError(invalid(d.Pointer, d.Message))
				}
			}
			return nil, invalid("/"+ExecutionExtension, "Применённое правило не прошло проверку")
		}
		if rule.Binding.Method == http.MethodHead {
			return nil, invalid(fmt.Sprintf("/%s/rules/%d/binding", ExecutionExtension, i), "Для HEAD используется правило операции GET; отдельное правило HEAD применить нельзя")
		}
		programs[overrides.OpKey(rule.Binding.Method, rule.Binding.Path)] = newProgram(rule)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return programs, nil
}

// Program is an immutable validated graph. Its maps and payloads are private;
// Evaluate neither modifies request input nor returns aliases to stored payloads.
type Program struct {
	id        string
	start     string
	nodes     map[string]Node
	edges     map[string]map[string]Edge
	admission *EntityAdmission
	nodeOrder map[string]int
}

func newProgram(rule Rule) *Program {
	p := &Program{id: rule.ID, nodes: make(map[string]Node, len(rule.Nodes)), edges: map[string]map[string]Edge{}, nodeOrder: map[string]int{}}
	for i, n := range rule.Nodes {
		p.nodeOrder[n.ID] = i
		p.nodes[n.ID] = n
		if n.Type == "start" {
			p.start = n.ID
		}
	}
	for _, edge := range rule.Edges {
		if p.edges[edge.From] == nil {
			p.edges[edge.From] = map[string]Edge{}
		}
		p.edges[edge.From][edge.Port] = edge
	}
	p.admission, _ = entityAdmission(rule)
	return p
}

func (p *Program) ID() string { return p.id }

// LiveInput preserves real HTTP query/header values without applying fixture
// authoring limits. The caller decides whether existing body capture is eligible.
// An unusable captured body makes body predicates false; cancellation is an error.
func LiveInput(ctx context.Context, query url.Values, header http.Header, body []byte, available bool) (overrides.Input, bool, error) {
	input := overrides.Input{Query: query, Header: header}
	if err := ctx.Err(); err != nil {
		return input, false, err
	}
	if !available {
		return input, false, nil
	}
	if len(body) > MaxBodyBytes {
		return input, true, nil
	}
	value, err := decodeBody(ctx, string(body))
	if err != nil {
		if ctx.Err() != nil {
			return input, false, ctx.Err()
		}
		return input, true, nil
	}
	input.Body, input.BodyOK = value, true
	return input, false, nil
}

// Evaluate follows exactly one bounded path without waiting or mutating state.
func (p *Program) Evaluate(ctx context.Context, input overrides.Input) (Simulation, error) {
	return p.EvaluateWithEntities(ctx, EvaluationInput{Request: input}, EvaluationOptions{})
}
func (p *Program) EvaluateWithEntities(ctx context.Context, input EvaluationInput, options EvaluationOptions) (Simulation, error) {
	if err := ctx.Err(); err != nil {
		return Simulation{}, err
	}
	if p == nil {
		return Simulation{}, invalid("", "правило не скомпилировано")
	}
	result := Simulation{Validation: Validation{Valid: true, Diagnostics: []Diagnostic{}}, Trace: []Step{}}
	traceBytes := 2
	results := map[string]any{}
	current := p.start
	total := 0
	for i := 0; i < MaxNodes; i++ {
		if err := ctx.Err(); err != nil {
			return Simulation{}, err
		}
		n, ok := p.nodes[current]
		if !ok {
			return Simulation{}, invalid("", "путь содержит отсутствующий узел")
		}
		step := Step{Step: i + 1, NodeID: n.ID}
		port := "next"
		switch n.Type {
		case "condition":
			var matched bool
			if n.ResultCondition != nil {
				var err error
				matched, step.ResultCondition, err = evaluateResultCondition(ctx, *n.ResultCondition, results)
				if err != nil {
					if ctx.Err() != nil {
						return Simulation{}, ctx.Err()
					}
					return Simulation{}, at(fmt.Sprintf("/nodes/%d/resultCondition", p.nodeOrder[n.ID]), err)
				}
			} else {
				matched = n.Condition.Match(input.Request)
			}
			step.Matched = &matched
			port = "false"
			if matched {
				port = "true"
			}
		case "delay":
			delay := *n.DelayMs
			total += delay
			step.DelayMs = &delay
			if options.Delay != nil {
				if err := options.Delay(ctx, delay); err != nil {
					return Simulation{}, err
				}
			}
		case "entity_read", "entity_create", "entity_update":
			var err error
			port, err = recordEntityStep(ctx, n, fmt.Sprintf("/nodes/%d", p.nodeOrder[n.ID]), input, options, results, &result, &step)
			if err != nil {
				return Simulation{}, err
			}
		case "response", "fallback":
			result.Outcome = n.Type
			result.TerminalNodeID = n.ID
			result.TotalDelayMs = &total
			response, err := evaluatedResponse(ctx, n, fmt.Sprintf("/nodes/%d", p.nodeOrder[n.ID]), input, results)
			if err != nil {
				return Simulation{}, err
			}
			result.Response = response
			if err := appendTraceStep(ctx, &result, step, &traceBytes); err != nil {
				return Simulation{}, err
			}
			return boundedResult(result)
		}
		edge, ok := p.edges[n.ID][port]
		if !ok {
			return Simulation{}, invalid("", "путь не завершён")
		}
		step.EdgeID = edge.ID
		if err := appendTraceStep(ctx, &result, step, &traceBytes); err != nil {
			return Simulation{}, err
		}
		current = edge.To
	}
	return Simulation{}, invalid("", "превышен предел 100 шагов")
}

// A group can repeat captured values many times in one step. Enforce the
// accumulated trace cap before following another edge, not only at the final
// response, so bounded output also bounds the retained execution trace.
func appendTraceStep(ctx context.Context, result *Simulation, step Step, traceBytes *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := jsonx.Marshal(step)
	if err != nil {
		return err
	}
	*traceBytes += len(encoded) + 1
	if *traceBytes > MaxResultBytes {
		return invalid("", "результат превышает 512 КиБ")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result.Trace = append(result.Trace, step)
	return nil
}
