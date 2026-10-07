package backendobservations

import (
	"encoding/hex"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func bounded(s string, n int) bool {
	return s != "" && len(s) <= n && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func one(s string, values ...string) bool { return slices.Contains(values, s) }
func decimal(s string) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 || strconv.FormatInt(n, 10) != s {
		return 0, invalid()
	}
	return n, nil
}

// plainDecimal admits only digits with at most one '.', and at least one digit.
// big.Rat.SetString, which follows, scans with base 0 and so also accepts
// 0x/0b/0o prefixes, '_' separators and 'p' exponents: "0x.8p0" passed as a
// probability and JUnit time="0x10" imported as 16 seconds
// (review 2026-10-06, F159).
func plainDecimal(s string) bool {
	digits, dots := 0, 0
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
		default:
			return false
		}
	}
	return digits > 0 && dots <= 1 && len(s) <= 64
}
func hexID(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && s == strings.ToLower(s) && strings.Trim(s, "0") != ""
}
func window(c ObservationContext) (int64, int64, error) {
	a, e := time.Parse(time.RFC3339Nano, c.Window.Start)
	b, f := time.Parse(time.RFC3339Nano, c.Window.End)
	if e != nil || f != nil || !strings.HasSuffix(c.Window.Start, "Z") || !strings.HasSuffix(c.Window.End, "Z") || a.Before(time.Unix(0, 0)) || b.Before(a) || b.After(time.Unix(0, 1<<63-1)) {
		return 0, 0, invalid()
	}
	return a.UnixNano(), b.UnixNano(), nil
}
func ValidateContext(c ObservationContext) error {
	if _, _, e := window(c); e != nil {
		return e
	}
	if !producerValid(c) {
		return invalid()
	}
	if !sourceValid(c.Source) {
		return invalid()
	}
	if (c.Input.Size == nil) != (c.Input.Unit == nil) || c.Input.Size != nil && (*c.Input.Size < 0 || !bounded(*c.Input.Unit, 128)) {
		return invalid()
	}
	if !samplingValid(c.Sampling) {
		return invalid()
	}
	if !instrumentationValid(c.Instrumentation) {
		return invalid()
	}
	if c.Scenario != nil && !scenarioValid(c.Scenario) {
		return invalid()
	}
	return nil
}

func producerValid(c ObservationContext) bool {
	return bounded(c.Producer.ID, 256) && bounded(c.Producer.SchemaVersion, 128) && bounded(c.Producer.AdapterVersion, 128) && bounded(c.Source.ServiceID, 256) && bounded(c.Environment.ID, 256) && one(c.Environment.Kind, "test", "staging", "production", "unknown") && p.ValidHash(c.ConfigurationHash) && len(c.Input.Description) <= 4096
}

// sourceValid: a known source names its build exactly; an unknown one says
// why and names nothing.
func sourceValid(s Source) bool {
	switch s.Status {
	case "known":
		return bounded(s.BuildID, 256) && p.ValidHash(s.SourceFilesHash) && p.ValidID(s.RepositoryID) && s.Reason == ""
	case "unknown":
		return bounded(s.Reason, 4096) && s.BuildID == "" && s.SourceFilesHash == "" && s.RepositoryID == ""
	default:
		return false
	}
}

// samplingValid: head and tail sampling carry a probability in (0, 1],
// unknown sampling carries a reason instead.
func samplingValid(sm Sampling) bool {
	if !one(sm.Kind, "all", "head", "tail", "unknown") || len(sm.Reason) > 4096 {
		return false
	}
	if one(sm.Kind, "head", "tail") && sm.Probability == nil || sm.Kind == "unknown" && (sm.Probability != nil || sm.Reason == "") {
		return false
	}
	if sm.Probability == nil {
		return true
	}
	v := *sm.Probability
	if !plainDecimal(v) {
		return false
	}
	n, ok := new(big.Rat).SetString(v)
	return ok && n.Sign() > 0 && n.Cmp(big.NewRat(1, 1)) <= 0
}

func instrumentationValid(i Instrumentation) bool {
	for _, s := range []string{i.SQL, i.ExternalCalls, i.Retries, i.Bytes, i.Latency} {
		if !one(s, "complete", "partial", "unknown") {
			return false
		}
	}
	if len(i.Limitations) > 100 {
		return false
	}
	for _, s := range i.Limitations {
		if !bounded(s, 4096) {
			return false
		}
	}
	return true
}

// scenarioValid: a scenario is either a design scenario revision or a replay
// package version, with the other kind's fields empty.
func scenarioValid(s *Scenario) bool {
	switch s.Kind {
	case "design_scenario":
		return p.ValidID(s.ID) && p.ValidID(s.RevisionID) && p.ValidHash(s.ContentHash) && s.PackageID == "" && s.Version == 0 && s.Hash == ""
	case "backend_replay":
		return p.ValidID(s.PackageID) && s.Version >= 1 && p.ValidHash(s.Hash) && s.ID == "" && s.RevisionID == "" && s.ContentHash == ""
	default:
		return false
	}
}

func ValidateRecord(c ObservationContext, r Record) error {
	// Round-trip through the closed union also protects direct Go callers.
	raw, e := canonical(r)
	if e != nil {
		return invalid()
	}
	var checked Record
	if decode(raw, (*recordWire)(&checked)) != nil {
		return invalid()
	}
	if checked.UnmarshalJSON(raw) != nil {
		return invalid()
	}
	if !bounded(r.ID, 256) || !bounded(r.ExecutionID, 256) {
		return invalid()
	}
	lo, hi, e := window(c)
	if e != nil {
		return e
	}
	checkTime := func(s string) bool { v, e := decimal(s); return e == nil && v >= lo && v <= hi }
	if r.IdentityRef != nil && !identityRefValid(r.IdentityRef) {
		return invalid()
	}
	if r.DiagramWitness != nil && !diagramWitnessValid(r, r.DiagramWitness) {
		return invalid()
	}
	switch r.Type {
	case "span":
		return validateSpan(c, r, checkTime)
	case "test":
		return validateTest(r, checkTime)
	case "measurement":
		return validateMeasurement(r, checkTime)
	default:
		return invalid()
	}
}

func identityRefValid(v *IdentityRef) bool {
	return p.ValidID(v.RepositoryID) && bounded(v.ProviderNamespace, 200) && bounded(v.ExternalKey, 200) && one(v.RecordType, "node", "edge")
}

// diagramWitnessValid: only a test record witnesses a replay assertion, and
// only that kind names one.
func diagramWitnessValid(r Record, v *DiagramWitness) bool {
	if v.Pin.Validate() != nil || !one(v.Kind, "membership", "state_change", "business_event", "replay_assertion") {
		return false
	}
	if (bm.DiagramScopeInput{Pin: v.Pin, Selectors: []bm.DiagramScopeSelector{v.Selector}}).Validate() != nil {
		return false
	}
	if v.Kind == "replay_assertion" {
		return r.Type == "test" && bounded(v.AssertionID, 256)
	}
	return v.AssertionID == ""
}

func validateSpan(c ObservationContext, r Record, checkTime func(string) bool) error {
	if !spanShapeValid(r, checkTime) {
		return invalid()
	}
	a, _ := decimal(r.StartTimeUnixNano)
	b, _ := decimal(r.EndTimeUnixNano)
	if b < a {
		return invalid()
	}
	at := r.Attrs
	if !spanAttrsValid(at) {
		return invalid()
	}
	causal := c.Producer.SchemaVersion == "orders-message-causal-v1"
	if at.MessageRole != "" || at.MessageIDHash != "" {
		if !causal || !one(at.MessageRole, "send", "receive") || !p.ValidHash(at.MessageIDHash) {
			return invalid()
		}
	}
	if r.Links != nil {
		return validateLinks(r, at, causal)
	}
	return nil
}

func spanShapeValid(r Record, checkTime func(string) bool) bool {
	if !hexID(r.TraceID, 16) || !hexID(r.SpanID, 8) || r.ParentSpanID != "" && (!hexID(r.ParentSpanID, 8) || r.ParentSpanID == r.SpanID) {
		return false
	}
	return checkTime(r.StartTimeUnixNano) && checkTime(r.EndTimeUnixNano) && one(r.Kind, "server", "client", "internal") && one(r.Category, "sql", "http", "retry", "internal") && one(r.Status, "ok", "error", "unset") && r.Attrs != nil
}

func spanAttrsValid(at *Attributes) bool {
	return len(at.Operation) <= 256 && len(at.SourcePath) <= 1024 && at.SourceLine >= 0 && (at.Fingerprint == "" || p.ValidHash(at.Fingerprint)) && (at.RequestBytes == nil || *at.RequestBytes >= 0) && (at.ResponseBytes == nil || *at.ResponseBytes >= 0)
}

// validateLinks admits at most 32 distinct links, none to the span itself.
func validateLinks(r Record, at *Attributes, causal bool) error {
	if len(*r.Links) > 32 {
		return invalid()
	}
	seen := map[string]bool{}
	for _, l := range *r.Links {
		if !hexID(l.TraceID, 16) || !hexID(l.SpanID, 8) || l.TraceID == r.TraceID && l.SpanID == r.SpanID {
			return invalid()
		}
		if !linkRelationValid(l, at, causal) {
			return invalid()
		}
		b, _ := canonical(l)
		if seen[string(b)] {
			return invalid()
		}
		seen[string(b)] = true
	}
	return nil
}

// linkRelationValid: an association carries no proof; follows_from is
// admitted only for the causal message profile, from the receive side of the
// very message the proof names.
func linkRelationValid(l SpanLink, at *Attributes, causal bool) bool {
	switch l.Relation {
	case "association":
		return l.Proof == nil
	case "follows_from":
		pr := l.Proof
		if !causal || pr == nil || pr.Profile != "orders-message-causal-v1" || !p.ValidHash(pr.MessageIDHash) {
			return false
		}
		return pr.PredecessorEvent == "send" && pr.SuccessorEvent == "receive" && at.MessageRole == "receive" && at.MessageIDHash == pr.MessageIDHash
	default:
		return false
	}
}

func validateTest(r Record, checkTime func(string) bool) error {
	if !checkTime(r.Timestamp) || !bounded(r.SuiteID, 256) || !bounded(r.CaseID, 256) || !one(r.Outcome, "passed", "failed", "skipped", "unknown") || r.Assertions == nil || r.RunPins == nil || len(*r.Assertions) > 500 {
		return invalid()
	}
	if _, e := decimal(r.DurationNs); e != nil {
		return e
	}
	rp := r.RunPins
	if !bounded(rp.RunID, 256) || !p.ValidHash(rp.ReportHash) || !p.ValidHash(rp.SourceHash) || !one(rp.TriggerVerdict, "verified", "unverified", "not_applicable") {
		return invalid()
	}
	for _, a := range *r.Assertions {
		if !bounded(a.ID, 256) || !one(a.Outcome, "passed", "failed", "skipped", "unknown") || !one(a.Scope, "actual_fixture", "mocked", "unknown") {
			return invalid()
		}
	}
	return nil
}

func validateMeasurement(r Record, checkTime func(string) bool) error {
	if !checkTime(r.Timestamp) || !one(r.Metric, "sql_count", "external_call_count", "retry_count", "request_bytes", "response_bytes") || !bounded(r.Basis, 256) || !bounded(r.Scope, 256) || !one(r.Unit, "count", "bytes") {
		return invalid()
	}
	// The unit is fixed by the metric. Analysis sums by metric name and
	// takes the unit from it, so {request_bytes, count} was frozen as
	// contradictory evidence and summed as bytes (review 2026-10-06, F161).
	unit := "count"
	if strings.HasSuffix(r.Metric, "_bytes") {
		unit = "bytes"
	}
	if r.Unit != unit {
		return invalid()
	}
	if _, e := decimal(r.Value); e != nil {
		return e
	}
	return nil
}

type recordWire Record
