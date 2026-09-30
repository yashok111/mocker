package responserules

import (
	"context"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Simulate selects one graph path without sleeping or consulting runtime state.
func Simulate(ctx context.Context, env Envelope, ruleID string, root map[string]any, request Request) (Simulation, error) {
	input, hash, err := normalizeRequest(ctx, request)
	if err != nil {
		return Simulation{}, err
	}
	validation, err := Validate(ctx, env, ruleID, root)
	if err != nil {
		return Simulation{}, err
	}
	result := Simulation{Validation: validation, InputHash: hash, Outcome: "invalid", Trace: []Step{}}
	if !validation.Valid {
		return boundedResult(result)
	}
	var rule Rule
	for _, r := range env.Rules {
		if r.ID == ruleID {
			rule = r
			break
		}
	}
	host, err := newFixtureEntities(ctx, request, rule.Binding)
	if err != nil {
		return Simulation{}, err
	}
	path, err := fixturePath(request)
	if err != nil {
		return Simulation{}, err
	}
	result, err = newProgram(rule).EvaluateWithEntities(ctx, EvaluationInput{Request: input, Path: path}, EvaluationOptions{Entities: host})
	if err != nil {
		return Simulation{}, err
	}
	if request.Entities != nil {
		result.Entities = host.families
	}
	result.Validation = validation
	result.InputHash = hash
	return boundedResult(result)
}

func boundedResult(result Simulation) (Simulation, error) {
	data, err := jsonx.Marshal(result)
	if err != nil {
		return Simulation{}, err
	}
	if len(data) > MaxResultBytes {
		return Simulation{}, invalid("", "результат превышает 512 КиБ")
	}
	return result, nil
}
