package backendobservations

import (
	"encoding/hex"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	if !bounded(c.Producer.ID, 256) || !bounded(c.Producer.SchemaVersion, 128) || !bounded(c.Producer.AdapterVersion, 128) || !bounded(c.Source.ServiceID, 256) || !bounded(c.Environment.ID, 256) || !one(c.Environment.Kind, "test", "staging", "production", "unknown") || !p.ValidHash(c.ConfigurationHash) || len(c.Input.Description) > 4096 {
		return invalid()
	}
	s := c.Source
	switch s.Status {
	case "known":
		if !bounded(s.BuildID, 256) || !p.ValidHash(s.SourceFilesHash) || !p.ValidID(s.RepositoryID) || s.Reason != "" {
			return invalid()
		}
	case "unknown":
		if !bounded(s.Reason, 4096) || s.BuildID != "" || s.SourceFilesHash != "" || s.RepositoryID != "" {
			return invalid()
		}
	default:
		return invalid()
	}
	if (c.Input.Size == nil) != (c.Input.Unit == nil) || c.Input.Size != nil && (*c.Input.Size < 0 || !bounded(*c.Input.Unit, 128)) {
		return invalid()
	}
	sm := c.Sampling
	if !one(sm.Kind, "all", "head", "tail", "unknown") || len(sm.Reason) > 4096 {
		return invalid()
	}
	if one(sm.Kind, "head", "tail") && sm.Probability == nil || sm.Kind == "unknown" && (sm.Probability != nil || sm.Reason == "") {
		return invalid()
	}
	if sm.Probability != nil {
		v := *sm.Probability
		if !plainDecimal(v) {
			return invalid()
		}
		n, ok := new(big.Rat).SetString(v)
		if !ok || n.Sign() <= 0 || n.Cmp(big.NewRat(1, 1)) > 0 {
			return invalid()
		}
	}
	i := c.Instrumentation
	for _, s := range []string{i.SQL, i.ExternalCalls, i.Retries, i.Bytes, i.Latency} {
		if !one(s, "complete", "partial", "unknown") {
			return invalid()
		}
	}
	if len(i.Limitations) > 100 {
		return invalid()
	}
	for _, s := range i.Limitations {
		if !bounded(s, 4096) {
			return invalid()
		}
	}
	if c.Scenario != nil {
		s := c.Scenario
		switch s.Kind {
		case "design_scenario":
			if !p.ValidID(s.ID) || !p.ValidID(s.RevisionID) || !p.ValidHash(s.ContentHash) || s.PackageID != "" || s.Version != 0 || s.Hash != "" {
				return invalid()
			}
		case "backend_replay":
			if !p.ValidID(s.PackageID) || s.Version < 1 || !p.ValidHash(s.Hash) || s.ID != "" || s.RevisionID != "" || s.ContentHash != "" {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	return nil
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
	if r.IdentityRef != nil {
		v := r.IdentityRef
		if !p.ValidID(v.RepositoryID) || !bounded(v.ProviderNamespace, 200) || !bounded(v.ExternalKey, 200) || !one(v.RecordType, "node", "edge") {
			return invalid()
		}
	}
	if r.DiagramWitness != nil {
		v := r.DiagramWitness
		if v.Pin.Validate() != nil || !one(v.Kind, "membership", "state_change", "business_event", "replay_assertion") {
			return invalid()
		}
		if (bm.DiagramScopeInput{Pin: v.Pin, Selectors: []bm.DiagramScopeSelector{v.Selector}}).Validate() != nil {
			return invalid()
		}
		if v.Kind == "replay_assertion" {
			if r.Type != "test" || !bounded(v.AssertionID, 256) {
				return invalid()
			}
		} else if v.AssertionID != "" {
			return invalid()
		}
	}
	switch r.Type {
	case "span":
		if !hexID(r.TraceID, 16) || !hexID(r.SpanID, 8) || r.ParentSpanID != "" && (!hexID(r.ParentSpanID, 8) || r.ParentSpanID == r.SpanID) || !checkTime(r.StartTimeUnixNano) || !checkTime(r.EndTimeUnixNano) || !one(r.Kind, "server", "client", "internal") || !one(r.Category, "sql", "http", "retry", "internal") || !one(r.Status, "ok", "error", "unset") || r.Attrs == nil {
			return invalid()
		}
		a, _ := decimal(r.StartTimeUnixNano)
		b, _ := decimal(r.EndTimeUnixNano)
		if b < a {
			return invalid()
		}
		at := r.Attrs
		if len(at.Operation) > 256 || len(at.SourcePath) > 1024 || at.SourceLine < 0 || at.Fingerprint != "" && !p.ValidHash(at.Fingerprint) || at.RequestBytes != nil && *at.RequestBytes < 0 || at.ResponseBytes != nil && *at.ResponseBytes < 0 {
			return invalid()
		}
		causal := c.Producer.SchemaVersion == "orders-message-causal-v1"
		if at.MessageRole != "" || at.MessageIDHash != "" {
			if !causal || !one(at.MessageRole, "send", "receive") || !p.ValidHash(at.MessageIDHash) {
				return invalid()
			}
		}
		if r.Links != nil {
			if len(*r.Links) > 32 {
				return invalid()
			}
			seen := map[string]bool{}
			for _, l := range *r.Links {
				if !hexID(l.TraceID, 16) || !hexID(l.SpanID, 8) || l.TraceID == r.TraceID && l.SpanID == r.SpanID {
					return invalid()
				}
				if l.Relation == "association" {
					if l.Proof != nil {
						return invalid()
					}
				} else if l.Relation == "follows_from" {
					pr := l.Proof
					if !causal || pr == nil || pr.Profile != "orders-message-causal-v1" || !p.ValidHash(pr.MessageIDHash) || pr.PredecessorEvent != "send" || pr.SuccessorEvent != "receive" || at.MessageRole != "receive" || at.MessageIDHash != pr.MessageIDHash {
						return invalid()
					}
				} else {
					return invalid()
				}
				b, _ := canonical(l)
				if seen[string(b)] {
					return invalid()
				}
				seen[string(b)] = true
			}
		}
	case "test":
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
	case "measurement":
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
	default:
		return invalid()
	}
	return nil
}

type recordWire Record
