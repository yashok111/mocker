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
	result, err = newProgram(rule).Evaluate(ctx, input)
	if err != nil {
		return Simulation{}, err
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
