package backendanalysis

import (
	"fmt"
	"maps"
	"math/big"
	"reflect"
	"slices"
)

type MeasurementComparison struct {
	Before               ScenarioMeasurements `json:"before"`
	After                ScenarioMeasurements `json:"after"`
	Deltas               map[string]*string   `json:"deltas"`
	ConditionDifferences []string             `json:"conditionDifferences"`
	Limitations          []string             `json:"limitations"`
}

func CompareMeasurements(before, after ScenarioMeasurements) MeasurementComparison {
	out := MeasurementComparison{Before: before, After: after, Deltas: map[string]*string{}, ConditionDifferences: []string{}, Limitations: []string{"Observed deltas do not establish causation or predict a proposal", "Percentiles use original samples on each side; never averaged"}}
	// Sorted keys keep the appended limitations in a stable order: the
	// comparison is frozen into the semantic result hash.
	for _, key := range slices.Sorted(maps.Keys(before.Metrics)) {
		a := before.Metrics[key]
		b, ok := after.Metrics[key]
		out.Deltas[key] = nil
		if !ok || a.Value == nil || b.Value == nil || a.Unit != b.Unit {
			continue
		}
		// Value is a total over the sampled executions. Subtracting totals over
		// different sample sets reads as a regression or improvement when every
		// execution is unchanged (3 x 10 vs 5 x 10 queries gave "+20"), so the
		// delta is withheld and said so (review 2026-10-06, F156).
		if a.SampleCount != b.SampleCount || a.MissingSamples != b.MissingSamples {
			out.Limitations = append(out.Limitations, fmt.Sprintf("%s: unequal sample sets (before %d samples, %d missing; after %d samples, %d missing); totals are not compared", key, a.SampleCount, a.MissingSamples, b.SampleCount, b.MissingSamples))
			continue
		}
		x, ok1 := new(big.Int).SetString(*a.Value, 10)
		y, ok2 := new(big.Int).SetString(*b.Value, 10)
		if ok1 && ok2 {
			out.Deltas[key] = new(y.Sub(y, x).String())
		}
	}
	// Compare complete vectors, including multi-service conditions, without losing
	// either original vector when their shapes differ.
	for _, field := range []string{"source/build", "environment", "configuration", "input", "window", "sampling", "instrumentation", "producer", "mocked", "scenario"} {
		if !reflect.DeepEqual(conditionVector(before, field), conditionVector(after, field)) {
			out.ConditionDifferences = append(out.ConditionDifferences, field)
		}
	}
	if !reflect.DeepEqual(before.Input.Basis, after.Input.Basis) {
		out.ConditionDifferences = append(out.ConditionDifferences, "metric basis")
	}
	return out
}

// conditionVector is the sorted, distinct canonical values one condition
// field takes across a side's conditions.
func conditionVector(m ScenarioMeasurements, field string) []string {
	out := make([]string, 0, len(m.Conditions))
	for _, c := range m.Conditions {
		raw, _ := canonical(conditionField(c, field))
		out = append(out, string(raw))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func conditionField(c MeasurementCondition, field string) any {
	switch field {
	case "source/build":
		return c.Context.Source
	case "environment":
		return c.Context.Environment
	case "configuration":
		return c.Context.ConfigurationHash
	case "input":
		return c.Context.Input
	case "window":
		return c.Context.Window
	case "sampling":
		return c.Context.Sampling
	case "instrumentation":
		return c.Context.Instrumentation
	case "producer":
		return c.Context.Producer
	case "mocked":
		return c.MockedDependencies
	case "scenario":
		return c.Context.Scenario
	}
	return nil
}
