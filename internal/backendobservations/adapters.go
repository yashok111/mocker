package backendobservations

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"reflect"

	br "github.com/yashok111/mocker/internal/backendreplay"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

type ReplayReader interface {
	Get(context.Context, string, string) (*br.Run, error)
}
type ReplayPin struct {
	RunID      string `json:"runId"`
	ReportHash string `json:"reportHash"`
}
type AdaptInput struct {
	Adapter string             `json:"adapter"`
	Context ObservationContext `json:"context"`
	Data    string             `json:"data,omitempty"`
	Replay  *ReplayPin         `json:"replay,omitzero"`
}
type AdaptBatch struct {
	Context ObservationContext `json:"context"`
	Records []Record           `json:"records"`
}
type AdaptedBatch struct {
	Adapter     string         `json:"adapter"`
	Batches     []AdaptBatch   `json:"batches"`
	Excluded    map[string]int `json:"excluded"`
	Unsupported []string       `json:"unsupported"`
	Gaps        []string       `json:"gaps"`
}

func (v *AdaptInput) UnmarshalJSON(b []byte) error {
	type wire AdaptInput
	// JSON escaping can expand the uploaded raw string up to sixfold. The
	// decoded input retains the independent 4 MiB admission limit below.
	if len(b) > 32<<20 {
		return fault(413, "adapter_envelope_limit")
	}
	if err := json.Unmarshal(b, (*wire)(v), json.RejectUnknownMembers(true)); err != nil {
		return invalid()
	}
	if len(v.Data) > AdapterBytes {
		return fault(413, "adapter_limit")
	}
	return required(b, reflect.TypeFor[wire](), false)
}

func Adapt(ctx context.Context, pid string, in AdaptInput, replay ReplayReader) (*AdaptedBatch, error) {
	if len(in.Data) > AdapterBytes {
		return nil, fault(413, "adapter_limit")
	}
	if e := ValidateContext(in.Context); e != nil {
		return nil, e
	}
	out := &AdaptedBatch{Adapter: in.Adapter, Batches: []AdaptBatch{}, Excluded: map[string]int{}, Unsupported: []string{}, Gaps: []string{}}
	var batches []AdaptBatch
	var e error
	switch in.Adapter {
	case "otlp-traces-json-v1":
		if in.Replay != nil {
			return nil, invalid()
		}
		batches, e = adaptOTel(in, out)
	case "junit-summary-v1":
		if in.Replay != nil {
			return nil, invalid()
		}
		batches, e = adaptJUnit(in, out)
	case "orders-replay-result-v1":
		if in.Data != "" || in.Replay == nil || replay == nil {
			return nil, invalid()
		}
		batches, e = adaptReplay(ctx, pid, in, replay, out)
	default:
		return nil, fault(422, "unsupported_adapter")
	}
	if e != nil {
		return nil, e
	}
	for _, b := range batches {
		b.Context.Producer.AdapterVersion = in.Adapter
		if e = ValidateContext(b.Context); e != nil {
			return nil, e
		}
		part := AdaptBatch{Context: b.Context, Records: []Record{}}
		size := 0
		cb, _ := canonical(b.Context)
		for _, r := range b.Records {
			if e = ValidateRecord(b.Context, r); e != nil {
				return nil, invalid()
			}
			raw, _ := canonical(r)
			if len(raw)+len(cb)+1024 > BatchBytes {
				return nil, fault(413, "batch_limit")
			}
			if len(part.Records) == 500 || size+len(raw)+len(cb)+1024 > BatchBytes {
				out.Batches = append(out.Batches, part)
				part = AdaptBatch{Context: b.Context, Records: []Record{}}
				size = 0
			}
			part.Records = append(part.Records, r)
			size += len(raw)
		}
		if len(part.Records) > 0 {
			out.Batches = append(out.Batches, part)
		}
	}
	return out, nil
}
func adaptReplay(ctx context.Context, pid string, in AdaptInput, reader ReplayReader, out *AdaptedBatch) ([]AdaptBatch, error) {
	if !p.ValidID(in.Replay.RunID) || !p.ValidHash(in.Replay.ReportHash) {
		return nil, invalid()
	}
	run, e := reader.Get(ctx, pid, in.Replay.RunID)
	if e != nil {
		return nil, e
	}
	if run.Report == nil || run.Status == "running" || run.Status == "queued" {
		return nil, fault(409, "report_not_terminal")
	}
	report := run.Report
	hash, e := p.Hash("backend-replay-report-v1", report)
	if e != nil || hash != in.Replay.ReportHash || report.RunID != run.ID || report.Package != run.Input.Start.Package {
		return nil, fault(409, "report_pin_mismatch")
	}
	if e = report.Provenance.Validate(report.Identity); e != nil {
		return nil, invalid()
	}
	c := in.Context
	if c.Source.Status != "known" || c.Source.SourceFilesHash != report.Identity.SourceTreeHash || c.Source.BuildID != report.Identity.BuildHash {
		return nil, fault(422, "source_pin_mismatch")
	}
	c.Scenario = &Scenario{Kind: "backend_replay", PackageID: report.Package.ID, Version: report.Package.Version, Hash: report.Package.ContentHash}
	c.Producer = Producer{"orders-replay", report.CheckerVersion, in.Adapter}
	assertions := []Assertion{}
	trigger := "unverified"
	for _, a := range report.Assertions {
		outcome := "failed"
		if a.Passed {
			outcome = "passed"
		}
		assertions = append(assertions, Assertion{a.ID, outcome, a.Scope})
		if a.Kind == "triggers" && a.Passed {
			trigger = "verified"
		}
	}
	outcome := "unknown"
	if report.Status == "succeeded" {
		outcome = "passed"
	} else if report.Status == "failed" {
		outcome = "failed"
	}
	start := run.CreatedAt.UnixNano()
	end := run.UpdatedAt.UnixNano()
	if end < start {
		return nil, invalid()
	}
	r := Record{Type: "test", ID: run.ID, ExecutionID: run.ID, SuiteID: "orders-replay", CaseID: report.Package.ID, Outcome: outcome, Timestamp: fmt.Sprint(end), DurationNs: fmt.Sprint(end - start), Assertions: &assertions, RunPins: &RunPins{RunID: run.ID, ReportHash: hash, Package: c.Scenario, TriggerVerdict: trigger, SourceHash: report.Identity.SourceTreeHash}}
	// Bindings remain exact report evidence; never re-read the mutable package head.
	out.Gaps = append(out.Gaps, "Replay assertions retain actual_fixture/mocked scope; no timing spans or state-change events inferred")
	return []AdaptBatch{{c, []Record{r}}}, nil
}
