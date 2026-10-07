package backendmodel

import "context"

func (r *Repo) queryEffectiveEvents(ctx context.Context, pid string, in EventsQueryInput) (*EventsPage, error) {
	target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
	if err := rejectStagedView(target); err != nil {
		return nil, err
	}
	if target.Proposal != nil {
		return nil, legacyProposalAdvancedUnsupported("Events")
	}
	graph, err := r.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	in.RevisionID = graph.State.Revision.ID
	in.Proposal = nil
	in.ChangeProposal = nil
	var native *SourceGraphSnapshot
	if graph.Source.SourceVector != nil {
		native = graph.Source
	}
	return projectEventsWithEffective(ctx, &graph.State, native, graph, in)
}
