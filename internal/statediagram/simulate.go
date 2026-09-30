package statediagram

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

type Step struct {
	TransitionID   string `json:"transitionId"`
	From           string `json:"from"`
	To             string `json:"to"`
	Accepted       bool   `json:"accepted"`
	Reason         string `json:"reason"`
	ResponseStatus int    `json:"responseStatus"`
	DataJSON       string `json:"dataJSON"`
}
type Simulation struct {
	StateID     string       `json:"stateId"`
	DataJSON    string       `json:"dataJSON"`
	Steps       []Step       `json:"steps"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Simulate replays a bounded trace from an immutable proposal. No session or
// entity store is involved, so retries and parallel callers cannot interact.
func Simulate(d Diagram, root map[string]any, dataJSON string, transitions []string) (Simulation, error) {
	out := Simulation{StateID: d.InitialStateID, DataJSON: dataJSON, Steps: []Step{}, Diagnostics: Validate(d, root)}
	if HasErrors(out.Diagnostics) {
		return out, nil
	}
	if len(transitions) > 100 {
		return out, fmt.Errorf("не более 100 шагов симуляции")
	}
	data, err := Object(dataJSON)
	if err != nil {
		return out, err
	}
	states, values := stateMaps(d)
	current, err := currentState(d.InitialStateID, d.Entity, values, data)
	if err != nil {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Severity: "error", ElementID: d.ID, Message: err.Error()})
		return out, nil
	}
	out.StateID = current
	byID := map[string]Transition{}
	for _, tr := range d.Transitions {
		byID[tr.ID] = tr
	}
	for _, id := range transitions {
		tr, found := byID[id]
		step := Step{TransitionID: id, From: out.StateID, To: out.StateID, DataJSON: out.DataJSON}

		selection, selectErr := advance(context.Background(), out.StateID, tr, found, states, data, d.Entity)
		if selectErr != nil {
			if conflict, ok := errors.AsType[*TransitionError](selectErr); ok {
				step.Reason = conflict.Message
				if !found {
					step.Reason = "Переход не найден"
				}
			} else {
				return out, selectErr
			}
		} else {
			next := maps.Clone(data)
			maps.Copy(next, selection.Patch)
			encoded, err := jsonx.Marshal(next)
			if err != nil {
				return out, err
			}
			if len(encoded) > MaxJSON {
				return out, fmt.Errorf("данные после перехода превышают 64 КиБ")
			}
			data = next
			out.StateID = selection.ToStateID
			out.DataJSON = string(encoded)
			step.Accepted = true
			step.To = selection.ToStateID
			step.ResponseStatus = selection.ResponseStatus
			step.DataJSON = out.DataJSON
		}
		out.Steps = append(out.Steps, step)
		if !step.Accepted {
			break
		}
	}
	return out, nil
}
func guardPasses(g *Guard, data map[string]any) bool {
	if g == nil {
		return true
	}
	value, ok := pointer(data, g.Pointer)
	if !ok {
		return false
	}
	expected, err := Value(g.EqualsJSON)
	return err == nil && equal(value, expected)
}
func pointer(value any, p string) (any, bool) {
	if p == "" {
		return value, true
	}
	for token := range strings.SplitSeq(p[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch v := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = v[token]
			if !ok {
				return nil, false
			}
		case []any:
			n, err := strconv.Atoi(token)
			if err != nil || n < 0 || n >= len(v) || strconv.Itoa(n) != token {
				return nil, false
			}
			value = v[n]
		default:
			return nil, false
		}
	}
	return value, true
}
func equal(a, b any) bool {
	switch av := a.(type) {
	case jsonx.Number:
		bv, ok := b.(jsonx.Number)
		if !ok {
			return false
		}
		// Compare decimal significands and exponents without expanding powers:
		// a compact 1e1000000000 must not allocate a billion-digit integer.
		return canonicalNumber(string(av)) == canonicalNumber(string(bv))
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, found := bv[k]
			if !found || !equal(v, other) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i, v := range av {
			if !equal(v, bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func canonicalNumber(raw string) string {
	mantissa, exponent, hasExponent := strings.Cut(strings.ToLower(raw), "e")
	power := new(big.Int)
	if hasExponent {
		power.SetString(exponent, 10)
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign = "-"
		mantissa = mantissa[1:]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0"
	}
	trimmed := strings.TrimRight(digits, "0")
	power.Add(power, big.NewInt(int64(len(digits)-len(trimmed)-len(fraction))))
	return sign + trimmed + "e" + power.String()
}
