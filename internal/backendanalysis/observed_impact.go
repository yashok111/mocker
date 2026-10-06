package backendanalysis

import (
	"context"
	"encoding/json/v2"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
	"reflect"
)

type ObservedImpact struct {
	Pin         o.AnalysisPin `json:"pin"`
	RecordID    string        `json:"recordId"`
	ExecutionID string        `json:"executionId"`
	Ref         bm.DiagramRef `json:"ref"`
	Certainty   string        `json:"certainty"`
	Method      string        `json:"method"`
	Limitations []string      `json:"limitations"`
}

func impactPins(in StartInput) ([]o.AnalysisPin, error) {
	out := []o.AnalysisPin{}
	for _, raw := range in.ObservationPins {
		var pin o.AnalysisPin
		if err := json.Unmarshal(raw, &pin); err != nil {
			return nil, err
		}
		out = append(out, pin)
	}
	return o.NormalizeAnalysisPins(out)
}
func (s *Service) resolveImpact(ctx context.Context, pid string, in StartInput, frozen *ImmutableInput, before, after *bm.EffectiveGraphSnapshot) error {
	if in.ObservationMode != "pinned" {
		return nil
	}
	if s.observations == nil {
		return fault(422, "observations_unavailable", "Observation reader unavailable")
	}
	pins, err := impactPins(in)
	if err != nil {
		return err
	}
	targets := map[string]o.SourcePin{"before": {RevisionID: before.Target.RevisionID, SemanticHash: before.Pins.BaseSemanticHash, TargetGraphHash: before.Pins.TargetHash}}
	// Desired proposal state cannot be measured after-side confirmation. The before
	// source remains eligible independently of an after proposal.
	if after.Target.RevisionID != "" {
		targets["after"] = o.SourcePin{RevisionID: after.Target.RevisionID, SemanticHash: after.Pins.BaseSemanticHash, TargetGraphHash: after.Pins.TargetHash}
	}
	data, err := s.observations.ResolveAnalysisPins(ctx, pid, o.PinRequest{Pins: pins, Targets: targets, RequireCompatible: true, MaxRecords: in.Limits.Records, MaxBytes: maxInputBytes})
	if err != nil {
		return err
	}
	frozen.ObservationMode = "pinned"
	frozen.ImpactObservations = data
	return nil
}
func (r *reportBuilder) addObservedImpact(ctx context.Context) error {
	if r.input.ImpactObservations == nil {
		return nil
	}
	mappings := map[string]o.CorrelationRow{}
	for _, set := range r.input.ImpactObservations.Sets {
		for _, row := range set.Correlation.Rows {
			key := set.Pin.Side + "/" + observationIdentity(set.Version.Context) + "/" + row.RecordID
			if previous, ok := mappings[key]; ok && !reflect.DeepEqual(previous, row) {
				return fault(422, "observation_mapping_conflict", "Conflicting correlations for the same observation")
			}
			mappings[key] = row
		}
	}
	for _, side := range []string{"before", "after"} {
		rows, err := uniqueObservations(ctx, *r.input.ImpactObservations, side)
		if err != nil {
			return err
		}
		records := map[string]uniqueObservation{}
		for _, row := range rows {
			records[observationIdentity(row.Context)+"/"+row.Record.ID] = row
		}
		seen := map[string]bool{}
		for _, set := range r.input.ImpactObservations.Sets {
			if set.Pin.Side != side {
				continue
			}
			for _, row := range set.Correlation.Rows {
				if err := ctx.Err(); err != nil {
					return err
				}
				key := observationIdentity(set.Version.Context) + "/" + row.RecordID
				rec, ok := records[key]
				if !ok || seen[key] || controlRecord(rec.Record) {
					continue
				}
				seen[key] = true
				if row.Selected == nil || row.Outcome == "unresolved" {
					r.gap("unresolved_observation", ObjectAddress{RecordType: "observation", ID: row.RecordID})
					continue
				}
				certainty := "possible"
				if row.Outcome == "explicit" {
					certainty = "confirmed"
				}
				ref := *row.Selected
				object := ObjectAddress{RecordType: ref.RecordType, ID: ref.ID}
				if !scopeObjectSelected(r.input.Scope, object, "") {
					continue
				}
				if service := r.input.Scope.Service; service != "" && service != set.Version.Context.Source.ServiceID {
					continue
				}
				r.add("witnesses", object, "observed_evidence", certainty, 0, ObservedImpact{Pin: set.Pin, RecordID: row.RecordID, ExecutionID: rec.Record.ExecutionID, Ref: ref, Certainty: certainty, Method: row.Method, Limitations: []string{"Observed object evidence does not confirm inferred structural paths or unexecuted branches", "Before-source evidence is not after-proposal measured confirmation"}})
			}
		}
	}
	return nil
}
