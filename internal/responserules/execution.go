package responserules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yashok111/mocker/internal/overrides"
)

const ExecutionExtension = "x-mocker-response-rules-execution"

// CheckResponse applies the shared structural and header/media safety checks
// again at the serving boundary without parsing or re-encoding the static body.
func CheckResponse(response Response) error { return checkResponse(response) }

// DecodeExecution admits the separate explicit execution copies. Decode owns
// the returned graph data, even when the caller supplied typed Go structures.
func DecodeExecution(root map[string]any) (Envelope, error) {
	authoringShape := map[string]any{}
	if value, ok := root[ExecutionExtension]; ok {
		authoringShape[Extension] = value
	}
	env, err := Decode(authoringShape)
	return env, executionError(err)
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
	id    string
	start string
	nodes map[string]Node
	edges map[string]map[string]Edge
}

func newProgram(rule Rule) *Program {
	p := &Program{id: rule.ID, nodes: make(map[string]Node, len(rule.Nodes)), edges: map[string]map[string]Edge{}}
	for _, n := range rule.Nodes {
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
	if err := ctx.Err(); err != nil {
		return Simulation{}, err
	}
	if p == nil {
		return Simulation{}, invalid("", "правило не скомпилировано")
	}
	result := Simulation{Validation: Validation{Valid: true, Diagnostics: []Diagnostic{}}, Trace: []Step{}}
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
			matched := n.Condition.Match(input)
			step.Matched = &matched
			port = "false"
			if matched {
				port = "true"
			}
		case "delay":
			delay := *n.DelayMs
			total += delay
			step.DelayMs = &delay
		case "response", "fallback":
			result.Outcome = n.Type
			result.TerminalNodeID = n.ID
			result.TotalDelayMs = &total
			if n.Response != nil {
				response := *n.Response
				response.Headers = append([]Field{}, response.Headers...)
				if response.BodyJSON != nil {
					body := *response.BodyJSON
					response.BodyJSON = &body
				}
				result.Response = &response
			}
			result.Trace = append(result.Trace, step)
			return boundedResult(result)
		}
		edge, ok := p.edges[n.ID][port]
		if !ok {
			return Simulation{}, invalid("", "путь не завершён")
		}
		step.EdgeID = edge.ID
		result.Trace = append(result.Trace, step)
		current = edge.To
	}
	return Simulation{}, invalid("", "превышен предел 100 шагов")
}
