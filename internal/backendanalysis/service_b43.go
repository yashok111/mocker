package backendanalysis

import (
	"bytes"
	"context"
	"errors"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type b43InputReader interface {
	AnalysisProposalBaseRevision(context.Context, string, backendmodel.ProposalReadTarget) (string, error)
	AnalysisEvidenceInputFootprint(context.Context, string, backendmodel.AnalysisEvidenceRequest) (backendmodel.AnalysisInputFootprint, error)
	ReadAnalysisEvidencePins(context.Context, string, backendmodel.AnalysisEvidenceRequest) ([]backendmodel.AnalysisEvidenceDocumentPin, error)
}

func (s *Service) startB43(ctx context.Context, pid string, in StartInput, hash string) (*Job, error) {
	reader, ok := s.graphs.(b43InputReader)
	if !ok {
		return nil, errors.New("analysis immutable evidence reader missing")
	}
	req, base, proposal, err := resolveB43StartRequest(ctx, pid, in, reader)
	if err != nil {
		return nil, err
	}
	footprint, err := reader.AnalysisEvidenceInputFootprint(ctx, pid, req)
	if err != nil {
		return nil, err
	}
	leased, lease, err := s.graphs.ReserveAnalysisInput(ctx, pid, footprint)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	graphs := map[string]*backendmodel.EffectiveGraphSnapshot{}
	for _, target := range req.Targets {
		key, _ := requestHash(target)
		if graphs[key] != nil {
			continue
		}
		graphs[key], err = s.graphs.ResolveEffectiveGraph(leased, pid, target)
		if err != nil {
			return nil, err
		}
	}
	get := func(target backendmodel.BackendReadTarget) *backendmodel.EffectiveGraphSnapshot {
		key, _ := requestHash(target)
		return graphs[key]
	}
	pins, err := reader.ReadAnalysisEvidencePins(leased, pid, req)
	if err != nil {
		return nil, err
	}
	var intent *PackagePayload
	if proposal != nil {
		b, d := get(backendmodel.BackendReadTarget{RevisionID: base}), get(backendmodel.BackendReadTarget{ChangeProposal: proposal})
		intent = &PackagePayload{ChangeProposal: *proposal, BaseRevisionID: base, BasePins: b.Pins, DraftPins: d.Pins, BaseSource: sourcePins(b), DraftSource: sourcePins(d), EvidencePins: pins}
	}
	var payload any
	switch in.Kind {
	case "change_package":
		payload = intent
	case "conformance":
		p := in.Conformance
		g := get(backendmodel.BackendReadTarget{RevisionID: p.ResultRevisionID})
		conformance := &ConformancePayload{PackagePayload: *intent, ResultRevisionID: p.ResultRevisionID, ResultPins: g.Pins, ResultSource: sourcePins(g), IdentityMap: p.IdentityMap, TestAttachments: p.TestAttachments}
		if _, err = validateConformanceAssociations(conformance, get(backendmodel.BackendReadTarget{RevisionID: base}), get(backendmodel.BackendReadTarget{ChangeProposal: proposal}), g); err != nil {
			return nil, err
		}
		payload = conformance
	case "endpoint_review":
		p := in.EndpointReview
		b, a := get(backendmodel.BackendReadTarget{RevisionID: p.FromRevisionID}), get(backendmodel.BackendReadTarget{RevisionID: p.ToRevisionID})
		endpoint := &EndpointReviewPayload{FromRevisionID: p.FromRevisionID, ToRevisionID: p.ToRevisionID, BeforeEndpointID: p.BeforeEndpointID, AfterEndpointID: p.AfterEndpointID, BeforePins: b.Pins, AfterPins: a.Pins, BeforeSource: sourcePins(b), AfterSource: sourcePins(a), EvidencePins: pins, Intent: intent}
		if err = validateEndpointRoots(endpoint, b, a); err != nil {
			return nil, err
		}
		payload = endpoint
	}
	input := ImmutableInputV2{DocumentVersion: "backend-analysis-input/v2", Kind: in.Kind, ProjectID: pid, Payload: payload, Limits: in.Limits, RuleSetVersion: "b43-rules/v1", TraversalVersion: "b42-traversal/v1", ObservationMode: "none"}
	raw, err := canonical(input)
	if err != nil {
		return nil, err
	}
	job, err := s.repo.Start(leased, PreparedStart{ProjectID: pid, InputJSON: raw, InputHash: digest(raw), RequestHash: hash, Key: in.IdempotencyKey, OutputReservation: in.Limits.ResultBytes}, nil)
	if err == nil {
		s.hint()
	}
	return job, err
}
func b43EvidenceRequest(in *ImmutableInputV2) backendmodel.AnalysisEvidenceRequest {
	req := backendmodel.AnalysisEvidenceRequest{}
	var p *PackagePayload
	switch v := in.Payload.(type) {
	case *PackagePayload:
		p = v
	case *ConformancePayload:
		p = &v.PackagePayload
		req.ResultRevisionID = v.ResultRevisionID
		req.Targets = append(req.Targets, backendmodel.BackendReadTarget{RevisionID: v.ResultRevisionID})
	case *EndpointReviewPayload:
		p = v.Intent
		req.BaselineRevisionID = v.FromRevisionID
		req.ResultRevisionID = v.ToRevisionID
		req.Targets = append(req.Targets, backendmodel.BackendReadTarget{RevisionID: v.FromRevisionID}, backendmodel.BackendReadTarget{RevisionID: v.ToRevisionID})
	}
	if p != nil {
		if req.BaselineRevisionID == "" {
			req.BaselineRevisionID = p.BaseRevisionID
		}
		req.Targets = append(req.Targets, backendmodel.BackendReadTarget{RevisionID: p.BaseRevisionID}, backendmodel.BackendReadTarget{ChangeProposal: &p.ChangeProposal})
	}
	return req
}
func (e *analysisEngine) analyzeB43(ctx context.Context, in *ImmutableInput, emit func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	reader, ok := e.graphs.(b43InputReader)
	if !ok {
		return nil, errors.New("analysis evidence reader missing")
	}
	req := b43EvidenceRequest(in.V2)
	footprint, err := reader.AnalysisEvidenceInputFootprint(ctx, in.ProjectID, req)
	if err != nil {
		return nil, err
	}
	leased, lease, err := e.graphs.ReserveAnalysisInput(ctx, in.ProjectID, footprint)
	if err != nil {
		return nil, err
	}
	defer lease.Release()

	graphs := map[string]*backendmodel.EffectiveGraphSnapshot{}
	for _, target := range req.Targets {
		key, _ := requestHash(target)
		if graphs[key] != nil {
			continue
		}
		graphs[key], err = e.graphs.ResolveEffectiveGraph(leased, in.ProjectID, target)
		if err != nil {
			return nil, err
		}
	}
	get := func(target backendmodel.BackendReadTarget) *backendmodel.EffectiveGraphSnapshot {
		key, _ := requestHash(target)
		return graphs[key]
	}
	pins, err := reader.ReadAnalysisEvidencePins(leased, in.ProjectID, req)
	if err != nil {
		return nil, err
	}
	if endpoint, ok := in.V2.Payload.(*EndpointReviewPayload); ok {
		return analyzeEndpointInput(leased, in, endpoint, get, pins)
	}
	var payload *PackagePayload
	var result *backendmodel.EffectiveGraphSnapshot
	pairs := [][2]any{}
	switch p := in.V2.Payload.(type) {
	case *PackagePayload:
		payload = p
	case *ConformancePayload:
		payload = &p.PackagePayload
		result = get(backendmodel.BackendReadTarget{RevisionID: p.ResultRevisionID})
		pairs = append(pairs, [2]any{p.ResultPins, result.Pins}, [2]any{p.ResultSource, sourcePins(result)})
	default:
		return nil, fault(422, "unsupported", "Invalid immutable analysis payload")
	}
	before := get(backendmodel.BackendReadTarget{RevisionID: payload.BaseRevisionID})
	after := get(backendmodel.BackendReadTarget{ChangeProposal: &payload.ChangeProposal})
	pairs = append(pairs, [2]any{payload.BasePins, before.Pins}, [2]any{payload.DraftPins, after.Pins}, [2]any{payload.BaseSource, sourcePins(before)}, [2]any{payload.DraftSource, sourcePins(after)}, [2]any{payload.EvidencePins, pins})
	for _, pair := range pairs {
		a, _ := canonical(pair[0])
		b, _ := canonical(pair[1])
		if !bytes.Equal(a, b) {
			return nil, fault(409, "input_conflict", "Immutable input pins differ")
		}
	}
	if p, ok := in.V2.Payload.(*ConformancePayload); ok {
		evidence, ok := e.graphs.(backendmodel.AnalysisEvidenceReader)
		if !ok {
			return nil, errors.New("conformance evidence reader missing")
		}
		var request *backendmodel.EditorArtifactRequest
		if artifacts, ok := e.graphs.(interface {
			AnalysisArtifactRequest(context.Context, string) (*backendmodel.EditorArtifactRequest, error)
		}); ok {
			request, err = artifacts.AnalysisArtifactRequest(leased, in.ProjectID)
			if err != nil {
				return nil, err
			}
		}
		return analyzeConformance(leased, in, p, before, after, result, evidence, request)
	}
	return analyzePackage(leased, in, payload, before, after, emit)
}

func resolveB43StartRequest(ctx context.Context, pid string, in StartInput, reader b43InputReader) (backendmodel.AnalysisEvidenceRequest, string, *backendmodel.ProposalReadTarget, error) {
	req := backendmodel.AnalysisEvidenceRequest{}
	var proposal *backendmodel.ProposalReadTarget
	switch in.Kind {
	case "change_package":
		proposal = &in.Package.ChangeProposal
	case "conformance":
		proposal = &in.Conformance.ChangeProposal
		req.ResultRevisionID = in.Conformance.ResultRevisionID
		req.Targets = append(req.Targets, backendmodel.BackendReadTarget{RevisionID: req.ResultRevisionID})
	case "endpoint_review":
		p := in.EndpointReview
		proposal = p.ChangeProposal
		req.BaselineRevisionID = p.FromRevisionID
		req.ResultRevisionID = p.ToRevisionID
		req.Targets = append(req.Targets, backendmodel.BackendReadTarget{RevisionID: p.FromRevisionID}, backendmodel.BackendReadTarget{RevisionID: p.ToRevisionID})
	}
	var base string
	var err error
	if proposal != nil {
		base, err = reader.AnalysisProposalBaseRevision(ctx, pid, *proposal)
		if err != nil {
			return backendmodel.AnalysisEvidenceRequest{}, "", nil, err
		}
		req.Targets = append(req.Targets, backendmodel.BackendReadTarget{RevisionID: base}, backendmodel.BackendReadTarget{ChangeProposal: proposal})
		if req.BaselineRevisionID == "" {
			req.BaselineRevisionID = base
		}
	}
	return req, base, proposal, nil
}

func analyzeEndpointInput(ctx context.Context, in *ImmutableInput, endpoint *EndpointReviewPayload, get func(backendmodel.BackendReadTarget) *backendmodel.EffectiveGraphSnapshot, pins []backendmodel.AnalysisEvidenceDocumentPin) (*TerminalSnapshot, error) {
	var err error

	before := get(backendmodel.BackendReadTarget{RevisionID: endpoint.FromRevisionID})
	after := get(backendmodel.BackendReadTarget{RevisionID: endpoint.ToRevisionID})
	pairs := [][2]any{{endpoint.BeforePins, before.Pins}, {endpoint.AfterPins, after.Pins}, {endpoint.BeforeSource, sourcePins(before)}, {endpoint.AfterSource, sourcePins(after)}, {endpoint.EvidencePins, pins}}
	var intentBase, intent *backendmodel.EffectiveGraphSnapshot
	if p := endpoint.Intent; p != nil {
		intentBase = get(backendmodel.BackendReadTarget{RevisionID: p.BaseRevisionID})
		intent = get(backendmodel.BackendReadTarget{ChangeProposal: &p.ChangeProposal})
		pairs = append(pairs, [2]any{p.BasePins, intentBase.Pins}, [2]any{p.DraftPins, intent.Pins}, [2]any{p.BaseSource, sourcePins(intentBase)}, [2]any{p.DraftSource, sourcePins(intent)}, [2]any{p.EvidencePins, pins})
	}
	for _, pair := range pairs {
		a, _ := canonical(pair[0])
		b, _ := canonical(pair[1])
		if !bytes.Equal(a, b) {
			return nil, fault(409, "input_conflict", "Endpoint immutable pins differ")
		}
	}
	if err = validateEndpointRoots(endpoint, before, after); err != nil {
		return nil, err
	}
	return analyzeEndpointReview(ctx, in, endpoint, before, after, intentBase, intent)
}
