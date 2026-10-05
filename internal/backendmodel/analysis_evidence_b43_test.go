package backendmodel

import (
	"slices"
	"testing"
)

func TestB43EvidenceUnionAndPinAdmission(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}
	rid, err := r.AnalysisProposalBaseRevision(t.Context(), base.Project.ID, *target.ChangeProposal)
	if err != nil || rid != base.Revision.ID {
		t.Fatalf("base: %s %v", rid, err)
	}
	req := AnalysisEvidenceRequest{Targets: []BackendReadTarget{target, target}, BaselineRevisionID: rid, ResultRevisionID: rid}
	footprint, err := r.AnalysisEvidenceInputFootprint(t.Context(), base.Project.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(footprint.Documents, func(d AnalysisDocumentBytes) bool { return d.Key == "revision_decisions:"+rid }) {
		t.Fatal("decisions not admitted")
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, footprint)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	pins, err := r.ReadAnalysisEvidencePins(ctx, base.Project.ID, req)
	if err != nil || len(pins) != 2 {
		t.Fatalf("pins %+v %v", pins, err)
	}
	if _, err = r.ReadAnalysisEvidencePins(t.Context(), base.Project.ID, req); err == nil {
		t.Fatal("unleased evidence read")
	}
}
