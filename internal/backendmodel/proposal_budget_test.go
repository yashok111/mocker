package backendmodel

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// A second Repo must see reservations held by the first, and import mutations
// must roll back when their staging would consume that same project budget.
func TestProposalTransientBudgetSharedWithImport(t *testing.T) {
	r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
	pid := detail.Proposal.ProjectID
	second := NewRepo(r.db)
	held, ok := r.db.ReserveTransient("backend:"+pid, MaxProjectStagingBytes-1, MaxProjectStagingBytes)
	if !ok {
		t.Fatal("reserve project budget")
	}
	defer held.Release()
	_, err := second.PreviewProposal(t.Context(), pid, detail.Proposal.ID, proposalPreviewInput(detail, proposalNullable(ids, "required", false)))
	if fault, ok := errors.AsType[*FaultError](err); !ok || fault.Status != 413 {
		t.Fatalf("concurrent preparation should refuse: %v", err)
	}
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error { return second.checkStaging(t.Context(), tx, pid, 2) }); err == nil {
		t.Fatal("import staging ignored proposal reservation")
	}
	held.Release()
	if _, err := second.PreviewProposal(t.Context(), pid, detail.Proposal.ID, proposalPreviewInput(detail, proposalNullable(ids, "required", false))); err != nil {
		t.Fatalf("preview after release: %v", err)
	}
	if got := r.db.TransientBytes("backend:" + pid); got != 0 {
		t.Fatalf("preview leaked %d bytes", got)
	}
}

func TestProposalTransientReservationLifecycle(t *testing.T) {
	r, detail, _, ids := proposalEvaluationFixture(t, "sqlite")
	pid := detail.Proposal.ProjectID
	in := proposalPreviewInput(detail, proposalNullable(ids, "required", false))
	prepared, err := r.prepareProposal(t.Context(), pid, detail.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.db.TransientBytes("backend:" + pid); got == 0 || got != prepared.reservedBytes {
		t.Fatalf("preparation not reserved: %d", got)
	}
	prepared.reservation.Release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.prepareProposal(ctx, pid, detail.Proposal.ID, in); err == nil {
		t.Fatal("canceled preparation succeeded")
	}
	if got := r.db.TransientBytes("backend:" + pid); got != 0 {
		t.Fatalf("canceled preparation leaked %d bytes", got)
	}
	apply := proposalApplyInput(t, r, detail, "budget-save", in.Commands...)
	if _, err := r.ApplyProposal(t.Context(), pid, detail.Proposal.ID, apply); err != nil {
		t.Fatal(err)
	}
	if got := r.db.TransientBytes("backend:" + pid); got != 0 {
		t.Fatalf("apply leaked %d bytes", got)
	}
}
