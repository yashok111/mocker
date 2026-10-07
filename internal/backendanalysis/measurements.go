package backendanalysis

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	o "github.com/yashok111/mocker/internal/backendobservations"
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
		if set.Pin.Side == in.Side {
			out.Conditions = append(out.Conditions, MeasurementCondition{Pin: set.Pin, Context: set.Version.Context, MockedDependencies: mockedDependencies(set.Records), SourceCompatible: set.Correlation.SourceCompatible, CorrelationGaps: slices.Clone(set.Correlation.Gaps)})
		}
	}
	for _, group := range groups {
		switch executionOutcome(in, group) {
		case "failed":
			out.FailedExecutions++
			out.SampledExecutions++
		case "skipped":
			out.SkippedExecutions++
		case "known":
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
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	mm := metricMeasurer{in: in, groups: groups, keys: keys, shared: sharedExecutionIDs, missing: len(in.ExecutionIDs) - len(found)}
	for _, name := range []string{"sql_count", "external_call_count", "retry_count", "request_bytes", "response_bytes", "latency_ns"} {
		out.Metrics[name] = mm.measure(name)
	}
	out.Gaps = append(out.Gaps, "Sampled failures are not population rates; one execution does not cover alternatives")
	return out, nil
}

func mockedDependencies(records []o.Record) []string {
	mocked := []string{}
	for _, r := range records {
		if r.Attrs != nil && strings.HasPrefix(r.Attrs.Operation, "mocked/") {
			mocked = append(mocked, r.Attrs.Operation)
		}
	}
	slices.Sort(mocked)
	return slices.Compact(mocked)
}

// executionOutcome classifies one execution as "failed", "skipped", "known"
// (a passed test or a root span of known status) or "unknown".
func executionOutcome(in MeasurementInput, group []uniqueObservation) string {
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
		return "failed"
	case skipped:
		return "skipped"
	case known:
		return "known"
	default:
		return "unknown"
	}
}

// metricMeasurer measures each metric over the same execution groups, in
// their sorted key order.
type metricMeasurer struct {
	in      MeasurementInput
	groups  map[string][]uniqueObservation
	keys    []string
	shared  map[string]bool
	missing int
}

func (mm metricMeasurer) measure(name string) MeasuredMetric {
	in := mm.in
	m := MeasuredMetric{Unit: "count", Basis: in.Basis, Samples: []MetricSample{}, Limitations: []string{}, MissingSamples: mm.missing}
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
	for _, key := range mm.keys {
		group := mm.groups[key]
		if !hasActualSample(group) {
			m.MissingSamples++
			continue
		}
		s := sampleGroup(in, name, group)
		overflow = overflow || s.overflow
		if s.bad || s.n == 0 && !s.complete {
			m.MissingSamples++
			continue
		}
		if !s.complete {
			m.Limitations = append(m.Limitations, "partial instrumentation: observed lower bound")
		}
		// The caller's raw execution id, not the internal grouping key
		// (canonical identity JSON + "/" + id), which an agent could not
		// match to its selection or the "missing execution" gap
		// (review 2026-10-06, F152).
		m.Samples = append(m.Samples, MetricSample{ExecutionID: group[0].Record.ExecutionID, Value: strconv.FormatInt(s.value, 10)})
		if mm.shared[group[0].Record.ExecutionID] {
			m.Limitations = append(m.Limitations, "an executionId is shared by several observation identities; its samples are separate executions")
		}
		values = append(values, s.value)
		if s.value > math.MaxInt64-total {
			overflow = true
		} else {
			total += s.value
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
	return m
}

// hasActualSample: an execution whose only records are skipped or unknown
// tests has nothing to measure.
func hasActualSample(group []uniqueObservation) bool {
	for _, row := range group {
		if row.Record.Type != "test" || row.Record.Outcome == "passed" || row.Record.Outcome == "failed" {
			return true
		}
	}
	return false
}

// groupSample is one execution's value for one metric: n contributions
// summed, whether they cover it completely, and whether any was unusable or
// overflowed.
type groupSample struct {
	value         int64
	n             int
	complete, bad bool
	overflow      bool
}

func (s *groupSample) add(v int64) {
	s.n++
	if v < 0 || v > math.MaxInt64-s.value {
		s.bad = true
		s.overflow = true
		return
	}
	s.value += v
}

func sampleGroup(in MeasurementInput, name string, group []uniqueObservation) groupSample {
	s := groupSample{complete: true}
	// Completeness needs an actual root/terminal witness and all supplied parent
	// references. Producer coverage alone cannot fill an uploaded trace fragment.
	if name != "latency_ns" && !traceComplete(in, group) {
		s.complete = false
	}
	for _, r := range group {
		if metricInstrumentation(r.Context, name) != "complete" {
			s.complete = false
		}
	}
	switch {
	case name == "latency_ns":
		sampleLatency(in, group, &s)
	case in.Basis.Kind == "measurements":
		sampleMeasurements(in, name, group, &s)
	default:
		sampleSpans(name, group, &s)
	}
	return s
}

// traceComplete: the group holds a selected root span or a terminal test, and
// every span's parent is in the group too.
func traceComplete(in MeasurementInput, group []uniqueObservation) bool {
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
	complete := len(roots) != 0 || terminal
	for _, row := range group {
		rec := row.Record
		if rec.Type == "span" && rec.ParentSpanID != "" && !parents[rec.TraceID+"/"+rec.ParentSpanID] {
			complete = false
		}
	}
	return complete
}

// sampleLatency takes the single selected root span's duration, or, with no
// roots selected, the single terminal test's; anything else is incomplete.
func sampleLatency(in MeasurementInput, group []uniqueObservation, s *groupSample) {
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
		s.add(candidates[0])
	} else {
		s.complete = false
	}
}

func sampleMeasurements(in MeasurementInput, name string, group []uniqueObservation, s *groupSample) {
	for _, r := range group {
		rec := r.Record
		if rec.Type == "measurement" && rec.Metric == name && rec.Basis == in.Basis.Basis && rec.Scope == in.Basis.Scope {
			v, e := strconv.ParseInt(rec.Value, 10, 64)
			if e != nil {
				s.bad = true
			} else {
				s.add(v)
			}
		}
	}
	// No matching measurement never asserts zero, regardless of instrumentation.
	if s.n == 0 {
		s.complete = false
	}
}

// sampleSpans counts the client spans of the metric's category, or sums the
// byte attribute a client HTTP span carries.
func sampleSpans(name string, group []uniqueObservation, s *groupSample) {
	for _, r := range group {
		rec := r.Record
		if rec.Type != "span" {
			continue
		}
		switch name {
		case "sql_count":
			if rec.Kind == "client" && rec.Category == "sql" {
				s.add(1)
			}
		case "external_call_count":
			if rec.Kind == "client" && rec.Category == "http" {
				s.add(1)
			}
		case "retry_count":
			if rec.Category == "retry" {
				s.add(1)
			}
		case "request_bytes", "response_bytes":
			sampleBytes(name, rec, s)
		}
	}
}

func sampleBytes(name string, rec o.Record, s *groupSample) {
	if rec.Kind != "client" || rec.Category != "http" {
		return
	}
	var v *int64
	if rec.Attrs != nil {
		v = rec.Attrs.RequestBytes
		if name == "response_bytes" {
			v = rec.Attrs.ResponseBytes
		}
	}
	if v == nil {
		s.complete = false
	} else {
		s.add(*v)
	}
}
