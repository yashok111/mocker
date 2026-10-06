package backendanalysis

import (
	"context"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func (s *Service) startDiagnostics(ctx context.Context, pid string, in StartInput, hash string) (*Job, error) {
	target, _ := targetInput(in.Target)
	footprint, err := s.graphs.EffectiveGraphInputFootprint(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	leased, lease, err := s.graphs.ReserveAnalysisInput(ctx, pid, footprint)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	g, err := s.graphs.ResolveEffectiveGraph(leased, pid, target)
	if err != nil {
		return nil, err
	}
	if in.FromRevisionID != "" && in.FromRevisionID != g.Pins.BaseRevisionID {
		return nil, fault(422, "scope_mismatch", "Diagnostics baseline differs from target base")
	}
	input := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", Kind: "diagnostics", ProjectID: pid, From: backendmodel.BackendReadTarget{RevisionID: g.Pins.BaseRevisionID}, To: &target, BeforePins: g.Pins, AfterPins: g.Pins, BeforeSource: sourcePins(g), AfterSource: sourcePins(g), Scope: in.Scope, Limits: in.Limits, RuleSetVersion: "diagnostics-v1", TraversalVersion: "b42-traversal/v1", ObservationMode: "none"}
	if in.DiagramScope != nil {
		reader, ok := s.graphs.(interface {
			ResolveDiagramScope(context.Context, string, backendmodel.DiagramScopeInput) (*backendmodel.DiagramScope, error)
			GetDiagram(context.Context, string, backendmodel.DiagramPin) (*backendmodel.DiagramVersion, error)
		})
		if !ok {
			return nil, fault(422, "unsupported", "Diagram reader unavailable")
		}
		input.DiagramScope, err = reader.ResolveDiagramScope(leased, pid, *in.DiagramScope)
		if err != nil {
			return nil, err
		}
		if input.DiagramScope.TargetHash != g.Pins.TargetHash {
			return nil, fault(422, "scope_mismatch", "Diagram target differs from analysis target")
		}
		input.DiagnosticDiagram, err = reader.GetDiagram(leased, pid, in.DiagramScope.Pin)
		if err != nil {
			return nil, err
		}
	}
	raw, err := canonical(input)
	if err != nil {
		return nil, err
	}
	job, err := s.repo.Start(ctx, PreparedStart{ProjectID: pid, InputJSON: raw, InputHash: digest(raw), RequestHash: hash, Key: in.IdempotencyKey, OutputReservation: in.Limits.ResultBytes}, nil)
	if err == nil {
		s.hint()
	}
	return job, err
}
func (e *analysisEngine) analyzeDiagnostics(ctx context.Context, in *ImmutableInput) (*TerminalSnapshot, error) {
	if in.To == nil {
		return nil, malformed("Exact diagnostics target required")
	}
	footprint, err := e.graphs.EffectiveGraphInputFootprint(ctx, in.ProjectID, *in.To)
	if err != nil {
		return nil, err
	}
	leased, lease, err := e.graphs.ReserveAnalysisInput(ctx, in.ProjectID, footprint)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	g, err := e.graphs.ResolveEffectiveGraph(leased, in.ProjectID, *in.To)
	if err != nil {
		return nil, err
	}
	saved, err := encodePins(in.AfterPins)
	if err != nil {
		return nil, err
	}
	actual, err := encodePins(g.Pins)
	if err != nil {
		return nil, err
	}
	a, err := requestHash(saved)
	if err != nil {
		return nil, err
	}
	b, err := requestHash(actual)
	if err != nil {
		return nil, err
	}
	if a != b {
		return nil, fault(409, "input_conflict", "Resolved immutable diagnostic pins differ")
	}
	return diagnosticSnapshot(leased, in, g)
}
