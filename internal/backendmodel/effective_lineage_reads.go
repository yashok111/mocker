package backendmodel

import "context"

func (r *Repo) queryEffectiveLineage(ctx context.Context, pid string, in LineageQueryInput) (*LineagePage, error) {
	target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
	if err := rejectStagedView(target); err != nil {
		return nil, err
	}
	if target.Proposal != nil {
		return nil, savedUnsupported()
	}
	graph, err := r.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	in.RevisionID = graph.State.Revision.ID
	in.ChangeProposal = nil
	in.Proposal = nil
	var native *SourceGraphSnapshot
	if graph.Source.SourceVector != nil {
		native = graph.Source
	}
	return projectLineageWithEffective(ctx, &graph.State, native, graph, in)
}
