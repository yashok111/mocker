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
		graph := r.before
		if side == "after" {
			graph = r.after
		}
		kinds := observedKinds(graph)
		services := map[string]bool{}
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
				object, kind := observedObject(kinds, ref)
				observedService := set.Version.Context.Source.ServiceID
				if _, ok := services[observedService]; !ok {
					services[observedService] = observedInService(graph, r.input.Scope.Service, observedService)
				}
				// Filter by the object's real kind and match the service by id or
				// name like inService; a filtered row is a visible scope gap. With
				// kind "" any scope.kind dropped every observed witness silently,
				// and a service name never matched (review 2026-10-06, F154).
				if !scopeObjectSelected(r.input.Scope, object, kind) || !services[observedService] {
					r.gap("scope_omitted_observation", object)
					continue
				}
				r.add("witnesses", object, "observed_evidence", certainty, 0, ObservedImpact{Pin: set.Pin, RecordID: row.RecordID, ExecutionID: rec.Record.ExecutionID, Ref: ref, Certainty: certainty, Method: row.Method, Limitations: []string{"Observed object evidence does not confirm inferred structural paths or unexecuted branches", "Before-source evidence is not after-proposal measured confirmation"}})
			}
		}
	}
	return nil
}

// observedKinds indexes one side's node and edge kinds once, so resolving each
// observation row's object kind stays linear in rows plus graph size.
func observedKinds(g *bm.EffectiveGraphSnapshot) map[ObjectAddress]string {
	out := map[ObjectAddress]string{}
	if g == nil {
		return out
	}
	for _, n := range g.State.Nodes {
		out[ObjectAddress{RecordType: "node", ID: n.ID}] = n.Kind
	}
	for _, e := range g.State.Edges {
		out[ObjectAddress{RecordType: "edge", ID: e.ID}] = e.Kind
	}
	return out
}

// observedObject addresses an observation's selected ref. An artifact or
// namespaced artifact ref has no recordType/id (validateDiagramRef requires
// them empty), so it is addressed as artifact_object by the digest of the
// exact ref instead of the empty object {"",""} every artifact witness used to
// share (review 2026-10-06, F188). A record's kind comes from the side's graph.
func observedObject(kinds map[ObjectAddress]string, ref bm.DiagramRef) (ObjectAddress, string) {
	if ref.Kind != "record" {
		raw, _ := canonical(ref)
		return ObjectAddress{RecordType: "artifact_object", ID: digest(raw)}, "artifact_object"
	}
	object := ObjectAddress{RecordType: ref.RecordType, ID: ref.ID}
	return object, kinds[object]
}

// observedInService matches the observation's source service against
// scope.service by id or by the service node's name, as inService does.
func observedInService(g *bm.EffectiveGraphSnapshot, service, observed string) bool {
	if service == "" || service == observed {
		return true
	}
	return g != nil && inService(g, observed, service)
}
