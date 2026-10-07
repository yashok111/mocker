package backendanalysis

import (
	"context"
	"slices"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
)

type StartMeasurementInput struct {
	Kind             string                `json:"kind"`
	BeforeRevisionID string                `json:"beforeRevisionId"`
	AfterRevisionID  string                `json:"afterRevisionId,omitempty"`
	ObservationPins  []o.AnalysisPin       `json:"observationPins"`
	Measurements     []MeasurementInput    `json:"measurements"`
	DiagramScope     *bm.DiagramScopeInput `json:"diagramScope,omitzero"`
	Limits           Limits                `json:"limits"`
	IdempotencyKey   string                `json:"idempotencyKey"`
}
type FrozenObservationAnalysis struct {
	BeforeCoverage bm.Coverage          `json:"beforeCoverage"`
	AfterCoverage  bm.Coverage          `json:"afterCoverage"`
	Measurements   []MeasurementInput   `json:"measurements"`
	Data           o.PinnedObservations `json:"data"`
}
type ObservationResolver interface {
	ResolveAnalysisPins(context.Context, string, o.PinRequest) (*o.PinnedObservations, error)
}

func (s *Service) SetObservations(resolver ObservationResolver) { s.observations = resolver }
func measurementKind(kind string) bool {
	return kind == "scenario_measurement" || kind == "scenario_comparison"
}
func (in *MeasurementInput) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"side", "executionIds", "rootSpanIds", "basis", "policy"}, nil); err != nil {
		return err
	}
	type wire MeasurementInput
	if err := decode(raw, (*wire)(in)); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	slices.Sort(in.ExecutionIDs)
	slices.Sort(in.RootSpanIDs)
	return nil
}
func (in *MetricBasis) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"kind"}, []string{"basis", "scope"}); err != nil {
		return err
	}
	type wire MetricBasis
	return decode(raw, (*wire)(in))
}
func (in *StartInput) unmarshalMeasurement(raw []byte) error {
	if _, err := closed(raw, []string{"kind", "beforeRevisionId", "observationPins", "measurements", "limits", "idempotencyKey"}, []string{"afterRevisionId", "diagramScope"}); err != nil {
		return err
	}
	var v StartMeasurementInput
	if err := decode(raw, &v); err != nil {
		return err
	}
	if !bm.ValidID(v.BeforeRevisionID) || !validKey(v.IdempotencyKey) {
		return malformed("Invalid measurement source/key")
	}
	if v.Kind == "scenario_measurement" {
		if len(v.Measurements) != 1 || v.Measurements[0].Side != "before" || v.AfterRevisionID != "" {
			return malformed("Measurement requires before side only")
		}
	} else {
		if len(v.Measurements) != 2 || v.Measurements[0].Side != "before" || v.Measurements[1].Side != "after" || !bm.ValidID(v.AfterRevisionID) {
			return malformed("Comparison requires before/after source and selections")
		}
	}
	pins, err := o.NormalizeAnalysisPins(v.ObservationPins)
	if err != nil {
		return err
	}
	v.ObservationPins = pins
	sides := map[string]bool{}
	for _, p := range pins {
		if p.Side == "after" && v.Kind != "scenario_comparison" {
			return malformed("Unexpected after pin")
		}
		sides[p.Side] = true
	}
	for _, m := range v.Measurements {
		if !sides[m.Side] {
			return malformed("Missing side observation pins")
		}
	}
	*in = StartInput{Kind: v.Kind, Measurement: &v, Limits: v.Limits, IdempotencyKey: v.IdempotencyKey, ObservationMode: "pinned"}
	return nil
}
func (s *Service) startMeasurement(ctx context.Context, pid string, in StartInput, requestHash string) (*Job, error) {
	if s.observations == nil {
		return nil, fault(422, "observations_unavailable", "Observation reader unavailable")
	}
	v := in.Measurement
	targets := map[string]o.SourcePin{}
	coverages := map[string]bm.Coverage{}
	frozen := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", Kind: in.Kind, ProjectID: pid, Limits: in.Limits, Scope: normalizedScope(Scope{}), RuleSetVersion: MeasurementPolicy, TraversalVersion: "b62-observed/v1", ObservationMode: "pinned"}
	for side, revision := range map[string]string{"before": v.BeforeRevisionID, "after": v.AfterRevisionID} {
		if revision == "" {
			continue
		}
		target := bm.BackendReadTarget{RevisionID: revision}
		fp, err := s.graphs.EffectiveGraphInputFootprint(ctx, pid, target)
		if err != nil {
			return nil, err
		}
		leased, res, err := s.graphs.ReserveAnalysisInput(ctx, pid, fp)
		if err != nil {
			return nil, err
		}
		graph, err := s.graphs.ResolveEffectiveGraph(leased, pid, target)
		res.Release()
		if err != nil {
			return nil, err
		}
		coverages[side] = graph.State.Revision.Coverage
		targets[side] = o.SourcePin{RevisionID: revision, SemanticHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash}
		if side == "before" {
			frozen.From = target
			frozen.BeforePins = graph.Pins
			frozen.BeforeSource = sourcePins(graph)
		} else {
			frozen.To = &target
			frozen.AfterPins = graph.Pins
			frozen.AfterSource = sourcePins(graph)
		}
	}
	if frozen.To == nil {
		target := frozen.From
		frozen.To = &target
		frozen.AfterPins = frozen.BeforePins
		frozen.AfterSource = frozen.BeforeSource
	}
	if v.DiagramScope != nil {
		reader, ok := s.graphs.(interface {
			ResolveDiagramScope(context.Context, string, bm.DiagramScopeInput) (*bm.DiagramScope, error)
		})
		if !ok {
			return nil, malformed("Diagram resolver unavailable")
		}
		scope, err := reader.ResolveDiagramScope(ctx, pid, *v.DiagramScope)
		if err != nil {
			return nil, err
		}
		if scope.Truncated || scope.TargetHash != targets["before"].TargetGraphHash || v.Kind == "scenario_comparison" {
			return nil, fault(422, "diagram_mismatch", "Overlay requires one exact source diagram")
		}
		frozen.DiagramScope = scope
	}
	data, err := s.observations.ResolveAnalysisPins(ctx, pid, o.PinRequest{Pins: v.ObservationPins, Targets: targets, DiagramScope: frozen.DiagramScope, MaxRecords: in.Limits.Records, MaxBytes: maxInputBytes})
	if err != nil {
		return nil, err
	}
	if _, ok := coverages["after"]; !ok {
		coverages["after"] = coverages["before"]
	}
	frozen.Observations = &FrozenObservationAnalysis{BeforeCoverage: coverages["before"], AfterCoverage: coverages["after"], Measurements: v.Measurements, Data: *data}
	raw, err := canonical(frozen)
	if err != nil {
		return nil, err
	}
	job, err := s.repo.Start(ctx, PreparedStart{ProjectID: pid, InputJSON: raw, InputHash: digest(raw), RequestHash: requestHash, Key: in.IdempotencyKey, OutputReservation: in.Limits.ResultBytes}, nil)
	if err == nil {
		s.hint()
	}
	return job, err
}
func analyzeMeasurements(ctx context.Context, in *ImmutableInput) (*TerminalSnapshot, error) {
	if in.Observations == nil {
		return nil, malformed("Missing frozen observations")
	}
	reports := []ScenarioMeasurements{}
	for _, selection := range in.Observations.Measurements {
		m, err := MeasureScenario(ctx, selection, in.Observations.Data)
		if err != nil {
			return nil, err
		}
		reports = append(reports, *m)
	}
	r := newReport(in)
	for _, m := range reports {
		r.add("checks", ObjectAddress{RecordType: "source", ID: m.Input.Side}, "scenario_measurement", "confirmed", 0, m)
	}
	if len(reports) == 2 {
		r.add("checks", ObjectAddress{RecordType: "source", ID: "comparison"}, "scenario_comparison", "confirmed", 0, CompareMeasurements(reports[0], reports[1]))
	}
	if in.DiagramScope != nil {
		overlay, err := ObserveDiagram(ctx, *in.DiagramScope, in.Observations.Data, reports[0])
		if err != nil {
			return nil, err
		}
		r.add("witnesses", ObjectAddress{RecordType: "diagram", ID: in.DiagramScope.Pin.ID}, "observed_sequence", "possible", 0, overlay)
	}
	manifest := ResultManifest{SourceCoverageBefore: in.Observations.BeforeCoverage, SourceCoverageAfter: in.Observations.AfterCoverage, DiagramScope: in.DiagramScope, Complete: len(r.truncations) == 0 && in.Observations.BeforeCoverage.Status != "" && in.Observations.AfterCoverage.Status != "", ChangedIDs: []ObjectAddress{}, CoveredChangedIDs: []ObjectAddress{}, Gaps: []Diagnostic{}, TruncationReasons: diagnostics(r.truncations), Scope: in.Scope, RuleSetVersion: in.RuleSetVersion, TraversalVersion: in.TraversalVersion, Verdict: "unknown", RuntimeVerified: false}
	snap, err := r.snapshot(manifest)
	return &TerminalSnapshot{Status: "completed", Snapshot: snap}, err
}
