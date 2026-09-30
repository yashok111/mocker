package responserules

import (
	"context"
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (e *Example) UnmarshalJSON(data []byte) error {
	var v Example
	if err := decodeObject(data, required("id", &v.ID), required("name", &v.Name), required("request", &v.Request)); err != nil {
		return err
	}
	if err := checkExample(v); err != nil {
		return err
	}
	*e = v
	return nil
}

func checkExample(example Example) error {
	if !ValidID(example.ID) {
		return invalid("/id", "недопустимый ID примера")
	}
	if !textBound(example.Name, 200) || strings.TrimSpace(example.Name) == "" {
		return invalid("/name", "требуется непустое имя до 200 байт UTF-8")
	}
	if err := CheckRequest(context.Background(), example.Request); err != nil {
		return at("/request", err)
	}
	return nil
}

func checkExamples(examples []Example) error {
	if len(examples) > MaxExamples {
		return invalid("", "допустимо до 20 примеров")
	}
	seen := make(map[string]bool, len(examples))
	for i, example := range examples {
		prefix := fmt.Sprintf("/%d", i)
		if seen[example.ID] {
			return invalid(prefix+"/id", "повторяющийся ID примера")
		}
		seen[example.ID] = true
		if err := checkExample(example); err != nil {
			return at(prefix, err)
		}
	}
	raw, err := jsonx.Marshal(examples)
	if err != nil {
		return err
	}
	if len(raw) > MaxExamplesBytes {
		return invalid("", "примеры превышают 256 КиБ")
	}
	return nil
}

// CheckRequest validates an authored fixture without requiring a graph or
// replacing exact JSON text with the normalized simulation copy.
func CheckRequest(ctx context.Context, request Request) error {
	if _, _, err := normalizeRequest(ctx, request); err != nil {
		return err
	}
	_, err := newFixtureEntities(ctx, request, nil)
	return err
}

// ExecutionRule removes simulation examples from an authoring rule before it
// is copied to HTTP execution or compared with an already applied copy.
func ExecutionRule(rule Rule) Rule {
	rule.Examples = nil
	return rule
}
