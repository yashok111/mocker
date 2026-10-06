package backendmodel

import "context"

func (s *ArtifactService) queryEffectiveArtifact(ctx context.Context, pid string, in ArtifactQueryInput) (*ArtifactProjectionPage, error) {
	target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
	if err := rejectStagedView(target); err != nil {
		return nil, err
	}
	if target.Proposal != nil {
		return nil, savedUnsupported()
	}
	graph, err := s.repo.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	if graph.Pins.ArtifactContextV3 != nil {
		return nil, invalid("context", "Use an explicit namespaced artifact resolver for v3")
	}
	if graph.Pins.ArtifactContext == nil {
		return nil, notFound()
	}
	in.RevisionID = graph.State.Revision.ID
	in.Proposal = nil
	in.ChangeProposal = nil
	request := NewEditorArtifactRequest(ctx, s.api, s.scenarios)
	request.effective = graph
	return request.Project(&graph.State, *graph.Pins.ArtifactContext, in)
}

func (s *APIArtifactService) queryEffectiveAPIArtifacts(ctx context.Context, pid string, in APIArtifactQueryInput) (*APIArtifactPage, error) {
	target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
	if err := rejectStagedView(target); err != nil {
		return nil, err
	}
	if target.Proposal != nil {
		return nil, savedUnsupported()
	}
	graph, err := s.repo.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	in.RevisionID = graph.State.Revision.ID
	in.Proposal = nil
	in.ChangeProposal = nil
	if graph.Pins.ArtifactContextV3 != nil {
		return nil, invalid("context", "Use an explicit namespaced artifact resolver for v3")
	}
	return s.projectAPIArtifactBindings(ctx, pid, in, &graph.State, legacyArtifactContext(graph.Pins.ArtifactContext), graph)
}
