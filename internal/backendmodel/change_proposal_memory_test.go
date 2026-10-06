package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

func TestChangeProposalTransientBudgetCoversCreatePreviewRestoreAndReplay(t *testing.T) {
	r, base, d := changeFixture(t)
	saved, input := saveChange(t, r, d, "before-memory", changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}}))
	var reservation *store.TransientReservation
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		var ok bool
		reservation, ok = r.db.ReserveTransient("backend:"+base.Project.ID, MaxProjectStagingBytes, MaxProjectStagingBytes)
		if !ok {
			t.Fatal("fixture reservation failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	_, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Blocked", BaseRevisionID: base.Revision.ID, IdempotencyKey: "memory-create"})
	if err == nil {
		t.Fatal("create bypassed project transient budget")
	}
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: saved.Proposal.Version, ProposalRevisionID: saved.Revision.ID, Commands: []ChangeProposalCommand{changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})}})
	if err == nil {
		t.Fatal("preview bypassed project transient budget")
	}
	_, err = r.RestoreChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, RestoreChangeProposalInput{ExpectedVersion: saved.Proposal.Version, ProposalRevisionID: saved.Revision.ID, RestoreRevisionID: d.Revision.ID, IdempotencyKey: "memory-restore"})
	if err == nil {
		t.Fatal("restore bypassed project transient budget")
	}
	if _, err = r.ApplyChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, input); err != nil {
		t.Fatal("receipt replay did not bypass materialization", err)
	}
}

func TestChangeProposalRestoredLedgerCountsBeforeDecodeAndAfterResize(t *testing.T) {
	r, _, initial := changeFixture(t)
	d := initial
	var lastInput ApplyChangeProposalInput
	for batch := range 4 {
		commands := make([]ChangeProposalCommand, 100)
		for i := range commands {
			commands[i] = changeCreateNode(t, uuid.NewV7().String(), "service", nil, map[string]any{})
			commands[i].Reason = strings.Repeat("r", 4096)
		}
		d, lastInput = saveChange(t, r, d, fmt.Sprintf("ledger-%d", batch), commands...)
	}
	original, err := r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, lastInput)
	if err != nil {
		t.Fatal(err)
	}
	originalJSON, _ := json.Marshal(original)
	restoreInput := RestoreChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, RestoreRevisionID: initial.Revision.ID, IdempotencyKey: "restore-small"}
	restored, err := r.RestoreChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, restoreInput)
	if err != nil {
		t.Fatal(err)
	}
	restoreJSON, _ := json.Marshal(restored)
	d = &ChangeProposalDetail{Proposal: restored.Proposal, Revision: restored.Revision}
	var ledgerBytes int64
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT sum(length(CAST(document AS BLOB))) FROM backend_change_proposal_identities_documents WHERE proposal_id=?`, d.Proposal.ID).Scan(&ledgerBytes); err != nil {
		t.Fatal(err)
	}
	const available int64 = 1500000
	if ledgerBytes <= available {
		t.Fatal("fixture must exceed the entire available budget")
	}
	scope := "backend:" + d.Proposal.ProjectID
	var held *store.TransientReservation
	if err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		var ok bool
		held, ok = r.db.ReserveTransient(scope, MaxProjectStagingBytes-available, MaxProjectStagingBytes)
		if !ok {
			t.Fatal("fixture pressure could not be reserved")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	previewInput := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})}}
	if _, err = r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, previewInput); err == nil {
		t.Fatalf("decoded %d ledger bytes with only%d bytes available", ledgerBytes, available)
	}
	if r.db.TransientBytes(scope) != MaxProjectStagingBytes-available {
		t.Fatal("refused preview leaked a reservation")
	}
	replay, err := r.ApplyChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, lastInput)
	if err != nil {
		t.Fatal(err)
	}
	replayJSON, _ := json.Marshal(replay)
	if string(replayJSON) != string(originalJSON) {
		t.Fatal("memory pressure changed original apply receipt")
	}
	restoredReplay, err := r.RestoreChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, restoreInput)
	if err != nil {
		t.Fatal(err)
	}
	replayedRestoreJSON, _ := json.Marshal(restoredReplay)
	if string(replayedRestoreJSON) != string(restoreJSON) {
		t.Fatal("memory pressure changed original restore receipt")
	}
	held.Release()
	prepared, err := r.prepareChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, previewInput)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.reservation.Release()
	if prepared.candidate.CandidateHash == nil {
		t.Fatal("valid preview failed after pressure was released")
	}
	if r.db.TransientBytes(scope) < ledgerBytes {
		t.Fatal("final materialization resize forgot the permanent ledger")
	}
}
