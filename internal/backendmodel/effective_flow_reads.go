package backendmodel

import "context"

func (r *Repo) queryEffectiveFlow(ctx context.Context, pid string, in FlowQueryInput) (*FlowPage, error) {
	target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
	if err := rejectStagedView(target); err != nil {
		return nil, err
	}
	if target.Proposal != nil {
		return nil, legacyProposalAdvancedUnsupported("Flow")
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
	return projectRuntimeFlowWithEffective(ctx, &graph.State, native, graph, in)
}
