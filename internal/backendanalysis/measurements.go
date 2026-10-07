package backendanalysis

import (
	"context"
	"fmt"
	o "github.com/yashok111/mocker/internal/backendobservations"
	"math"
	"slices"
	"strconv"
	"strings"
)

const MeasurementPolicy = "backend-scenario-measures-v1"

type MetricBasis struct {
	Kind  string `json:"kind"`
	Basis string `json:"basis,omitempty"`
	Scope string `json:"scope,omitempty"`
}
type MeasurementInput struct {
	Side         string      `json:"side"`
	ExecutionIDs []string    `json:"executionIds"`
	RootSpanIDs  []string    `json:"rootSpanIds"`
	Basis        MetricBasis `json:"basis"`
	Policy       string      `json:"policy"`
}
type MetricSample struct {
	ExecutionID string `json:"executionId"`
	Value       string `json:"value"`
}
type MeasuredMetric struct {
	Unit           string         `json:"unit"`
	Value          *string        `json:"value"`
	P50            *string        `json:"p50"`
	P95            *string        `json:"p95"`
	Samples        []MetricSample `json:"samples"`
	SampleCount    int            `json:"sampleCount"`
	MissingSamples int            `json:"missingSamples"`
	Basis          MetricBasis    `json:"basis"`
	Limitations    []string       `json:"limitations"`
}
type MeasurementCondition struct {
	SourceCompatible   bool                 `json:"sourceCompatible"`
	CorrelationGaps    []string             `json:"correlationGaps"`
	Pin                o.AnalysisPin        `json:"pin"`
	Context            o.ObservationContext `json:"context"`
	MockedDependencies []string             `json:"mockedDependencies"`
}
type ScenarioMeasurements struct {
	Policy            string                    `json:"policy"`
	Input             MeasurementInput          `json:"input"`
	Conditions        []MeasurementCondition    `json:"conditions"`
	Metrics           map[string]MeasuredMetric `json:"metrics"`
	SampledExecutions int                       `json:"sampledExecutions"`
	FailedExecutions  int                       `json:"failedExecutions"`
	SkippedExecutions int                       `json:"skippedExecutions"`
	UnknownExecutions int                       `json:"unknownExecutions"`
	PopulationRate    *string                   `json:"populationRate"`
	Gaps              []string                  `json:"gaps"`
}
type PinnedMeasurementData = o.PinnedObservations

type uniqueObservation struct {
	Record    o.Record
	Context   o.ObservationContext
	Key       string
	Execution string
}

func observationIdentity(c o.ObservationContext) string {
	raw, _ := canonical(struct {
		Producer    o.Producer
		Source      o.Source
		Environment o.Environment
	}{c.Producer, c.Source, c.Environment})
	return string(raw)
}
func observationKey(identity, executionID, recordID string) string {
	raw, _ := canonical([3]string{identity, executionID, recordID})
	return string(raw)
}
func uniqueObservations(ctx context.Context, data PinnedMeasurementData, side string) ([]uniqueObservation, error) {
	seen := map[string]string{}
	spans := map[string]string{}
	conditions := map[string]string{}
	out := []uniqueObservation{}
	for _, set := range data.Sets {
		if set.Pin.Side != side {
			continue
		}
		for _, rec := range set.Records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			identity := observationIdentity(set.Version.Context)
			executionKey := identity + "/" + rec.ExecutionID
			condition := set.Version.Context
			condition.Window = o.Window{}
			conditionRaw, _ := canonical(condition)
			if prior, ok := conditions[executionKey]; ok && prior != string(conditionRaw) {
				return nil, fault(422, "observation_conditions_conflict", "One execution has contradictory conditions")
			}
			conditions[executionKey] = string(conditionRaw)
			// Both IDs may contain "/", so a "/"-joined key let
			// {executionId:"a", id:"b/c"} and {executionId:"a/b", id:"c"}
			// collide into a false 422 observation_conflict and share one
			// diagram element id. The canonical JSON tuple is unambiguous
			// (review 2026-10-06, F37). executionKey stays "/"-joined: the
			// identity is a JSON object, which no other JSON object extends.
			key := observationKey(identity, rec.ExecutionID, rec.ID)
			raw, err := canonical(rec)
			if err != nil {
				return nil, err
			}
			if prev, ok := seen[key]; ok {
				if prev != string(raw) {
					return nil, fault(422, "observation_conflict", "Conflicting duplicate observation")
				}
				continue
			}
			seen[key] = string(raw)
			if rec.Type == "span" {
				spanKey := identity + "/" + rec.TraceID + "/" + rec.SpanID
				neutral := rec
				neutral.ID = ""
				raw, _ := canonical(neutral)
				if prev, ok := spans[spanKey]; ok {
					if prev != string(raw) {
						return nil, fault(422, "observation_conflict", "Conflicting span identity")
					}
					continue
				}
				spans[spanKey] = string(raw)
			}
			out = append(out, uniqueObservation{rec, set.Version.Context, key, identity + "/" + rec.ExecutionID})
		}
	}
	slices.SortFunc(out, func(a, b uniqueObservation) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}
func (in MeasurementInput) validate() error {
	if in.Policy != MeasurementPolicy || !slices.Contains([]string{"before", "after"}, in.Side) || len(in.ExecutionIDs) < 1 || len(in.ExecutionIDs) > 1000 || len(in.RootSpanIDs) > 1000 {
		return fault(422, "measurement_input", "Invalid metric selection")
	}
	for _, ids := range [][]string{in.ExecutionIDs, in.RootSpanIDs} {
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || len(id) > 256 || seen[id] {
				return malformed("Invalid or duplicate execution/root")
			}
			seen[id] = true
		}
	}
	if in.Basis.Kind == "spans" {
		if in.Basis.Basis != "" || in.Basis.Scope != "" {
			return malformed("Mixed metric basis")
		}
	} else if in.Basis.Kind != "measurements" || in.Basis.Basis == "" || in.Basis.Scope == "" {
		return malformed("Select explicit measurement basis and scope")
	}
	return nil
}
func nearestRank(values []int64, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	v := slices.Clone(values)
	slices.Sort(v)
	return v[(len(v)*percentile+99)/100-1]
}
func decimal(v int64) *string { return new(strconv.FormatInt(v, 10)) }
func metricInstrumentation(c o.ObservationContext, metric string) string {
	switch metric {
	case "sql_count":
		return c.Instrumentation.SQL
	case "external_call_count":
		return c.Instrumentation.ExternalCalls
	case "retry_count":
		return c.Instrumentation.Retries
	case "request_bytes", "response_bytes":
		return c.Instrumentation.Bytes
	default:
		return c.Instrumentation.Latency
	}
}
func controlRecord(r o.Record) bool {
	return r.Attrs != nil && (strings.HasPrefix(r.Attrs.Operation, "control/") || strings.HasPrefix(r.Attrs.Operation, "reset/"))
}

// MeasureScenario operates only on saved raw observations. No source traversal,
// current reads, percentile averaging or model-derived counts occur here.
func MeasureScenario(ctx context.Context, in MeasurementInput, data PinnedMeasurementData) (*ScenarioMeasurements, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	rows, err := uniqueObservations(ctx, data, in.Side)
	if err != nil {
		return nil, err
	}
	out := &ScenarioMeasurements{Policy: MeasurementPolicy, Input: in, Conditions: []MeasurementCondition{}, Metrics: map[string]MeasuredMetric{}, Gaps: []string{}}
	groups := map[string][]uniqueObservation{}
	found := map[string]bool{}
	for _, r := range rows {
		if slices.Contains(in.ExecutionIDs, r.Record.ExecutionID) && !controlRecord(r.Record) {
			groups[r.Execution] = append(groups[r.Execution], r)
			found[r.Record.ExecutionID] = true
		}
	}
	for _, id := range in.ExecutionIDs {
		if !found[id] {
			out.Gaps = append(out.Gaps, "missing execution: "+id)
		}
	}
	for _, set := range data.Sets {
		if set.Pin.Side != in.Side {
			continue
		}
		mocked := []string{}
		for _, r := range set.Records {
			if r.Attrs != nil && strings.HasPrefix(r.Attrs.Operation, "mocked/") {
				mocked = append(mocked, r.Attrs.Operation)
			}
		}
		slices.Sort(mocked)
		mocked = slices.Compact(mocked)
		out.Conditions = append(out.Conditions, MeasurementCondition{Pin: set.Pin, Context: set.Version.Context, MockedDependencies: mocked, SourceCompatible: set.Correlation.SourceCompatible, CorrelationGaps: slices.Clone(set.Correlation.Gaps)})
	}
	for _, group := range groups {
		failed, skipped, known := false, false, false
		for _, r := range group {
			if r.Record.Type == "test" {
				switch r.Record.Outcome {
				case "failed":
					failed = true
					known = true
				case "passed":
					known = true
				case "skipped":
					skipped = true
				}
			}
			if r.Record.Type == "span" && r.Record.ParentSpanID == "" && slices.Contains(in.RootSpanIDs, r.Record.SpanID) {
				// OR, never assign: an assignment let an unknown root span
				// erase the "known" a passed test set, depending only on which
				// record id sorts last (review 2026-10-06, F153).
				known = known || r.Record.Status != "unknown"
				failed = failed || r.Record.Status == "error"
			}
		}
		switch {
		case failed:
			out.FailedExecutions++
			out.SampledExecutions++
		case skipped:
			out.SkippedExecutions++
		case known:
			out.SampledExecutions++
		default:
			out.UnknownExecutions++
		}
	}
	// Two producers/sources may report the same execution id; their groups stay
	// separate executions, so their samples carry the same executionId and a
	// limitation says so.
	sharedExecutionIDs := map[string]bool{}
	groupsPerID := map[string]int{}
	for _, group := range groups {
		id := group[0].Record.ExecutionID
		groupsPerID[id]++
		sharedExecutionIDs[id] = groupsPerID[id] > 1
	}
	for _, name := range []string{"sql_count", "external_call_count", "retry_count", "request_bytes", "response_bytes", "latency_ns"} {
		m := MeasuredMetric{Unit: "count", Basis: in.Basis, Samples: []MetricSample{}, Limitations: []string{}, MissingSamples: len(in.ExecutionIDs) - len(found)}
		if strings.HasSuffix(name, "bytes") {
			m.Unit = "bytes"
		}
		if name == "latency_ns" {
			m.Unit = "ns"
			m.Basis = MetricBasis{Kind: "selected_roots_or_terminal_tests"}
		}
		total := int64(0)
		values := []int64{}
		overflow := false
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			group := groups[key]
			hasActual := false
			for _, row := range group {
				if row.Record.Type != "test" || row.Record.Outcome == "passed" || row.Record.Outcome == "failed" {
					hasActual = true
				}
			}
			if !hasActual {
				m.MissingSamples++
				continue
			}
			value := int64(0)
			n := 0
			complete := true
			// Completeness needs an actual root/terminal witness and all supplied parent
			// references. Producer coverage alone cannot fill an uploaded trace fragment.
			if name != "latency_ns" {
				roots, parents := map[string]bool{}, map[string]bool{}
				terminal := false
				for _, row := range group {
					rec := row.Record
					if rec.Type == "span" {
						parents[rec.TraceID+"/"+rec.SpanID] = true
						if rec.ParentSpanID == "" && slices.Contains(in.RootSpanIDs, rec.SpanID) {
							roots[rec.SpanID] = true
						}
					}
					if rec.Type == "test" && (rec.Outcome == "passed" || rec.Outcome == "failed") {
						terminal = true
					}
				}
				if len(roots) == 0 && !terminal {
					complete = false
				}
				for _, row := range group {
					rec := row.Record
					if rec.Type == "span" && rec.ParentSpanID != "" && !parents[rec.TraceID+"/"+rec.ParentSpanID] {
						complete = false
					}
				}
			}
			bad := false
			for _, r := range group {
				if metricInstrumentation(r.Context, name) != "complete" {
					complete = false
				}
			}
			add := func(v int64) {
				n++
				if v < 0 || v > math.MaxInt64-value {
					bad = true
					overflow = true
					return
				}
				value += v
			}
			if name == "latency_ns" {
				candidates := []int64{}
				testCandidates := []int64{}
				for _, r := range group {
					rec := r.Record
					if rec.Type == "span" && slices.Contains(in.RootSpanIDs, rec.SpanID) && rec.ParentSpanID == "" {
						start, e1 := strconv.ParseInt(rec.StartTimeUnixNano, 10, 64)
						end, e2 := strconv.ParseInt(rec.EndTimeUnixNano, 10, 64)
						if e1 == nil && e2 == nil && end >= start && start >= 0 {
							candidates = append(candidates, end-start)
						}
					}
					if rec.Type == "test" && (rec.Outcome == "passed" || rec.Outcome == "failed") {
						v, e := strconv.ParseInt(rec.DurationNs, 10, 64)
						if e == nil && v >= 0 {
							testCandidates = append(testCandidates, v)
						}
					}
				}
				if len(candidates) == 0 && len(in.RootSpanIDs) == 0 {
					candidates = testCandidates
				}
				if len(candidates) == 1 {
					add(candidates[0])
				} else {
					complete = false
				}
			} else if in.Basis.Kind == "measurements" {
				for _, r := range group {
					rec := r.Record
					if rec.Type == "measurement" && rec.Metric == name && rec.Basis == in.Basis.Basis && rec.Scope == in.Basis.Scope {
						v, e := strconv.ParseInt(rec.Value, 10, 64)
						if e != nil {
							bad = true
						} else {
							add(v)
						}
					}
				}
				// No matching measurement never asserts zero, regardless of instrumentation.
				if n == 0 {
					complete = false
				}
			} else {
				for _, r := range group {
					rec := r.Record
					if rec.Type != "span" {
						continue
					}
					switch name {
					case "sql_count":
						if rec.Kind == "client" && rec.Category == "sql" {
							add(1)
						}
					case "external_call_count":
						if rec.Kind == "client" && rec.Category == "http" {
							add(1)
						}
					case "retry_count":
						if rec.Category == "retry" {
							add(1)
						}
					case "request_bytes", "response_bytes":
						if rec.Kind != "client" || rec.Category != "http" {
							continue
						}
						var v *int64
						if rec.Attrs != nil {
							v = rec.Attrs.RequestBytes
							if name == "response_bytes" {
								v = rec.Attrs.ResponseBytes
							}
						}
						if v == nil {
							complete = false
						} else {
							add(*v)
						}
					}
				}
			}
			if bad || n == 0 && !complete {
				m.MissingSamples++
				continue
			}
			if !complete {
				m.Limitations = append(m.Limitations, "partial instrumentation: observed lower bound")
			}
			// The caller's raw execution id, not the internal grouping key
			// (canonical identity JSON + "/" + id), which an agent could not
			// match to its selection or the "missing execution" gap
			// (review 2026-10-06, F152).
			m.Samples = append(m.Samples, MetricSample{ExecutionID: group[0].Record.ExecutionID, Value: strconv.FormatInt(value, 10)})
			if sharedExecutionIDs[group[0].Record.ExecutionID] {
				m.Limitations = append(m.Limitations, "an executionId is shared by several observation identities; its samples are separate executions")
			}
			values = append(values, value)
			if value > math.MaxInt64-total {
				overflow = true
			} else {
				total += value
			}
		}
		m.SampleCount = len(m.Samples)
		if len(values) > 0 && !overflow {
			m.Value = decimal(total)
			if name == "latency_ns" {
				m.P50 = decimal(nearestRank(values, 50))
				m.P95 = decimal(nearestRank(values, 95))
			}
		}
		if overflow {
			m.Value = nil
			m.P50 = nil
			m.P95 = nil
			m.Limitations = append(m.Limitations, "overflow")
		}
		if m.MissingSamples > 0 {
			m.Limitations = append(m.Limitations, fmt.Sprintf("%d missing samples; absence is unknown", m.MissingSamples))
		}
		slices.Sort(m.Limitations)
		m.Limitations = slices.Compact(m.Limitations)
		out.Metrics[name] = m
	}
	out.Gaps = append(out.Gaps, "Sampled failures are not population rates; one execution does not cover alternatives")
	return out, nil
}
